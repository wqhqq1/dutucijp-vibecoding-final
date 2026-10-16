package reconcile

import (
	"context"
	"fmt"
	"runtime"
	"sort"
	"sync"
	"time"

	"github.com/shopspring/decimal"
	"returnsrecon/internal/model"
)

type key struct{ customer, stock, price, country string }
type group struct{ sales, cancels []*model.Row }
type groupResult struct {
	decisions []model.Decision
	balances  []model.Balance
}

func before(a, b model.Row) bool {
	if a.Time != b.Time {
		return a.Time < b.Time
	}
	if a.SheetIndex != b.SheetIndex {
		return a.SheetIndex < b.SheetIndex
	}
	return a.SourceRow < b.SourceRow
}

func Compute(ctx context.Context, rows []model.Row, workers int) (model.Result, error) {
	result := model.Result{RuleVersion: model.RuleVersion}
	groups := make(map[key]*group)
	seen := make(map[string]bool, len(rows))
	prices := make(map[string]string)
	timestamps := make(map[string]bool)
	for i := range rows {
		if i%1024 == 0 {
			if err := ctx.Err(); err != nil {
				return result, err
			}
		}
		r := &rows[i]
		if seen[r.ID] {
			return result, fmt.Errorf("duplicate source row %s", r.ID)
		}
		seen[r.ID] = true
		if r.Excluded != "" {
			continue
		}
		price, cached := prices[r.Price]
		if !cached {
			p, err := decimal.NewFromString(r.Price)
			if err != nil || !p.IsPositive() {
				return result, fmt.Errorf("invalid accepted price: %s", r.ID)
			}
			price = p.String()
			if len(prices) < 65536 {
				prices[r.Price] = price
			}
		}
		if !timestamps[r.Time] {
			if _, err := time.Parse("2006-01-02T15:04:05.000000", r.Time); err != nil {
				return result, fmt.Errorf("invalid source timestamp: %s", r.ID)
			}
			if len(timestamps) < 65536 {
				timestamps[r.Time] = true
			}
		}
		if (r.Kind == "sale" && r.Quantity <= 0) || (r.Kind == "cancel" && r.Quantity >= 0) || (r.Kind != "sale" && r.Kind != "cancel") || r.Quantity == -1<<63 {
			return result, fmt.Errorf("invalid accepted quantity: %s", r.ID)
		}
		k := key{r.Customer, r.Stock, price, r.Country}
		g := groups[k]
		if g == nil {
			g = &group{}
			groups[k] = g
		}
		if r.Kind == "sale" {
			g.sales = append(g.sales, r)
		} else {
			g.cancels = append(g.cancels, r)
		}
	}
	if workers <= 0 {
		workers = runtime.GOMAXPROCS(0)
	}
	workers = min(8, max(1, workers))
	result.Workers = workers
	jobs := make(chan *group, workers)
	outputs := make(chan groupResult, workers)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for g := range jobs {
				out := computeGroup(g)
				select {
				case outputs <- out:
				case <-ctx.Done():
					return
				}
			}
		}()
	}
	go func() {
		defer close(jobs)
		for _, g := range groups {
			select {
			case jobs <- g:
			case <-ctx.Done():
				return
			}
		}
	}()
	go func() { wg.Wait(); close(outputs) }()
	for out := range outputs {
		result.Decisions = append(result.Decisions, out.decisions...)
		result.Balances = append(result.Balances, out.balances...)
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	sort.Slice(result.Decisions, func(i, j int) bool { return result.Decisions[i].CancelID < result.Decisions[j].CancelID })
	sort.Slice(result.Balances, func(i, j int) bool { return result.Balances[i].SaleID < result.Balances[j].SaleID })
	for _, d := range result.Decisions {
		switch d.Classification {
		case "matched":
			result.Matched++
		case "suspected":
			result.Suspected++
		case "no_original":
			result.NoOriginal++
		}
	}
	return result, Validate(rows, result)
}

