package store

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"returnsrecon/internal/model"
)

var testFinance = WorkflowActor{"test-finance", "finance"}
var testAdmin = WorkflowActor{"test-admin", "admin"}
var testOwner = WorkflowActor{"test-warehouse-a", "warehouse"}
var testOther = WorkflowActor{"test-warehouse-b", "warehouse"}

func workflowFixture(t *testing.T) (*Store, string, Manifest) {
	t.Helper()
	s := openTest(t, filepath.Join(t.TempDir(), "workflow.sqlite"))
	dir, m := preparedFixture(t, []model.Row{fixtureRow("0:2", 2, 4, "08:00"), fixtureRow("0:3", 3, 10, "09:00"), fixtureRow("0:4", 4, -4, "10:00")})
	if _, err := s.Import(context.Background(), dir); err != nil {
		t.Fatal(err)
	}
	r, err := s.Run(context.Background(), m.DatasetID, 2)
	if err != nil {
		t.Fatal(err)
	}
	return s, ID(r.RunID + "|0:4"), m
}

func workflowCurrent(t *testing.T, s *Store, id string) WorkflowCase {
	t.Helper()
	c, err := s.WorkflowCase(context.Background(), testFinance, id)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func requireWorkflowCode(t *testing.T, err error, code string) *WorkflowError {
	t.Helper()
	var got *WorkflowError
	if !errors.As(err, &got) || got.Code != code {
		t.Fatalf("wanted %s, got %v", code, err)
	}
	return got
}

func assertWorkflowRejected(t *testing.T, s *Store, id, code string, call func() error) {
	t.Helper()
	before := workflowCurrent(t, s, id)
	audits := scalar(t, s, "SELECT count(*) FROM audit_events")
	err := call()
	requireWorkflowCode(t, err, code)
	if after := workflowCurrent(t, s, id); after != before || audits != scalar(t, s, "SELECT count(*) FROM audit_events") {
		t.Fatalf("rejected operation changed state or audit: before=%+v after=%+v", before, after)
	}
	t.Logf("rejected: code=%s; state=%s version=%d audit_count=%d unchanged", code, before.Status, before.Version, audits)
}

func assignWorkflow(t *testing.T, s *Store, id string) WorkflowCase {
	t.Helper()
	c, err := s.AssignCase(context.Background(), testFinance, testOwner, id)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestTC19_AssignmentAndAuditAtomic(t *testing.T) {
	for _, actor := range []WorkflowActor{testFinance, testAdmin} {
		t.Run(actor.Role, func(t *testing.T) {
			s, id, m := workflowFixture(t)
			before := workflowCurrent(t, s, id)
			c, err := s.AssignCase(context.Background(), actor, testOwner, id)
			if err != nil {
				t.Fatal(err)
			}
			history, err := s.WorkflowHistory(context.Background(), testOwner, id)
			if err != nil || len(history) != 1 {
				t.Fatalf("history=%+v err=%v", history, err)
			}
			a := history[0]
			if c.Assignee != testOwner.ID || c.AssignedBy != actor.ID || c.AssignedAt == "" || c.Status != "pending" || c.Version != 2 || a.Actor != actor.ID || a.Action != "assign" || a.OccurredAt != c.AssignedAt || a.Before != before || a.After != c || c.SourceHash != m.SourceHash || c.SourceRow != 4 || c.RuleVersion != model.RuleVersion {
				t.Fatalf("assignment/audit mismatch: %+v %+v", c, a)
			}
			if _, err = time.Parse(time.RFC3339Nano, c.AssignedAt); err != nil {
				t.Fatal(err)
			}
			t.Logf("assigned by=%s to=%s at=%s status=pending version=2 audit=1 source=%s/%d", actor.ID, c.Assignee, c.AssignedAt, c.Sheet, c.SourceRow)
		})
	}
}

func TestTC20_DuplicateAndConcurrentAssignment(t *testing.T) {
	s, id, _ := workflowFixture(t)
	s2 := openTest(t, s.path)
	const contenders = 12
	gate := make(chan struct{})
	type response struct {
		caseValue WorkflowCase
		err       error
	}
	out := make(chan response, contenders)
	var wg sync.WaitGroup
	for i := 0; i < contenders; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-gate
			db, recipient, actor := s, testOwner, testFinance
			if i%2 != 0 {
				db, recipient, actor = s2, testOther, testAdmin
			}
			c, err := db.AssignCase(context.Background(), actor, recipient, id)
			out <- response{c, err}
		}(i)
	}
	close(gate)
	wg.Wait()
	close(out)
	winner := workflowCurrent(t, s, id)
	success, rejected := 0, 0
	for r := range out {
		if r.err == nil {
			success++
			if r.caseValue != winner {
				t.Fatal("winner snapshot mismatch")
			}
		} else {
			e := requireWorkflowCode(t, r.err, "already_assigned")
			if e.ExistingAssignee != winner.Assignee || !strings.Contains(e.Message, winner.Assignee) || r.caseValue != (WorkflowCase{}) {
				t.Fatalf("missing owner or leaked success: %+v", r)
			}
			rejected++
		}
	}
	if success != 1 || rejected != contenders-1 || winner.Version != 2 || scalar(t, s, "SELECT count(*) FROM audit_events WHERE action='assign'") != 1 {
		t.Fatalf("success=%d rejected=%d winner=%+v", success, rejected, winner)
	}
	assertWorkflowRejected(t, s, id, "already_assigned", func() error {
		_, err := s2.AssignCase(context.Background(), testAdmin, testOther, id)
		return err
	})
	t.Logf("12 contenders / 2 independent stores: success=%d rejected=%d current_assignee=%s assign_audit=1", success, rejected, winner.Assignee)
}

