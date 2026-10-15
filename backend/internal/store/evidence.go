package store

import (
	"context"
	"fmt"
	"returnsrecon/internal/model"
)

func (s *Store) source(ctx context.Context, dataset, id string) (model.Row, error) {
	var r model.Row
	err := s.DB.QueryRowContext(ctx, `SELECT n.id,s.sheet_index,s.sheet,s.source_row,s.raw_json,n.invoice,n.stock,n.description,n.quantity,n.source_time,n.price,n.customer,n.country,n.kind,n.excluded,n.flags,n.duplicate_of FROM normalized_rows n JOIN source_rows s ON s.id=n.source_id WHERE n.dataset_id=? AND n.id=?`, dataset, id).Scan(&r.ID, &r.SheetIndex, &r.Sheet, &r.SourceRow, &r.RawJSON, &r.Invoice, &r.Stock, &r.Description, &r.Quantity, &r.Time, &r.Price, &r.Customer, &r.Country, &r.Kind, &r.Excluded, &r.Flags, &r.DuplicateOf)
	return r, err
}

// Evidence is a local CLI interface. No unauthenticated HTTP business API exists.
func (s *Store) Evidence(ctx context.Context, run, cancel string) (map[string]any, error) {
	var dataset, rule string
	if err := s.DB.QueryRowContext(ctx, "SELECT dataset_id,rule_version FROM runs WHERE id=? AND status='published'", run).Scan(&dataset, &rule); err != nil {
		return nil, err
	}
	var reason, class string
	var lower int
	if err := s.DB.QueryRowContext(ctx, "SELECT classification,reason,candidate_lower_bound FROM decisions WHERE run_id=? AND cancel_id=?", run, cancel).Scan(&class, &reason, &lower); err != nil {
		return nil, err
	}
	c, err := s.source(ctx, dataset, cancel)
	if err != nil {
		return nil, err
	}
	query, err := s.DB.QueryContext(ctx, "SELECT sale_id,remaining,eligible,reason FROM candidate_evidence WHERE run_id=? AND cancel_id=? ORDER BY position", run, cancel)
	if err != nil {
		return nil, err
	}
	witnesses := []model.Evidence{}
	for query.Next() {
		var e model.Evidence
		if err = query.Scan(&e.SaleID, &e.Remaining, &e.Eligible, &e.Reason); err != nil {
			query.Close()
			return nil, err
		}
		witnesses = append(witnesses, e)
	}
	err = query.Err()
	query.Close()
	if err != nil {
		return nil, err
	}
	sources := []any{}
	for _, e := range witnesses {
		sale, err := s.source(ctx, dataset, e.SaleID)
		if err != nil {
			return nil, err
		}
		var b model.Balance
		b.SaleID = e.SaleID
		if err = s.DB.QueryRowContext(ctx, "SELECT original,allocated,remaining FROM sale_balances WHERE run_id=? AND sale_id=?", run, e.SaleID).Scan(&b.Original, &b.Allocated, &b.Remaining); err != nil {
			return nil, err
		}
		sources = append(sources, map[string]any{"source": sale, "decision_time_evidence": e, "current_balance": b})
	}
	return map[string]any{"run_id": run, "dataset_id": dataset, "rule_version": rule, "cancel": c, "classification": class, "reason": reason, "candidate_count_lower_bound": lower, "witnesses": sources, "notice": "规则推断；决策时证据只保留最多两条支撑，不能当作完整候选列表。当前余量与决策时余量分开显示。"}, nil
}

func (s *Store) Examples(ctx context.Context, run string) (map[string]any, error) {
	out := map[string]any{}
	queries := []struct{ name, sql string }{
		{"buy10_return4", `SELECT a.cancel_id FROM allocations a JOIN sale_balances b ON b.run_id=a.run_id AND b.sale_id=a.sale_id WHERE a.run_id=? AND b.original=10 AND a.quantity=4 ORDER BY a.cancel_id LIMIT 1`},
		{"ambiguous", `SELECT cancel_id FROM decisions WHERE run_id=? AND reason='ambiguous_candidates' ORDER BY cancel_id LIMIT 1`},
		{"insufficient_remaining", `SELECT cancel_id FROM decisions WHERE run_id=? AND reason='insufficient_remaining_quantity' ORDER BY cancel_id LIMIT 1`},
	}
	for _, q := range queries {
		var cancel string
		if err := s.DB.QueryRowContext(ctx, q.sql, run).Scan(&cancel); err != nil {
			out[q.name] = fmt.Sprintf("No example found in this official input: %v", err)
			continue
		}
		e, err := s.Evidence(ctx, run, cancel)
		if err != nil {
			return nil, err
		}
		out[q.name] = e
	}
	return out, nil
}