func computeGroup(g *group) groupResult {
	if len(g.cancels) == 0 {
		out := groupResult{balances: make([]model.Balance, 0, len(g.sales))}
		for _, s := range g.sales {
			out.balances = append(out.balances, model.Balance{SaleID: s.ID, Original: s.Quantity, Remaining: s.Quantity})
		}
		return out
	}
	sort.Slice(g.sales, func(i, j int) bool { return before(*g.sales[i], *g.sales[j]) })
	sort.Slice(g.cancels, func(i, j int) bool { return before(*g.cancels[i], *g.cancels[j]) })
	index := newIndex(len(g.sales))
	remaining := make([]int64, len(g.sales))
	for i, s := range g.sales {
		remaining[i] = s.Quantity
	}
	active := 0
	out := groupResult{}
	for _, c := range g.cancels {
		for active < len(g.sales) && g.sales[active].Time < c.Time {
			index.set(active, remaining[active])
			active++
		}
		d := model.Decision{CancelID: c.ID, Quantity: -c.Quantity, Classification: "suspected", Evidence: []model.Evidence{}}
		if !c.HasIdentity() {
			d.Reason = "missing_identity"
			out.decisions = append(out.decisions, d)
			continue
		}
		candidates := index.find(d.Quantity)
		d.CandidateLowerBound = len(candidates)
		for _, i := range candidates {
			outReason := "qualified"
			if g.sales[i].Duplicate() {
				outReason = "suspected_duplicate_sale"
			}
			d.Evidence = append(d.Evidence, model.Evidence{SaleID: g.sales[i].ID, Remaining: remaining[i], Eligible: true, Reason: outReason})
		}
		switch {
		case c.Duplicate():
			d.Reason = "suspected_duplicate_cancel"
		case len(candidates) >= 2:
			d.Reason = "ambiguous_candidates"
		case len(candidates) == 1 && g.sales[candidates[0]].Duplicate():
			d.Reason = "suspected_duplicate_sale"
		case len(candidates) == 1:
			i := candidates[0]
			d.Classification = "matched"
			d.Reason = "unique_qualified_candidate"
			d.SaleID = g.sales[i].ID
			p, _ := decimal.NewFromString(c.Price)
			d.Amount = p.Mul(decimal.NewFromInt(d.Quantity)).String()
			remaining[i] -= d.Quantity
			index.set(i, remaining[i])
		default:
			d.Classification = "no_original"
			switch {
			case len(g.sales) == 0:
				d.Reason = "no_identity_match"
			case active == 0:
				d.Reason = "no_strictly_earlier_sale"
			default:
				d.Reason = "insufficient_remaining_quantity"
			}
			limit := min(2, len(g.sales))
			if active > 0 {
				limit = min(2, active)
			}
			for i := 0; i < limit; i++ {
				reason := "remaining_quantity_too_small"
				if g.sales[i].Time >= c.Time {
					reason = "sale_not_strictly_earlier"
				}
				d.Evidence = append(d.Evidence, model.Evidence{SaleID: g.sales[i].ID, Remaining: remaining[i], Reason: reason})
			}
		}
		out.decisions = append(out.decisions, d)
	}
	for i, s := range g.sales {
		out.balances = append(out.balances, model.Balance{SaleID: s.ID, Original: s.Quantity, Allocated: s.Quantity - remaining[i], Remaining: remaining[i]})
	}
	return out
}

// Validate independently checks row/classification, allocation and balance conservation.
// It runs before persistence, then SQL checks persisted totals before publication.
func Validate(rows []model.Row, result model.Result) error {
	byID := make(map[string]*model.Row, len(rows))
	cancels, sales := 0, 0
	for i := range rows {
		r := &rows[i]
		if r.Excluded != "" {
			continue
		}
		byID[r.ID] = r
		if r.Kind == "cancel" {
			cancels++
		} else {
			sales++
		}
	}
	if len(result.Decisions) != cancels || len(result.Balances) != sales || result.Matched+result.Suspected+result.NoOriginal != cancels {
		return fmt.Errorf("result row conservation failed")
	}
	used := map[string]int64{}
	seen := map[string]bool{}
	classes := map[string]int{}
	for _, d := range result.Decisions {
		c, ok := byID[d.CancelID]
		if !ok || c.Kind != "cancel" || seen[c.ID] || d.Quantity != -c.Quantity {
			return fmt.Errorf("invalid decision %s", d.CancelID)
		}
		seen[c.ID] = true
		classes[d.Classification]++
		if d.Classification != "matched" {
			if d.SaleID != "" || (d.Classification != "suspected" && d.Classification != "no_original") {
				return fmt.Errorf("invalid unresolved classification")
			}
			continue
		}
		s, ok := byID[d.SaleID]
		if !ok || s.Kind != "sale" || !c.HasIdentity() || s.Duplicate() || c.Duplicate() || s.Customer != c.Customer || s.Stock != c.Stock || s.Country != c.Country || s.Time >= c.Time {
			return fmt.Errorf("ineligible allocation %s", c.ID)
		}
		sp, _ := decimal.NewFromString(s.Price)
		cp, _ := decimal.NewFromString(c.Price)
		if !sp.Equal(cp) || cp.Mul(decimal.NewFromInt(d.Quantity)).String() != d.Amount {
			return fmt.Errorf("price or amount mismatch")
		}
		if d.Quantity > s.Quantity-used[s.ID] {
			return fmt.Errorf("over allocation %s", s.ID)
		}
		used[s.ID] += d.Quantity
	}
	if classes["matched"] != result.Matched || classes["suspected"] != result.Suspected || classes["no_original"] != result.NoOriginal {
		return fmt.Errorf("classification counters differ")
	}
	seen = map[string]bool{}
	for _, b := range result.Balances {
		s, ok := byID[b.SaleID]
		if !ok || s.Kind != "sale" || seen[s.ID] || b.Original != s.Quantity || b.Allocated != used[s.ID] || b.Remaining < 0 || b.Allocated < 0 || b.Original-b.Allocated != b.Remaining {
			return fmt.Errorf("balance conservation failed for %s", b.SaleID)
		}
		seen[s.ID] = true
	}
	return nil
}
