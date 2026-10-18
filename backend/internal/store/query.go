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
	ReviewGroupBy  string `json:"review_group_by,omitempty"`
	ReviewGroupKey string `json:"review_group_key,omitempty"`
	RunID          string `json:"run_id"`
	Status         string `json:"status"`
	Reason         string `json:"reason"`
	Assignee       string `json:"assignee"`
	Customer       string `json:"customer"`
	Stock          string `json:"stock"`
	Invoice        string `json:"invoice"`
	Country        string `json:"country"`
	Sort           string `json:"sort"`
	Direction      string `json:"direction"`
	Classification string `json:"classification"`
	Kind           string `json:"kind"`
	DateFrom       string `json:"date_from"`
	DateTo         string `json:"date_to"`
	Page           int    `json:"page"`
	PageSize       int    `json:"page_size"`
	datasetID      string // Resolved from the published run, never from client input.
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
	Original       int64             `json:"original"`
	Allocated      int64             `json:"allocated"`
	Remaining      int64             `json:"current_remaining"`
	DuplicateGroup *DuplicateBalance `json:"duplicate_group,omitempty"`
}
type DecisionEvidence struct {
	CandidateRecord
	RemainingAtDecision int64  `json:"remaining_at_decision"`
	EligibleAtDecision  bool   `json:"eligible_at_decision"`
	Reason              string `json:"reason"`
}
type AllocationRecord struct {
	BatchID            string `json:"batch_id,omitempty"`
	RecommendationKind string `json:"recommendation_kind,omitempty"`
	PolicyVersion      string `json:"policy_version,omitempty"`
	RunID              string `json:"run_id"`
	CancelID           string `json:"cancel_id"`
	SaleID             string `json:"sale_id"`
	Quantity           int64  `json:"quantity"`
	Amount             string `json:"amount"`
	Method             string `json:"method"`
	Actor              string `json:"actor"`
	RuleVersion        string `json:"rule_version"`
	CancelSheet        string `json:"cancel_sheet"`
	CancelSourceRow    int    `json:"cancel_source_row"`
	SaleSheet          string `json:"sale_sheet"`
	SaleSourceRow      int    `json:"sale_source_row"`
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
	Confirmation     *CaseConfirmation  `json:"confirmation,omitempty"`
	Caveat           string             `json:"caveat"`
}

// A saved allocation remains visible even when its sale has no eligible balance
// left or is outside the current candidate page. Authorization is inherited from
// CaseEvidence's ownership check; this never broadens the candidate query.
type CaseConfirmation struct {
	Allocation AllocationRecord `json:"allocation"`
	Sale       CandidateRecord  `json:"sale"`
}

// EnsureQueryIndexes is an idempotent startup migration. It changes no business
// records and must run before serving queries, never on a user's read request.
func (s *Store) EnsureQueryIndexes(ctx context.Context) error {
	return s.ensureQueryIndexes(ctx, false)
}

