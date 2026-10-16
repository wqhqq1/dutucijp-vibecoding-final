package store

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
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

func TestTC16_SourceOrderAndCountAcrossSharedSourceRanges(t *testing.T) {
	s := openTest(t, filepath.Join(t.TempDir(), "ranges.sqlite"))
	ctx := context.Background()
	rows := []model.Row{fixtureRow("0:2", 2, -6, "08:00"), fixtureRow("0:10", 10, -6, "08:00"), fixtureRow("1:2", 2, -6, "08:00")}
	rows[2].SheetIndex, rows[2].Sheet = 1, "Year 2010-2011"
	var runs []string
	for i, subset := range [][]model.Row{rows, rows[1:]} {
		dir, m := preparedFixture(t, subset)
		m.Scope = fmt.Sprintf("test-range-%d", i)
		m.DatasetID = ID(m.SourceHash + "|" + m.Scope + "|" + m.CleaningVersion)
		encoded, err := json.Marshal(m)
		if err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(filepath.Join(dir, "manifest.json"), encoded, 0600); err != nil {
			t.Fatal(err)
		}
		if _, err = s.Import(ctx, dir); err != nil {
			t.Fatal(err)
		}
		run, err := s.Run(ctx, m.DatasetID, 1)
		if err != nil {
			t.Fatal(err)
		}
		runs = append(runs, run.RunID)
	}
	for i, want := range [][]string{{"0:2", "0:10", "1:2"}, {"0:10", "1:2"}} {
		for page := 1; page <= len(want)+1; page++ {
			got, err := s.ListTransactions(ctx, testFinance, QueryFilter{RunID: runs[i], Page: page, PageSize: 1})
			if err != nil {
				t.Fatal(err)
			}
			if got.Total != len(want) {
				t.Fatalf("range %d total=%d, want %d", i, got.Total, len(want))
			}
			if page <= len(want) {
				if len(got.Items) != 1 || got.Items[0].ID != want[page-1] {
					t.Fatalf("range %d page %d: %+v", i, page, got.Items)
				}
			} else if len(got.Items) != 0 {
				t.Fatal("page beyond range returned rows")
			}
			cases, err := s.ListCases(ctx, testFinance, QueryFilter{RunID: runs[i], Page: page, PageSize: 1})
			if err != nil {
				t.Fatal(err)
			}
			if cases.Total != len(want) {
				t.Fatal("case total differs from source rows")
			}
			if page <= len(want) && (len(cases.Items) != 1 || cases.Items[0].CancelID != want[page-1]) {
				t.Fatalf("case numeric source order differs: %+v", cases.Items)
			}
		}
	}
}

