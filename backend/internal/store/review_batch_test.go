package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"testing"

	"returnsrecon/internal/model"
)

func reviewBatchFixture(t *testing.T, sharedCapacity int64) (*Store, Summary, ReviewSet) {
	t.Helper()
	rows := []model.Row{
		fixtureRow("0:2", 2, sharedCapacity, "08:00"), fixtureRow("0:3", 3, sharedCapacity, "09:00"),
		fixtureRow("0:4", 4, -4, "10:00"), fixtureRow("0:5", 5, -4, "11:00"),
		fixtureRow("0:6", 6, 6, "08:00"), fixtureRow("0:7", 7, 6, "09:00"), fixtureRow("0:8", 8, -4, "10:00"),
	}
	for i := range rows {
		if i >= 4 {
			rows[i].Stock = "INDEPENDENT"
		}
		rows[i] = reviewRaw(t, rows[i])
	}
	s, run, _ := reviewFixture(t, rows)
	set, err := s.GenerateReviewSet(context.Background(), testFinance, run.RunID)
	if err != nil {
		t.Fatal(err)
	}
	return s, run, set
}

func reviewApprove(t *testing.T, s *Store, set ReviewSet) ReviewBatch {
	t.Helper()
	b, err := s.PreviewBatch(context.Background(), testFinance, BatchRequest{SetID: set.ID, Action: "approve", All: true, Conclusion: "隔离合成夹具复核结论"}, WorkflowActor{})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestTC53_SharedResourceConflictSkipsWholeComponentOnly(t *testing.T) {
	s, run, set := reviewBatchFixture(t, 6)
	b := reviewApprove(t, s, set)
	if len(b.Groups) != 2 || b.Total != 3 {
		t.Fatalf("shared component construction: %+v", b)
	}
	if scalar(t, s, "SELECT count(*) FROM allocations") != 0 || scalar(t, s, "SELECT count(*) FROM audit_events") != 0 || scalar(t, s, "SELECT sum(version) FROM cases") != 3 {
		t.Fatal("preview changed workflow or capacity")
	}
	out, err := s.CommitBatch(context.Background(), testFinance, b.ID)
	if err != nil {
		t.Fatal(err)
	}
	if out.Status != "completed" || out.Succeeded != 1 || out.Skipped != 2 || out.Failed != 0 {
		t.Fatalf("partial result: %+v", out)
	}
	for _, cancel := range []string{"0:4", "0:5"} {
		c := workflowCurrent(t, s, ID(run.RunID+"|"+cancel))
		if c.Status != "pending" || c.Version != 1 || c.Assignee != "" {
			t.Fatalf("conflict component partially changed: %+v", c)
		}
	}
	c := workflowCurrent(t, s, ID(run.RunID+"|0:8"))
	if c.Status != "closed" || c.Version != 5 || c.Assignee != testFinance.ID || c.Conclusion == "" {
		t.Fatalf("independent approval incomplete: %+v", c)
	}
	if scalar(t, s, "SELECT count(*) FROM allocations") != 1 || scalar(t, s, "SELECT sum(quantity) FROM allocations") != 4 || scalar(t, s, "SELECT count(*) FROM review_confirmations") != 1 || scalar(t, s, "SELECT count(*) FROM audit_events") != 4 {
		t.Fatal("approval did not preserve four workflow audit steps")
	}
	history, err := s.WorkflowHistory(context.Background(), testFinance, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	for i, want := range []string{"assign", "start", "confirm_candidate", "close"} {
		if history[i].Action != want {
			t.Fatalf("audit order %d: %s", i, history[i].Action)
		}
	}
}

func TestTC53_PreviewVersionConflictRollsBackSharedComponent(t *testing.T) {
	s, run, set := reviewBatchFixture(t, 10)
	b := reviewApprove(t, s, set)
	for _, g := range b.Groups {
		if g.Status != "ready" {
			t.Fatalf("unexpected preview conflict: %+v", g)
		}
	}
	if _, err := s.AssignCase(context.Background(), testFinance, testOther, ID(run.RunID+"|0:4")); err != nil {
		t.Fatal(err)
	}
	out, err := s.CommitBatch(context.Background(), testFinance, b.ID)
	if err != nil {
		t.Fatal(err)
	}
	if out.Succeeded != 1 || out.Skipped != 2 {
		t.Fatalf("stale component result: %+v", out)
	}
	if workflowCurrent(t, s, ID(run.RunID+"|0:5")).Version != 1 || scalar(t, s, "SELECT remaining FROM sale_balances WHERE sale_id='0:3'") != 10 || scalar(t, s, "SELECT count(*) FROM allocations WHERE cancel_id IN ('0:4','0:5')") != 0 {
		t.Fatal("version conflict mutated sibling or shared balance")
	}
}

func TestTC54_AuditFailureRollsBackWholeBasketAndOtherGroupSucceeds(t *testing.T) {
	rows := reviewBasketRows(t)
	for _, r := range []model.Row{fixtureRow("0:8", 8, 6, "08:00"), fixtureRow("0:9", 9, 6, "09:00"), fixtureRow("0:10", 10, -4, "10:00")} {
		r.Stock = "OTHER"
		rows = append(rows, reviewRaw(t, r))
	}
	s, run, _ := reviewFixture(t, rows)
	set, err := s.GenerateReviewSet(context.Background(), testFinance, run.RunID)
	if err != nil {
		t.Fatal(err)
	}
	b := reviewApprove(t, s, set)
	failID := ID(run.RunID + "|0:7")
	if _, err = s.DB.Exec(`CREATE TRIGGER isolated_review_audit_fault BEFORE INSERT ON audit_events WHEN NEW.case_id='` + failID + `' AND NEW.action='close' BEGIN SELECT RAISE(ABORT,'isolated review audit failure'); END;`); err != nil {
		t.Fatal(err)
	}
	out, err := s.CommitBatch(context.Background(), testFinance, b.ID)
	if err != nil {
		t.Fatal(err)
	}
	if out.Failed != 2 || out.Succeeded != 1 || out.Skipped != 0 {
		t.Fatalf("audit failure component result: %+v", out)
	}
	for _, id := range []string{"0:6", "0:7"} {
		c := workflowCurrent(t, s, ID(run.RunID+"|"+id))
		if c.Version != 1 || c.Assignee != "" || c.Status != "pending" {
			t.Fatalf("audit failure left workflow: %+v", c)
		}
	}
	if scalar(t, s, "SELECT count(*) FROM allocations WHERE cancel_id IN ('0:6','0:7')") != 0 || scalar(t, s, "SELECT count(*) FROM audit_events") != 4 || scalar(t, s, "SELECT count(*) FROM review_confirmations") != 1 || scalar(t, s, "SELECT remaining FROM sale_balances WHERE sale_id='0:2'") != 6 || scalar(t, s, "SELECT remaining FROM sale_balances WHERE sale_id='0:3'") != 6 {
		t.Fatal("whole basket audit rollback incomplete")
	}
}

func TestTC54_ConcurrentCommitReopenAndRevokedReplay(t *testing.T) {
	s, _, set := reviewBatchFixture(t, 10)
	b := reviewApprove(t, s, set)
	other := openTest(t, s.path)
	start := make(chan struct{})
	errs := make(chan error, 2)
	var wait sync.WaitGroup
	for _, db := range []*Store{s, other} {
		wait.Add(1)
		go func(db *Store) {
			defer wait.Done()
			<-start
			_, err := db.CommitBatch(context.Background(), testFinance, b.ID)
			errs <- err
		}(db)
	}
	close(start)
	wait.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if scalar(t, s, "SELECT count(*) FROM allocations") != 3 || scalar(t, s, "SELECT count(*) FROM audit_events") != 12 || scalar(t, s, "SELECT count(*) FROM review_confirmations") != 3 {
		t.Fatal("concurrent commit duplicated or lost workflow")
	}
	saved, err := other.Batch(context.Background(), testFinance, b.ID)
	if err != nil || saved.Status != "completed" || saved.Succeeded != 3 {
		t.Fatalf("persisted result: %+v %v", saved, err)
	}
	repeated, err := other.CommitBatch(context.Background(), testFinance, b.ID)
	if err != nil || !reflect.DeepEqual(saved, repeated) {
		t.Fatalf("repeat result changed: %+v %v", repeated, err)
	}
	if _, err = other.Batch(context.Background(), testOther, b.ID); err == nil {
		t.Fatal("other actor read batch")
	}
	denied := errors.New("isolated revoked business session")
	checks := 0
	ctx := WithWriteAuthorization(context.Background(), func(context.Context, *sql.Conn) error { checks++; return denied })
	if _, err = other.CommitBatch(ctx, testFinance, b.ID); !errors.Is(err, denied) || checks == 0 {
		t.Fatalf("completed replay bypassed session recheck: %v count=%d", err, checks)
	}
	if scalar(t, s, "SELECT count(*) FROM audit_events") != 12 {
		t.Fatal("replay appended audit")
	}
}

func TestTC51_AllSelectionExclusionsFreezeAcrossPages(t *testing.T) {
	rows := []model.Row{fixtureRow("0:2", 2, 1000, "08:00"), fixtureRow("0:3", 3, 1000, "09:00")}
	for i := 4; i < 109; i++ {
		r := fixtureRow(fmt.Sprintf("0:%d", i), i, -1, "10:00")
		r.Invoice = "C-SELECT"
		rows = append(rows, r)
	}
	for i := range rows {
		rows[i] = reviewRaw(t, rows[i])
	}
	s, run, _ := reviewFixture(t, rows)
	set, err := s.GenerateReviewSet(context.Background(), testFinance, run.RunID)
	if err != nil {
		t.Fatal(err)
	}
	excluded := ID(run.RunID + "|0:108")
	b, err := s.PreviewBatch(context.Background(), testFinance, BatchRequest{SetID: set.ID, Action: "assign", All: true, Filter: QueryFilter{Invoice: "C-SELECT", Page: 2, PageSize: 1}, ExcludeIDs: []string{excluded}, Assignee: testOwner.ID}, testOwner)
	if err != nil {
		t.Fatal(err)
	}
	if b.Total != 104 || len(b.Groups) != 104 {
		t.Fatalf("All was truncated to visible page: total=%d groups=%d", b.Total, len(b.Groups))
	}
	for _, g := range b.Groups {
		if g.Items[0].Item.Case.ID == excluded {
			t.Fatal("excluded row selected")
		}
	}
	if scalar(t, s, "SELECT count(*) FROM audit_events") != 0 || scalar(t, s, "SELECT count(*) FROM cases WHERE assignee IS NOT NULL AND assignee<>''") != 0 {
		t.Fatal("selection preview assigned cases")
	}
	other := openTest(t, s.path)
	frozen, err := other.Batch(context.Background(), testFinance, b.ID)
	if err != nil || frozen.Total != 104 || !reflect.DeepEqual(b.Groups, frozen.Groups) {
		t.Fatalf("reopen lost frozen selection: %v", err)
	}
	for _, by := range []string{"invoice", "customer", "reason", "stock"} {
		groups, err := s.ReviewGroups(context.Background(), testFinance, set.ID, by, QueryFilter{}, true)
		if err != nil {
			t.Fatal(err)
		}
		total := 0
		for _, g := range groups {
			total += g.Count
		}
		if total != 105 {
			t.Fatalf("%s grouping lost selected scope: %d", by, total)
		}
	}
}

func TestTC53_BasketPartialSelectionIsRejected(t *testing.T) {
	s, run, _ := reviewFixture(t, reviewBasketRows(t))
	set, err := s.GenerateReviewSet(context.Background(), testFinance, run.RunID)
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.PreviewBatch(context.Background(), testFinance, BatchRequest{SetID: set.ID, Action: "approve", IDs: []string{ID(run.RunID + "|0:6")}, Conclusion: "隔离复核"}, WorkflowActor{})
	if err != nil {
		t.Fatal(err)
	}
	if len(b.Groups) != 1 || b.Groups[0].Status != "skipped" || b.Groups[0].Code != "incomplete_basket" {
		t.Fatalf("partial basket accepted: %+v", b)
	}
	if _, err = s.CommitBatch(context.Background(), testFinance, b.ID); err != nil {
		t.Fatal(err)
	}
	if scalar(t, s, "SELECT count(*) FROM allocations") != 0 || scalar(t, s, "SELECT count(*) FROM audit_events") != 0 {
		t.Fatal("partial basket wrote workflow")
	}
}

func TestTC52_BulkAssignStartConfirmCloseAndClosedReadonly(t *testing.T) {
	s, run, set := reviewBatchFixture(t, 10)
	ctx := context.Background()
	for i, action := range []string{"assign", "start", "confirm", "close"} {
		actor, target := testOwner, WorkflowActor{}
		if action == "assign" {
			actor, target = testFinance, testOwner
		}
		var err error
		if i != 0 {
			set, err = s.GenerateReviewSet(ctx, actor, run.RunID)
			if err != nil {
				t.Fatal(err)
			}
		}
		req := BatchRequest{SetID: set.ID, Action: action, All: true, Assignee: target.ID, Conclusion: "隔离逐步批量复核结论"}
		if action == "close" {
			empty := req
			empty.Conclusion = " \n\t "
			_, err = s.PreviewBatch(ctx, actor, empty, target)
			requireWorkflowCode(t, err, "empty_conclusion")
		}
		batch, err := s.PreviewBatch(ctx, actor, req, target)
		if err != nil {
			t.Fatal(err)
		}
		out, err := s.CommitBatch(ctx, actor, batch.ID)
		if err != nil {
			t.Fatal(err)
		}
		if out.Succeeded != 3 || out.Skipped != 0 || out.Failed != 0 {
			t.Fatalf("%s batch failed: %+v", action, out)
		}
		if scalar(t, s, "SELECT count(*) FROM audit_events") != (i+1)*3 {
			t.Fatalf("%s did not audit every case", action)
		}
	}
	for _, cancel := range []string{"0:4", "0:5", "0:8"} {
		c := workflowCurrent(t, s, ID(run.RunID+"|"+cancel))
		if c.Status != "closed" || c.Version != 5 || c.Assignee != testOwner.ID || c.Conclusion == "" {
			t.Fatalf("bulk steps lost owner/conclusion: %+v", c)
		}
	}
	// The snapshot taken before closing still lists the items as open; the
	// server precheck must reject them as closed rather than rewrite them.
	stale := set
	for _, action := range []string{"start", "confirm", "close", "approve"} {
		batch, err := s.PreviewBatch(ctx, testOwner, BatchRequest{SetID: stale.ID, Action: action, All: true, Conclusion: "禁止重写"}, WorkflowActor{})
		if err != nil {
			t.Fatal(err)
		}
		out, err := s.CommitBatch(ctx, testOwner, batch.ID)
		if err != nil {
			t.Fatal(err)
		}
		if out.Skipped != 3 || out.Succeeded != 0 || out.Groups[0].Code != "closed" {
			t.Fatalf("closed %s accepted: %+v", action, out)
		}
	}
	// A fresh snapshot never lists, groups or selects closed items.
	set, err := s.GenerateReviewSet(ctx, testOwner, run.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if set.Counts["closed"] != 3 {
		t.Fatalf("closed count missing: %+v", set.Counts)
	}
	page, err := s.ListReview(ctx, testOwner, set.ID, QueryFilter{}, false)
	if err != nil || page.Total != 0 || len(page.Items) != 0 || page.ReadOnly != 3 {
		t.Fatalf("closed items listed in workbench: %+v %v", page, err)
	}
	groups, err := s.ReviewGroups(ctx, testOwner, set.ID, "invoice", QueryFilter{}, false)
	if err != nil || len(groups) != 0 {
		t.Fatalf("closed items grouped: %+v %v", groups, err)
	}
	closedIDs := []string{ID(run.RunID + "|0:4"), ID(run.RunID + "|0:5"), ID(run.RunID + "|0:8")}
	for _, action := range []string{"start", "confirm", "close", "approve"} {
		for _, req := range []BatchRequest{{SetID: set.ID, Action: action, All: true, Conclusion: "禁止重写"}, {SetID: set.ID, Action: action, IDs: closedIDs, Conclusion: "禁止重写"}} {
			_, err = s.PreviewBatch(ctx, testOwner, req, WorkflowActor{})
			requireWorkflowCode(t, err, "closed")
		}
	}
	if scalar(t, s, "SELECT count(*) FROM audit_events") != 12 || scalar(t, s, "SELECT count(*) FROM allocations") != 3 {
		t.Fatal("closed batch rewrote workflow/history")
	}
}