func TestTC21_RejectSkippedReverseStaleAndClosedWrites(t *testing.T) {
	s, id, _ := workflowFixture(t)
	c := assignWorkflow(t, s, id)
	transition := func(target string, version int, note string) func() error {
		return func() error {
			_, err := s.TransitionCase(context.Background(), testOwner, id, target, version, note)
			return err
		}
	}
	assertWorkflowRejected(t, s, id, "invalid_transition", transition("closed", c.Version, "资料不足"))
	assertWorkflowRejected(t, s, id, "version_conflict", transition("in_progress", 1, ""))
	assertWorkflowRejected(t, s, id, "invalid_input", transition("in_progress", c.Version, "不应在开始时保存结论"))
	c, err := s.TransitionCase(context.Background(), testOwner, id, "in_progress", c.Version, "")
	if err != nil {
		t.Fatal(err)
	}
	assertWorkflowRejected(t, s, id, "invalid_transition", transition("pending", c.Version, ""))
	assertWorkflowRejected(t, s, id, "invalid_transition", transition("in_progress", c.Version, ""))
	assertWorkflowRejected(t, s, id, "version_conflict", transition("closed", 2, "资料不足"))
	c, err = s.TransitionCase(context.Background(), testOwner, id, "closed", c.Version, "资料不足，无法确定原销售，说明后结案。")
	if err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{"pending", "in_progress", "closed", "unknown"} {
		assertWorkflowRejected(t, s, id, "closed", transition(target, c.Version, "试图改写"))
	}
	assertWorkflowRejected(t, s, id, "already_assigned", func() error { _, err := s.AssignCase(context.Background(), testAdmin, testOther, id); return err })
}

