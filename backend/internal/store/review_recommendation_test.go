package store

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"returnsrecon/internal/model"
)

func TestTC49_StrictEightFieldFingerprint(t *testing.T) {
	base := reviewRaw(t, fixtureRow("0:2", 2, 6, "08:00"))
	want, err := rawFingerprint(base.RawJSON)
	if err != nil {
		t.Fatal(err)
	}
	var cells [8][2]string
	if err = json.Unmarshal([]byte(base.RawJSON), &cells); err != nil {
		t.Fatal(err)
	}
	for i := range cells {
		changed := cells
		changed[i][1] += "1"
		if i == 3 {
			changed[i][1] = "7"
		}
		if i == 5 {
			changed[i][1] = "0.2"
		}
		raw, _ := json.Marshal(changed)
		got, err := rawFingerprint(string(raw))
		if err != nil || got == want {
			t.Fatalf("field %d did not distinguish original source: %v", i, err)
		}
	}
	for _, value := range []string{"0.10", "1e-1", "0.1000000000000000000000000"} {
		changed := cells
		changed[5][1] = value
		raw, _ := json.Marshal(changed)
		got, err := rawFingerprint(string(raw))
		if err != nil || got != want {
			t.Fatalf("numeric lexeme %s must have exact decimal identity: %v", value, err)
		}
	}
	changed := cells
	changed[5][0] = "s"
	raw, _ := json.Marshal(changed)
	got, err := rawFingerprint(string(raw))
	if err != nil || got == want {
		t.Fatal("numeric and text source cells merged")
	}
	for _, raw := range []string{`[]`, `[null]`, `[["s","x"],null,null,null,null,null,null,["n","NaN"]]`} {
		if _, err := rawFingerprint(raw); err == nil {
			t.Fatalf("invalid original source accepted: %s", raw)
		}
	}
}

func reviewItemFor(t *testing.T, set ReviewSet, cancel string) ReviewItem {
	t.Helper()
	for _, item := range set.Items {
		if item.Cancel.ID == cancel {
			return item
		}
	}
	t.Fatalf("missing cancellation %s", cancel)
	return ReviewItem{}
}

func reviewChosen(t *testing.T, item ReviewItem) ReviewOption {
	t.Helper()
	for _, option := range item.Options {
		if option.ID == item.ChoiceID {
			return option
		}
	}
	t.Fatalf("missing chosen option: %+v", item)
	return ReviewOption{}
}

func TestTC50_NearestTieAndStrictDuplicateRecommendation(t *testing.T) {
	for _, scenario := range []string{"nearest", "tie", "duplicate", "different-description", "different-type"} {
		t.Run(scenario, func(t *testing.T) {
			a := fixtureRow("0:2", 2, 6, "08:00")
			b := fixtureRow("0:3", 3, 6, "09:00")
			if scenario != "nearest" {
				b.Time = a.Time
			}
			if scenario == "duplicate" || strings.HasPrefix(scenario, "different-") {
				a.Invoice, b.Invoice = "SAME", "SAME"
				a.Flags, b.Flags = "same_sheet_duplicate", "same_sheet_duplicate"
			}
			if scenario == "different-description" {
				b.Description = "different original description"
			}
			a, b = reviewRaw(t, a), reviewRaw(t, b)
			if scenario == "different-type" {
				b.RawJSON = strings.Replace(b.RawJSON, `["n","0.1"]`, `["s","0.1"]`, 1)
			}
			s, run, _ := reviewFixture(t, []model.Row{a, b, reviewRaw(t, fixtureRow("0:4", 4, -4, "10:00"))})
			set, err := s.GenerateReviewSet(context.Background(), testFinance, run.RunID)
			if err != nil {
				t.Fatal(err)
			}
			item := reviewItemFor(t, set, "0:4")
			if scenario == "nearest" {
				chosen := reviewChosen(t, item)
				if chosen.Kind != "nearest" || chosen.SaleID != b.ID || chosen.GapSeconds != 3600 {
					t.Fatalf("wrong latest sale: %+v", chosen)
				}
			} else if scenario == "duplicate" {
				chosen := reviewChosen(t, item)
				if chosen.Kind != "duplicate" || chosen.Duplicate == nil || chosen.SaleID != a.ID || chosen.Remaining != 6 || chosen.Duplicate.Capacity != 6 || len(chosen.Duplicate.Members) != 2 {
					t.Fatalf("duplicate evidence/capacity: %+v", chosen)
				}
			} else if item.ChoiceID != "" || len(item.Options) != 2 {
				t.Fatalf("different latest groups must remain unchosen: %+v", item)
			}
			if scalar(t, s, "SELECT count(*) FROM allocations") != 0 || scalar(t, s, "SELECT count(*) FROM audit_events") != 0 || scalar(t, s, "SELECT sum(remaining) FROM sale_balances") != 12 || scalar(t, s, "SELECT sum(version) FROM cases") != 1 {
				t.Fatal("recommendation reserved or changed business state")
			}
		})
	}
}

