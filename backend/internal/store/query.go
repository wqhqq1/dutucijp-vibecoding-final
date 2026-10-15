package store

import (
	"context"
	"database/sql"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"
	"unicode"

	"returnsrecon/internal/model"
)

type PageRequest struct {
	Page     int `json:"page"`
	PageSize int `json:"page_size"`
}
type QueryFilter struct {
	RunID          string `json:"run_id"`
	Status         string `json:"status"`
	Assignee       string `json:"assignee"`
	Customer       string `json:"customer"`
	Stock          string `json:"stock"`
	Invoice        string `json:"invoice"`
	Classification string `json:"classification"`
	Kind           string `json:"kind"`
	DateFrom       string `json:"date_from"`
	DateTo         string `json:"date_to"`
	Page           int    `json:"page"`
	PageSize       int    `json:"page_size"`
}
type TransactionRecord struct {
	model.Row
	DatasetID       string `json:"dataset_id"`
	Scope           string `json:"scope"`
	SourceHash      string `json:"source_hash"`
	RuleVersion     string `json:"rule_version"`
	CleaningVersion string `json:"cleaning_version"`
}
type CandidateRecord struct {
	TransactionRecord
	Original  int64 `json:"original"`
	Allocated int64 `json:"allocated"`
	Remaining int64 `json:"current_remaining"`
}
type DecisionEvidence struct {
	CandidateRecord
	RemainingAtDecision int64  `json:"remaining_at_decision"`
	EligibleAtDecision  bool   `json:"eligible_at_decision"`
	Reason              string `json:"reason"`
}
type AllocationRecord struct {
	RunID           string `json:"run_id"`
	CancelID        string `json:"cancel_id"`
	SaleID          string `json:"sale_id"`
	Quantity        int64  `json:"quantity"`
	Amount          string `json:"amount"`
	Method          string `json:"method"`
	Actor           string `json:"actor"`
	RuleVersion     string `json:"rule_version"`
	CancelSheet     string `json:"cancel_sheet"`
	CancelSourceRow int    `json:"cancel_source_row"`
	SaleSheet       string `json:"sale_sheet"`
	SaleSourceRow   int    `json:"sale_source_row"`
}
type CasePage struct {
	Items    []WorkflowCase `json:"items"`
	Page     int            `json:"page"`
	PageSize int            `json:"page_size"`
	Total    int            `json:"total"`
}
type TransactionPage struct {
	Items    []TransactionRecord `json:"items"`
	Page     int                 `json:"page"`
	PageSize int                 `json:"page_size"`
	Total    int                 `json:"total"`
}
type AllocationPage struct {
	Items    []AllocationRecord `json:"items"`
	Page     int                `json:"page"`
	PageSize int                `json:"page_size"`
	Total    int                `json:"total"`
}
type CandidatePage struct {
	Items    []CandidateRecord `json:"items"`
	Page     int               `json:"page"`
	PageSize int               `json:"page_size"`
	Total    int               `json:"total"`
}
type CaseEvidencePage struct {
	Case             WorkflowCase       `json:"case"`
	Cancel           TransactionRecord  `json:"cancel"`
	DecisionEvidence []DecisionEvidence `json:"decision_evidence"`
	Candidates       CandidatePage      `json:"candidates"`
	Caveat           string             `json:"caveat"`
}

// EnsureQueryIndexes is an idempotent startup migration. It changes no business
// records and must run before serving queries, never on a user's read request.
func (s *Store) EnsureQueryIndexes(ctx context.Context) error {
	return s.immediate(ctx, func(conn *sql.Conn) error {
		for _, statement := range []string{
			"CREATE INDEX IF NOT EXISTS cases_owner ON cases(run_id,assignee,cancel_id)",
			"CREATE INDEX IF NOT EXISTS row_invoice ON normalized_rows(dataset_id,invoice)",
			"CREATE INDEX IF NOT EXISTS row_source_time ON normalized_rows(dataset_id,source_time,id)",
			"CREATE INDEX IF NOT EXISTS row_stock ON normalized_rows(dataset_id,stock,id)",
		} {
			if _, err := conn.ExecContext(ctx, statement); err != nil {
				return err
			}
		}
		return nil
	})
}