func TestTC16_OptimizedListsMatchReferenceAfterWorkflowWrites(t *testing.T) {
	s, run, own, other := queryFixture(t)
	ctx := context.Background()
	c := assignWorkflow(t, s, own)
	c, err := s.TransitionCase(ctx, testOwner, c.ID, "in_progress", c.Version, "")
	if err != nil {
		t.Fatal(err)
	}
	confirmed, err := s.ConfirmCandidate(ctx, testOwner, c.ID, "0:2", c.Version)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.TransitionCase(ctx, testOwner, c.ID, "closed", confirmed.Case.Version, "isolated reference query test"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.AssignCase(ctx, testFinance, testOther, other); err != nil {
		t.Fatal(err)
	}
	// The original joined query is a reference for exact source order, filters,
	// counts and visible ownership after assignment, allocation and close.
	const referenceFrom = ` FROM runs r JOIN datasets d ON d.id=r.dataset_id JOIN normalized_rows n ON n.dataset_id=d.id JOIN source_rows sr ON sr.id=n.source_id LEFT JOIN decisions dc ON dc.run_id=r.id AND dc.cancel_id=n.id LEFT JOIN cases c ON c.run_id=r.id AND c.cancel_id=n.id`
	filters := []QueryFilter{{}, {Status: "closed"}, {Status: "pending"}, {Assignee: testOwner.ID}, {Classification: "suspected"}, {Classification: "matched"}, {Kind: "sale"}, {Kind: "cancel"}, {Customer: "42", Stock: "A"}, {Invoice: "0:125"}, {DateFrom: "2010-01-01T09:00:00", DateTo: "2010-01-02"}, {Status: "closed", Assignee: testOwner.ID, Customer: "42", Kind: "cancel", Page: 2}}
	for _, actor := range []WorkflowActor{testFinance, testOwner, testOther} {
		for i, input := range filters {
			t.Run(fmt.Sprintf("%s-%s-%d", actor.Role, actor.ID, i), func(t *testing.T) {
				input.RunID, input.PageSize = run, 2
				f, err := s.validateFilter(ctx, actor, input)
				if err != nil {
					t.Fatal(err)
				}
				where, args := queryWhere(actor, f)
				read, err := s.DB.QueryContext(ctx, "SELECT "+transactionColumns+referenceFrom+where+" ORDER BY sr.sheet_index,sr.source_row,n.id", args...)
				if err != nil {
					t.Fatal(err)
				}
				wantRows := []TransactionRecord{}
				for read.Next() {
					var r TransactionRecord
					if err = scanTransaction(read, &r); err != nil {
						t.Fatal(err)
					}
					wantRows = append(wantRows, r)
				}
				if err = read.Err(); err != nil {
					t.Fatal(err)
				}
				read.Close()
				got, err := s.ListTransactions(ctx, actor, input)
				if err != nil {
					t.Fatal(err)
				}
				start, end := (f.Page-1)*f.PageSize, f.Page*f.PageSize
				if start > len(wantRows) {
					start = len(wantRows)
				}
				if end > len(wantRows) {
					end = len(wantRows)
				}
				if got.Total != len(wantRows) || !reflect.DeepEqual(got.Items, wantRows[start:end]) {
					t.Fatalf("transaction reference differs: total=%d/%d items=%d/%d", got.Total, len(wantRows), len(got.Items), end-start)
				}
				read, err = s.DB.QueryContext(ctx, "SELECT c.id"+referenceFrom+where+" AND c.id IS NOT NULL ORDER BY sr.sheet_index,sr.source_row,c.id", args...)
				if err != nil {
					t.Fatal(err)
				}
				ids := []string{}
				for read.Next() {
					var id string
					if err = read.Scan(&id); err != nil {
						t.Fatal(err)
					}
					ids = append(ids, id)
				}
				if err = read.Err(); err != nil {
					t.Fatal(err)
				}
				read.Close()
				wantCases := []WorkflowCase{}
				start, end = (f.Page-1)*f.PageSize, f.Page*f.PageSize
				if start > len(ids) {
					start = len(ids)
				}
				if end > len(ids) {
					end = len(ids)
				}
				for _, id := range ids[start:end] {
					c, e := s.WorkflowCase(ctx, actor, id)
					if e != nil {
						t.Fatal(e)
					}
					wantCases = append(wantCases, c)
				}
				cases, err := s.ListCases(ctx, actor, input)
				if err != nil {
					t.Fatal(err)
				}
				if cases.Total != len(ids) || !reflect.DeepEqual(cases.Items, wantCases) {
					t.Fatalf("case reference differs: %+v vs %+v", cases, wantCases)
				}
				allocationFrom := referenceFrom + ` JOIN allocations a ON a.run_id=r.id AND a.cancel_id=n.id JOIN normalized_rows sn ON sn.dataset_id=d.id AND sn.id=a.sale_id JOIN source_rows ss ON ss.id=sn.source_id`
				read, err = s.DB.QueryContext(ctx, `SELECT a.run_id,a.cancel_id,a.sale_id,a.quantity,a.amount,a.method,coalesce(a.actor,''),r.rule_version,sr.sheet,sr.source_row,ss.sheet,ss.source_row`+allocationFrom+where+" ORDER BY sr.sheet_index,sr.source_row,a.cancel_id", args...)
				if err != nil {
					t.Fatal(err)
				}
				wantAllocations := []AllocationRecord{}
				for read.Next() {
					var a AllocationRecord
					if err = read.Scan(&a.RunID, &a.CancelID, &a.SaleID, &a.Quantity, &a.Amount, &a.Method, &a.Actor, &a.RuleVersion, &a.CancelSheet, &a.CancelSourceRow, &a.SaleSheet, &a.SaleSourceRow); err != nil {
						t.Fatal(err)
					}
					wantAllocations = append(wantAllocations, a)
				}
				if err = read.Err(); err != nil {
					t.Fatal(err)
				}
				read.Close()
				allocations, err := s.ListAllocations(ctx, actor, input)
				if err != nil {
					t.Fatal(err)
				}
				start, end = (f.Page-1)*f.PageSize, f.Page*f.PageSize
				if start > len(wantAllocations) {
					start = len(wantAllocations)
				}
				if end > len(wantAllocations) {
					end = len(wantAllocations)
				}
				if allocations.Total != len(wantAllocations) || !reflect.DeepEqual(allocations.Items, wantAllocations[start:end]) {
					t.Fatalf("allocation reference differs: %+v vs %+v", allocations, wantAllocations[start:end])
				}
			})
		}
	}
}