func reviewBasketRows(t *testing.T) []model.Row {
	t.Helper()
	rows := []model.Row{fixtureRow("0:2", 2, 6, "08:00"), fixtureRow("0:3", 3, 6, "08:00"), fixtureRow("0:4", 4, 6, "09:00"), fixtureRow("0:5", 5, 6, "09:00"), fixtureRow("0:6", 6, -4, "10:00"), fixtureRow("0:7", 7, -4, "10:00")}
	rows[0].Invoice, rows[1].Invoice = "ORIGINAL-A", "ORIGINAL-A"
	rows[2].Invoice, rows[3].Invoice = "ORIGINAL-B", "ORIGINAL-C"
	rows[4].Invoice, rows[5].Invoice = "C-RETURN", "C-RETURN"
	rows[1].Stock, rows[3].Stock, rows[5].Stock = "Y", "Y", "Y"
	for i := range rows {
		rows[i] = reviewRaw(t, rows[i])
	}
	return rows
}

func TestTC50_BasketPrecedesNearestAndRejectsIncompleteEvidence(t *testing.T) {
	for _, scenario := range []string{"valid", "duplicate-cancel", "missing-identity", "different-cancel-time", "empty-sale-invoice", "repeated-stock-price", "duplicate-selected-sale", "excluded-cancel"} {
		t.Run(scenario, func(t *testing.T) {
			rows := reviewBasketRows(t)
			switch scenario {
			case "duplicate-cancel":
				rows[5].Flags = "same_sheet_duplicate"
			case "missing-identity":
				rows[5].Customer = ""
			case "different-cancel-time":
				rows[5].Time = "2010-01-01T11:00:00.000000"
			case "empty-sale-invoice":
				rows[2].Invoice = ""
			case "repeated-stock-price":
				rows[5].Stock = "A"
			case "duplicate-selected-sale":
				rows[0].Flags = "same_sheet_duplicate"
			case "excluded-cancel":
				rows[5].Excluded = "isolated excluded row"
			}
			for i := range rows {
				rows[i] = reviewRaw(t, rows[i])
			}
			s, run, _ := reviewFixture(t, rows)
			set, err := s.GenerateReviewSet(context.Background(), testFinance, run.RunID)
			if err != nil {
				t.Fatal(err)
			}
			for _, item := range set.Items {
				for _, o := range item.Options {
					if scenario != "valid" && o.Kind == "basket" {
						t.Fatalf("unsafe basket survived %s: %+v", scenario, o)
					}
				}
			}
			if scenario == "valid" {
				for _, id := range []string{"0:6", "0:7"} {
					chosen := reviewChosen(t, reviewItemFor(t, set, id))
					if chosen.Kind != "basket" || chosen.Invoice != "ORIGINAL-A" || len(chosen.BasketCases) != 2 || !strings.Contains(chosen.Warning, "合并退货") || !strings.Contains(chosen.Warning, "不同") {
						t.Fatalf("basket priority/evidence: %+v", chosen)
					}
				}
			}
		})
	}
}

func TestTC50_ResidualUniqueSnapshotAndActorScope(t *testing.T) {
	rows := []model.Row{fixtureRow("0:2", 2, 6, "08:00"), fixtureRow("0:3", 3, 6, "09:00"), fixtureRow("0:4", 4, -4, "10:00"), fixtureRow("0:5", 5, -4, "11:00")}
	for i := range rows {
		rows[i] = reviewRaw(t, rows[i])
	}
	s, run, _ := reviewFixture(t, rows)
	c := reviewProcessing(t, s, run.RunID, "0:5")
	if _, err := s.ConfirmCandidate(context.Background(), testOwner, c.ID, "0:3", c.Version); err != nil {
		t.Fatal(err)
	}
	set, err := s.GenerateReviewSet(context.Background(), testFinance, run.RunID)
	if err != nil {
		t.Fatal(err)
	}
	chosen := reviewChosen(t, reviewItemFor(t, set, "0:4"))
	if chosen.Kind != "unique" || chosen.SaleID != "0:2" {
		t.Fatalf("residual unique: %+v", chosen)
	}
	if _, err := s.ListReview(context.Background(), testOther, set.ID, QueryFilter{}, false); err == nil {
		t.Fatal("another actor read persisted recommendations")
	}
	warehouse, err := s.GenerateReviewSet(context.Background(), testOwner, run.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if len(warehouse.Items) != 1 || warehouse.Items[0].Case.ID != c.ID {
		t.Fatalf("warehouse scope leaked unassigned case: %+v", warehouse.Items)
	}
	// Saved source, versions, and choices remain a snapshot after later changes.
	before := reviewItemFor(t, set, "0:4")
	reviewProcessing(t, s, run.RunID, "0:4")
	page, err := s.ListReview(context.Background(), testFinance, set.ID, QueryFilter{}, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 2 || page.Items[0].Case.Version != before.Case.Version || page.Items[0].ChoiceID != before.ChoiceID {
		t.Fatal("saved recommendation changed with live workflow")
	}
}