// Defer non-constraint row indexes on an empty database until the import has
// inserted and verified its rows. Building them once retains the same indexes
// without maintaining every B-tree for each of a million individual inserts.
func (s *Store) ensureQueryIndexes(ctx context.Context, force bool) error {
	return s.immediate(ctx, func(conn *sql.Conn) error {
		statements := []string{
			"CREATE INDEX IF NOT EXISTS cases_owner ON cases(run_id,assignee,cancel_id)",
			"CREATE INDEX IF NOT EXISTS cases_source_order ON cases(run_id," + cancelOrder + ",id)",
			"CREATE INDEX IF NOT EXISTS allocations_source_order ON allocations(run_id," + cancelOrder + ")",
		}
		var populated bool
		if !force {
			if err := conn.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM normalized_rows LIMIT 1)").Scan(&populated); err != nil {
				return err
			}
		}
		if force || populated {
			statements = append(statements,
				"CREATE INDEX IF NOT EXISTS row_lookup ON normalized_rows(dataset_id,excluded,kind)",
				"CREATE INDEX IF NOT EXISTS row_identity ON normalized_rows(dataset_id,customer,stock,price,country,source_time)",
				"CREATE INDEX IF NOT EXISTS row_invoice ON normalized_rows(dataset_id,invoice)",
				"CREATE INDEX IF NOT EXISTS row_source_time ON normalized_rows(dataset_id,source_time,id)",
				"CREATE INDEX IF NOT EXISTS row_stock ON normalized_rows(dataset_id,stock,id)",
				"CREATE INDEX IF NOT EXISTS row_query_source ON normalized_rows(dataset_id,"+rowSourceOrder("id")+",id)",
				"CREATE INDEX IF NOT EXISTS row_country ON normalized_rows(dataset_id,country,id)",
				"CREATE INDEX IF NOT EXISTS row_customer ON normalized_rows(dataset_id,customer,id)",
			)
			// The old row_source/source_order paths have been replaced by numeric
			// page keys. row_lookup already covers dataset/kind counts. Do not build
			// those redundant indexes on new input; existing indexes are preserved.
		}
		for _, statement := range statements {
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
	if f.Reason != "" && !caseReasons[f.Reason] {
		return f, workflowError("invalid_input", "未知差异原因", WorkflowCase{})
	}
	if f.Classification != "" && f.Classification != "matched" && f.Classification != "suspected" && f.Classification != "no_original" {
		return f, workflowError("invalid_input", "未知初始分类", WorkflowCase{})
	}
	if f.Kind != "" && f.Kind != "sale" && f.Kind != "cancel" {
		return f, workflowError("invalid_input", "未知交易类型", WorkflowCase{})
	}
	if f.Sort == "" {
		f.Sort = "source"
	}
	if _, ok := transactionSortColumns[f.Sort]; !ok {
		return f, workflowError("invalid_input", "未知交易排序字段", WorkflowCase{})
	}
	if f.Direction == "" {
		f.Direction = "asc"
	}
	if f.Direction != "asc" && f.Direction != "desc" {
		return f, workflowError("invalid_input", "排序方向须为asc或desc", WorkflowCase{})
	}
	err = s.DB.QueryRowContext(ctx, "SELECT d.id FROM runs r JOIN datasets d ON d.id=r.dataset_id WHERE r.id=? AND r.status='published' AND d.status='published'", f.RunID).Scan(&f.datasetID)
	if errors.Is(err, sql.ErrNoRows) {
		err = workflowError("not_found", "运行不存在或尚未发布", WorkflowCase{})
	}
	return f, err
}

const transactionColumns = `n.id,sr.sheet_index,sr.sheet,sr.source_row,n.invoice,n.stock,n.description,n.quantity,n.source_time,n.price,n.customer,n.country,n.kind,n.excluded,n.flags,n.duplicate_of,d.id,d.scope,d.source_hash,r.rule_version,d.cleaning_version`
const runFrom = ` FROM runs r JOIN datasets d ON d.id=r.dataset_id`

// Exports and permission-scoped keys use only the relations their filters need.
func transactionQueryFrom(f QueryFilter, includeSource bool) string {
	from := runFrom + ` JOIN normalized_rows n ON n.dataset_id=d.id`
	if includeSource {
		from += ` JOIN source_rows sr ON sr.id=n.source_id`
	}
	return from + filterJoins(f, false)
}

// SQLite's generic row-count estimates can choose the year-wide date index
// over an exact customer lookup. Pick only known production indexes and let
// each narrow lookup sort its compact keys. Broad source pages walk their
// numeric order directly, including arbitrary OFFSET jumps.
func transactionKeyFrom(f QueryFilter, page bool) string {
	return runFrom + ` JOIN ` + transactionRowsFrom(f, page) + ` ON n.dataset_id=d.id` + filterJoins(f, false)
}

func transactionRowsFrom(f QueryFilter, page bool) string {
	index := ""
	switch {
	case f.Invoice != "":
		index = "row_invoice"
	case f.Customer != "":
		index = "row_customer"
	case f.Stock != "":
		index = "row_stock"
	case page && narrowTransactionDate(f):
		index = "row_source_time"
	case page && (f.Sort == "source" || f.Sort == "") && !caseFiltered(f) && f.Classification == "":
		index = "row_query_source"
	case page && f.Sort == "date":
		index = "row_source_time"
	case page && f.Sort == "customer":
		index = "row_customer"
	case page && f.Sort == "stock":
		index = "row_stock"
	case page && f.Sort == "country":
		index = "row_country"
	case page && f.Sort == "invoice":
		index = "row_invoice"
	case f.Country != "":
		index = "row_country"
	case f.DateFrom != "" || f.DateTo != "":
		index = "row_source_time"
	case f.Kind != "":
		index = "row_lookup"
	}
	from := `normalized_rows n`
	if index != "" {
		from += " INDEXED BY " + index
	}
	return from
}

// These scopes contain only immutable published input. Warehouse ownership and
// workflow/decision filters must retain their live joins on both count and page.
func immutableTransactionScope(actor WorkflowActor, f QueryFilter) bool {
	return (actor.Role == "finance" || actor.Role == "admin") && !caseFiltered(f) && f.Classification == ""
}

func immutableTransactionWhere(f QueryFilter) (string, []any) {
	parts := []string{"n.dataset_id=?"}
	args := []any{f.datasetID}
	for _, pair := range []struct{ sql, value string }{{"n.customer=?", f.Customer}, {"n.stock=?", f.Stock}, {"n.invoice=?", f.Invoice}, {"n.country=?", f.Country}, {"n.kind=?", f.Kind}, {"n.source_time>=?", f.DateFrom}, {"n.source_time<?", f.DateTo}} {
		if pair.value != "" {
			parts = append(parts, pair.sql)
			args = append(args, pair.value)
		}
	}
	return " WHERE " + strings.Join(parts, " AND "), args
}

func narrowTransactionDate(f QueryFilter) bool {
	if f.DateFrom == "" || f.DateTo == "" {
		return false
	}
	from, fromErr := time.Parse("2006-01-02T15:04:05.000000", f.DateFrom)
	to, toErr := time.Parse("2006-01-02T15:04:05.000000", f.DateTo)
	return fromErr == nil && toErr == nil && to.Sub(from) <= 31*24*time.Hour
}

func filterJoins(f QueryFilter, hasCase bool) string {
	from := ""
	if !hasCase && caseFiltered(f) {
		from += ` JOIN cases c ON c.run_id=r.id AND c.cancel_id=n.id`
	}
	if f.Classification != "" {
		from += ` JOIN decisions dc ON dc.run_id=r.id AND dc.cancel_id=n.id`
	}
	return from
}

// Case reasons produced by the conservative rule version. They are stored on
// cases, so a reason filter needs the live cases join like status/assignee.
var caseReasons = map[string]bool{
	"missing_identity": true, "suspected_duplicate_cancel": true, "ambiguous_candidates": true, "suspected_duplicate_sale": true,
	"no_identity_match": true, "no_strictly_earlier_sale": true, "insufficient_remaining_quantity": true,
}

func caseFiltered(f QueryFilter) bool {
	return f.Status != "" || f.Reason != "" || f.Assignee != ""
}

func unfilteredTransactions(f QueryFilter) bool {
	return !caseFiltered(f) && f.Customer == "" && f.Stock == "" && f.Invoice == "" && f.Country == "" && f.Classification == "" && f.Kind == "" && f.DateFrom == "" && f.DateTo == ""
}

func manifestTransactionCount(actor WorkflowActor, f QueryFilter) bool {
	return actor.Role != "warehouse" && unfilteredTransactions(f)
}

type scanner interface{ Scan(...any) error }

func scanTransaction(s scanner, r *TransactionRecord, extra ...any) error {
	args := []any{&r.ID, &r.SheetIndex, &r.Sheet, &r.SourceRow, &r.Invoice, &r.Stock, &r.Description, &r.Quantity, &r.Time, &r.Price, &r.Customer, &r.Country, &r.Kind, &r.Excluded, &r.Flags, &r.DuplicateOf, &r.DatasetID, &r.Scope, &r.SourceHash, &r.RuleVersion, &r.CleaningVersion}
	return s.Scan(append(args, extra...)...)
}

// Related sales are restricted to a warehouse user's own case's current
// qualified candidates, original decision evidence or confirmed allocation.
const warehouseRelated = `n.id IN (
 SELECT own.cancel_id FROM cases own WHERE own.run_id=? AND own.assignee=?
 UNION SELECT oa.sale_id FROM cases own CROSS JOIN allocations oa ON oa.run_id=own.run_id AND oa.cancel_id=own.cancel_id WHERE own.run_id=? AND own.assignee=?
 UNION SELECT oe.sale_id FROM cases own CROSS JOIN candidate_evidence oe ON oe.run_id=own.run_id AND oe.cancel_id=own.cancel_id WHERE own.run_id=? AND own.assignee=?
 UNION SELECT eligible.id FROM cases own CROSS JOIN runs ownrun ON ownrun.id=own.run_id
 CROSS JOIN normalized_rows cr ON cr.dataset_id=ownrun.dataset_id AND cr.id=own.cancel_id
 CROSS JOIN normalized_rows eligible ON eligible.dataset_id=cr.dataset_id AND eligible.customer=cr.customer AND eligible.country=cr.country AND eligible.stock=cr.stock AND eligible.price=cr.price
 CROSS JOIN sale_balances rb ON rb.run_id=own.run_id AND rb.sale_id=eligible.id
 WHERE own.run_id=? AND own.assignee=? AND cr.kind='cancel' AND cr.quantity<0 AND cr.quantity>-9223372036854775808 AND cr.excluded=''
 AND cr.customer<>'' AND cr.country<>'' AND cr.stock<>'' AND eligible.kind='sale' AND eligible.excluded='' AND eligible.quantity>0 AND eligible.source_time<cr.source_time AND rb.remaining>=-cr.quantity
)`

// Import validates every row ID against its sheet_index:source_row. These
// numeric expression indexes preserve source order without joining/sorting all
// rows before LIMIT. Both values are nonnegative integers, never lexical IDs.
const cancelOrder = `CAST(substr(cancel_id,1,instr(cancel_id,':')-1) AS INTEGER),CAST(substr(cancel_id,instr(cancel_id,':')+1) AS INTEGER)`

func sourceOrder(alias string) string {
	return strings.ReplaceAll(cancelOrder, "cancel_id", alias+".cancel_id")
}

func rowSourceOrder(id string) string {
	return strings.ReplaceAll(cancelOrder, "cancel_id", id)
}

var transactionSortColumns = map[string]string{
	"source": "", "date": "n.source_time", "invoice": "n.invoice", "customer": "n.customer", "stock": "n.stock", "country": "n.country",
}

// Source order is numeric, as verified on import; other sorts use the unique
// row ID as a deterministic tie breaker. Only validated constants reach SQL.
func transactionOrder(f QueryFilter) string {
	direction := " ASC"
	if f.Direction == "desc" {
		direction = " DESC"
	}
	if f.Sort == "" || f.Sort == "source" {
		return "CAST(substr(n.id,1,instr(n.id,':')-1) AS INTEGER)" + direction + ",CAST(substr(n.id,instr(n.id,':')+1) AS INTEGER)" + direction + ",n.id" + direction
	}
	return transactionSortColumns[f.Sort] + direction + ",n.id" + direction
}

func hasRowFilter(f QueryFilter) bool {
	return f.Customer != "" || f.Stock != "" || f.Invoice != "" || f.Country != "" || f.Kind != "" || f.DateFrom != "" || f.DateTo != ""
}

func queryWhere(actor WorkflowActor, f QueryFilter) (string, []any) {
	return queryScopeWhere(actor, f, false)
}

func queryScopeWhere(actor WorkflowActor, f QueryFilter, ownCases bool) (string, []any) {
	parts := []string{"r.id=?", "r.status='published'", "d.status='published'"}
	args := []any{f.RunID}
	for _, pair := range []struct{ sql, value string }{{"c.status=?", f.Status}, {"c.reason=?", f.Reason}, {"c.assignee=?", f.Assignee}, {"n.customer=?", f.Customer}, {"n.stock=?", f.Stock}, {"n.invoice=?", f.Invoice}, {"n.country=?", f.Country}, {"dc.classification=?", f.Classification}, {"n.kind=?", f.Kind}, {"n.source_time>=?", f.DateFrom}, {"n.source_time<?", f.DateTo}} {
		if pair.value != "" {
			parts = append(parts, pair.sql)
			args = append(args, pair.value)
		}
	}
	if actor.Role == "warehouse" {
		if ownCases {
			parts = append(parts, "c.assignee=?")
			args = append(args, actor.ID)
		} else {
			parts = append(parts, warehouseRelated)
			for range 4 {
				args = append(args, f.RunID, actor.ID)
			}
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
	ordered := manifestTransactionCount(actor, f)
	countFrom := transactionKeyFrom(f, false)
	keyFrom := transactionKeyFrom(f, true)
	immutable := immutableTransactionScope(actor, f)
	if immutable {
		// Publication is checked above on every request. Bind the resolved dataset
		// directly, avoiding a run/dataset lookup for each scanned input row.
		where, args = immutableTransactionWhere(f)
		countFrom = " FROM " + transactionRowsFrom(f, false)
		keyFrom = " FROM " + transactionRowsFrom(f, true)
	}
	countSQL := "SELECT count(*)" + countFrom + where
	countArgs := args
	if ordered {
		// Import verifies the immutable manifest's raw count before publication.
		// Read that persisted count, not an account cache or a full-row COUNT.
		countSQL = `SELECT json_extract(d.manifest_json,'$.stats.raw')` + runFrom + ` WHERE r.id=? AND r.status='published' AND d.status='published'`
		countArgs = []any{f.RunID}
	}
	countKey, boundedKey := immutableCountKey(f)
	cacheCount := immutable && !ordered && boundedKey
	cached := false
	if cacheCount {
		out.Total, cached = s.transactionCounts.get(countKey)
	}
	if !cached {
		if err = s.DB.QueryRowContext(ctx, countSQL, countArgs...).Scan(&out.Total); err != nil {
			return
		}
		if cacheCount {
			s.transactionCounts.put(countKey, out.Total)
		}
	}
	// OFFSET walks compact row/index entries. Read full source/provenance only
	// for the selected 50/100 keys, rather than for every skipped/sorted row.
	if actor.Role == "warehouse" {
		keyFrom = transactionQueryFrom(f, false)
	}
	pageOrder := f
	limit, offset := f.PageSize, (f.Page-1)*f.PageSize
	if immutable {
		if offset >= out.Total {
			return out, nil
		}
		limit = min(limit, out.Total-offset)
		// Scan from the closer end. Use the actual tail length, so a partial
		// final page selects the same keys. The outer query restores original order.
		if reverseOffset := out.Total - offset - limit; reverseOffset < offset {
			offset = reverseOffset
			pageOrder.Direction = "desc"
			if f.Direction == "desc" {
				pageOrder.Direction = "asc"
			}
		}
	}
	keys := "SELECT n.dataset_id,n.id" + keyFrom + where + " ORDER BY " + transactionOrder(pageOrder) + " LIMIT ? OFFSET ?"
	args = append(args, limit, offset, f.RunID)
	rows, e := s.DB.QueryContext(ctx, "WITH page AS MATERIALIZED ("+keys+") SELECT "+transactionColumns+` FROM page CROSS JOIN normalized_rows n ON n.dataset_id=page.dataset_id AND n.id=page.id CROSS JOIN source_rows sr ON sr.id=n.source_id CROSS JOIN datasets d ON d.id=n.dataset_id CROSS JOIN runs r ON r.id=? ORDER BY `+transactionOrder(f), args...)
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
	where, args := queryScopeWhere(actor, f, true)
	from := runFrom + ` CROSS JOIN cases c ON c.run_id=r.id CROSS JOIN normalized_rows n ON n.dataset_id=d.id AND n.id=c.cancel_id` + filterJoins(f, true)
	countFrom := from
	if !hasRowFilter(f) && f.Classification == "" {
		countFrom = runFrom + ` CROSS JOIN cases c ON c.run_id=r.id`
	}
	if err = s.DB.QueryRowContext(ctx, "SELECT count(*)"+countFrom+where, args...).Scan(&out.Total); err != nil {
		return
	}
	args = append(args, f.PageSize, (f.Page-1)*f.PageSize)
	rows, e := s.DB.QueryContext(ctx, `SELECT c.id,c.run_id,r.dataset_id,c.cancel_id,c.reason,
 c.status,coalesce(c.assignee,''),c.version,c.conclusion,c.assigned_at,c.assigned_by,
 d.source_hash,sr.sheet,sr.source_row,r.rule_version,d.cleaning_version`+from+` CROSS JOIN source_rows sr ON sr.id=n.source_id`+where+" ORDER BY "+sourceOrder("c")+",c.id LIMIT ? OFFSET ?", args...)
	if e != nil {
		err = e
		return
	}
	defer rows.Close()
	for rows.Next() {
		var c WorkflowCase
		if err = rows.Scan(&c.ID, &c.RunID, &c.DatasetID, &c.CancelID, &c.Reason,
			&c.Status, &c.Assignee, &c.Version, &c.Conclusion, &c.AssignedAt, &c.AssignedBy,
			&c.SourceHash, &c.Sheet, &c.SourceRow, &c.RuleVersion, &c.CleaningVersion); err != nil {
			return
		}
		if err = authorizeWorkflowRead(actor, c); err != nil {
			return
		}
		out.Items = append(out.Items, c)
	}
	err = rows.Err()
	return
}

func (s *Store) ListAllocations(ctx context.Context, actor WorkflowActor, f QueryFilter) (out AllocationPage, err error) {
	f, err = s.validateFilter(ctx, actor, f)
	if err != nil {
		return
	}
	out = AllocationPage{Items: []AllocationRecord{}, Page: f.Page, PageSize: f.PageSize}
	from := runFrom + ` CROSS JOIN allocations a ON a.run_id=r.id CROSS JOIN normalized_rows n ON n.dataset_id=d.id AND n.id=a.cancel_id`
	if actor.Role == "warehouse" || caseFiltered(f) {
		from += ` JOIN cases c ON c.run_id=r.id AND c.cancel_id=a.cancel_id`
	}
	from += filterJoins(f, true)
	where, args := queryScopeWhere(actor, f, true)
	countFrom := from
	if !hasRowFilter(f) && f.Classification == "" {
		countFrom = runFrom + ` CROSS JOIN allocations a ON a.run_id=r.id`
		if actor.Role == "warehouse" || caseFiltered(f) {
			countFrom += ` CROSS JOIN cases c ON c.run_id=r.id AND c.cancel_id=a.cancel_id`
		}
	}
	if err = s.DB.QueryRowContext(ctx, "SELECT count(*)"+countFrom+where, args...).Scan(&out.Total); err != nil {
		return
	}
	from += ` CROSS JOIN source_rows sr ON sr.id=n.source_id CROSS JOIN normalized_rows sn ON sn.dataset_id=d.id AND sn.id=a.sale_id CROSS JOIN source_rows ss ON ss.id=sn.source_id`
	args = append(args, f.PageSize, (f.Page-1)*f.PageSize)
	rows, e := s.DB.QueryContext(ctx, `SELECT a.run_id,a.cancel_id,a.sale_id,a.quantity,a.amount,a.method,coalesce(a.actor,''),r.rule_version,sr.sheet,sr.source_row,ss.sheet,ss.source_row`+from+where+" ORDER BY "+sourceOrder("a")+" LIMIT ? OFFSET ?", args...)
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
	rows.Close()
	if err != nil {
		return
	}
	for i := range out.Items {
		if err = s.allocationReviewTrace(ctx, &out.Items[i]); err != nil {
			return
		}
	}
	return
}

const candidateFrom = ` FROM cases cc JOIN runs r ON r.id=cc.run_id JOIN datasets d ON d.id=r.dataset_id
 JOIN normalized_rows cr ON cr.dataset_id=d.id AND cr.id=cc.cancel_id
 JOIN normalized_rows n ON n.dataset_id=d.id AND n.customer=cr.customer AND n.stock=cr.stock AND n.price=cr.price AND n.country=cr.country
 JOIN source_rows sr ON sr.id=n.source_id JOIN sale_balances b ON b.run_id=r.id AND b.sale_id=n.id`
const candidateWhere = ` WHERE cc.id=? AND r.status='published' AND d.status='published' AND cr.kind='cancel' AND cr.quantity<0 AND cr.quantity>-9223372036854775808
 AND cr.excluded='' AND cr.customer<>'' AND cr.country<>'' AND cr.stock<>'' AND n.kind='sale' AND n.excluded='' AND n.quantity>0 AND n.source_time<cr.source_time AND b.remaining>=-cr.quantity
 AND NOT EXISTS(SELECT 1 FROM duplicate_sale_members dm JOIN duplicate_sale_groups dg ON dg.run_id=dm.run_id AND dg.id=dm.group_id WHERE dm.run_id=r.id AND dm.sale_id=n.id AND (dg.canonical_id<>n.id OR dg.capacity-(SELECT coalesce(sum(db.allocated),0) FROM duplicate_sale_members mm JOIN sale_balances db ON db.run_id=mm.run_id AND db.sale_id=mm.sale_id WHERE mm.run_id=dg.run_id AND mm.group_id=dg.id)<-cr.quantity))`

func (s *Store) ListCandidates(ctx context.Context, actor WorkflowActor, caseID string, p PageRequest) (out CandidatePage, err error) {
	c, caseErr := s.WorkflowCase(ctx, actor, caseID)
	if caseErr != nil {
		err = caseErr
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
	rows.Close()
	if err != nil {
		return
	}
	for i := range out.Items {
		out.Items[i].DuplicateGroup, err = s.duplicateBalance(ctx, c.RunID, out.Items[i].ID)
		if err != nil {
			return
		}
	}
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
	confirmation := CaseConfirmation{}
	a := &confirmation.Allocation
	err = s.DB.QueryRowContext(ctx, `SELECT run_id,cancel_id,sale_id,quantity,amount,method,coalesce(actor,'') FROM allocations WHERE run_id=? AND cancel_id=?`, out.Case.RunID, out.Case.CancelID).Scan(&a.RunID, &a.CancelID, &a.SaleID, &a.Quantity, &a.Amount, &a.Method, &a.Actor)
	if err == nil {
		sale, e := readManualRow(ctx, s.DB, out.Case.DatasetID, a.SaleID)
		if e != nil {
			return out, e
		}
		confirmation.Sale.TransactionRecord = out.Cancel
		confirmation.Sale.Row = sale
		if err = s.allocationReviewTrace(ctx, a); err != nil {
			return
		}
		if confirmation.Sale.DuplicateGroup, err = s.duplicateBalance(ctx, a.RunID, a.SaleID); err != nil {
			return
		}
		err = s.DB.QueryRowContext(ctx, `SELECT original,allocated,remaining FROM sale_balances WHERE run_id=? AND sale_id=?`, a.RunID, a.SaleID).Scan(&confirmation.Sale.Original, &confirmation.Sale.Allocated, &confirmation.Sale.Remaining)
		if err != nil {
			return
		}
		a.RuleVersion, a.CancelSheet, a.CancelSourceRow = out.Case.RuleVersion, out.Cancel.Sheet, out.Cancel.SourceRow
		a.SaleSheet, a.SaleSourceRow = sale.Sheet, sale.SourceRow
		out.Confirmation = &confirmation
	} else if !errors.Is(err, sql.ErrNoRows) {
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
	rows, err := s.DB.QueryContext(ctx, "SELECT "+transactionColumns+transactionQueryFrom(f, true)+where+" ORDER BY "+transactionOrder(f), args...)
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
