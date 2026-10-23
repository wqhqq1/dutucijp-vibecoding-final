package store

import (
	"bytes"
	"context"
	"reflect"
	"testing"
)

func sparseEffectiveCount(t *testing.T, s *Store, run string) int {
	t.Helper()
	var count int
	if err := s.DB.QueryRow(s.BusinessSQL(context.Background(), run, "SELECT count(*) FROM normalized_rows")).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

func TestFR20Sparse_OneOverrideKeepsCompleteQueriesCSVAndRestart(t *testing.T) {
	s, c := remediationFixture(t, fixtureRow("0:2", 2, 6, "08:00"), fixtureRow("0:3", 3, 8, "09:00"), remediationMissingCountry())
	req := remediationRequest(c, "correct_return", "sparse-country-change-1")
	req.Changes = map[string]string{"country": "UK"}
	out := remediateOK(t, s, c, req)
	if out.Reclassified != 1 || scalar(t, s, "SELECT count(*) FROM business_rows") != 1 || scalar(t, s, "SELECT sparse FROM remediation_runs WHERE run_id=?", c.RunID) != 1 || sparseEffectiveCount(t, s, c.RunID) != 3 {
		t.Fatalf("sparse initialization copied or omitted base rows: %+v", out)
	}
	f := QueryFilter{RunID: c.RunID, Country: "UK"}
	before, err := s.ListTransactions(context.Background(), testFinance, f)
	if err != nil || before.Total != 3 || len(before.Items) != 3 {
		t.Fatalf("complete effective query: %+v %v", before, err)
	}
	var csvBefore, csvAfter bytes.Buffer
	if err = s.ExportCSV(context.Background(), testFinance, f, &csvBefore); err != nil {
		t.Fatal(err)
	}
	s2 := openTest(t, s.path)
	after, err := s2.ListTransactions(context.Background(), testFinance, f)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatalf("restart changed effective query: %+v %v", after, err)
	}
	if err = s2.ExportCSV(context.Background(), testFinance, f, &csvAfter); err != nil || csvBefore.String() != csvAfter.String() {
		t.Fatalf("restart changed effective CSV: %v", err)
	}
	if scalar(t, s2, "SELECT count(*) FROM business_rows") != 1 || sparseEffectiveCount(t, s2, c.RunID) != 3 {
		t.Fatal("restart materialized unchanged rows")
	}
}

func TestFR20Sparse_EqualCorrectionAndRecheckDoNotCopyUnchangedRows(t *testing.T) {
	s, c := remediationFixture(t, fixtureRow("0:2", 2, 6, "08:00"), fixtureRow("0:3", 3, 8, "09:00"), fixtureRow("0:4", 4, -4, "10:00"))
	req := remediationRequest(c, "correct_return", "sparse-equal-stock-1")
	req.Changes = map[string]string{"stock": "A"}
	out := remediateOK(t, s, c, req)
	if out.Revision != 1 || out.Case.ID != c.ID || scalar(t, s, "SELECT count(*) FROM business_rows") != 1 || sparseEffectiveCount(t, s, c.RunID) != 3 {
		t.Fatalf("same-value correction did not execute sparsely: %+v", out)
	}
	out = remediateOK(t, s, out.Case, remediationRequest(out.Case, "recheck", "sparse-next-recheck-1"))
	if out.Revision != 2 || scalar(t, s, "SELECT count(*) FROM business_rows") != 1 || scalar(t, s, "SELECT count(*) FROM remediation_events") != 2 {
		t.Fatalf("non-idempotent recheck expanded override: %+v", out)
	}
}

func TestFR20Sparse_OverridesDoNotLeakIntoAnotherRun(t *testing.T) {
	s, c := remediationFixture(t, fixtureRow("0:2", 2, 6, "08:00"), remediationMissingCountry())
	second := ID("isolated-sparse-second-run")
	if _, err := s.DB.Exec(`INSERT INTO runs SELECT ?,dataset_id,'isolated-second-rule',status,started_at,completed_at,summary_json,error FROM runs WHERE id=?`, second, c.RunID); err != nil {
		t.Fatal(err)
	}
	req := remediationRequest(c, "correct_return", "sparse-isolate-run-1")
	req.Changes = map[string]string{"country": "UK"}
	remediateOK(t, s, c, req)
	changed, err := readBusinessRow(context.Background(), s.DB, c.RunID, c.DatasetID, c.CancelID)
	if err != nil || changed.Country != "UK" {
		t.Fatalf("changed run: %+v %v", changed, err)
	}
	original, err := readBusinessRow(context.Background(), s.DB, second, c.DatasetID, c.CancelID)
	if err != nil || original.Country != "" || sparseEffectiveCount(t, s, second) != 2 {
		t.Fatalf("other run inherited override: %+v %v", original, err)
	}
	// The second run can independently initialize sparse mode with no overrides.
	if _, err = s.DB.Exec("INSERT INTO remediation_runs(run_id,initialized,sparse) VALUES(?,1,1)", second); err != nil {
		t.Fatal(err)
	}
	original, err = readBusinessRow(context.Background(), s.DB, second, c.DatasetID, c.CancelID)
	if err != nil || original.Country != "" || sparseEffectiveCount(t, s, second) != 2 {
		t.Fatalf("second sparse run inherited override: %+v %v", original, err)
	}
}

