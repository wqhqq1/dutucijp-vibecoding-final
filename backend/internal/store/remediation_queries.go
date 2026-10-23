package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"returnsrecon/internal/access"
	"returnsrecon/internal/model"
)

// A historical case may still be visible after its identity changed. Evidence
// inside its append-only events must pass today's country scope independently.
func auditJSONVisible(ctx context.Context, raw []byte) bool {
	if access.Global(ctx) {
		return true
	}
	var value any
	if json.Unmarshal(raw, &value) != nil {
		return false
	}
	var visible func(any) bool
	visible = func(v any) bool {
		switch x := v.(type) {
		case map[string]any:
			for key, item := range x {
				if key == "country" {
					if country, ok := item.(string); ok && !access.Allows(ctx, country) {
						return false
					}
				}
				if !visible(item) {
					return false
				}
			}
		case []any:
			for _, item := range x {
				if !visible(item) {
					return false
				}
			}
		}
		return true
	}
	return visible(value)
}

func (s *Store) BusinessSQL(ctx context.Context, run, query string) string {
	return businessSQL(ctx, s.DB, run, query)
}

func (s *Store) remediationEvidence(ctx context.Context, actor WorkflowActor, out *CaseEvidencePage) error {
	w := out.Case
	original, e := s.source(ctx, w.DatasetID, w.CancelID)
	if e != nil {
		return e
	}
	if access.Allows(ctx, original.Country) {
		out.OriginalCancel = &original
	}
	effective, e := readBusinessRow(ctx, s.DB, w.RunID, w.DatasetID, w.CancelID)
	if e != nil {
		return e
	}
	if access.Allows(ctx, effective.Country) {
		out.EffectiveCancel = &effective
	}
	if !w.Current {
		var raw string
		e = s.DB.QueryRowContext(ctx, "SELECT row_json FROM case_snapshots WHERE case_id=?", w.ID).Scan(&raw)
		if e == nil {
			if e = json.Unmarshal([]byte(raw), &out.Cancel.Row); e != nil {
				return e
			}
		} else if !errors.Is(e, sql.ErrNoRows) {
			return e
		}
	}
	var raw string
	e = s.DB.QueryRowContext(ctx, "SELECT quantity,classification,reason,candidate_lower_bound,evidence_json FROM current_assessments WHERE run_id=? AND cancel_id=?", w.RunID, w.CancelID).Scan(&out.CurrentAssessment.Quantity, &out.CurrentAssessment.Classification, &out.CurrentAssessment.Reason, &out.CurrentAssessment.CandidateLowerBound, &raw)
	if e != nil && !errors.Is(e, sql.ErrNoRows) {
		return e
	}
	if e == nil && w.Current && access.Allows(ctx, effective.Country) {
		out.CurrentAssessment.CancelID = w.CancelID
		if e = json.Unmarshal([]byte(raw), &out.CurrentAssessment.Evidence); e != nil {
			return e
		}
	}
	if !w.Current || !access.Allows(ctx, effective.Country) {
		out.CurrentAssessment = model.Decision{}
	}
	var roundJSON string
	e = s.DB.QueryRowContext(ctx, "SELECT decision_json FROM case_decision_snapshots WHERE case_id=?", w.ID).Scan(&roundJSON)
	if e == nil {
		var round model.Decision
		if e = json.Unmarshal([]byte(roundJSON), &round); e != nil {
			return e
		}
		out.RoundAssessment = &round
		out.DecisionEvidence = []DecisionEvidence{}
		for _, witness := range round.Evidence {
			sale, e := readBusinessRow(ctx, s.DB, w.RunID, w.DatasetID, witness.SaleID)
			if e != nil {
				return e
			}
			if !access.Allows(ctx, sale.Country) {
				continue
			}
			item := DecisionEvidence{CandidateRecord: CandidateRecord{TransactionRecord: TransactionRecord{Row: sale, DatasetID: w.DatasetID, SourceHash: w.SourceHash, RuleVersion: w.RuleVersion, CleaningVersion: w.CleaningVersion}}, RemainingAtDecision: witness.Remaining, EligibleAtDecision: witness.Eligible, Reason: witness.Reason}
			if e = s.DB.QueryRowContext(ctx, "SELECT original,allocated,remaining FROM sale_balances WHERE run_id=? AND sale_id=?", w.RunID, sale.ID).Scan(&item.Original, &item.Allocated, &item.Remaining); e != nil {
				return e
			}
			out.DecisionEvidence = append(out.DecisionEvidence, item)
		}
	} else if !errors.Is(e, sql.ErrNoRows) {
		return e
	}
	out.Remediations = []RemediationEvent{}
	out.RelatedCases = []WorkflowCase{}
	out.DuplicateRows = []model.Row{}
	rows, e := s.DB.QueryContext(ctx, "SELECT id,action,evidence,actor,occurred_at,before_json,after_json FROM remediation_events WHERE case_id=? ORDER BY occurred_at,id", w.ID)
	if e != nil {
		return e
	}
	for rows.Next() {
		var event RemediationEvent
		var beforeText, afterText string
		if e = rows.Scan(&event.ID, &event.Action, &event.Evidence, &event.Actor, &event.OccurredAt, &beforeText, &afterText); e != nil {
			rows.Close()
			return e
		}
		event.Before = json.RawMessage(beforeText)
		event.After = json.RawMessage(afterText)
		var beforeRow, afterRow model.Row
		if e = json.Unmarshal(event.Before, &beforeRow); e != nil {
			rows.Close()
			return e
		}
		if e = json.Unmarshal(event.After, &afterRow); e != nil {
			rows.Close()
			return e
		}
		if !access.Allows(ctx, beforeRow.Country) || !access.Allows(ctx, afterRow.Country) {
			continue
		}
		out.Remediations = append(out.Remediations, event)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return e
	}
	rows, e = s.DB.QueryContext(ctx, "SELECT id FROM cases WHERE run_id=? AND cancel_id=? ORDER BY generation", w.RunID, w.CancelID)
	if e != nil {
		return e
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if e = rows.Scan(&id); e != nil {
			rows.Close()
			return e
		}
		ids = append(ids, id)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return e
	}
	for _, id := range ids {
		c, e := s.WorkflowCase(ctx, actor, id)
		if e == nil {
			out.RelatedCases = append(out.RelatedCases, c)
		}
	}
	if w.Current && w.Status != "closed" {
		query := `SELECT n.id FROM normalized_rows n INDEXED BY row_identity WHERE n.dataset_id=? AND n.customer=? AND n.stock=? AND n.price=? AND n.country=? AND n.excluded='' AND instr('|'||n.flags||'|','|same_sheet_duplicate|')>0 ORDER BY n.id LIMIT 100`
		rows, e = s.businessQuery(ctx, w.RunID, query, w.DatasetID, effective.Customer, effective.Stock, effective.Price, effective.Country)
		if e != nil {
			return e
		}
		ids = nil
		for rows.Next() {
			var id string
			if e = rows.Scan(&id); e != nil {
				rows.Close()
				return e
			}
			ids = append(ids, id)
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return e
		}
		for _, id := range ids {
			r, e := readBusinessRow(ctx, s.DB, w.RunID, w.DatasetID, id)
			if e != nil {
				return e
			}
			out.DuplicateRows = append(out.DuplicateRows, r)
		}
	}
	return nil
}

