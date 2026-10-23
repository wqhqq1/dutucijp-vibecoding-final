package store

import (
	"context"
	"database/sql"
	"errors"
)

type DuplicateBalance struct {
	ID          string            `json:"id"`
	CanonicalID string            `json:"canonical_id"`
	Original    int64             `json:"original"`
	Allocated   int64             `json:"allocated"`
	Remaining   int64             `json:"remaining"`
	Members     []DuplicateMember `json:"members"`
}

func (s *Store) duplicateBalance(ctx context.Context, run, sale string) (*DuplicateBalance, error) {
	var d DuplicateBalance
	e := s.DB.QueryRowContext(ctx, `SELECT g.id,g.canonical_id,g.capacity,coalesce((SELECT sum(b.allocated) FROM duplicate_sale_members mm JOIN sale_balances b ON b.run_id=mm.run_id AND b.sale_id=mm.sale_id WHERE mm.run_id=g.run_id AND mm.group_id=g.id),0) FROM duplicate_sale_members m JOIN duplicate_sale_groups g ON g.run_id=m.run_id AND g.id=m.group_id WHERE m.run_id=? AND m.sale_id=?`, run, sale).Scan(&d.ID, &d.CanonicalID, &d.Original, &d.Allocated)
	if errors.Is(e, sql.ErrNoRows) {
		return nil, nil
	}
	if e != nil {
		return nil, e
	}
	d.Remaining = d.Original - d.Allocated
	d.Members = []DuplicateMember{}
	rows, e := s.DB.QueryContext(ctx, businessSQL(ctx, s.DB, run, `SELECT m.sale_id,s.sheet,s.source_row FROM duplicate_sale_members m JOIN runs r ON r.id=m.run_id JOIN normalized_rows n ON n.dataset_id=r.dataset_id AND n.id=m.sale_id JOIN source_rows s ON s.id=n.source_id WHERE m.run_id=? AND m.group_id=? ORDER BY s.sheet_index,s.source_row`), run, d.ID)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	for rows.Next() {
		var m DuplicateMember
		if e = rows.Scan(&m.SaleID, &m.Sheet, &m.SourceRow); e != nil {
			return nil, e
		}
		d.Members = append(d.Members, m)
	}
	return &d, rows.Err()
}
func (s *Store) allocationReviewTrace(ctx context.Context, a *AllocationRecord) error {
	e := s.DB.QueryRowContext(ctx, "SELECT batch_id,recommendation_kind,policy_version FROM review_confirmations WHERE run_id=? AND cancel_id=?", a.RunID, a.CancelID).Scan(&a.BatchID, &a.RecommendationKind, &a.PolicyVersion)
	if errors.Is(e, sql.ErrNoRows) {
		return nil
	}
	return e
}