func TestFR20Sparse_SupplementSourceAvailableWithOnlyOneStoredRow(t *testing.T) {
	s, c := remediationFixture(t, fixtureRow("0:4", 4, -4, "10:00"))
	req := remediationRequest(c, "supplement_sale", "sparse-new-sale-1")
	req.Sale = &SupplementalSale{Invoice: "S100", Stock: "A", Customer: "42", Country: "UK", Quantity: "4", Time: "2010-01-01T08:00:00", Price: "0.1", DocumentRef: "isolated-proof", LineRef: "1"}
	out := remediateOK(t, s, c, req)
	if out.Matched != 1 || scalar(t, s, "SELECT count(*) FROM business_rows") != 1 || sparseEffectiveCount(t, s, c.RunID) != 2 {
		t.Fatalf("supplement copied original base: %+v", out)
	}
	evidence, err := s.CaseEvidence(context.Background(), testOwner, c.ID, PageRequest{})
	if err != nil || evidence.Confirmation == nil || evidence.Confirmation.Sale.ID != out.SupplementalSaleID || evidence.Confirmation.Sale.Sheet == "" || evidence.Confirmation.Sale.RawJSON == "" {
		t.Fatalf("supplemental source not resolved: %+v %v", evidence, err)
	}
	page, err := s.ListTransactions(context.Background(), testFinance, QueryFilter{RunID: c.RunID})
	if err != nil || page.Total != 2 || len(page.Items) != 2 {
		t.Fatalf("base plus supplemental transaction scope: %+v %v", page, err)
	}
}

func TestFR20Sparse_LegacyFullProjectionStillAuthoritative(t *testing.T) {
	s, c := remediationFixture(t, fixtureRow("0:2", 2, 6, "08:00"), fixtureRow("0:3", 3, 8, "09:00"), fixtureRow("0:4", 4, -4, "10:00"))
	if _, err := s.DB.Exec("INSERT INTO remediation_runs(run_id,initialized,sparse) VALUES(?,1,0)", c.RunID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.Exec("INSERT INTO business_rows SELECT ?,n.* FROM normalized_rows n WHERE dataset_id=?", c.RunID, c.DatasetID); err != nil {
		t.Fatal(err)
	}
	if err := ensureBusinessIndexes(context.Background(), s.DB); err != nil {
		t.Fatal(err)
	}
	req := remediationRequest(c, "correct_return", "legacy-full-equal-1")
	req.Changes = map[string]string{"stock": "A"}
	out := remediateOK(t, s, c, req)
	if out.Case.ID != c.ID || scalar(t, s, "SELECT sparse FROM remediation_runs WHERE run_id=?", c.RunID) != 0 || scalar(t, s, "SELECT count(*) FROM business_rows") != 3 || sparseEffectiveCount(t, s, c.RunID) != 3 {
		t.Fatalf("legacy full mode changed: %+v", out)
	}
	s2 := openTest(t, s.path)
	if sparseEffectiveCount(t, s2, c.RunID) != 3 || scalar(t, s2, "SELECT sparse FROM remediation_runs WHERE run_id=?", c.RunID) != 0 {
		t.Fatal("restart converted legacy full mode")
	}
}

func TestFR20Sparse_AuditFailureRollsBackOverlayInitialization(t *testing.T) {
	s, c := remediationFixture(t, fixtureRow("0:2", 2, 6, "08:00"), fixtureRow("0:3", 3, 8, "09:00"), fixtureRow("0:4", 4, -4, "10:00"))
	if _, err := s.DB.Exec("CREATE TRIGGER isolated_sparse_audit_failure BEFORE INSERT ON audit_events WHEN NEW.action='correct_return' BEGIN SELECT RAISE(ABORT,'isolated sparse audit failure'); END"); err != nil {
		t.Fatal(err)
	}
	req := remediationRequest(c, "correct_return", "sparse-audit-fail-1")
	req.Changes = map[string]string{"stock": "A"}
	_, err := s.RemediateCase(context.Background(), testOwner, c.ID, req)
	if err == nil {
		t.Fatal("audit failure accepted")
	}
	if workflowCurrent(t, s, c.ID) != c || scalar(t, s, "SELECT count(*) FROM business_rows") != 0 || scalar(t, s, "SELECT count(*) FROM remediation_runs") != 0 || scalar(t, s, "SELECT count(*) FROM remediation_events") != 0 || sparseEffectiveCount(t, s, c.RunID) != 3 {
		t.Fatal("failed correction persisted sparse overlay or changed base visibility")
	}
}

func TestFR20Sparse_OldRemediationRunsWithoutSparseColumnMigratesAsFull(t *testing.T) {
	s, c := remediationFixture(t, fixtureRow("0:2", 2, 6, "08:00"), fixtureRow("0:3", 3, 8, "09:00"), fixtureRow("0:4", 4, -4, "10:00"))
	if _, err := s.DB.Exec("INSERT INTO remediation_runs(run_id,initialized,sparse) VALUES(?,1,0)", c.RunID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.Exec("INSERT INTO business_rows SELECT ?,n.* FROM normalized_rows n WHERE dataset_id=?", c.RunID, c.DatasetID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.Exec("ALTER TABLE remediation_runs DROP COLUMN sparse"); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s2 := openTest(t, s.path)
	if scalar(t, s2, "SELECT sparse FROM remediation_runs WHERE run_id=?", c.RunID) != 0 || scalar(t, s2, "SELECT count(*) FROM business_rows") != 3 || sparseEffectiveCount(t, s2, c.RunID) != 3 {
		t.Fatal("old full projection was mistaken for sparse overrides")
	}
	req := remediationRequest(c, "correct_return", "migrated-full-equal-1")
	req.Changes = map[string]string{"stock": "A"}
	out := remediateOK(t, s2, c, req)
	if out.Case.ID != c.ID || scalar(t, s2, "SELECT sparse FROM remediation_runs WHERE run_id=?", c.RunID) != 0 || scalar(t, s2, "SELECT count(*) FROM business_rows") != 3 {
		t.Fatalf("migration changed legacy correction semantics: %+v", out)
	}
}
