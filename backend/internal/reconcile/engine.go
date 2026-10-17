package reconcile

import (
	"context"
	"fmt"
	"runtime"
	"slices"
	"sort"
	"strings"
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
	result := model.Result{RuleVersion: model.RuleVersion, Balances: make([]model.Balance, 0, len(rows))}
	groups := make(map[key]*group)
	byID := make(map[string]int, len(rows))
	prices := make(map[string]string)
	timestamps := make(map[string]bool)
	for i := range rows {
		if i%1024 == 0 {
			if err := ctx.Err(); err != nil {
				return result, err
			}
		}
		r := &rows[i]
		if _, exists := byID[r.ID]; exists {
			return result, fmt.Errorf("duplicate source row %s", r.ID)
		}
		byID[r.ID] = i
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
		// Only cancellation-bearing identities need a matching job. Ordinary
		// sales retain full balances without allocating and scheduling hundreds
		// of thousands of groups that cannot produce a decision.
		if r.Kind == "cancel" {
			g := groups[k]
			if g == nil {
				g = &group{}
				groups[k] = g
			}
			g.cancels = append(g.cancels, r)
		}
	}
	for i := range rows {
		if i%1024 == 0 {
			if err := ctx.Err(); err != nil {
				return result, err
			}
		}
		r := &rows[i]
		if r.Excluded != "" || r.Kind != "sale" {
			continue
		}
		price, cached := prices[r.Price]
		if !cached {
			p, _ := decimal.NewFromString(r.Price)
			price = p.String()
		}
		if g := groups[key{r.Customer, r.Stock, price, r.Country}]; g != nil {
			g.sales = append(g.sales, r)
		} else {
			result.Balances = append(result.Balances, model.Balance{SaleID: r.ID, Original: r.Quantity, Remaining: r.Quantity})
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
	slices.SortFunc(result.Decisions, func(a, b model.Decision) int { return strings.Compare(a.CancelID, b.CancelID) })
	slices.SortFunc(result.Balances, func(a, b model.Balance) int { return strings.Compare(a.SaleID, b.SaleID) })
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
	// The index contains input positions only. The independent validation below
	// still reconstructs classifications and allocated quantities from decisions,
	// and checks every balance against its original input row.
	return result, validateIndexed(rows, result, byID)
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
	byID := make(map[string]int, len(rows))
	for i := range rows {
		if _, exists := byID[rows[i].ID]; exists {
			return fmt.Errorf("duplicate source row %s", rows[i].ID)
		}
		byID[rows[i].ID] = i
	}
	return validateIndexed(rows, result, byID)
}

func validateIndexed(rows []model.Row, result model.Result, byID map[string]int) error {
	cancels, sales := 0, 0
	for i := range rows {
		r := &rows[i]
		if r.Excluded != "" {
			continue
		}
		if r.Kind == "cancel" {
			cancels++
		} else {
			sales++
		}
	}
	if len(result.Decisions) != cancels || len(result.Balances) != sales || result.Matched+result.Suspected+result.NoOriginal != cancels {
		return fmt.Errorf("result row conservation failed")
	}
	used := map[int]int64{}
	seen := make([]bool, len(rows))
	classes := map[string]int{}
	for _, d := range result.Decisions {
		ci, ok := byID[d.CancelID]
		if !ok {
			return fmt.Errorf("invalid decision %s", d.CancelID)
		}
		c := &rows[ci]
		if c.Excluded != "" || c.Kind != "cancel" || seen[ci] || d.Quantity != -c.Quantity {
			return fmt.Errorf("invalid decision %s", d.CancelID)
		}
		seen[ci] = true
		classes[d.Classification]++
		if d.Classification != "matched" {
			if d.SaleID != "" || (d.Classification != "suspected" && d.Classification != "no_original") {
				return fmt.Errorf("invalid unresolved classification")
			}
			continue
		}
		si, ok := byID[d.SaleID]
		if !ok {
			return fmt.Errorf("ineligible allocation %s", c.ID)
		}
		s := &rows[si]
		if s.Excluded != "" || s.Kind != "sale" || !c.HasIdentity() || s.Duplicate() || c.Duplicate() || s.Customer != c.Customer || s.Stock != c.Stock || s.Country != c.Country || s.Time >= c.Time {
			return fmt.Errorf("ineligible allocation %s", c.ID)
		}
		sp, _ := decimal.NewFromString(s.Price)
		cp, _ := decimal.NewFromString(c.Price)
		if !sp.Equal(cp) || cp.Mul(decimal.NewFromInt(d.Quantity)).String() != d.Amount {
			return fmt.Errorf("price or amount mismatch")
		}
		if d.Quantity > s.Quantity-used[si] {
			return fmt.Errorf("over allocation %s", s.ID)
		}
		used[si] += d.Quantity
	}
	if classes["matched"] != result.Matched || classes["suspected"] != result.Suspected || classes["no_original"] != result.NoOriginal {
		return fmt.Errorf("classification counters differ")
	}
	for _, b := range result.Balances {
		si, ok := byID[b.SaleID]
		if !ok {
			return fmt.Errorf("balance conservation failed for %s", b.SaleID)
		}
		s := &rows[si]
		if s.Excluded != "" || s.Kind != "sale" || seen[si] || b.Original != s.Quantity || b.Allocated != used[si] || b.Remaining < 0 || b.Allocated < 0 || b.Original-b.Allocated != b.Remaining {
			return fmt.Errorf("balance conservation failed for %s", b.SaleID)
		}
		seen[si] = true
	}
	return nil
}