func normalizePage(p PageRequest) (PageRequest, error) {
	if p.Page < 0 || p.PageSize < 0 || p.Page > 10000000 {
		return p, workflowError("invalid_input", "分页参数不合法", WorkflowCase{})
	}
	if p.Page == 0 {
		p.Page = 1
	}
	if p.PageSize == 0 {
		p.PageSize = 50
	}
	if p.PageSize > 100 {
		p.PageSize = 100
	}
	return p, nil
}
func normalizeDate(v string) (string, error) {
	if v == "" {
		return "", nil
	}
	for _, layout := range []string{"2006-01-02", "2006-01-02T15:04:05.000000", "2006-01-02T15:04:05"} {
		if parsed, err := time.Parse(layout, v); err == nil {
			return parsed.Format("2006-01-02T15:04:05.000000"), nil
		}
	}
	return "", workflowError("invalid_input", "日期须为YYYY-MM-DD或无时区来源时间", WorkflowCase{})
}
func (s *Store) validateFilter(ctx context.Context, actor WorkflowActor, f QueryFilter) (QueryFilter, error) {
	if !actor.valid() {
		return f, workflowError("unauthenticated", "需要可信服务端身份", WorkflowCase{})
	}
	if f.RunID == "" {
		return f, workflowError("invalid_input", "必须指定当前运行版本", WorkflowCase{})
	}
	p, err := normalizePage(PageRequest{f.Page, f.PageSize})
	if err != nil {
		return f, err
	}
	f.Page, f.PageSize = p.Page, p.PageSize
	if f.DateFrom, err = normalizeDate(f.DateFrom); err != nil {
		return f, err
	}
	if f.DateTo, err = normalizeDate(f.DateTo); err != nil {
		return f, err
	}
	if f.DateFrom != "" && f.DateTo != "" && f.DateFrom >= f.DateTo {
		return f, workflowError("invalid_input", "日期终点须晚于起点（起含终不含）", WorkflowCase{})
	}
	if f.Status != "" && f.Status != "pending" && f.Status != "in_progress" && f.Status != "closed" {
		return f, workflowError("invalid_input", "未知差异状态", WorkflowCase{})
	}
	if f.Classification != "" && f.Classification != "matched" && f.Classification != "suspected" && f.Classification != "no_original" {
		return f, workflowError("invalid_input", "未知初始分类", WorkflowCase{})
	}
	if f.Kind != "" && f.Kind != "sale" && f.Kind != "cancel" {
		return f, workflowError("invalid_input", "未知交易类型", WorkflowCase{})
	}
	var published int
	err = s.DB.QueryRowContext(ctx, "SELECT 1 FROM runs r JOIN datasets d ON d.id=r.dataset_id WHERE r.id=? AND r.status='published' AND d.status='published'", f.RunID).Scan(&published)
	if errors.Is(err, sql.ErrNoRows) {
		err = workflowError("not_found", "运行不存在或尚未发布", WorkflowCase{})
	}
	return f, err
}

const transactionColumns = `n.id,sr.sheet_index,sr.sheet,sr.source_row,n.invoice,n.stock,n.description,n.quantity,n.source_time,n.price,n.customer,n.country,n.kind,n.excluded,n.flags,n.duplicate_of,d.id,d.scope,d.source_hash,r.rule_version,d.cleaning_version`
const transactionFrom = ` FROM runs r JOIN datasets d ON d.id=r.dataset_id JOIN normalized_rows n ON n.dataset_id=d.id JOIN source_rows sr ON sr.id=n.source_id LEFT JOIN decisions dc ON dc.run_id=r.id AND dc.cancel_id=n.id LEFT JOIN cases c ON c.run_id=r.id AND c.cancel_id=n.id`

type scanner interface{ Scan(...any) error }

func scanTransaction(s scanner, r *TransactionRecord, extra ...any) error {
	args := []any{&r.ID, &r.SheetIndex, &r.Sheet, &r.SourceRow, &r.Invoice, &r.Stock, &r.Description, &r.Quantity, &r.Time, &r.Price, &r.Customer, &r.Country, &r.Kind, &r.Excluded, &r.Flags, &r.DuplicateOf, &r.DatasetID, &r.Scope, &r.SourceHash, &r.RuleVersion, &r.CleaningVersion}
	return s.Scan(append(args, extra...)...)
}

