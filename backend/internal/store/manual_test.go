package store

import (
	"context"
	"encoding/json"
	"path/filepath"
	"sync"
	"testing"

	"returnsrecon/internal/model"
)

func manualFixture(t *testing.T, twoCancels bool) (*Store, []WorkflowCase) {
	t.Helper()
	s := openTest(t, filepath.Join(t.TempDir(), "manual.sqlite"))
	sale := fixtureRow("0:2", 2, 6, "08:00")
	sale.Flags = "same_sheet_duplicate" // Explicit isolated ambiguity fixture only.
	rows := []model.Row{sale, fixtureRow("0:3", 3, -4, "10:00")}
	if twoCancels {
		rows = append(rows, fixtureRow("0:4", 4, -4, "11:00"))
	}
	dir, m := preparedFixture(t, rows)
	if _, err := s.Import(context.Background(), dir); err != nil {
		t.Fatal(err)
	}
	r, err := s.Run(context.Background(), m.DatasetID, 2)
	if err != nil {
		t.Fatal(err)
	}
	// FR-20: this fixture models a sale already verified as independent, before manual matching.
	if _, err = s.DB.Exec("INSERT INTO duplicate_verifications VALUES(?,'0:2','independent','','test-only-verification')", r.RunID); err != nil {
		t.Fatal(err)
	}
	cases := []WorkflowCase{}
	for _, row := range rows[1:] {
		c := assignWorkflow(t, s, ID(r.RunID+"|"+row.ID))
		c, err = s.TransitionCase(context.Background(), testOwner, c.ID, "in_progress", c.Version, "")
		if err != nil {
			t.Fatal(err)
		}
		cases = append(cases, c)
	}
	return s, cases
}

func TestTC22_ManualConfirmationAtomicQualifiedAndAudited(t *testing.T) {
	s, cases := manualFixture(t, false)
	c := cases[0]
	ctx := context.Background()
	before := scalar(t, s, "SELECT count(*) FROM audit_events")
	out, err := s.ConfirmCandidate(ctx, testOwner, c.ID, "0:2", c.Version)
	if err != nil {
		t.Fatal(err)
	}
	if out.Case.Status != "in_progress" || out.Case.Version != c.Version+1 || out.Allocation.Quantity != 4 || out.Allocation.Amount != "0.4" || out.Allocation.Method != "manual" || out.Allocation.SaleSourceRow != 2 {
		t.Fatalf("bad confirmation: %+v", out)
	}
	if scalar(t, s, "SELECT remaining FROM sale_balances WHERE sale_id='0:2'") != 2 || scalar(t, s, "SELECT allocated FROM sale_balances WHERE sale_id='0:2'") != 4 || scalar(t, s, "SELECT count(*) FROM allocations") != 1 || scalar(t, s, "SELECT count(*) FROM audit_events") != before+1 {
		t.Fatal("non-atomic allocation/balance/audit")
	}
	var b, a string
	if err = s.DB.QueryRow("SELECT before_json,after_json FROM audit_events WHERE action='confirm_candidate'").Scan(&b, &a); err != nil {
		t.Fatal(err)
	}
	var bv, av struct {
		WorkflowCase
		Manual ManualAuditDetail `json:"manual_confirmation"`
	}
	if err = json.Unmarshal([]byte(b), &bv); err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal([]byte(a), &av); err != nil {
		t.Fatal(err)
	}
	if bv.Manual.Remaining != 6 || av.Manual.Remaining != 2 || av.Manual.SaleID != "0:2" || av.Manual.SourceRow != 2 || av.RuleVersion != model.RuleVersion || av.SourceHash == "" || av.Version != c.Version+1 {
		t.Fatalf("missing evidence in audit: %s", a)
	}
	history, err := s.WorkflowHistory(ctx, testOwner, c.ID)
	if err != nil || len(history) != 3 || history[2].Action != "confirm_candidate" || history[2].After.Version != c.Version+1 {
		t.Fatalf("history compatibility: %+v %v", history, err)
	}
	var class string
	if err = s.DB.QueryRow("SELECT classification FROM decisions WHERE cancel_id=?", c.CancelID).Scan(&class); err != nil || class != "suspected" {
		t.Fatalf("initial classification changed: %s %v", class, err)
	}
	if _, err = s.Run(ctx, c.DatasetID, 1); err != nil {
		t.Fatal(err)
	}
	if scalar(t, s, "SELECT count(*) FROM allocations") != 1 || scalar(t, s, "SELECT count(*) FROM audit_events") != 3 {
		t.Fatal("idempotent run lost manual history")
	}
	t.Log("TC-22: owner confirmed duplicate-suspect candidate after review; quantity=4 amount=0.4 remaining=2 version=4; initial decision preserved; audit includes both source locations and before/after balance")
}

