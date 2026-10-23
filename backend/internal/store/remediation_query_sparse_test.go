package store

import (
	"bytes"
	"context"
	"encoding/csv"
	"fmt"
	"reflect"
	"returnsrecon/internal/access"
	"returnsrecon/internal/model"
	"testing"
)

func TestFR20Sparse_WorkflowFilteredTransactionPage(t *testing.T) {
	s, c := remediationFixture(t, fixtureRow("0:2", 2, 6, "08:00"), fixtureRow("0:3", 3, 8, "09:00"), fixtureRow("0:4", 4, -4, "10:00"))
	req := remediationRequest(c, "correct_return", "sparse-filtered-page")
	req.Changes = map[string]string{"quantity": "20"}
	remediateOK(t, s, c, req)
	page, err := s.ListTransactions(context.Background(), testFinance, QueryFilter{RunID: c.RunID, Status: "pending"})
	if err != nil || page.Total != 1 || len(page.Items) != 1 {
		t.Fatalf("workflow page: %+v %v", page, err)
	}
}

func TestFR20_OriginalPrecisePriceSurvivesUnrelatedCorrection(t *testing.T) {
	a, b, r := fixtureRow("0:2", 2, 6, "08:00"), fixtureRow("0:3", 3, 8, "09:00"), fixtureRow("0:4", 4, -4, "10:00")
	price := "0.8500000000000001"
	a.Price, b.Price, r.Price = price, price, price
	s, c := remediationFixture(t, a, b, r)
	req := remediationRequest(c, "correct_return", "precise-price-original-stock")
	req.Changes = map[string]string{"stock": "A"}
	out := remediateOK(t, s, c, req)
	effective, err := readBusinessRow(context.Background(), s.DB, c.RunID, c.DatasetID, c.CancelID)
	if err != nil || effective.Price != price || out.Case.Reason != "ambiguous_candidates" {
		t.Fatalf("exact original price changed: %+v %v", effective, err)
	}
	for _, invalid := range []string{"0", "-1", "1e1000000", "1e-1000000"} {
		bad := r
		bad.Price = invalid
		if validateBusinessRow(&bad) == nil {
			t.Fatalf("unbounded/nonpositive price accepted: %s", invalid)
		}
	}
}

func TestFR20Sparse_SortedPagesMatchCSVWithOverridesSupplementAndScopes(t *testing.T) {
	rows := []model.Row{}
	for i := 2; i < 82; i++ {
		r := fixtureRow(fmt.Sprintf("0:%d", i), i, 6, "08:00")
		r.Invoice = fmt.Sprintf("S%03d", i)
		if i%3 == 0 {
			r.Stock = "B"
		}
		if i%5 == 0 {
			r.Customer = "43"
		}
		if i%7 == 0 {
			r.Country = "FR"
		}
		rows = append(rows, r)
	}
	rows = append(rows, fixtureRow("0:82", 82, -4, "10:00"))
	s, c := remediationFixture(t, rows...)
	req := remediationRequest(c, "correct_return", "sparse-sort-correction")
	req.Changes = map[string]string{"customer": "43", "country": "FR"}
	out := remediateOK(t, s, c, req)
	if out.Case.ID != c.ID || out.Case.Status != "in_progress" {
		t.Fatalf("expected unresolved same reason: %+v", out)
	}
	req = remediationRequest(out.Case, "supplement_sale", "sparse-sort-supplement")
	req.Sale = &SupplementalSale{Invoice: "S999", Stock: "A", Customer: "43", Country: "FR", Quantity: "4", Time: "2010-01-01T08:00:00", Price: "0.1", DocumentRef: "isolated-sort-proof", LineRef: "1"}
	out = remediateOK(t, s, out.Case, req)
	if out.SupplementalSaleID == "" || sparseEffectiveCount(t, s, c.RunID) != 82 {
		t.Fatal("expected baseline, override and supplement")
	}
	for _, scope := range []struct {
		name  string
		ctx   context.Context
		actor WorkflowActor
	}{
		{"finance", context.Background(), testFinance},
		{"country", access.With(context.Background(), access.Profile{Role: "finance", Countries: []string{"FR"}}), testFinance},
		{"warehouse", context.Background(), testOwner},
	} {
		for _, filter := range []QueryFilter{{}, {Country: "FR"}, {Customer: "43", Stock: "A"}, {Status: "in_progress"}} {
			for _, sort := range []string{"source", "date", "invoice", "customer", "stock", "country"} {
				for _, direction := range []string{"asc", "desc"} {
					f := filter
					f.RunID = c.RunID
					f.Sort = sort
					f.Direction = direction
					want := []string{}
					if scope.actor.Role == "warehouse" {
						where, args := scopedQueryWhere(scope.ctx, scope.actor, f, false)
						read, err := s.businessQuery(scope.ctx, c.RunID, "SELECT n.id"+transactionQueryFrom(f, true)+where+" ORDER BY "+transactionOrder(f), args...)
						if err != nil {
							t.Fatal(err)
						}
						for read.Next() {
							var id string
							if err = read.Scan(&id); err != nil {
								t.Fatal(err)
							}
							want = append(want, id)
						}
						if err = read.Err(); err != nil {
							t.Fatal(err)
						}
						read.Close()
					} else {
						var b bytes.Buffer
						if err := s.ExportCSV(scope.ctx, scope.actor, f, &b); err != nil {
							t.Fatal(scope.name, sort, direction, err)
						}
						cells, err := csv.NewReader(&b).ReadAll()
						if err != nil {
							t.Fatal(err)
						}
						for _, line := range cells[1:] {
							want = append(want, line[8])
						}
					}
					got := []string{}
					f.PageSize = 31
					for page := 1; page <= len(want)/31+2; page++ {
						f.Page = page
						p, err := s.ListTransactions(scope.ctx, scope.actor, f)
						if err != nil {
							t.Fatal(scope.name, sort, direction, err)
						}
						if p.Total != len(want) {
							t.Fatalf("total changed: %d vs %d", p.Total, len(want))
						}
						for _, row := range p.Items {
							got = append(got, row.ID)
						}
					}
					if !reflect.DeepEqual(want, got) {
						t.Fatalf("%s %s %s filter=%+v: sorted pages differ from CSV", scope.name, sort, direction, filter)
					}
				}
			}
		}
	}
}
