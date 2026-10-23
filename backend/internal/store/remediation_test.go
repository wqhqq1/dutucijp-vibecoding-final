package store

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"reflect"
	"sync"
	"testing"

	"returnsrecon/internal/access"
	"returnsrecon/internal/model"
)

func remediationFixture(t *testing.T, rows ...model.Row) (*Store, WorkflowCase) {
	t.Helper()
	for i := range rows {
		rows[i] = reviewRaw(t, rows[i])
	}
	s, run, _ := reviewFixture(t, rows)
	return s, reviewProcessing(t, s, run.RunID, rows[len(rows)-1].ID)
}

func remediationRequest(c WorkflowCase, action, request string) RemediationRequest {
	return RemediationRequest{Version: c.Version, Action: action, RequestID: request, Evidence: "隔离夹具：已核对原凭证与业务资料"}
}

func remediateOK(t *testing.T, s *Store, c WorkflowCase, req RemediationRequest) RemediationResult {
	t.Helper()
	out, err := s.RemediateCase(context.Background(), testOwner, c.ID, req)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func remediationMissingCountry() model.Row {
	r := fixtureRow("0:4", 4, -4, "10:00")
	r.Country = ""
	return r
}

func TestFR20_CountryCorrectionMatchesAndPreservesOriginalAfterRestart(t *testing.T) {
	s, c := remediationFixture(t, fixtureRow("0:2", 2, 6, "08:00"), remediationMissingCountry())
	var raw, originalReason string
	if err := s.DB.QueryRow("SELECT s.raw_json FROM source_rows s JOIN normalized_rows n ON n.source_id=s.id WHERE n.id='0:4'").Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if err := s.DB.QueryRow("SELECT reason FROM decisions WHERE cancel_id='0:4'").Scan(&originalReason); err != nil {
		t.Fatal(err)
	}
	req := remediationRequest(c, "correct_return", "country-unique-1")
	req.Changes = map[string]string{"country": "UK"}
	out := remediateOK(t, s, c, req)
	if out.Matched != 1 || out.Reclassified != 0 || out.Case.Status != "closed" || out.Case.Outcome != "matched" || !out.Case.Current || out.Revision != 1 {
		t.Fatalf("unexpected correction: %+v", out)
	}
	if scalar(t, s, "SELECT count(*) FROM allocations WHERE cancel_id='0:4' AND sale_id='0:2' AND quantity=4 AND method='remediation'") != 1 || scalar(t, s, "SELECT remaining FROM sale_balances WHERE sale_id='0:2'") != 2 {
		t.Fatal("correction did not atomically consume the unique sale")
	}
	s2 := openTest(t, s.path)
	if got := workflowCurrent(t, s2, c.ID); got != out.Case {
		t.Fatalf("restart changed current case: %+v", got)
	}
	var country, gotRaw, gotReason string
	if err := s2.DB.QueryRow("SELECT n.country,s.raw_json,d.reason FROM normalized_rows n JOIN source_rows s ON s.id=n.source_id JOIN decisions d ON d.cancel_id=n.id WHERE n.id='0:4'").Scan(&country, &gotRaw, &gotReason); err != nil {
		t.Fatal(err)
	}
	if country != "" || gotRaw != raw || gotReason != originalReason || originalReason != "missing_identity" {
		t.Fatalf("original evidence mutated: country=%q reason=%s raw=%s", country, gotReason, gotRaw)
	}
	row, err := readBusinessRow(context.Background(), s2.DB, c.RunID, c.DatasetID, c.CancelID)
	if err != nil || row.Country != "UK" {
		t.Fatalf("corrected projection lost on reopen: %+v %v", row, err)
	}
	if _, err = s2.Run(context.Background(), c.DatasetID, 1); err != nil {
		t.Fatal(err)
	}
	if scalar(t, s2, "SELECT count(*) FROM allocations") != 1 || scalar(t, s2, "SELECT count(*) FROM remediation_events") != 1 {
		t.Fatal("rerunning initial reconciliation lost remediation history")
	}
}

func TestFR20_ReclassifiedProblemsRequireFreshAssignmentEachRound(t *testing.T) {
	s, c := remediationFixture(t, fixtureRow("0:2", 2, 6, "08:00"), fixtureRow("0:3", 3, 8, "09:00"), remediationMissingCountry())
	req := remediationRequest(c, "correct_return", "country-ambiguous-1")
	req.Changes = map[string]string{"country": "UK"}
	out := remediateOK(t, s, c, req)
	if out.Reclassified != 1 || out.Matched != 0 || len(out.CreatedCases) != 0 {
		t.Fatalf("warehouse result should not expose unassigned child: %+v", out)
	}
	old := workflowCurrent(t, s, c.ID)
	if old.Current || old.Status != "closed" || old.Outcome != "reclassified" || old.Reason != "missing_identity" || old.Country != "" {
		t.Fatalf("previous problem history changed: %+v", old)
	}
	var id string
	if err := s.DB.QueryRow("SELECT id FROM cases WHERE run_id=? AND cancel_id=? AND is_current=1", c.RunID, c.CancelID).Scan(&id); err != nil {
		t.Fatal(err)
	}
	next := workflowCurrent(t, s, id)
	if next.ID == c.ID || next.Generation != 2 || next.PreviousCaseID != c.ID || next.Status != "pending" || next.Assignee != "" || next.Version != 1 || next.Reason != "ambiguous_candidates" || next.Country != "UK" {
		t.Fatalf("new problem was not reset for assignment: %+v", next)
	}
	_, err := s.RemediateCase(context.Background(), testOwner, next.ID, remediationRequest(next, "recheck", "unassigned-child-1"))
	requireWorkflowCode(t, err, "forbidden")
}

func TestFR20_SameReasonWaitAndRecheckCannotCloseUnresolved(t *testing.T) {
	s, c := remediationFixture(t, fixtureRow("0:4", 4, -4, "10:00"))
	req := remediationRequest(c, "wait_materials", "wait-materials-1")
	req.MissingMaterials, req.NextStep = "原销售发票及明细", "向财务核对后补录"
	out := remediateOK(t, s, c, req)
	if !out.Case.Waiting || out.Case.Status != "in_progress" || out.Case.MissingMaterials != req.MissingMaterials || out.Case.NextStep != req.NextStep || out.Case.Version != c.Version+1 {
		t.Fatalf("waiting state not retained: %+v", out)
	}
	_, err := s.TransitionCase(context.Background(), testOwner, c.ID, "closed", out.Case.Version, "资料不足，无法确认")
	requireWorkflowCode(t, err, "unresolved")
	out = remediateOK(t, s, out.Case, remediationRequest(out.Case, "recheck", "same-reason-recheck-1"))
	if out.Case.ID != c.ID || out.Case.Generation != 1 || out.Case.Status != "in_progress" || out.Case.Waiting || out.Reclassified != 0 || scalar(t, s, "SELECT count(*) FROM cases") != 1 {
		t.Fatalf("same reason should retain current problem: %+v", out)
	}
	_, err = s.TransitionCase(context.Background(), testOwner, c.ID, "closed", out.Case.Version, "无凭证")
	requireWorkflowCode(t, err, "unresolved")
}

func TestFR20_MultipleGenerationsPreserveEachPreviousSnapshot(t *testing.T) {
	s, c := remediationFixture(t, fixtureRow("0:2", 2, 6, "08:00"), fixtureRow("0:3", 3, 8, "09:00"), remediationMissingCountry())
	req := remediationRequest(c, "correct_return", "multi-country-1")
	req.Changes = map[string]string{"country": "UK"}
	remediateOK(t, s, c, req)
	var nextID string
	if err := s.DB.QueryRow("SELECT id FROM cases WHERE is_current=1").Scan(&nextID); err != nil {
		t.Fatal(err)
	}
	next := assignWorkflow(t, s, nextID)
	next, err := s.TransitionCase(context.Background(), testOwner, next.ID, "in_progress", next.Version, "")
	if err != nil {
		t.Fatal(err)
	}
	req = remediationRequest(next, "correct_return", "multi-country-2")
	req.Changes = map[string]string{"country": "FR"}
	out := remediateOK(t, s, next, req)
	if out.Reclassified != 1 || out.Revision != 2 || scalar(t, s, "SELECT count(*) FROM cases") != 3 {
		t.Fatalf("second reclassification: %+v", out)
	}
	if scalar(t, s, "SELECT count(*) FROM cases WHERE generation=3 AND reason='no_identity_match' AND status='pending' AND assignee IS NULL AND is_current=1 AND previous_case_id=?", next.ID) != 1 {
		t.Fatal("third generation did not start pending")
	}
	if got := workflowCurrent(t, s, next.ID); got.Country != "UK" || got.Current || got.Reason != "ambiguous_candidates" {
		t.Fatalf("generation two snapshot changed: %+v", got)
	}
}

func TestFR20_SupplementSaleAndDuplicateDocument(t *testing.T) {
	s, c := remediationFixture(t, fixtureRow("0:3", 3, -4, "09:00"), fixtureRow("0:4", 4, -4, "10:00"))
	req := remediationRequest(c, "supplement_sale", "supplement-sale-1")
	req.Sale = &SupplementalSale{Invoice: "S100", Stock: "A", Customer: "42", Country: "UK", Quantity: "4", Time: "2010-01-01T08:00:00", Price: "0.1", DocumentRef: "archive-100", LineRef: "1"}
	out := remediateOK(t, s, c, req)
	if out.SupplementalSaleID == "" || out.Matched != 1 || scalar(t, s, "SELECT count(*) FROM supplemental_sources") != 1 || scalar(t, s, "SELECT count(*) FROM source_rows") != 2 {
		t.Fatalf("supplement not distinct from source: %+v", out)
	}
	// Reassessment consumes the earliest return first; the later origin becomes a fresh quantity problem.
	if scalar(t, s, "SELECT count(*) FROM allocations WHERE cancel_id='0:3' AND sale_id=?", out.SupplementalSaleID) != 1 || out.Case.Outcome != "reclassified" {
		t.Fatalf("earliest-first correction failed: %+v", out)
	}
	var nextID string
	if err := s.DB.QueryRow("SELECT id FROM cases WHERE cancel_id='0:4' AND is_current=1").Scan(&nextID); err != nil {
		t.Fatal(err)
	}
	next := assignWorkflow(t, s, nextID)
	next, err := s.TransitionCase(context.Background(), testOwner, next.ID, "in_progress", next.Version, "")
	if err != nil {
		t.Fatal(err)
	}
	req = remediationRequest(next, "supplement_sale", "supplement-sale-2")
	req.Sale = &SupplementalSale{Invoice: "S200", Stock: "A", Customer: "42", Country: "UK", Quantity: "4", Time: "2010-01-01T08:00:00", Price: "0.1", DocumentRef: "archive-100", LineRef: "1"}
	_, err = s.RemediateCase(context.Background(), testOwner, next.ID, req)
	requireWorkflowCode(t, err, "duplicate_document")
	if scalar(t, s, "SELECT count(*) FROM supplemental_sources") != 1 || scalar(t, s, "SELECT count(*) FROM remediation_events") != 1 {
		t.Fatal("duplicate document left partial writes")
	}
}

func TestFR20_VerifyDuplicateSalesAndIndependent(t *testing.T) {
	for _, disposition := range []string{"duplicate", "independent"} {
		t.Run(disposition, func(t *testing.T) {
			a := fixtureRow("0:2", 2, 6, "08:00")
			a.Invoice = "S100"
			a.Flags = "same_sheet_duplicate"
			b := a
			b.ID = "0:3"
			b.SourceRow = 3
			s, c := remediationFixture(t, a, b, fixtureRow("0:4", 4, -4, "10:00"))
			req := remediationRequest(c, "verify_duplicate", "verify-sales-"+disposition)
			req.Disposition, req.RowIDs, req.CanonicalID = disposition, []string{b.ID, a.ID}, b.ID
			out := remediateOK(t, s, c, req)
			if disposition == "duplicate" {
				if out.Matched != 1 || scalar(t, s, "SELECT count(*) FROM duplicate_sale_members") != 2 || scalar(t, s, "SELECT count(*) FROM allocations WHERE sale_id='0:2'") != 1 {
					t.Fatalf("duplicate group did not use source representative: %+v", out)
				}
			} else if out.Matched != 0 || out.Reclassified != 0 || out.Case.ID != c.ID || out.Case.Status != "in_progress" || scalar(t, s, "SELECT count(*) FROM duplicate_sale_members") != 0 || scalar(t, s, "SELECT count(*) FROM cases WHERE is_current=1 AND reason='ambiguous_candidates'") != 1 {
				t.Fatalf("independent sales silently matched: %+v", out)
			}
			if scalar(t, s, "SELECT count(*) FROM duplicate_verifications WHERE disposition=?", disposition) != 2 {
				t.Fatal("missing verification trail")
			}
		})
	}
}

func TestFR20_VerifyDuplicateReturnsExcludesOnlyConfirmedCopy(t *testing.T) {
	a := fixtureRow("0:3", 3, -4, "10:00")
	a.Invoice = "C100"
	a.Flags = "same_sheet_duplicate"
	b := a
	b.ID = "0:4"
	b.SourceRow = 4
	s, c := remediationFixture(t, fixtureRow("0:2", 2, 6, "08:00"), a, b)
	req := remediationRequest(c, "verify_duplicate", "verify-returns-1")
	req.Disposition, req.RowIDs, req.CanonicalID = "duplicate", []string{a.ID, b.ID}, a.ID
	out := remediateOK(t, s, c, req)
	if out.Excluded != 1 || out.Matched != 1 || out.Case.Outcome != "verified_duplicate" || scalar(t, s, "SELECT count(*) FROM allocations WHERE cancel_id='0:3'") != 1 || scalar(t, s, "SELECT count(*) FROM allocations WHERE cancel_id='0:4'") != 0 {
		t.Fatalf("duplicate return handling: %+v", out)
	}
	if scalar(t, s, "SELECT count(*) FROM normalized_rows WHERE excluded<>''") != 0 || scalar(t, s, "SELECT count(*) FROM business_rows WHERE excluded='verified_duplicate' AND duplicate_of='0:3'") != 1 {
		t.Fatal("duplicate verification changed original or wrong copy")
	}
}

func TestFR20_IdempotentRetryAndDifferentContentConflict(t *testing.T) {
	s, c := remediationFixture(t, fixtureRow("0:2", 2, 6, "08:00"), remediationMissingCountry())
	req := remediationRequest(c, "correct_return", "idempotent-country-1")
	req.Changes = map[string]string{"country": "UK"}
	first := remediateOK(t, s, c, req)
	before := scalar(t, s, "SELECT count(*) FROM audit_events")
	again := remediateOK(t, s, c, req)
	if !reflect.DeepEqual(first, again) || scalar(t, s, "SELECT count(*) FROM audit_events") != before || scalar(t, s, "SELECT count(*) FROM remediation_events") != 1 {
		t.Fatal("retry was not exactly idempotent")
	}
	req.Evidence = "同请求编号却更改核实依据"
	_, err := s.RemediateCase(context.Background(), testOwner, c.ID, req)
	requireWorkflowCode(t, err, "idempotency_conflict")
}

func TestFR20_AuditFailureRollsBackCorrectionAllocationAndProjection(t *testing.T) {
	s, c := remediationFixture(t, fixtureRow("0:2", 2, 6, "08:00"), remediationMissingCountry())
	if _, err := s.DB.Exec("CREATE TRIGGER test_remediation_audit_failure BEFORE INSERT ON audit_events WHEN NEW.action='remediation_match' BEGIN SELECT RAISE(ABORT,'isolated remediation audit failure'); END"); err != nil {
		t.Fatal(err)
	}
	req := remediationRequest(c, "correct_return", "audit-rollback-1")
	req.Changes = map[string]string{"country": "UK"}
	_, err := s.RemediateCase(context.Background(), testOwner, c.ID, req)
	if err == nil {
		t.Fatal("injected audit failure accepted")
	}
	if workflowCurrent(t, s, c.ID) != c || scalar(t, s, "SELECT count(*) FROM business_rows") != 0 || scalar(t, s, "SELECT count(*) FROM remediation_runs") != 0 || scalar(t, s, "SELECT count(*) FROM remediation_events") != 0 || scalar(t, s, "SELECT count(*) FROM case_snapshots") != 0 || scalar(t, s, "SELECT count(*) FROM allocations") != 0 || scalar(t, s, "SELECT remaining FROM sale_balances WHERE sale_id='0:2'") != 6 || scalar(t, s, "SELECT count(*) FROM audit_events") != 2 {
		t.Fatal("failed audit committed partial remediation")
	}
}

func TestFR20_ConcurrentVersionAllowsOnlyOneCorrection(t *testing.T) {
	s, c := remediationFixture(t, fixtureRow("0:4", 4, -4, "10:00"))
	s2 := openTest(t, s.path)
	start := make(chan struct{})
	results := make(chan error, 2)
	var wg sync.WaitGroup
	for i, db := range []*Store{s, s2} {
		wg.Add(1)
		go func(i int, db *Store) {
			defer wg.Done()
			<-start
			req := remediationRequest(c, "wait_materials", fmt.Sprintf("concurrent-wait-%d", i))
			req.MissingMaterials = "缺少原票"
			req.NextStep = "再次索取"
			_, err := db.RemediateCase(context.Background(), testOwner, c.ID, req)
			results <- err
		}(i, db)
	}
	close(start)
	wg.Wait()
	close(results)
	success, conflict := 0, 0
	for err := range results {
		if err == nil {
			success++
		} else {
			requireWorkflowCode(t, err, "version_conflict")
			conflict++
		}
	}
	if success != 1 || conflict != 1 || workflowCurrent(t, s, c.ID).Version != c.Version+1 || scalar(t, s, "SELECT count(*) FROM remediation_events") != 1 {
		t.Fatalf("success=%d conflict=%d", success, conflict)
	}
}

func TestFR20_PermissionsAndChangedCountryScope(t *testing.T) {
	s, c := remediationFixture(t, fixtureRow("0:4", 4, -4, "10:00"))
	req := remediationRequest(c, "correct_return", "permission-country-1")
	req.Changes = map[string]string{"country": "FR"}
	for _, tc := range []struct {
		actor WorkflowActor
		ctx   context.Context
		code  string
	}{
		{WorkflowActor{}, context.Background(), "unauthenticated"},
		{testOther, context.Background(), "forbidden"},
		{testFinance, context.Background(), "forbidden"},
		{testOwner, access.With(context.Background(), access.Profile{Role: "warehouse", Countries: []string{"FR"}}), "not_found"},
		{testOwner, access.With(context.Background(), access.Profile{Role: "warehouse", Countries: []string{"UK"}}), "forbidden"},
	} {
		_, err := s.RemediateCase(tc.ctx, tc.actor, c.ID, req)
		requireWorkflowCode(t, err, tc.code)
	}
	if workflowCurrent(t, s, c.ID) != c || scalar(t, s, "SELECT count(*) FROM remediation_events") != 0 || scalar(t, s, "SELECT count(*) FROM business_rows") != 0 {
		t.Fatal("rejected operation changed business state")
	}
}

func TestFR20_ReassessmentPreservesExistingAllocationsAndLegacyClosedCase(t *testing.T) {
	a := fixtureRow("0:2", 2, 4, "08:00")
	a.Stock = "A"
	b := fixtureRow("0:3", 3, -4, "09:00")
	b.Stock = "A"
	d := fixtureRow("0:5", 5, -4, "10:00")
	d.Stock = "B"
	s, c := remediationFixture(t, a, b, d, remediationMissingCountry())
	legacyID := ID(c.RunID + "|" + d.ID)
	if _, err := s.DB.Exec("UPDATE cases SET status='closed',conclusion='隔离模拟迁移前已结案',outcome='legacy_closed' WHERE id=?", legacyID); err != nil {
		t.Fatal(err)
	}
	legacy := workflowCurrent(t, s, legacyID)
	req := remediationRequest(c, "correct_return", "preserve-history-1")
	req.Changes = map[string]string{"country": "UK"}
	remediateOK(t, s, c, req)
	if workflowCurrent(t, s, legacyID) != legacy || scalar(t, s, "SELECT count(*) FROM allocations WHERE cancel_id='0:3' AND sale_id='0:2' AND quantity=4") != 1 || scalar(t, s, "SELECT remaining FROM sale_balances WHERE sale_id='0:2'") != 0 {
		t.Fatal("reassessment rewrote an existing allocation or historical closure")
	}
}

func TestFR20_ProjectionConsistentAcrossQueriesCSVAndReview(t *testing.T) {
	s, c := remediationFixture(t, fixtureRow("0:2", 2, 6, "08:00"), fixtureRow("0:3", 3, 8, "09:00"), remediationMissingCountry())
	req := remediationRequest(c, "correct_return", "projection-country-1")
	req.Changes = map[string]string{"country": "UK"}
	remediateOK(t, s, c, req)
	f := QueryFilter{RunID: c.RunID, Country: "UK", Kind: "cancel"}
	transactions, err := s.ListTransactions(context.Background(), testFinance, f)
	if err != nil || transactions.Total != 1 || len(transactions.Items) != 1 || transactions.Items[0].Country != "UK" {
		t.Fatalf("effective transaction query: %+v %v", transactions, err)
	}
	problems, err := s.ListCases(context.Background(), testFinance, f)
	if err != nil || problems.Total != 1 || len(problems.Items) != 1 || problems.Items[0].Generation != 2 || problems.Items[0].ID == c.ID {
		t.Fatalf("current case query: %+v %v", problems, err)
	}
	next := problems.Items[0]
	evidence, err := s.CaseEvidence(context.Background(), testFinance, next.ID, PageRequest{})
	if err != nil || evidence.Cancel.Country != "UK" || evidence.OriginalCancel == nil || evidence.OriginalCancel.Country != "" || evidence.EffectiveCancel == nil || evidence.Candidates.Total != 2 || evidence.CurrentAssessment.Reason != "ambiguous_candidates" {
		t.Fatalf("current evidence query: %+v %v", evidence, err)
	}
	var exported bytes.Buffer
	if err = s.ExportCSV(context.Background(), testFinance, f, &exported); err != nil {
		t.Fatal(err)
	}
	cells, err := csv.NewReader(&exported).ReadAll()
	if err != nil || len(cells) != 2 || cells[1][8] != c.CancelID || cells[1][16] != "UK" {
		t.Fatalf("CSV disagrees with transaction query: %v %v", cells, err)
	}
	set, err := s.GenerateReviewSet(context.Background(), testFinance, c.RunID)
	if err != nil {
		t.Fatal(err)
	}
	item := reviewItemFor(t, set, c.CancelID)
	if item.Cancel.Country != "UK" || item.Case.ID != next.ID || item.Case.Generation != next.Generation || !item.Case.Current || len(item.Options) == 0 {
		t.Fatalf("review snapshot disagrees with current generation: %+v", item)
	}
}

func TestFR20_IndependentVerificationSurvivesReviewRegeneration(t *testing.T) {
	a := fixtureRow("0:2", 2, 6, "08:00")
	a.Invoice = "S100"
	a.Flags = "same_sheet_duplicate"
	b := a
	b.ID = "0:3"
	b.SourceRow = 3
	s, c := remediationFixture(t, a, b, fixtureRow("0:4", 4, -4, "10:00"))
	req := remediationRequest(c, "verify_duplicate", "independent-review-1")
	req.Disposition, req.RowIDs = "independent", []string{a.ID, b.ID}
	remediateOK(t, s, c, req)
	set, err := s.GenerateReviewSet(context.Background(), testOwner, c.RunID)
	if err != nil {
		t.Fatal(err)
	}
	item := reviewItemFor(t, set, c.CancelID)
	for _, option := range item.Options {
		if option.Duplicate != nil {
			t.Fatalf("verified independent sales were proposed as a duplicate group again: %+v", option)
		}
	}
	if len(item.Options) < 2 {
		t.Fatalf("independent sales should remain two options: %+v", item)
	}
}

func TestFR20_HistoricalEvidenceDoesNotLeakCurrentCountry(t *testing.T) {
	a := fixtureRow("0:2", 2, 6, "08:00")
	a.Country = "FR"
	b := fixtureRow("0:3", 3, 8, "09:00")
	b.Country = "FR"
	s, c := remediationFixture(t, a, b, fixtureRow("0:4", 4, -4, "10:00"))
	req := remediationRequest(c, "correct_return", "history-permission-1")
	req.Changes = map[string]string{"country": "FR"}
	remediateOK(t, s, c, req)
	ctx := access.With(context.Background(), access.Profile{Role: "warehouse", Countries: []string{"UK"}})
	evidence, err := s.CaseEvidence(ctx, testOwner, c.ID, PageRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if evidence.Cancel.Country != "UK" || evidence.EffectiveCancel != nil {
		t.Fatalf("historical row selection leaked current country: %+v", evidence)
	}
	for _, candidate := range evidence.Candidates.Items {
		if !access.Allows(ctx, candidate.Country) {
			t.Errorf("historical candidates leaked country %s from current generation", candidate.Country)
		}
	}
	for _, event := range evidence.Remediations {
		var before, after model.Row
		if err = json.Unmarshal(event.Before, &before); err != nil {
			t.Fatal(err)
		}
		if err = json.Unmarshal(event.After, &after); err != nil {
			t.Fatal(err)
		}
		if !access.Allows(ctx, before.Country) || !access.Allows(ctx, after.Country) {
			t.Errorf("historical remediation event exposed out-of-scope row: before=%s after=%s", before.Country, after.Country)
		}
	}
}

func TestFR20_SupplementalDuplicateGroupRetainsOneCapacityAndBothSourceMembers(t *testing.T) {
	a := fixtureRow("0:2", 2, 4, "08:00")
	a.Invoice = "S100"
	s, c := remediationFixture(t, a, fixtureRow("0:3", 3, -4, "09:00"), fixtureRow("0:4", 4, -4, "10:00"))
	req := remediationRequest(c, "supplement_sale", "supplement-identical-1")
	req.Sale = &SupplementalSale{Invoice: a.Invoice, Stock: a.Stock, Description: a.Description, Customer: a.Customer, Country: a.Country, Quantity: "4", Time: a.Time, Price: a.Price, DocumentRef: "duplicate-proof", LineRef: "1"}
	out := remediateOK(t, s, c, req)
	if out.Matched != 0 || scalar(t, s, "SELECT count(*) FROM allocations") != 1 {
		t.Fatalf("identical supplemental sale added usable capacity: %+v", out)
	}
	var currentID string
	if err := s.DB.QueryRow("SELECT id FROM cases WHERE cancel_id=? AND is_current=1", c.CancelID).Scan(&currentID); err != nil {
		t.Fatal(err)
	}
	next := assignWorkflow(t, s, currentID)
	next, err := s.TransitionCase(context.Background(), testOwner, next.ID, "in_progress", next.Version, "")
	if err != nil {
		t.Fatal(err)
	}
	req = remediationRequest(next, "verify_duplicate", "supplement-identical-2")
	req.Disposition, req.RowIDs, req.CanonicalID = "duplicate", []string{a.ID, out.SupplementalSaleID}, a.ID
	remediateOK(t, s, next, req)
	if scalar(t, s, "SELECT count(*) FROM allocations") != 1 || scalar(t, s, "SELECT sum(allocated) FROM sale_balances") != 4 {
		t.Fatal("duplicate supplemental sale bypassed one-sale capacity")
	}
	group, err := s.duplicateBalance(context.Background(), c.RunID, a.ID)
	if err != nil || group == nil || group.Original != 4 || group.Remaining != 0 || len(group.Members) != 2 {
		t.Fatalf("duplicate group lost supplemental source member: %+v %v", group, err)
	}
}

func TestFR20_IdempotentReplayRechecksSessionAndCountry(t *testing.T) {
	s, c := remediationFixture(t, fixtureRow("0:2", 2, 6, "08:00"), remediationMissingCountry())
	req := remediationRequest(c, "correct_return", "replay-authorized-1")
	req.Changes = map[string]string{"country": "UK"}
	remediateOK(t, s, c, req)
	blocked := WithWriteAuthorization(context.Background(), func(context.Context, *sql.Conn) error { return fmt.Errorf("isolated expired session") })
	if _, err := s.RemediateCase(blocked, testOwner, c.ID, req); err == nil {
		t.Fatal("idempotent replay bypassed session recheck")
	}
	ctx := access.With(context.Background(), access.Profile{Role: "warehouse", Countries: []string{"FR"}})
	_, err := s.RemediateCase(ctx, testOwner, c.ID, req)
	requireWorkflowCode(t, err, "not_found")
	if scalar(t, s, "SELECT count(*) FROM remediation_events") != 1 {
		t.Fatal("rejected replay mutated event history")
	}
}

func TestFR20_HistoricalCandidatesRespectCountryScope(t *testing.T) {
	a := fixtureRow("0:2", 2, 6, "08:00")
	a.Country = "FR"
	b := fixtureRow("0:3", 3, 8, "09:00")
	b.Country = "FR"
	s, c := remediationFixture(t, a, b, fixtureRow("0:4", 4, -4, "10:00"))
	req := remediationRequest(c, "correct_return", "history-candidates-1")
	req.Changes = map[string]string{"country": "FR"}
	remediateOK(t, s, c, req)
	ctx := access.With(context.Background(), access.Profile{Role: "warehouse", Countries: []string{"UK"}})
	page, err := s.ListCandidates(ctx, testOwner, c.ID, PageRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 0 || len(page.Items) != 0 {
		t.Fatalf("historical UK case exposed FR candidates: %+v", page)
	}
}

func TestFR20_CurrentEvidenceDoesNotLeakOriginalCountry(t *testing.T) {
	a := fixtureRow("0:2", 2, 6, "08:00")
	a.Country = "FR"
	b := fixtureRow("0:3", 3, 8, "09:00")
	b.Country = "FR"
	s, c := remediationFixture(t, a, b, fixtureRow("0:4", 4, -4, "10:00"))
	req := remediationRequest(c, "correct_return", "current-origin-scope-1")
	req.Changes = map[string]string{"country": "FR"}
	remediateOK(t, s, c, req)
	var nextID string
	if err := s.DB.QueryRow("SELECT id FROM cases WHERE is_current=1").Scan(&nextID); err != nil {
		t.Fatal(err)
	}
	ctx := access.With(context.Background(), access.Profile{Role: "finance", Countries: []string{"FR"}})
	evidence, err := s.CaseEvidence(ctx, testFinance, nextID, PageRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if evidence.OriginalCancel != nil && !access.Allows(ctx, evidence.OriginalCancel.Country) {
		t.Fatalf("FR-only user received original UK row: %+v", evidence.OriginalCancel)
	}
}

func TestFR20_QuantityCorrectionInvalidatesOldReviewAndUsesEffectiveAmount(t *testing.T) {
	s, c := remediationFixture(t, fixtureRow("0:2", 2, 6, "08:00"), fixtureRow("0:3", 3, 8, "09:00"), fixtureRow("0:4", 4, -4, "10:00"))
	ctx := context.Background()
	old, err := s.GenerateReviewSet(ctx, testOwner, c.RunID)
	if err != nil {
		t.Fatal(err)
	}
	preview, err := s.PreviewBatch(ctx, testOwner, BatchRequest{SetID: old.ID, Action: "confirm", All: true}, WorkflowActor{})
	if err != nil {
		t.Fatal(err)
	}
	req := remediationRequest(c, "correct_return", "quantity-projection-1")
	req.Changes = map[string]string{"quantity": "5"}
	out := remediateOK(t, s, c, req)
	if out.Case.ID != c.ID || out.Case.Status != "in_progress" || out.Matched != 0 {
		t.Fatalf("quantity correction unexpectedly changed problem: %+v", out)
	}
	_, err = s.ListReview(ctx, testOwner, old.ID, QueryFilter{}, false)
	requireWorkflowCode(t, err, "materials_changed")
	stale, err := s.CommitBatch(ctx, testOwner, preview.ID)
	if err != nil || stale.Succeeded != 0 || stale.Skipped != 1 || len(stale.Groups) != 1 || stale.Groups[0].Code != "materials_changed" {
		t.Fatalf("stale preview was not rejected: %+v %v", stale, err)
	}
	fresh, err := s.GenerateReviewSet(ctx, testOwner, c.RunID)
	if err != nil {
		t.Fatal(err)
	}
	preview, err = s.PreviewBatch(ctx, testOwner, BatchRequest{SetID: fresh.ID, Action: "confirm", All: true}, WorkflowActor{})
	if err != nil {
		t.Fatal(err)
	}
	committed, err := s.CommitBatch(ctx, testOwner, preview.ID)
	if err != nil || committed.Succeeded != 1 {
		t.Fatalf("effective review confirmation failed: %+v %v", committed, err)
	}
	if scalar(t, s, "SELECT quantity FROM allocations WHERE cancel_id='0:4'") != 5 || scalar(t, s, "SELECT remaining FROM sale_balances WHERE sale_id='0:3'") != 3 {
		t.Fatal("manual batch used original quantity rather than corrected quantity")
	}
	var amount string
	if err = s.DB.QueryRow("SELECT amount FROM allocations WHERE cancel_id='0:4'").Scan(&amount); err != nil || amount != "0.5" {
		t.Fatalf("corrected amount=%s %v", amount, err)
	}
	allocations, err := s.ListAllocations(ctx, testFinance, QueryFilter{RunID: c.RunID})
	if err != nil || allocations.Total != 1 || allocations.Items[0].Quantity != 5 {
		t.Fatalf("allocation query lost corrected manual result: %+v %v", allocations, err)
	}
}

func TestFR20_RemediationAuditPreservesCaseSnapshots(t *testing.T) {
	s, c := remediationFixture(t, fixtureRow("0:2", 2, 6, "08:00"), remediationMissingCountry())
	req := remediationRequest(c, "correct_return", "audit-case-snapshot-1")
	req.Changes = map[string]string{"country": "UK"}
	out := remediateOK(t, s, c, req)
	history, err := s.WorkflowHistory(context.Background(), testOwner, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	var final *WorkflowAudit
	for i := range history {
		if history[i].Action == "correct_return" {
			final = &history[i]
		}
	}
	if final == nil || final.Before != c || final.After != out.Case {
		t.Fatalf("remediation audit lost full before/after case snapshots: %+v", final)
	}
}

func TestFR20_HistoricalWorkflowAuditDoesNotLeakCurrentCountry(t *testing.T) {
	a := fixtureRow("0:2", 2, 6, "08:00")
	a.Country = "FR"
	b := fixtureRow("0:3", 3, 8, "09:00")
	b.Country = "FR"
	s, c := remediationFixture(t, a, b, fixtureRow("0:4", 4, -4, "10:00"))
	req := remediationRequest(c, "correct_return", "audit-country-scope-1")
	req.Changes = map[string]string{"country": "FR"}
	remediateOK(t, s, c, req)
	ctx := access.With(context.Background(), access.Profile{Role: "warehouse", Countries: []string{"UK"}})
	history, err := s.WorkflowHistory(ctx, testOwner, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range history {
		var detail struct {
			Remediation struct {
				Before *model.Row `json:"before"`
				After  *model.Row `json:"after"`
			} `json:"remediation"`
		}
		if err = json.Unmarshal(event.AfterEvidence, &detail); err != nil {
			t.Fatal(err)
		}
		for _, row := range []*model.Row{detail.Remediation.Before, detail.Remediation.After} {
			if row != nil && !access.Allows(ctx, row.Country) {
				t.Errorf("historical workflow event %s leaked row country %s", event.Action, row.Country)
			}
		}
	}
}

func TestFR20_IdempotentReplayFiltersCreatedCasesAfterCountryNarrowing(t *testing.T) {
	a := fixtureRow("0:2", 2, 6, "08:00")
	a.Country = "FR"
	b := fixtureRow("0:3", 3, 8, "09:00")
	b.Country = "FR"
	s, run, _ := reviewFixture(t, []model.Row{reviewRaw(t, a), reviewRaw(t, b), reviewRaw(t, fixtureRow("0:4", 4, -4, "10:00"))})
	c, err := s.AssignCase(context.Background(), testFinance, testFinance, ID(run.RunID+"|0:4"))
	if err != nil {
		t.Fatal(err)
	}
	c, err = s.TransitionCase(context.Background(), testFinance, c.ID, "in_progress", c.Version, "")
	if err != nil {
		t.Fatal(err)
	}
	req := remediationRequest(c, "correct_return", "replay-created-scope-1")
	req.Changes = map[string]string{"country": "FR"}
	first, err := s.RemediateCase(context.Background(), testFinance, c.ID, req)
	if err != nil {
		t.Fatal(err)
	}
	if len(first.CreatedCases) != 1 || first.CreatedCases[0].Country != "FR" {
		t.Fatalf("global initial result: %+v", first)
	}
	ctx := access.With(context.Background(), access.Profile{Role: "finance", Countries: []string{"UK"}})
	again, err := s.RemediateCase(ctx, testFinance, c.ID, req)
	if err != nil {
		t.Fatal(err)
	}
	if len(again.CreatedCases) != 0 || again.Revision != first.Revision || scalar(t, s, "SELECT count(*) FROM remediation_events") != 1 {
		t.Fatalf("idempotent replay leaked out-of-scope created problem: %+v", again)
	}
}

func TestFR20_LegacyUniqueCaseMigrationPreservesAuditForeignKeysAndRestart(t *testing.T) {
	s, c := remediationFixture(t, fixtureRow("0:4", 4, -4, "10:00"))
	ctx := context.Background()
	if _, err := s.DB.Exec("UPDATE cases SET status='closed',conclusion='隔离模拟旧版已结案资料' WHERE id=?", c.ID); err != nil {
		t.Fatal(err)
	}
	var beforeAudit string
	if err := s.DB.QueryRow("SELECT group_concat(before_json||after_json,'|') FROM audit_events ORDER BY id").Scan(&beforeAudit); err != nil {
		t.Fatal(err)
	}
	conn, err := s.DB.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{
		"PRAGMA foreign_keys=OFF", "BEGIN IMMEDIATE",
		`CREATE TABLE legacy_cases(id TEXT PRIMARY KEY,run_id TEXT NOT NULL,cancel_id TEXT NOT NULL,reason TEXT NOT NULL,status TEXT NOT NULL DEFAULT 'pending' CHECK(status IN ('pending','in_progress','closed')),assignee TEXT,version INTEGER NOT NULL DEFAULT 1,conclusion TEXT NOT NULL DEFAULT '',assigned_at TEXT NOT NULL DEFAULT '',assigned_by TEXT NOT NULL DEFAULT '',UNIQUE(run_id,cancel_id),FOREIGN KEY(run_id,cancel_id) REFERENCES decisions(run_id,cancel_id) ON DELETE CASCADE)`,
		`INSERT INTO legacy_cases SELECT id,run_id,cancel_id,reason,status,assignee,version,conclusion,assigned_at,assigned_by FROM cases`,
		"DROP TABLE cases", "ALTER TABLE legacy_cases RENAME TO cases", "COMMIT", "PRAGMA foreign_keys=ON",
	} {
		if _, err = conn.ExecContext(ctx, query); err != nil {
			conn.Close()
			t.Fatal(err)
		}
	}
	conn.Close()
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s2 := openTest(t, s.path)
	after := workflowCurrent(t, s2, c.ID)
	if after.Status != "closed" || after.Conclusion != "隔离模拟旧版已结案资料" || after.Generation != 1 || !after.Current || after.Outcome != "legacy_closed" || after.Assignee != c.Assignee || after.Version != c.Version {
		t.Fatalf("legacy closure changed during migration: %+v", after)
	}
	var afterAudit string
	if err = s2.DB.QueryRow("SELECT group_concat(before_json||after_json,'|') FROM audit_events ORDER BY id").Scan(&afterAudit); err != nil {
		t.Fatal(err)
	}
	if beforeAudit != afterAudit || scalar(t, s2, "SELECT count(*) FROM audit_events") != 2 {
		t.Fatal("legacy audit content changed")
	}
	if scalar(t, s2, "SELECT count(*) FROM pragma_foreign_key_check") != 0 || scalar(t, s2, "SELECT count(*) FROM pragma_foreign_key_list('audit_events') WHERE \"table\"='cases'") != 1 {
		t.Fatal("migration broke audit or decision foreign keys")
	}
	if _, err = s2.DB.Exec("INSERT INTO audit_events(case_id,actor,action,occurred_at,before_json,after_json) VALUES('missing-case','isolated','test','now','{}','{}')"); err == nil {
		t.Fatal("migrated audit accepted invalid case reference")
	}
	s3 := openTest(t, s.path)
	if again := workflowCurrent(t, s3, c.ID); again != after || scalar(t, s3, "SELECT count(*) FROM pragma_foreign_key_check") != 0 {
		t.Fatalf("second startup changed migrated history: %+v", again)
	}
}