// Related sales are restricted to a warehouse user's own case's current
// qualified candidates, original decision evidence or confirmed allocation.
const warehouseRelated = `n.id IN (
 SELECT own.cancel_id FROM cases own WHERE own.run_id=? AND own.assignee=?
 UNION SELECT oa.sale_id FROM cases own JOIN allocations oa ON oa.run_id=own.run_id AND oa.cancel_id=own.cancel_id WHERE own.run_id=? AND own.assignee=?
 UNION SELECT oe.sale_id FROM cases own JOIN candidate_evidence oe ON oe.run_id=own.run_id AND oe.cancel_id=own.cancel_id WHERE own.run_id=? AND own.assignee=?
 UNION SELECT eligible.id FROM cases own JOIN runs ownrun ON ownrun.id=own.run_id
 JOIN normalized_rows cr ON cr.dataset_id=ownrun.dataset_id AND cr.id=own.cancel_id
 JOIN normalized_rows eligible ON eligible.dataset_id=cr.dataset_id AND eligible.customer=cr.customer AND eligible.country=cr.country AND eligible.stock=cr.stock AND eligible.price=cr.price
 JOIN sale_balances rb ON rb.run_id=own.run_id AND rb.sale_id=eligible.id
 WHERE own.run_id=? AND own.assignee=? AND cr.kind='cancel' AND cr.quantity<0 AND cr.quantity>-9223372036854775808 AND cr.excluded=''
 AND cr.customer<>'' AND cr.country<>'' AND cr.stock<>'' AND eligible.kind='sale' AND eligible.excluded='' AND eligible.quantity>0 AND eligible.source_time<cr.source_time AND rb.remaining>=-cr.quantity
)`

func queryWhere(actor WorkflowActor, f QueryFilter) (string, []any) {
	parts := []string{"r.id=?", "r.status='published'", "d.status='published'"}
	args := []any{f.RunID}
	for _, pair := range []struct{ sql, value string }{{"c.status=?", f.Status}, {"c.assignee=?", f.Assignee}, {"n.customer=?", f.Customer}, {"n.stock=?", f.Stock}, {"n.invoice=?", f.Invoice}, {"dc.classification=?", f.Classification}, {"n.kind=?", f.Kind}, {"n.source_time>=?", f.DateFrom}, {"n.source_time<?", f.DateTo}} {
		if pair.value != "" {
			parts = append(parts, pair.sql)
			args = append(args, pair.value)
		}
	}
	if actor.Role == "warehouse" {
		parts = append(parts, warehouseRelated)
		for range 4 {
			args = append(args, f.RunID, actor.ID)
		}
	}
	return " WHERE " + strings.Join(parts, " AND "), args
}

func (s *Store) ListTransactions(ctx context.Context, actor WorkflowActor, f QueryFilter) (out TransactionPage, err error) {
	f, err = s.validateFilter(ctx, actor, f)
	if err != nil {
		return
	}
	out = TransactionPage{Items: []TransactionRecord{}, Page: f.Page, PageSize: f.PageSize}
	where, args := queryWhere(actor, f)
	if err = s.DB.QueryRowContext(ctx, "SELECT count(*)"+transactionFrom+where, args...).Scan(&out.Total); err != nil {
		return
	}
	args = append(args, f.PageSize, (f.Page-1)*f.PageSize)
	rows, e := s.DB.QueryContext(ctx, "SELECT "+transactionColumns+transactionFrom+where+" ORDER BY sr.sheet_index,sr.source_row,n.id LIMIT ? OFFSET ?", args...)
	if e != nil {
		err = e
		return
	}
	defer rows.Close()
	for rows.Next() {
		var r TransactionRecord
		if err = scanTransaction(rows, &r); err != nil {
			return
		}
		out.Items = append(out.Items, r)
	}
	err = rows.Err()
	return
}

func (s *Store) ListCases(ctx context.Context, actor WorkflowActor, f QueryFilter) (out CasePage, err error) {
	f, err = s.validateFilter(ctx, actor, f)
	if err != nil {
		return
	}
	out = CasePage{Items: []WorkflowCase{}, Page: f.Page, PageSize: f.PageSize}
	where, args := queryWhere(actor, f)
	where += " AND c.id IS NOT NULL"
	if err = s.DB.QueryRowContext(ctx, "SELECT count(*)"+transactionFrom+where, args...).Scan(&out.Total); err != nil {
		return
	}
	args = append(args, f.PageSize, (f.Page-1)*f.PageSize)
	rows, e := s.DB.QueryContext(ctx, "SELECT c.id"+transactionFrom+where+" ORDER BY sr.sheet_index,sr.source_row,c.id LIMIT ? OFFSET ?", args...)
	if e != nil {
		err = e
		return
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return
	}
	for _, id := range ids {
		c, e := s.WorkflowCase(ctx, actor, id)
		if e != nil {
			err = e
			return
		}
		out.Items = append(out.Items, c)
	}
	return
}

