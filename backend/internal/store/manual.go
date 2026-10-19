package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"math"
	"strings"
	"time"

	"github.com/shopspring/decimal"
	"returnsrecon/internal/model"
)

// ManualConfirmation records a rule-qualified inference, never a verified real
// order relationship. The initial decision and run summary remain unchanged.
type ManualConfirmation struct {
	Case       WorkflowCase     `json:"case"`
	Allocation AllocationRecord `json:"allocation"`
}

type ManualAuditDetail struct {
	SaleID    string `json:"sale_id"`
	Quantity  int64  `json:"quantity"`
	Amount    string `json:"amount"`
	Sheet     string `json:"sheet"`
	SourceRow int    `json:"source_row"`
	Remaining int64  `json:"current_remaining"`
}

func readManualRow(ctx context.Context, q rowQuerier, dataset, id string) (r model.Row, err error) {
	err = q.QueryRowContext(ctx, `SELECT n.id,n.invoice,n.stock,n.description,n.quantity,n.source_time,n.price,n.customer,n.country,n.kind,n.excluded,n.flags,s.sheet,s.sheet_index,s.source_row
 FROM normalized_rows n JOIN source_rows s ON s.id=n.source_id WHERE n.dataset_id=? AND n.id=?`, dataset, id).Scan(
		&r.ID, &r.Invoice, &r.Stock, &r.Description, &r.Quantity, &r.Time, &r.Price, &r.Customer, &r.Country, &r.Kind, &r.Excluded, &r.Flags, &r.Sheet, &r.SheetIndex, &r.SourceRow)
	return
}

func manualEligible(cancel, sale model.Row) (int64, decimal.Decimal, bool) {
	if cancel.Kind != "cancel" || cancel.Quantity >= 0 || cancel.Quantity == math.MinInt64 || sale.Kind != "sale" || sale.Quantity <= 0 || cancel.Excluded != "" || sale.Excluded != "" || !cancel.HasIdentity() || !sale.HasIdentity() || cancel.Customer != sale.Customer || cancel.Stock != sale.Stock || cancel.Country != sale.Country {
		return 0, decimal.Zero, false
	}
	ct, ce := time.Parse("2006-01-02T15:04:05.000000", cancel.Time)
	st, se := time.Parse("2006-01-02T15:04:05.000000", sale.Time)
	cp, pe := decimal.NewFromString(cancel.Price)
	sp, spe := decimal.NewFromString(sale.Price)
	if ce != nil || se != nil || !st.Before(ct) || pe != nil || spe != nil || !cp.IsPositive() || !sp.Equal(cp) {
		return 0, decimal.Zero, false
	}
	return -cancel.Quantity, cp, true
}

// ConfirmCandidate re-reads source identity, ownership, version and balance while
// holding SQLite's cross-process write lock. Any failure also rolls back audit.
func (s *Store) ConfirmCandidate(ctx context.Context, actor WorkflowActor, caseID, saleID string, version int) (out ManualConfirmation, err error) {
	err = s.immediate(ctx, func(conn *sql.Conn) error {
		out, err = confirmCandidateTx(ctx, conn, actor, caseID, saleID, version)
		return err
	})
	if err != nil {
		out = ManualConfirmation{}
	}
	return
}