func TestTC22_ManualAuditFailureRollsBackEverything(t *testing.T) {
	s, cases := manualFixture(t, false)
	c := cases[0]
	if _, err := s.DB.Exec(`CREATE TRIGGER test_deny_manual_audit BEFORE INSERT ON audit_events WHEN NEW.action='confirm_candidate' BEGIN SELECT RAISE(ABORT,'isolated audit failure'); END;`); err != nil {
		t.Fatal(err)
	}
	_, err := s.ConfirmCandidate(context.Background(), testOwner, c.ID, "0:2", c.Version)
	if err == nil {
		t.Fatal("audit fault accepted")
	}
	if scalar(t, s, "SELECT count(*) FROM allocations") != 0 || scalar(t, s, "SELECT remaining FROM sale_balances") != 6 || scalar(t, s, "SELECT allocated FROM sale_balances") != 0 || workflowCurrent(t, s, c.ID) != c || scalar(t, s, "SELECT count(*) FROM audit_events") != 2 {
		t.Fatal("audit failure left partial write")
	}
	t.Log("TC-22 fault injection: audit INSERT denied; allocation absent, remaining=6, allocated=0, case version=3, audit count=2; entire transaction rolled back")
}

func TestTC22_ConfirmationVisibleOutsideCandidatesAndOwnerCanClose(t *testing.T) {
	s, _, caseID, _ := queryFixture(t)
	ctx := context.Background()
	c := assignWorkflow(t, s, caseID)
	c, err := s.TransitionCase(ctx, testOwner, c.ID, "in_progress", c.Version, "")
	if err != nil {
		t.Fatal(err)
	}
	initial, err := s.CaseEvidence(ctx, testOwner, c.ID, PageRequest{PageSize: 50})
	if err != nil || initial.Confirmation != nil || initial.Candidates.Total != 123 {
		t.Fatalf("unexpected initial evidence: %+v %v", initial, err)
	}
	confirmed, err := s.ConfirmCandidate(ctx, testOwner, c.ID, "0:124", c.Version)
	if err != nil {
		t.Fatal(err)
	}
	// A separate connection pool reads persisted state, never UI/audit caches.
	reopened := openTest(t, s.path)
	for _, page := range []int{1, 2, 4} {
		evidence, err := reopened.CaseEvidence(ctx, testOwner, c.ID, PageRequest{Page: page, PageSize: 50})
		if err != nil || evidence.Confirmation == nil {
			t.Fatalf("page %d lost saved confirmation: %v", page, err)
		}
		got := evidence.Confirmation
		if got.Allocation != confirmed.Allocation || got.Sale.ID != "0:124" || got.Sale.Remaining != 2 || got.Sale.SourceRow != 124 || evidence.Candidates.Total != 122 || evidence.Case.Status != "in_progress" {
			t.Fatalf("page %d bad persisted evidence: %+v", page, evidence)
		}
		for _, candidate := range evidence.Candidates.Items {
			if candidate.ID == got.Sale.ID {
				t.Fatal("insufficient saved sale leaked into eligible candidate page")
			}
		}
	}
	_, err = reopened.CaseEvidence(ctx, testOther, c.ID, PageRequest{})
	requireWorkflowCode(t, err, "forbidden")
	_, err = reopened.ConfirmCandidate(ctx, testOwner, c.ID, "0:2", confirmed.Case.Version)
	requireWorkflowCode(t, err, "already_allocated")
	closed, err := reopened.TransitionCase(ctx, testOwner, c.ID, "closed", confirmed.Case.Version, "确认后仍可填写核查结论")
	if err != nil || closed.Conclusion != "确认后仍可填写核查结论" {
		t.Fatalf("owner cannot close after confirmation: %+v %v", closed, err)
	}
	evidence, err := reopened.CaseEvidence(ctx, testOwner, c.ID, PageRequest{})
	if err != nil || evidence.Confirmation == nil || evidence.Case.Status != "closed" {
		t.Fatalf("closed evidence lost confirmation: %+v %v", evidence, err)
	}
	_, err = reopened.TransitionCase(ctx, testOwner, c.ID, "closed", closed.Version, "禁止改写已关闭结论")
	requireWorkflowCode(t, err, "closed")
}