func (s *Store) BusinessOverview(ctx context.Context, actor WorkflowActor, run string) (map[string]any, error) {
	var published int
	if e := s.DB.QueryRowContext(ctx, "SELECT 1 FROM runs r JOIN datasets d ON d.id=r.dataset_id WHERE r.id=? AND r.status='published' AND d.status='published'", run).Scan(&published); e != nil {
		return nil, workflowError("not_found", "运行不存在或尚未发布", WorkflowCase{})
	}
	where := ` WHERE c.run_id=? AND c.is_current=1`
	args := []any{run}
	if actor.Role == "warehouse" {
		where += " AND c.assignee=?"
		args = append(args, actor.ID)
	}
	suffix, countries := access.SQL(ctx, "n.country")
	where += suffix
	args = append(args, countries...)
	from := ` FROM cases c JOIN runs r ON r.id=c.run_id JOIN normalized_rows n ON n.dataset_id=r.dataset_id AND n.id=c.cancel_id`
	var total, pending, progress, closed, waiting, excluded, matched, unallocated, reviewed int
	query := `SELECT count(*),coalesce(sum(c.status='pending'),0),coalesce(sum(c.status='in_progress'),0),coalesce(sum(c.status='closed'),0),coalesce(sum(c.waiting=1),0),coalesce(sum(c.outcome='verified_duplicate'),0),coalesce(sum(EXISTS(SELECT 1 FROM allocations a WHERE a.run_id=c.run_id AND a.cancel_id=c.cancel_id)),0),coalesce(sum(c.status<>'closed' AND NOT EXISTS(SELECT 1 FROM allocations a WHERE a.run_id=c.run_id AND a.cancel_id=c.cancel_id)),0),coalesce(sum(EXISTS(SELECT 1 FROM review_confirmations v WHERE v.run_id=c.run_id AND v.cancel_id=c.cancel_id)),0)` + from + where
	if e := s.businessQueryRow(ctx, run, query, args...).Scan(&total, &pending, &progress, &closed, &waiting, &excluded, &matched, &unallocated, &reviewed); e != nil {
		return nil, e
	}
	out := map[string]any{"run_id": run, "cases": map[string]int{"total": total, "pending": pending, "in_progress": progress, "closed": closed, "waiting": waiting}, "currently_unallocated": unallocated, "reviewed_allocations": reviewed, "remediation": map[string]int{"verified_duplicate": excluded, "matched_cases": matched}, "caveat": "规则推断；首次计算、当前办理结果和补录来源分别保留。"}
	if actor.Role != "warehouse" {
		var supplemental int
		q := `SELECT count(*) FROM supplemental_sources ss JOIN business_rows n ON n.run_id=ss.run_id AND n.id=ss.id WHERE ss.run_id=?` + suffix
		if e := s.DB.QueryRowContext(ctx, q, append([]any{run}, countries...)...).Scan(&supplemental); e != nil {
			return nil, e
		}
		out["supplemental_sales"] = supplemental
	}
	if actor.Role != "warehouse" && access.Global(ctx) {
		var raw string
		if e := s.DB.QueryRowContext(ctx, "SELECT summary_json FROM runs WHERE id=?", run).Scan(&raw); e != nil {
			return nil, e
		}
		var initial any
		if e := json.Unmarshal([]byte(raw), &initial); e != nil {
			return nil, e
		}
		out["initial_reconciliation"] = initial
	}
	return out, nil
}
