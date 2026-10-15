package store

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"returnsrecon/internal/model"
	"strconv"
	"sync"
	"testing"
)

func fixtureRow(id string, rn int, q int64, at string) model.Row {
	kind := "sale"
	if q < 0 {
		kind = "cancel"
	}
	return model.Row{ID: id, SourceRow: rn, Sheet: "Year 2009-2010", RawJSON: `[["s","isolated fixture only"],null,null,null,null,null,null,null]`, Invoice: id, Stock: "A", Customer: "42", Country: "UK", Price: "0.1", Time: "2010-01-01T" + at + ":00.000000", Quantity: q, Kind: kind}
}
func preparedFixture(t *testing.T, rows []model.Row) (string, Manifest) {
	t.Helper()
	dir := t.TempDir()
	m := Manifest{Scope: "benchmark", CleaningVersion: "cr-test-only", SourceHash: ID("isolated fixture only"), Status: "prepared"}
	m.DatasetID = ID(m.SourceHash + "|" + m.Scope + "|" + m.CleaningVersion)
	f, err := os.Create(filepath.Join(dir, "records.csv"))
	if err != nil {
		t.Fatal(err)
	}
	w := csv.NewWriter(f)
	w.Write([]string{"id", "sheet_index", "sheet", "source_row", "raw_json", "invoice", "stock", "description", "quantity", "time", "price", "customer", "country", "kind", "excluded", "flags", "duplicate_of"})
	for _, r := range rows {
		w.Write([]string{r.ID, strconv.Itoa(r.SheetIndex), r.Sheet, strconv.Itoa(r.SourceRow), r.RawJSON, r.Invoice, r.Stock, r.Description, strconv.FormatInt(r.Quantity, 10), r.Time, r.Price, r.Customer, r.Country, r.Kind, r.Excluded, r.Flags, r.DuplicateOf})
		m.Stats.Raw++
		if r.Excluded != "" {
			m.Stats.Excluded++
		} else {
			m.Stats.Accepted++
			if r.Kind == "sale" {
				m.Stats.AcceptedSales++
			} else {
				m.Stats.AcceptedCancels++
			}
		}
		if r.Kind == "cancel" {
			m.Stats.RawCancels++
			if r.Excluded != "" {
				m.Stats.ExcludedCancels++
			}
		}
	}
	w.Flush()
	if err = w.Error(); err != nil {
		t.Fatal(err)
	}
	f.Close()
	m.RecordsHash, err = hashFile(filepath.Join(dir, "records.csv"))
	if err != nil {
		t.Fatal(err)
	}
	v, _ := json.Marshal(m)
	if err = os.WriteFile(filepath.Join(dir, "manifest.json"), v, 0600); err != nil {
		t.Fatal(err)
	}
	return dir, m
}
func openTest(t *testing.T, path string) *Store {
	t.Helper()
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}
func scalar(t *testing.T, s *Store, query string, args ...any) int {
	t.Helper()
	var n int
	if err := s.DB.QueryRow(query, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestTC18_IdempotencyPreservesSimulatedManualHistory(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "test.sqlite")
	s := openTest(t, path)
	dir, m := preparedFixture(t, []model.Row{fixtureRow("0:2", 2, 4, "08:00"), fixtureRow("0:3", 3, 10, "09:00"), fixtureRow("0:4", 4, -4, "10:00")})
	if _, err := s.Import(ctx, dir); err != nil {
		t.Fatal(err)
	}
	a, err := s.Run(ctx, m.DatasetID, 2)
	if err != nil {
		t.Fatal(err)
	}
	caseID := ID(a.RunID + "|0:4")
	if _, err = s.DB.Exec("UPDATE cases SET assignee='test-operator',status='in_progress',version=7,conclusion='test-only preserved note' WHERE id=?", caseID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.DB.Exec("INSERT INTO audit_events(case_id,actor,action,occurred_at,before_json,after_json) VALUES(?,'test-operator','fixture_manual_note','test timestamp','{}','{\"note\":\"preserve\"}')", caseID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Import(ctx, dir); err != nil {
		t.Fatal(err)
	}
	b, err := s.Run(ctx, m.DatasetID, 8)
	if err != nil {
		t.Fatal(err)
	}
	if a.RunID != b.RunID || !b.Reused || scalar(t, s, "SELECT count(*) FROM cases") != 1 || scalar(t, s, "SELECT count(*) FROM audit_events") != 1 {
		t.Fatalf("not idempotent: %+v %+v", a, b)
	}
	var who, state, note string
	var version int
	if err = s.DB.QueryRow("SELECT assignee,status,version,conclusion FROM cases WHERE id=?", caseID).Scan(&who, &state, &version, &note); err != nil {
		t.Fatal(err)
	}
	if who != "test-operator" || state != "in_progress" || version != 7 || note != "test-only preserved note" {
		t.Fatal("manual fields overwritten")
	}
	// Reopen another connection to prove reuse is durable, not an in-memory shortcut.
	s2 := openTest(t, path)
	c, err := s2.Run(ctx, m.DatasetID, 1)
	if err != nil || !c.Reused || c.RunID != a.RunID {
		t.Fatalf("reopen: %+v %v", c, err)
	}
	t.Logf("actual: run=%s reused=true cases=1 audit_events=1 assignee=%s version=%d note=%s (isolated simulated history)", a.RunID, who, version, note)
}
func TestTC18_ConcurrentSameInputOnePublishedRun(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "test.sqlite")
	s := openTest(t, path)
	s2 := openTest(t, path)
	dir, m := preparedFixture(t, []model.Row{fixtureRow("0:2", 2, 10, "09:00"), fixtureRow("0:3", 3, -4, "10:00")})
	if _, err := s.Import(ctx, dir); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	out := make(chan Summary, 2)
	errs := make(chan error, 2)
	for _, db := range []*Store{s, s2} {
		wg.Add(1)
		go func(db *Store) { defer wg.Done(); v, e := db.Run(ctx, m.DatasetID, 2); out <- v; errs <- e }(db)
	}
	wg.Wait()
	close(out)
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	reused := 0
	for v := range out {
		if v.Reused {
			reused++
		}
	}
	if reused != 1 || scalar(t, s, "SELECT count(*) FROM runs WHERE status='published'") != 1 || scalar(t, s, "SELECT count(*) FROM allocations") != 1 {
		t.Fatal("concurrent runs duplicated")
	}
}
func TestTC14_RejectLeavesNoPartialDatabaseAllocation(t *testing.T) {
	ctx := context.Background()
	s := openTest(t, filepath.Join(t.TempDir(), "test.sqlite"))
	dir, m := preparedFixture(t, []model.Row{fixtureRow("0:2", 2, 10, "09:00"), fixtureRow("0:3", 3, -7, "10:00"), fixtureRow("0:4", 4, -4, "11:00")})
	if _, err := s.Import(ctx, dir); err != nil {
		t.Fatal(err)
	}
	summary, err := s.Run(ctx, m.DatasetID, 1)
	if err != nil {
		t.Fatal(err)
	}
	if scalar(t, s, "SELECT remaining FROM sale_balances") != 3 || scalar(t, s, "SELECT count(*) FROM allocations WHERE cancel_id='0:4'") != 0 || scalar(t, s, "SELECT count(*) FROM audit_events") != 0 {
		t.Fatal("partial update after rejection")
	}
	e, err := s.Evidence(ctx, summary.RunID, "0:4")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("actual persisted rejection=%+v", e)
}
func TestSourceAndDecisionEvidenceRetained(t *testing.T) {
	ctx := context.Background()
	s := openTest(t, filepath.Join(t.TempDir(), "test.sqlite"))
	rows := []model.Row{fixtureRow("0:2", 2, 10, "09:00"), fixtureRow("0:3", 3, -4, "10:00")}
	dir, m := preparedFixture(t, rows)
	if _, err := s.Import(ctx, dir); err != nil {
		t.Fatal(err)
	}
	v, err := s.Run(ctx, m.DatasetID, 1)
	if err != nil {
		t.Fatal(err)
	}
	e, err := s.Evidence(ctx, v.RunID, "0:3")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(e["cancel"].(model.Row), rows[1]) || e["rule_version"] != model.RuleVersion {
		t.Fatalf("lost source/version: %+v", e)
	}
}
func TestFailedPersistenceNeverPublishesAndCanRetry(t *testing.T) {
	ctx := context.Background()
	s := openTest(t, filepath.Join(t.TempDir(), "test.sqlite"))
	dir, m := preparedFixture(t, []model.Row{fixtureRow("0:2", 2, 10, "09:00"), fixtureRow("0:3", 3, -4, "10:00")})
	if _, err := s.Import(ctx, dir); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.Exec(`CREATE TRIGGER test_failure BEFORE INSERT ON candidate_evidence BEGIN SELECT RAISE(ABORT,'injected test-only write failure'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Run(ctx, m.DatasetID, 2); err == nil {
		t.Fatal("injected failure was ignored")
	}
	if scalar(t, s, "SELECT count(*) FROM runs WHERE status='published'") != 0 {
		t.Fatal("failed run published")
	}
	if _, err := s.Evidence(ctx, ID(m.DatasetID+"|"+model.RuleVersion), "0:3"); err == nil {
		t.Fatal("unpublished evidence exposed")
	}
	if _, err := s.DB.Exec("DROP TRIGGER test_failure"); err != nil {
		t.Fatal(err)
	}
	v, err := s.Run(ctx, m.DatasetID, 2)
	if err != nil || v.Reused || v.Matched != 1 || scalar(t, s, "SELECT count(*) FROM runs") != 1 {
		t.Fatalf("retry failed: %+v %v", v, err)
	}
}
func TestPreparedTamperRefused(t *testing.T) {
	ctx := context.Background()
	s := openTest(t, filepath.Join(t.TempDir(), "test.sqlite"))
	dir, _ := preparedFixture(t, []model.Row{fixtureRow("0:2", 2, 10, "09:00")})
	f, e := os.OpenFile(filepath.Join(dir, "records.csv"), os.O_APPEND|os.O_WRONLY, 0600)
	if e != nil {
		t.Fatal(e)
	}
	fmt.Fprintln(f, "tamper")
	f.Close()
	if _, err := s.Import(ctx, dir); err == nil {
		t.Fatal("tampered input accepted")
	}
	if scalar(t, s, "SELECT count(*) FROM datasets") != 0 {
		t.Fatal("tampered dataset inserted")
	}
	// A corrupt published input must also fail its manifest bridge before results publish.
	cleanDir, m := preparedFixture(t, []model.Row{fixtureRow("0:2", 2, 10, "09:00")})
	if _, err := s.Import(ctx, cleanDir); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.Exec("DELETE FROM normalized_rows WHERE dataset_id=?", m.DatasetID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Run(ctx, m.DatasetID, 1); err == nil {
		t.Fatal("corrupt published input passed its manifest conservation bridge")
	}
	if scalar(t, s, "SELECT count(*) FROM runs WHERE status='published'") != 0 {
		t.Fatal("corrupt input published a result")
	}
}