func confirmCandidateTx(ctx context.Context, conn *sql.Conn, actor WorkflowActor, caseID, saleID string, version int) (out ManualConfirmation, err error) {
	if !actor.valid() {
		return out, workflowError("unauthenticated", "需要可信服务端身份", WorkflowCase{})
	}
	if strings.TrimSpace(saleID) == "" {
		return out, workflowError("invalid_input", "请选择销售候选", WorkflowCase{})
	}
	err = func() error {
		before, err := readWorkflowCase(ctx, conn, caseID)
		if err != nil {
			return err
		}
		if err = authorizeWorkflowRead(ctx, actor, before); err != nil {
			return err
		}
		if before.Assignee != actor.ID {
			return workflowError("forbidden", "仅当前责任人可以确认候选", WorkflowCase{})
		}
		if before.Status == "closed" {
			return workflowError("closed", "已关闭差异不能修改", before)
		}
		if before.Status != "in_progress" {
			return workflowError("invalid_transition", "开始处理后才能确认候选", before)
		}
		if before.Version != version {
			return workflowError("version_conflict", "差异版本已变化，请刷新", before)
		}
		var existing string
		err = conn.QueryRowContext(ctx, "SELECT sale_id FROM allocations WHERE run_id=? AND cancel_id=?", before.RunID, before.CancelID).Scan(&existing)
		if err == nil {
			return workflowError("already_allocated", "该取消行已经分配", before)
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		cancel, err := readManualRow(ctx, conn, before.DatasetID, before.CancelID)
		if err != nil {
			return err
		}
		sale, err := readManualRow(ctx, conn, before.DatasetID, saleID)
		if errors.Is(err, sql.ErrNoRows) {
			return workflowError("ineligible_candidate", "当前数据版本中不存在该候选", before)
		}
		if err != nil {
			return err
		}
		quantity, price, ok := manualEligible(cancel, sale)
		if !ok {
			return workflowError("ineligible_candidate", "客户、商品、精确单价、国家、时间或数量不符合规则", before)
		}
		var decisionQuantity, original, allocated, remaining int64
		if err = conn.QueryRowContext(ctx, "SELECT quantity FROM decisions WHERE run_id=? AND cancel_id=?", before.RunID, before.CancelID).Scan(&decisionQuantity); err != nil {
			return err
		}
		if err = conn.QueryRowContext(ctx, "SELECT original,allocated,remaining FROM sale_balances WHERE run_id=? AND sale_id=?", before.RunID, saleID).Scan(&original, &allocated, &remaining); errors.Is(err, sql.ErrNoRows) {
			return workflowError("ineligible_candidate", "候选无当前运行余量", before)
		}
		if err != nil {
			return err
		}
		if decisionQuantity != quantity || original != sale.Quantity || allocated < 0 || remaining < 0 || original-allocated != remaining {
			return workflowError("balance_conflict", "当前数量不一致，请停止操作并核查", before)
		}
		if remaining < quantity {
			return workflowError("insufficient_remaining", "当前可退余量不足，请刷新候选", before)
		}
		if err = checkDuplicateCapacity(ctx, conn, before.RunID, saleID, quantity); err != nil {
			return err
		}
		balanceResult, err := conn.ExecContext(ctx, `UPDATE sale_balances SET allocated=allocated+?,remaining=remaining-? WHERE run_id=? AND sale_id=? AND original=? AND allocated=? AND remaining=? AND remaining>=?`, quantity, quantity, before.RunID, saleID, original, allocated, remaining, quantity)
		if err != nil {
			return err
		}
		changed, err := balanceResult.RowsAffected()
		if err != nil {
			return err
		}
		if changed != 1 {
			return workflowError("balance_conflict", "当前余量已经变化", before)
		}
		amount := price.Mul(decimal.NewFromInt(quantity)).String()
		if _, err = conn.ExecContext(ctx, "INSERT INTO allocations(run_id,cancel_id,sale_id,quantity,amount,method,actor) VALUES(?,?,?,?,?,'manual',?)", before.RunID, before.CancelID, saleID, quantity, amount, actor.ID); err != nil {
			return err
		}
		after := before
		after.Version++
		result, err := conn.ExecContext(ctx, "UPDATE cases SET version=version+1 WHERE id=? AND status='in_progress' AND assignee=? AND version=?", caseID, actor.ID, version)
		if err != nil {
			return err
		}
		changed, err = result.RowsAffected()
		if err != nil {
			return err
		}
		if changed != 1 {
			return workflowError("version_conflict", "差异已变化，请刷新", before)
		}
		detail := ManualAuditDetail{saleID, quantity, amount, sale.Sheet, sale.SourceRow, remaining}
		type snapshot struct {
			WorkflowCase
			Manual ManualAuditDetail `json:"manual_confirmation"`
		}
		b, err := json.Marshal(snapshot{before, detail})
		if err != nil {
			return err
		}
		detail.Remaining -= quantity
		a, err := json.Marshal(snapshot{after, detail})
		if err != nil {
			return err
		}
		if _, err = conn.ExecContext(ctx, "INSERT INTO audit_events(case_id,actor,action,occurred_at,before_json,after_json) VALUES(?,?,'confirm_candidate',?,?,?)", caseID, actor.ID, time.Now().Format(time.RFC3339Nano), string(b), string(a)); err != nil {
			return err
		}
		out = ManualConfirmation{Case: after, Allocation: AllocationRecord{RunID: before.RunID, CancelID: before.CancelID, SaleID: saleID, Quantity: quantity, Amount: amount, Method: "manual", Actor: actor.ID, RuleVersion: before.RuleVersion, CancelSheet: cancel.Sheet, CancelSourceRow: cancel.SourceRow, SaleSheet: sale.Sheet, SaleSourceRow: sale.SourceRow}}
		return nil
	}()
	if err != nil {
		out = ManualConfirmation{}
	}
	return
}