func (s *Store) ListAllocations(ctx context.Context, actor WorkflowActor, f QueryFilter) (out AllocationPage, err error) {
	f, err = s.validateFilter(ctx, actor, f)
	if err != nil {
		return
	}
	out = AllocationPage{Items: []AllocationRecord{}, Page: f.Page, PageSize: f.PageSize}
	from := transactionFrom + ` JOIN allocations a ON a.run_id=r.id AND a.cancel_id=n.id JOIN normalized_rows sn ON sn.dataset_id=d.id AND sn.id=a.sale_id JOIN source_rows ss ON ss.id=sn.source_id`
	where, args := queryWhere(actor, f)
	if err = s.DB.QueryRowContext(ctx, "SELECT count(*)"+from+where, args...).Scan(&out.Total); err != nil {
		return
	}
	args = append(args, f.PageSize, (f.Page-1)*f.PageSize)
	rows, e := s.DB.QueryContext(ctx, `SELECT a.run_id,a.cancel_id,a.sale_id,a.quantity,a.amount,a.method,coalesce(a.actor,''),r.rule_version,sr.sheet,sr.source_row,ss.sheet,ss.source_row`+from+where+" ORDER BY sr.sheet_index,sr.source_row,a.cancel_id LIMIT ? OFFSET ?", args...)
	if e != nil {
		err = e
		return
	}
	defer rows.Close()
	for rows.Next() {
		var a AllocationRecord
		if err = rows.Scan(&a.RunID, &a.CancelID, &a.SaleID, &a.Quantity, &a.Amount, &a.Method, &a.Actor, &a.RuleVersion, &a.CancelSheet, &a.CancelSourceRow, &a.SaleSheet, &a.SaleSourceRow); err != nil {
			return
		}
		out.Items = append(out.Items, a)
	}
	err = rows.Err()
	return
}

const candidateFrom = ` FROM cases cc JOIN runs r ON r.id=cc.run_id JOIN datasets d ON d.id=r.dataset_id
 JOIN normalized_rows cr ON cr.dataset_id=d.id AND cr.id=cc.cancel_id
 JOIN normalized_rows n ON n.dataset_id=d.id AND n.customer=cr.customer AND n.stock=cr.stock AND n.price=cr.price AND n.country=cr.country
 JOIN source_rows sr ON sr.id=n.source_id JOIN sale_balances b ON b.run_id=r.id AND b.sale_id=n.id`
const candidateWhere = ` WHERE cc.id=? AND r.status='published' AND d.status='published' AND cr.kind='cancel' AND cr.quantity<0 AND cr.quantity>-9223372036854775808
 AND cr.excluded='' AND cr.customer<>'' AND cr.country<>'' AND cr.stock<>'' AND n.kind='sale' AND n.excluded='' AND n.quantity>0 AND n.source_time<cr.source_time AND b.remaining>=-cr.quantity`

func (s *Store) ListCandidates(ctx context.Context, actor WorkflowActor, caseID string, p PageRequest) (out CandidatePage, err error) {
	if _, err = s.WorkflowCase(ctx, actor, caseID); err != nil {
		return
	}
	p, err = normalizePage(p)
	if err != nil {
		return
	}
	out = CandidatePage{Items: []CandidateRecord{}, Page: p.Page, PageSize: p.PageSize}
	if err = s.DB.QueryRowContext(ctx, "SELECT count(*)"+candidateFrom+candidateWhere, caseID).Scan(&out.Total); err != nil {
		return
	}
	rows, e := s.DB.QueryContext(ctx, "SELECT "+transactionColumns+",b.original,b.allocated,b.remaining"+candidateFrom+candidateWhere+" ORDER BY sr.sheet_index,sr.source_row,n.id LIMIT ? OFFSET ?", caseID, p.PageSize, (p.Page-1)*p.PageSize)
	if e != nil {
		err = e
		return
	}
	defer rows.Close()
	for rows.Next() {
		var c CandidateRecord
		if err = scanTransaction(rows, &c.TransactionRecord, &c.Original, &c.Allocated, &c.Remaining); err != nil {
			return
		}
		out.Items = append(out.Items, c)
	}
	err = rows.Err()
	return
}

