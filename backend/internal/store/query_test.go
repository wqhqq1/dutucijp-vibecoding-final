package store

import (
	"bytes"
	"context"
	"encoding/csv"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"testing"

	"returnsrecon/internal/model"
)

func queryFixture(t *testing.T) (*Store, string, string, string) {
	t.Helper()
	s := openTest(t, filepath.Join(t.TempDir(), "query.sqlite"))
	for range 2 {
		if err := s.EnsureQueryIndexes(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	rows := []model.Row{}
	for i := 2; i < 125; i++ {
		rows = append(rows, fixtureRow(fmt.Sprintf("0:%d", i), i, 6, "08:00"))
	}
	rows = append(rows, fixtureRow("0:125", 125, -4, "10:00"))
	for _, r := range []model.Row{fixtureRow("0:126", 126, 6, "08:00"), fixtureRow("0:127", 127, 6, "09:00"), fixtureRow("0:128", 128, -4, "10:00")} {
		r.Customer = "other-customer"
		rows = append(rows, r)
	}
	dir, m := preparedFixture(t, rows)
	if _, err := s.Import(context.Background(), dir); err != nil {
		t.Fatal(err)
	}
	run, err := s.Run(context.Background(), m.DatasetID, 2)
	if err != nil {
		t.Fatal(err)
	}
	return s, run.RunID, ID(run.RunID + "|0:125"), ID(run.RunID + "|0:128")
}

func TestTC16_ServerFilteringPaginationAndDateBoundaries(t *testing.T) {
	s, run, _, _ := queryFixture(t)
	ctx := context.Background()
	first, err := s.ListTransactions(ctx, testFinance, QueryFilter{RunID: run, Customer: "42", PageSize: 9999})
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.ListTransactions(ctx, testFinance, QueryFilter{RunID: run, Customer: "42", Page: 2, PageSize: 9999})
	if err != nil {
		t.Fatal(err)
	}
	if first.Total != 124 || first.PageSize != 100 || len(first.Items) != 100 || len(second.Items) != 24 {
		t.Fatalf("pagination=%+v/%+v", first, second)
	}
	seen := map[string]bool{}
	for _, p := range []TransactionPage{first, second} {
		for _, r := range p.Items {
			if seen[r.ID] {
				t.Fatalf("duplicate row %s", r.ID)
			}
			seen[r.ID] = true
			if r.Customer != "42" || r.SourceRow < 2 || r.SourceHash == "" || r.RuleVersion != model.RuleVersion || r.DatasetID == "" || r.Scope != "benchmark" {
				t.Fatalf("filter/source provenance missing: %+v", r)
			}
		}
	}
	if len(seen) != 124 {
		t.Fatal("page loss")
	}
	defaultPage, err := s.ListTransactions(ctx, testFinance, QueryFilter{RunID: run})
	if err != nil || len(defaultPage.Items) != 50 || defaultPage.PageSize != 50 {
		t.Fatalf("default page %d %v", len(defaultPage.Items), err)
	}
	datePage, err := s.ListTransactions(ctx, testFinance, QueryFilter{RunID: run, DateFrom: "2010-01-01T08:00:00", DateTo: "2010-01-01T09:00:00", Customer: "42", Kind: "sale"})
	if err != nil || datePage.Total != 123 {
		t.Fatalf("inclusive/exclusive date result %d %v", datePage.Total, err)
	}
	one, err := s.ListTransactions(ctx, testFinance, QueryFilter{RunID: run, Customer: "42", Invoice: "0:125", Stock: "A", DateFrom: "2010-01-01", DateTo: "2010-01-02"})
	if err != nil || one.Total != 1 || one.Items[0].SourceRow != 125 {
		t.Fatalf("combined filter total=%d err=%v", one.Total, err)
	}
	empty, err := s.ListTransactions(ctx, testFinance, QueryFilter{RunID: run, Invoice: "' OR 1=1 --"})
	if err != nil || empty.Total != 0 {
		t.Fatalf("query not parameterized: %d %v", empty.Total, err)
	}
	for _, f := range []QueryFilter{{RunID: run, DateFrom: "2010-02-30"}, {RunID: run, DateFrom: "2010-01-02", DateTo: "2010-01-01"}, {RunID: run, Page: -1}, {RunID: run, Status: "reopened"}, {RunID: run, Classification: "invented"}, {RunID: run, Kind: "other"}, {}} {
		_, err = s.ListTransactions(ctx, testFinance, f)
		requireWorkflowCode(t, err, "invalid_input")
	}
	t.Log("TC-16 store: default=50 max=100; customer/stock/invoice/date filters; 124 filtered rows returned as 100+24 without loss or duplicate; invalid dates/reversed dates/SQL-shaped value rejected or empty")
}

func TestTC17_CurrentCandidatesAllPagesAndOriginalEvidence(t *testing.T) {
	s, _, id, _ := queryFixture(t)
	ctx := context.Background()
	assignWorkflow(t, s, id)
	evidence, err := s.CaseEvidence(ctx, testOwner, id, PageRequest{Page: 1, PageSize: 100})
	if err != nil {
		t.Fatal(err)
	}
	last, err := s.ListCandidates(ctx, testOwner, id, PageRequest{Page: 2, PageSize: 100})
	if err != nil {
		t.Fatal(err)
	}
	if len(evidence.DecisionEvidence) != 2 || evidence.Candidates.Total != 123 || len(evidence.Candidates.Items) != 100 || len(last.Items) != 23 || evidence.Cancel.SourceRow != 125 || evidence.Cancel.RuleVersion != model.RuleVersion {
		t.Fatalf("original vs current evidence: decision=%d current=%d/%d+%d", len(evidence.DecisionEvidence), evidence.Candidates.Total, len(evidence.Candidates.Items), len(last.Items))
	}
	seen := map[string]bool{}
	for _, p := range []CandidatePage{evidence.Candidates, last} {
		for _, c := range p.Items {
			if seen[c.ID] || c.Remaining != 6 || c.Allocated != 0 || c.SourceRow < 2 || c.SourceRow > 124 {
				t.Fatalf("candidate invalid %+v", c)
			}
			seen[c.ID] = true
		}
	}
	for _, e := range evidence.DecisionEvidence {
		if e.RemainingAtDecision != 6 || !e.EligibleAtDecision || e.RuleVersion != model.RuleVersion || e.SourceHash == "" {
			t.Fatalf("bad decision evidence %+v", e)
		}
	}
	t.Log("TC-17 store: 2 original decision witnesses separately labeled; all 123 current eligible candidates paginated 100+23 with source and rule provenance")
}

func TestTC26_WarehouseCannotReadOtherCasesCandidatesOrTransactions(t *testing.T) {
	s, run, own, other := queryFixture(t)
	ctx := context.Background()
	before, err := s.ListTransactions(ctx, testOwner, QueryFilter{RunID: run})
	if err != nil || before.Total != 0 {
		t.Fatalf("unassigned warehouse leaked rows: %d %v", before.Total, err)
	}
	assignWorkflow(t, s, own)
	if _, err = s.AssignCase(ctx, testFinance, testOther, other); err != nil {
		t.Fatal(err)
	}
	page, err := s.ListTransactions(ctx, testOwner, QueryFilter{RunID: run, PageSize: 100})
	if err != nil || page.Total != 124 {
		t.Fatalf("own related transaction scope %d %v", page.Total, err)
	}
	for _, r := range page.Items {
		if r.Customer != "42" {
			t.Fatalf("warehouse saw unrelated customer %s", r.Customer)
		}
	}
	wrong, err := s.ListTransactions(ctx, testOwner, QueryFilter{RunID: run, Customer: "other-customer"})
	if err != nil || wrong.Total != 0 {
		t.Fatalf("direct filter escaped scope %d %v", wrong.Total, err)
	}
	cases, err := s.ListCases(ctx, testOwner, QueryFilter{RunID: run})
	if err != nil || cases.Total != 1 || cases.Items[0].ID != own {
		t.Fatalf("case scope %d %v", cases.Total, err)
	}
	for _, actor := range []WorkflowActor{testFinance, testAdmin} {
		all, e := s.ListCases(ctx, actor, QueryFilter{RunID: run})
		if e != nil || all.Total != 2 {
			t.Fatalf("finance/admin scope %d %v", all.Total, e)
		}
	}
	_, err = s.CaseEvidence(ctx, testOwner, other, PageRequest{})
	requireWorkflowCode(t, err, "forbidden")
	_, err = s.ListCandidates(ctx, testOwner, other, PageRequest{})
	requireWorkflowCode(t, err, "forbidden")
	_, err = s.WorkflowHistory(ctx, testOwner, other)
	requireWorkflowCode(t, err, "forbidden")
	_, err = s.ListTransactions(ctx, WorkflowActor{}, QueryFilter{RunID: run})
	requireWorkflowCode(t, err, "unauthenticated")
	t.Log("TC-26 store: unassigned warehouse total=0; assigned warehouse sees 1 case + its cancel/123 related sales only; other customer filter total=0; other evidence/candidates/history forbidden; finance/admin see 2 cases")
}

func TestTC26_ConsumedCandidateStopsBeingUnrelatedEvidence(t *testing.T) {
	s, cs := manualFixture(t, false)
	c := cs[0]
	ctx := context.Background()
	if _, err := s.ConfirmCandidate(ctx, testOwner, c.ID, "0:2", c.Version); err != nil {
		t.Fatal(err)
	}
	page, err := s.ListAllocations(ctx, testOwner, QueryFilter{RunID: c.RunID})
	if err != nil || page.Total != 1 || page.Items[0].Method != "manual" || page.Items[0].SaleSourceRow != 2 {
		t.Fatalf("own allocation evidence: %+v %v", page, err)
	}
	if _, err = s.ListAllocations(ctx, testOther, QueryFilter{RunID: c.RunID}); err != nil {
		t.Fatal(err)
	}
	other, err := s.ListTransactions(ctx, testOther, QueryFilter{RunID: c.RunID})
	if err != nil || other.Total != 0 {
		t.Fatalf("unrelated warehouse saw allocation source: %d %v", other.Total, err)
	}
}

func TestTC27_ExportCurrentFilteredScopeAndFormulaSafety(t *testing.T) {
	s, run, _, _ := queryFixture(t)
	ctx := context.Background()
	if _, err := s.DB.Exec("UPDATE normalized_rows SET description=? WHERE id='0:2'", "\t =HYPERLINK(\"example\")"); err != nil {
		t.Fatal(err)
	}
	for _, actor := range []WorkflowActor{testFinance, testAdmin} {
		var output bytes.Buffer
		if err := s.ExportCSV(ctx, actor, QueryFilter{RunID: run, Customer: "42", Kind: "sale", Page: 2, PageSize: 1}, &output); err != nil {
			t.Fatal(err)
		}
		rows, err := csv.NewReader(&output).ReadAll()
		if err != nil {
			t.Fatal(err)
		}
		if len(rows) != 124 {
			t.Fatalf("exported %d rows incl header, expected 124", len(rows))
		}
		indices := map[string]int{}
		for i, h := range rows[0] {
			indices[h] = i
		}
		for _, r := range rows[1:] {
			if r[indices["run_id"]] != run || r[indices["scope"]] != "benchmark" || r[indices["customer"]] != "42" || r[indices["rule_version"]] != model.RuleVersion || r[indices["dataset_id"]] == "" || r[indices["source_hash"]] == "" || r[indices["sheet"]] == "" || r[indices["source_row"]] == "" {
				t.Fatalf("CSV lost scope/provenance: %+v", r)
			}
		}
		if !strings.HasPrefix(rows[1][indices["description"]], "'\t =") {
			t.Fatalf("formula escaped incorrectly: %q", rows[1][indices["description"]])
		}
	}
	var denied bytes.Buffer
	err := s.ExportCSV(ctx, testOwner, QueryFilter{RunID: run}, &denied)
	requireWorkflowCode(t, err, "forbidden")
	if denied.Len() != 0 {
		t.Fatal("denied export wrote partial CSV")
	}
	err = s.ExportCSV(ctx, testFinance, QueryFilter{}, &denied)
	requireWorkflowCode(t, err, "invalid_input")
	if denied.Len() != 0 {
		t.Fatal("missing current run wrote CSV")
	}
	t.Log("TC-27 store: finance/admin exported 123 filtered sales + header, independent of page; current run/dataset/scope/hash/rule/source position included; leading-tab formula neutralized; warehouse export rejected before any byte")
}

type failingCSVWriter struct{}

func (failingCSVWriter) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }

func TestExportStreamFailureAndFormulaVariants(t *testing.T) {
	s, run, _, _ := queryFixture(t)
	if err := s.ExportCSV(context.Background(), testFinance, QueryFilter{RunID: run}, failingCSVWriter{}); err == nil {
		t.Fatal("stream write failure hidden")
	}
	for _, v := range []string{"=1+1", "+cmd", "-cmd", "@SUM(1)", "\r\n\t =1", "\ufeff=1"} {
		if got := CSVCell(v); got != "'"+v {
			t.Fatalf("unsafe variant %q -> %q", v, got)
		}
	}
	if got := CSVCell("ordinary text"); got != "ordinary text" {
		t.Fatal("ordinary text altered")
	}
}

func TestQueriesRejectUnpublishedRun(t *testing.T) {
	s, run, id, _ := queryFixture(t)
	if _, err := s.DB.Exec("UPDATE runs SET status='staging' WHERE id=?", run); err != nil {
		t.Fatal(err)
	}
	_, err := s.ListTransactions(context.Background(), testFinance, QueryFilter{RunID: run})
	requireWorkflowCode(t, err, "not_found")
	_, err = s.ListCandidates(context.Background(), testFinance, id, PageRequest{})
	requireWorkflowCode(t, err, "not_found")
}
