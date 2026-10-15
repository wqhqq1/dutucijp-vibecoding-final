package reconcile

import (
	"context"
	"encoding/json"
	"reflect"
	"returnsrecon/internal/model"
	"testing"
)

func row(id string, quantity int64, at string) model.Row {
	kind := "sale"
	if quantity < 0 {
		kind = "cancel"
	}
	return model.Row{ID: id, SourceRow: int(id[0]), Sheet: "test fixture only", Stock: "A", Customer: "42", Country: "UK", Price: "0.1", Time: "2010-01-01T" + at + ":00.000000", Quantity: quantity, Kind: kind}
}
func compute(t *testing.T, rows ...model.Row) model.Result {
	t.Helper()
	r, err := Compute(context.Background(), rows, 4)
	if err != nil {
		t.Fatal(err)
	}
	return r
}
func decision(r model.Result, id string) model.Decision {
	for _, d := range r.Decisions {
		if d.CancelID == id {
			return d
		}
	}
	panic(id)
}
func balance(r model.Result, id string) model.Balance {
	for _, b := range r.Balances {
		if b.SaleID == id {
			return b
		}
	}
	panic(id)
}
func report(t *testing.T, r model.Result) {
	t.Helper()
	v, _ := json.Marshal(r)
	t.Log("隔离边界夹具；规则推断；actual=" + string(v))
}