func (s *Store) CaseEvidence(ctx context.Context, actor WorkflowActor, caseID string, p PageRequest) (out CaseEvidencePage, err error) {
	if out.Case, err = s.WorkflowCase(ctx, actor, caseID); err != nil {
		return
	}
	r, err := readManualRow(ctx, s.DB, out.Case.DatasetID, out.Case.CancelID)
	if err != nil {
		return
	}
	out.Cancel = TransactionRecord{Row: r, DatasetID: out.Case.DatasetID, SourceHash: out.Case.SourceHash, RuleVersion: out.Case.RuleVersion, CleaningVersion: out.Case.CleaningVersion}
	if err = s.DB.QueryRowContext(ctx, "SELECT scope FROM datasets WHERE id=?", out.Case.DatasetID).Scan(&out.Cancel.Scope); err != nil {
		return
	}
	out.DecisionEvidence = []DecisionEvidence{}
	rows, e := s.DB.QueryContext(ctx, "SELECT "+transactionColumns+`,b.original,b.allocated,b.remaining,e.remaining,e.eligible,e.reason
 FROM cases cc JOIN runs r ON r.id=cc.run_id JOIN datasets d ON d.id=r.dataset_id
 JOIN candidate_evidence e ON e.run_id=r.id AND e.cancel_id=cc.cancel_id JOIN normalized_rows n ON n.dataset_id=d.id AND n.id=e.sale_id
 JOIN source_rows sr ON sr.id=n.source_id JOIN sale_balances b ON b.run_id=r.id AND b.sale_id=n.id WHERE cc.id=? ORDER BY e.position`, caseID)
	if e != nil {
		err = e
		return
	}
	for rows.Next() {
		var item DecisionEvidence
		if err = scanTransaction(rows, &item.TransactionRecord, &item.Original, &item.Allocated, &item.Remaining, &item.RemainingAtDecision, &item.EligibleAtDecision, &item.Reason); err != nil {
			rows.Close()
			return
		}
		out.DecisionEvidence = append(out.DecisionEvidence, item)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return
	}
	out.Candidates, err = s.ListCandidates(ctx, actor, caseID, p)
	out.Caveat = "规则推断；决策时证据仅支撑原分类，当前合格候选分页列出；确认时仍需重新校验。没有真实原订单关联号。"
	return
}

// CSVCell neutralizes formulas, including leading whitespace/control variants.
// Strings are kept textual; numeric output also passes this guard consistently.
func CSVCell(v string) string {
	trimmed := strings.TrimLeftFunc(v, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) || r == '\ufeff' })
	if trimmed != "" && strings.ContainsRune("=+-@", rune(trimmed[0])) {
		return "'" + v
	}
	return v
}

// ExportCSV streams the same transaction scope and filters as ListTransactions.
// It deliberately ignores page/page_size so all filtered rows are exported.
func (s *Store) ExportCSV(ctx context.Context, actor WorkflowActor, f QueryFilter, w io.Writer) error {
	if !actor.valid() {
		return workflowError("unauthenticated", "需要可信服务端身份", WorkflowCase{})
	}
	if actor.Role != "finance" && actor.Role != "admin" {
		return workflowError("forbidden", "只有财务或管理员可以导出当前筛选明细", WorkflowCase{})
	}
	f, err := s.validateFilter(ctx, actor, f)
	if err != nil {
		return err
	}
	where, args := queryWhere(actor, f)
	rows, err := s.DB.QueryContext(ctx, "SELECT "+transactionColumns+transactionFrom+where+" ORDER BY sr.sheet_index,sr.source_row,n.id", args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	writer := csv.NewWriter(w)
	if err = writer.Write([]string{"run_id", "dataset_id", "scope", "source_hash", "sheet", "source_row", "rule_version", "cleaning_version", "row_id", "invoice", "stock", "description", "quantity", "source_time_without_timezone", "exact_price", "customer", "country", "kind", "excluded", "flags", "basis"}); err != nil {
		return err
	}
	for rows.Next() {
		var r TransactionRecord
		if err = scanTransaction(rows, &r); err != nil {
			return err
		}
		record := []string{f.RunID, r.DatasetID, r.Scope, r.SourceHash, r.Sheet, strconv.Itoa(r.SourceRow), r.RuleVersion, r.CleaningVersion, r.ID, r.Invoice, r.Stock, r.Description, strconv.FormatInt(r.Quantity, 10), r.Time, r.Price, r.Customer, r.Country, r.Kind, r.Excluded, r.Flags, "规则推断；非真实原订单关系"}
		for i := range record {
			record[i] = CSVCell(record[i])
		}
		if err = writer.Write(record); err != nil {
			return err
		}
		if err = ctx.Err(); err != nil {
			return err
		}
	}
	if err = rows.Err(); err != nil {
		return err
	}
	writer.Flush()
	if err = writer.Error(); err != nil {
		return fmt.Errorf("stream CSV: %w", err)
	}
	return nil
}