func TestTC24_ConclusionAndDurableHistoryWithoutInventedMatch(t *testing.T) {
	ctx := context.Background()
	s, id, m := workflowFixture(t)
	c := assignWorkflow(t, s, id)
	c, err := s.TransitionCase(ctx, testOwner, id, "in_progress", c.Version, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, blank := range []string{"", " \t\r\n", "\u3000\u00a0"} {
		assertWorkflowRejected(t, s, id, "empty_conclusion", func() error { _, err := s.TransitionCase(ctx, testOwner, id, "closed", c.Version, blank); return err })
	}
	const note = "资料不足，两个候选均合格但无法确定原销售；不制造关联，说明后结案。"
	c, err = s.TransitionCase(ctx, testOwner, id, "closed", c.Version, note)
	if err != nil || c.Status != "closed" || c.Version != 4 || c.Conclusion != note {
		t.Fatalf("closed=%+v err=%v", c, err)
	}
	history, err := s.WorkflowHistory(ctx, testOwner, id)
	if err != nil || len(history) != 3 {
		t.Fatalf("history=%+v err=%v", history, err)
	}
	for i, action := range []string{"assign", "start", "close"} {
		a := history[i]
		if a.Action != action || a.After.Version != i+2 || a.Before.Version != i+1 || a.Before.ID != id || a.After.ID != id || a.After.CancelID != "0:4" || a.After.SourceRow != 4 || a.After.DatasetID != m.DatasetID || a.After.SourceHash != m.SourceHash {
			t.Fatalf("bad audit: %+v", a)
		}
		if i > 0 && a.Before != history[i-1].After {
			t.Fatal("history chain broken")
		}
	}
	s2 := openTest(t, s.path)
	if got := workflowCurrent(t, s2, id); got != c {
		t.Fatal("history lost on reopen")
	}
	r, err := s2.Run(ctx, m.DatasetID, 1)
	if err != nil || !r.Reused || workflowCurrent(t, s2, id) != c {
		t.Fatalf("idempotency overwrote workflow: %+v %v", r, err)
	}
	if scalar(t, s, "SELECT count(*) FROM allocations") != 0 || scalar(t, s, "SELECT sum(allocated) FROM sale_balances") != 0 || scalar(t, s, "SELECT count(*) FROM audit_events") != 3 {
		t.Fatal("closure fabricated allocation or changed history")
	}
	if _, err = s.DB.Exec("UPDATE audit_events SET actor='tamper' WHERE case_id=?", id); err == nil {
		t.Fatal("audit update was accepted")
	}
	if _, err = s.DB.Exec("DELETE FROM audit_events WHERE case_id=?", id); err == nil {
		t.Fatal("audit delete was accepted")
	}
	t.Logf("pending(v1) -> assigned pending(v2) -> in_progress(v3) -> closed(v4); audits=3; allocations=0; durable/reused=true; conclusion=%s", c.Conclusion)
}

func TestWorkflowPermissionsAndPublishedBoundary(t *testing.T) {
	s, id, _ := workflowFixture(t)
	for _, actor := range []WorkflowActor{testOwner, {}, {"fake", "unknown"}} {
		code := "forbidden"
		if !actor.valid() {
			code = "unauthenticated"
		}
		assertWorkflowRejected(t, s, id, code, func() error { _, err := s.AssignCase(context.Background(), actor, testOwner, id); return err })
	}
	assertWorkflowRejected(t, s, id, "invalid_assignee", func() error {
		_, err := s.AssignCase(context.Background(), testFinance, WorkflowActor{}, id)
		return err
	})
	c := assignWorkflow(t, s, id)
	for _, actor := range []WorkflowActor{testOther, testFinance, testAdmin} {
		assertWorkflowRejected(t, s, id, "forbidden", func() error {
			_, err := s.TransitionCase(context.Background(), actor, id, "in_progress", c.Version, "")
			return err
		})
	}
	if got, err := s.WorkflowCase(context.Background(), testOther, id); requireWorkflowCode(t, err, "forbidden") == nil || got != (WorkflowCase{}) {
		t.Fatal("read leaked another owner's case")
	}
	if _, err := s.WorkflowHistory(context.Background(), testOther, id); err == nil {
		t.Fatal("audit history leaked")
	}
	if _, err := s.DB.Exec("UPDATE runs SET status='failed' WHERE id=?", c.RunID); err != nil {
		t.Fatal(err)
	}
	_, err := s.AssignCase(context.Background(), testFinance, testOwner, id)
	requireWorkflowCode(t, err, "not_found")
	if scalar(t, s, "SELECT count(*) FROM audit_events") != 1 {
		t.Fatal("unpublished mutation wrote audit")
	}
}

func TestWorkflowAuditFailureRollsBackEveryAction(t *testing.T) {
	for _, action := range []string{"assign", "start", "close"} {
		t.Run(action, func(t *testing.T) {
			s, id, _ := workflowFixture(t)
			if action != "assign" {
				assignWorkflow(t, s, id)
			}
			if action == "close" {
				if _, err := s.TransitionCase(context.Background(), testOwner, id, "in_progress", 2, ""); err != nil {
					t.Fatal(err)
				}
			}
			before := workflowCurrent(t, s, id)
			audits := scalar(t, s, "SELECT count(*) FROM audit_events")
			if _, err := s.DB.Exec(`CREATE TRIGGER test_fail_audit BEFORE INSERT ON audit_events BEGIN SELECT RAISE(ABORT,'test-only audit failure'); END`); err != nil {
				t.Fatal(err)
			}
			var out WorkflowCase
			var err error
			switch action {
			case "assign":
				out, err = s.AssignCase(context.Background(), testFinance, testOwner, id)
			case "start":
				out, err = s.TransitionCase(context.Background(), testOwner, id, "in_progress", before.Version, "")
			case "close":
				out, err = s.TransitionCase(context.Background(), testOwner, id, "closed", before.Version, "资料不足")
			}
			if err == nil || !strings.Contains(err.Error(), "test-only audit failure") || out != (WorkflowCase{}) || workflowCurrent(t, s, id) != before || audits != scalar(t, s, "SELECT count(*) FROM audit_events") {
				t.Fatalf("half committed: action=%s out=%+v err=%v", action, out, err)
			}
			t.Logf("%s audit insert fault -> state/version/assignment/conclusion/audit unchanged", action)
		})
	}
}

func TestWorkflowOldSchemaMigrationPreservesHistory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.sqlite")
	db, err := sql.Open("sqlite", path+"?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	legacy := strings.Replace(schema, " assigned_at TEXT NOT NULL DEFAULT '', assigned_by TEXT NOT NULL DEFAULT '',\n", "", 1)
	if legacy == schema {
		t.Fatal("legacy schema fixture not produced")
	}
	if _, err = db.Exec(legacy); err != nil {
		t.Fatal(err)
	}
	s := &Store{DB: db, path: path}
	dir, m := preparedFixture(t, []model.Row{fixtureRow("0:2", 2, -4, "08:00")})
	if _, err = s.Import(context.Background(), dir); err != nil {
		t.Fatal(err)
	}
	r, err := s.Run(context.Background(), m.DatasetID, 1)
	if err != nil {
		t.Fatal(err)
	}
	id := ID(r.RunID + "|0:2")
	if _, err = db.Exec("UPDATE cases SET assignee='legacy-test-owner',status='in_progress',version=7,conclusion='test-only legacy note' WHERE id=?", id); err != nil {
		t.Fatal(err)
	}
	db.Close()
	s2 := openTest(t, path)
	c := workflowCurrent(t, s2, id)
	if c.Assignee != "legacy-test-owner" || c.Status != "in_progress" || c.Version != 7 || c.Conclusion != "test-only legacy note" || c.AssignedAt != "" || c.AssignedBy != "" {
		t.Fatalf("migration changed history or fabricated metadata: %+v", c)
	}
	s3 := openTest(t, path)
	if !reflect.DeepEqual(c, workflowCurrent(t, s3, id)) {
		t.Fatal("migration not idempotent")
	}
	t.Log("legacy case preserved; missing historical assignment time/actor remain unknown; migration reopens idempotently")
}