func TestTC09_PartialReturn10Minus4(t *testing.T) {
	r := compute(t, row("s", 10, "09:00"), row("c", -4, "10:00"))
	d := decision(r, "c")
	b := balance(r, "s")
	if d.SaleID != "s" || d.Amount != "0.4" || b.Remaining != 6 || b.Allocated != 4 || d.Evidence[0].Remaining != 10 {
		t.Fatalf("wrong partial return: %+v %+v", d, b)
	}
	report(t, r)
}
func TestTC09_AmountPrecision(t *testing.T) {
	for _, price := range []string{"0.1", "0.10", "0.1000000000000000000000000001"} {
		t.Run(price, func(t *testing.T) {
			s, c := row("s", 10, "09:00"), row("c", -4, "10:00")
			s.Price = price
			c.Price = price
			r := compute(t, s, c)
			expected := "0.4"
			if price == "0.1000000000000000000000000001" {
				expected = "0.4000000000000000000000000004"
			}
			if decision(r, "c").Amount != expected {
				t.Fatal(r)
			}
			report(t, r)
		})
	}
	s, c := row("s", 10, "09:00"), row("c", -4, "10:00")
	s.Price = "1.003"
	c.Price = "1.004"
	r := compute(t, s, c)
	if r.Matched != 0 {
		t.Fatal("display rounding was used for matching")
	}
	report(t, r)
}
func TestTC10_TwoCandidates(t *testing.T) {
	r := compute(t, row("a", 10, "08:00"), row("b", 10, "09:00"), row("c", -4, "10:00"))
	d := decision(r, "c")
	if r.Matched != 0 || d.Reason != "ambiguous_candidates" || len(d.Evidence) != 2 {
		t.Fatal(r)
	}
	report(t, r)
}
func TestTC11_IdentityBoundaries(t *testing.T) {
	for _, field := range []string{"customer", "country", "stock", "missing_customer", "missing_country"} {
		t.Run(field, func(t *testing.T) {
			s, c := row("s", 10, "09:00"), row("c", -4, "10:00")
			switch field {
			case "customer":
				c.Customer = "43"
			case "country":
				c.Country = "FR"
			case "stock":
				c.Stock = "B"
			case "missing_customer":
				c.Customer = ""
			case "missing_country":
				c.Country = ""
			}
			r := compute(t, s, c)
			if r.Matched != 0 {
				t.Fatal(r)
			}
			if !c.HasIdentity() && decision(r, "c").Classification != "suspected" {
				t.Fatal(r)
			}
		})
	}
}
func TestTC12_StrictlyEarlier(t *testing.T) {
	for _, at := range []string{"10:00", "11:00"} {
		t.Run(at, func(t *testing.T) {
			r := compute(t, row("s", 10, at), row("c", -4, "10:00"))
			if r.Matched != 0 || decision(r, "c").Reason != "no_strictly_earlier_sale" {
				t.Fatal(r)
			}
			report(t, r)
		})
	}
}
func TestTC13_CumulativeReturns(t *testing.T) {
	r := compute(t, row("s", 10, "09:00"), row("c", -4, "10:00"), row("d", -6, "11:00"))
	if r.Matched != 2 || balance(r, "s").Remaining != 0 || decision(r, "d").Evidence[0].Remaining != 6 {
		t.Fatal(r)
	}
	report(t, r)
}
func TestTC14_OverReturnRejected(t *testing.T) {
	r := compute(t, row("s", 10, "09:00"), row("c", -7, "10:00"), row("d", -4, "11:00"))
	d := decision(r, "d")
	if r.Matched != 1 || balance(r, "s").Remaining != 3 || d.SaleID != "" || d.Reason != "insufficient_remaining_quantity" || d.Evidence[0].Remaining != 3 {
		t.Fatal(r)
	}
	report(t, r)
}
func TestTC14_NoSplitAcrossSales(t *testing.T) {
	r := compute(t, row("a", 3, "08:00"), row("b", 3, "09:00"), row("c", -4, "10:00"))
	if r.Matched != 0 || balance(r, "a").Remaining != 3 || balance(r, "b").Remaining != 3 {
		t.Fatal(r)
	}
	report(t, r)
}
func TestTC15_NoExactQuantityPreference(t *testing.T) {
	r := compute(t, row("a", 4, "08:00"), row("b", 10, "09:00"), row("c", -4, "10:00"))
	if r.Matched != 0 || decision(r, "c").Reason != "ambiguous_candidates" {
		t.Fatal(r)
	}
	report(t, r)
}
func TestTC15_PrecisionCandidateBranch(t *testing.T) {
	a, b, c := row("a", 4, "08:00"), row("b", 10, "09:00"), row("c", -4, "10:00")
	a.Price = "1.003"
	b.Price = "1.004"
	c.Price = "1.004"
	r := compute(t, a, b, c)
	if decision(r, "c").SaleID != "b" || decision(r, "c").Amount != "4.016" {
		t.Fatal(r)
	}
	report(t, r)
}
func TestTC07_DuplicateInvolvement(t *testing.T) {
	for _, kind := range []string{"sale", "cancel"} {
		t.Run(kind, func(t *testing.T) {
			s, c := row("s", 10, "09:00"), row("c", -4, "10:00")
			if kind == "sale" {
				s.Flags = "same_sheet_duplicate"
			} else {
				c.Flags = "same_sheet_duplicate"
			}
			r := compute(t, s, c)
			if r.Matched != 0 || decision(r, "c").Classification != "suspected" || balance(r, "s").Remaining != 10 {
				t.Fatal(r)
			}
		})
	}
}
func TestBoundedWorkersAndDeterministicOrder(t *testing.T) {
	rows := []model.Row{row("s", 10, "09:00"), row("c", -4, "10:00"), row("d", -6, "10:00")}
	rows[1].SourceRow = 2
	rows[2].SourceRow = 3
	a, e := Compute(context.Background(), rows, 1)
	if e != nil {
		t.Fatal(e)
	}
	rows[0], rows[2] = rows[2], rows[0]
	b, e := Compute(context.Background(), rows, 10000)
	if e != nil {
		t.Fatal(e)
	}
	if b.Workers != 8 {
		t.Fatal(b.Workers)
	}
	a.Workers = b.Workers
	if !reflect.DeepEqual(a, b) {
		t.Fatalf("worker/order changed result: %+v %+v", a, b)
	}
}
func TestInvalidAcceptedInputFailsClosed(t *testing.T) {
	for _, q := range []int64{0, -1, -1 << 63} {
		s := row("s", 10, "09:00")
		s.Quantity = q
		if _, err := Compute(context.Background(), []model.Row{s}, 1); err == nil {
			t.Fatal(q)
		}
	}
	s := row("s", 10, "09:00")
	if _, err := Compute(context.Background(), []model.Row{s, s}, 1); err == nil {
		t.Fatal("duplicate source ID accepted")
	}
}
func TestConservationRejectsCorruptResult(t *testing.T) {
	rows := []model.Row{row("s", 10, "09:00"), row("c", -4, "10:00")}
	r := compute(t, rows...)
	r.Balances[0].Remaining = 7
	if err := Validate(rows, r); err == nil {
		t.Fatal("corrupt balance accepted")
	}
}
func TestCancelledCompute(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Compute(ctx, []model.Row{row("s", 10, "09:00")}, 2); err == nil {
		t.Fatal("cancelled computation accepted")
	}
}