func TestTC22_ManualRejectsPermissionStateVersionIdentity(t *testing.T) {
	for _, tc := range []struct {
		name, sql, code string
		actor           WorkflowActor
		versionDelta    int
	}{
		{"other_owner", "", "forbidden", testOther, 0},
		{"finance_without_ownership", "", "forbidden", testFinance, 0},
		{"no_actor", "", "unauthenticated", WorkflowActor{}, 0},
		{"stale_version", "", "version_conflict", testOwner, -1},
		{"pending", "UPDATE cases SET status='pending'", "invalid_transition", testOwner, 0},
		{"closed", "UPDATE cases SET status='closed'", "closed", testOwner, 0},
		{"customer", "UPDATE normalized_rows SET customer='different' WHERE kind='sale'", "ineligible_candidate", testOwner, 0},
		{"stock", "UPDATE normalized_rows SET stock='different' WHERE kind='sale'", "ineligible_candidate", testOwner, 0},
		{"country", "UPDATE normalized_rows SET country='different' WHERE kind='sale'", "ineligible_candidate", testOwner, 0},
		{"price", "UPDATE normalized_rows SET price='0.10000000000000000000001' WHERE kind='sale'", "ineligible_candidate", testOwner, 0},
		{"same_time", "UPDATE normalized_rows SET source_time='2010-01-01T10:00:00.000000' WHERE kind='sale'", "ineligible_candidate", testOwner, 0},
		{"invalid_time", "UPDATE normalized_rows SET source_time='bad' WHERE kind='sale'", "ineligible_candidate", testOwner, 0},
		{"missing_identity", "UPDATE normalized_rows SET customer='' WHERE kind='cancel'", "ineligible_candidate", testOwner, 0},
		{"positive_cancel", "UPDATE normalized_rows SET quantity=4 WHERE kind='cancel'", "ineligible_candidate", testOwner, 0},
		{"min_int_cancel", "UPDATE normalized_rows SET quantity=-9223372036854775808 WHERE kind='cancel'", "ineligible_candidate", testOwner, 0},
		{"excluded_sale", "UPDATE normalized_rows SET excluded='invalid' WHERE kind='sale'", "ineligible_candidate", testOwner, 0},
		{"balance_mismatch", "UPDATE sale_balances SET original=7,remaining=7", "balance_conflict", testOwner, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, cs := manualFixture(t, false)
			c := cs[0]
			if tc.sql != "" {
				if _, err := s.DB.Exec(tc.sql); err != nil {
					t.Fatal(err)
				}
			}
			beforeRemaining := scalar(t, s, "SELECT remaining FROM sale_balances")
			before := workflowCurrent(t, s, c.ID)
			_, err := s.ConfirmCandidate(context.Background(), tc.actor, c.ID, "0:2", c.Version+tc.versionDelta)
			requireWorkflowCode(t, err, tc.code)
			if scalar(t, s, "SELECT count(*) FROM allocations") != 0 || scalar(t, s, "SELECT count(*) FROM audit_events") != 2 || scalar(t, s, "SELECT remaining FROM sale_balances") != beforeRemaining || workflowCurrent(t, s, c.ID) != before {
				t.Fatal("rejected confirmation wrote state")
			}
			t.Logf("rejected %s -> %s; no business/audit write", tc.name, tc.code)
		})
	}
}

func TestTC23_ConcurrentManualAllocationCannotOverdraw(t *testing.T) {
	s, cs := manualFixture(t, true)
	other := openTest(t, s.path) // Separate Store/connection pool, same isolated database.
	var wg sync.WaitGroup
	start := make(chan struct{})
	results := make(chan error, 2)
	for i, c := range cs {
		wg.Add(1)
		go func(i int, c WorkflowCase) {
			defer wg.Done()
			<-start
			db := s
			if i == 1 {
				db = other
			}
			_, err := db.ConfirmCandidate(context.Background(), testOwner, c.ID, "0:2", c.Version)
			results <- err
		}(i, c)
	}
	close(start)
	wg.Wait()
	close(results)
	success, rejected := 0, 0
	for err := range results {
		if err == nil {
			success++
		} else {
			requireWorkflowCode(t, err, "insufficient_remaining")
			rejected++
		}
	}
	if success != 1 || rejected != 1 || scalar(t, s, "SELECT remaining FROM sale_balances") != 2 || scalar(t, s, "SELECT allocated FROM sale_balances") != 4 || scalar(t, s, "SELECT sum(quantity) FROM allocations") != 4 || scalar(t, s, "SELECT count(*) FROM audit_events WHERE action='confirm_candidate'") != 1 {
		t.Fatalf("overdraw: success=%d rejected=%d", success, rejected)
	}
	t.Logf("TC-23: two cases / two Store pools raced for original=6, each requested=4; success=%d rejected=%d (insufficient_remaining), allocated=4 remaining=2 audit=1", success, rejected)
}

func TestTC23_ConcurrentSameCancelAndRepeatRejected(t *testing.T) {
	s, cs := manualFixture(t, false)
	c := cs[0]
	const workers = 8
	var wg sync.WaitGroup
	start := make(chan struct{})
	results := make(chan error, workers)
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, err := s.ConfirmCandidate(context.Background(), testOwner, c.ID, "0:2", c.Version)
			results <- err
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	success, rejected := 0, 0
	for err := range results {
		if err == nil {
			success++
		} else {
			requireWorkflowCode(t, err, "version_conflict")
			rejected++
		}
	}
	if success != 1 || rejected != workers-1 || scalar(t, s, "SELECT count(*) FROM allocations") != 1 {
		t.Fatalf("same cancel race: %d/%d", success, rejected)
	}
	current := workflowCurrent(t, s, c.ID)
	_, err := s.ConfirmCandidate(context.Background(), testOwner, c.ID, "0:2", current.Version)
	requireWorkflowCode(t, err, "already_allocated")
	if scalar(t, s, "SELECT count(*) FROM audit_events WHERE action='confirm_candidate'") != 1 {
		t.Fatal("repeat confirmation appended audit")
	}
	t.Logf("TC-23: 8 same-cancel contenders -> 1 success, 7 version conflicts; refreshed repeat -> already_allocated; allocation=1 audit=1")
}
