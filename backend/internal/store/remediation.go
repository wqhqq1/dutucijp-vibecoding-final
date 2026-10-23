package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/shopspring/decimal"
	"returnsrecon/internal/access"
	"returnsrecon/internal/model"
)

// The original dataset and first decisions are immutable. New runs keep an
// indexed overlay of changed rows, created atomically with each correction.
func ensureBusinessIndexes(ctx context.Context, q interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}) error {
	_, err := q.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS business_identity ON business_rows(run_id,customer,stock,price,country,source_time);
CREATE INDEX IF NOT EXISTS business_country ON business_rows(run_id,country,id);
CREATE INDEX IF NOT EXISTS business_invoice ON business_rows(run_id,invoice,id);
CREATE INDEX IF NOT EXISTS business_stock ON business_rows(run_id,stock,id);
CREATE INDEX IF NOT EXISTS business_date ON business_rows(run_id,source_time,id);
CREATE INDEX IF NOT EXISTS business_kind ON business_rows(run_id,excluded,kind,id);
CREATE INDEX IF NOT EXISTS business_dataset ON business_rows(run_id,dataset_id,id);
CREATE INDEX IF NOT EXISTS business_source_id ON business_rows(run_id,source_id);
CREATE INDEX IF NOT EXISTS business_source_order ON business_rows(run_id,CAST(substr(id,1,instr(id,':')-1) AS INTEGER),CAST(substr(id,instr(id,':')+1) AS INTEGER),id);`)
	return err
}

func (s *Store) migrateRemediation(ctx context.Context) error {
	var definition string
	if err := s.DB.QueryRowContext(ctx, "SELECT sql FROM sqlite_master WHERE type='table' AND name='cases'").Scan(&definition); err != nil {
		return err
	}
	if !strings.Contains(definition, "generation") {
		c, err := s.DB.Conn(ctx)
		if err != nil {
			return err
		}
		defer c.Close()
		if _, err = c.ExecContext(ctx, "PRAGMA foreign_keys=OFF"); err != nil {
			return err
		}
		defer c.ExecContext(context.Background(), "PRAGMA foreign_keys=ON")
		if _, err = c.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
			return err
		}
		defer c.ExecContext(context.Background(), "ROLLBACK")
		statements := []string{
			`CREATE TABLE cases_remediation(id TEXT PRIMARY KEY,run_id TEXT NOT NULL,cancel_id TEXT NOT NULL,reason TEXT NOT NULL,status TEXT NOT NULL DEFAULT 'pending' CHECK(status IN ('pending','in_progress','closed')),assignee TEXT,version INTEGER NOT NULL DEFAULT 1,conclusion TEXT NOT NULL DEFAULT '',assigned_at TEXT NOT NULL DEFAULT '',assigned_by TEXT NOT NULL DEFAULT '',generation INTEGER NOT NULL DEFAULT 1,is_current INTEGER NOT NULL DEFAULT 1,previous_case_id TEXT NOT NULL DEFAULT '',outcome TEXT NOT NULL DEFAULT '',waiting INTEGER NOT NULL DEFAULT 0,missing_materials TEXT NOT NULL DEFAULT '',next_step TEXT NOT NULL DEFAULT '',UNIQUE(run_id,cancel_id,generation),FOREIGN KEY(run_id,cancel_id) REFERENCES decisions(run_id,cancel_id) ON DELETE CASCADE)`,
			`INSERT INTO cases_remediation(id,run_id,cancel_id,reason,status,assignee,version,conclusion,assigned_at,assigned_by,outcome) SELECT id,run_id,cancel_id,reason,status,assignee,version,conclusion,assigned_at,assigned_by,CASE WHEN status='closed' THEN 'legacy_closed' ELSE '' END FROM cases`,
			"DROP TABLE cases", "ALTER TABLE cases_remediation RENAME TO cases", "COMMIT",
		}
		for _, q := range statements {
			if _, err = c.ExecContext(ctx, q); err != nil {
				return err
			}
		}
		if _, err = c.ExecContext(ctx, "PRAGMA foreign_keys=ON"); err != nil {
			return err
		}
	}
	_, err := s.DB.ExecContext(ctx, `CREATE UNIQUE INDEX IF NOT EXISTS cases_current ON cases(run_id,cancel_id) WHERE is_current=1;
 CREATE INDEX IF NOT EXISTS cases_workflow ON cases(run_id,status,assignee);
 CREATE INDEX IF NOT EXISTS audit_case ON audit_events(case_id,id);`)
	if err != nil {
		return err
	}
	var sparseColumn int
	if err = s.DB.QueryRowContext(ctx, "SELECT count(*) FROM pragma_table_info('remediation_runs') WHERE name='sparse'").Scan(&sparseColumn); err != nil {
		return err
	}
	if sparseColumn == 0 {
		if _, err = s.DB.ExecContext(ctx, "ALTER TABLE remediation_runs ADD COLUMN sparse INTEGER NOT NULL DEFAULT 0"); err != nil {
			return err
		}
	}
	var initialized int
	if err = s.DB.QueryRowContext(ctx, "SELECT count(*) FROM remediation_runs WHERE initialized=1").Scan(&initialized); err != nil {
		return err
	}
	if initialized > 0 {
		return ensureBusinessIndexes(ctx, s.DB)
	}
	return nil
}

type businessQuerier interface {
	rowQuerier
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func (s *Store) businessQuery(ctx context.Context, run, query string, args ...any) (*sql.Rows, error) {
	return s.DB.QueryContext(ctx, businessSQL(ctx, s.DB, run, query), args...)
}
func (s *Store) businessQueryRow(ctx context.Context, run, query string, args ...any) *sql.Row {
	return s.DB.QueryRowContext(ctx, businessSQL(ctx, s.DB, run, query), args...)
}

func remediationState(ctx context.Context, q rowQuerier, run string) (revision int, initialized, sparse bool, err error) {
	err = q.QueryRowContext(ctx, "SELECT revision,initialized,sparse FROM remediation_runs WHERE run_id=?", run).Scan(&revision, &initialized, &sparse)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, false, nil
	}
	return
}
func remediationRevision(ctx context.Context, q rowQuerier, run string) (int, bool, error) {
	revision, active, _, err := remediationState(ctx, q, run)
	return revision, active, err
}

const effectiveRowColumns = "dataset_id,id,source_id,invoice,stock,description,quantity,source_time,price,customer,country,kind,excluded,flags,duplicate_of"

var businessIndexes = [][2]string{{"row_invoice", "business_invoice"}, {"row_customer", "business_identity"}, {"row_stock", "business_stock"}, {"row_source_time", "business_date"}, {"row_query_source", "business_source_order"}, {"row_country", "business_country"}, {"row_lookup", "business_kind"}, {"row_identity", "business_identity"}}

var businessSourceJoin = regexp.MustCompile(`(?:CROSS\s+)?JOIN\s+source_rows\s+(\w+)\s+ON\s+(\w+)\.id\s*=\s*(\w+)\.source_id`)

var businessRowJoin = regexp.MustCompile(`(?:CROSS\s+)?JOIN\s+normalized_rows\s+(\w+)\s+ON\s+(\w+)\.dataset_id\s*=\s*(\w+\.\w+)\s+AND\s+(\w+)\.id\s*=\s*(\w+\.\w+)`)

// An ID lookup must use the two primary indexes, not materialize the entire
// effective UNION before joining a page or the relatively small case list.
func businessLookupSQL(statement, key string) string {
	aliases := map[string]bool{}
	statement = businessRowJoin.ReplaceAllStringFunc(statement, func(join string) string {
		m := businessRowJoin.FindStringSubmatch(join)
		alias := m[1]
		if alias != m[2] || alias != m[4] {
			return join
		}
		// The same alias in another scope needs its own projection. Callers use
		// distinct aliases for page keys and hydration to keep this unambiguous.
		refs := regexp.MustCompile(`\bnormalized_rows\s+` + alias + `\b`)
		if len(refs.FindAllStringIndex(statement, -1)) != 1 {
			return join
		}
		aliases[alias] = true
		return "LEFT JOIN main.normalized_rows " + alias + "_original ON " + alias + "_original.dataset_id=" + m[3] + " AND " + alias + "_original.id=" + m[5] +
			" LEFT JOIN business_rows " + alias + "_override ON " + alias + "_override.run_id=" + key + " AND " + alias + "_override.dataset_id=" + m[3] + " AND " + alias + "_override.id=" + m[5] +
			" JOIN (SELECT 1) " + alias + "_present ON coalesce(" + alias + "_override.id," + alias + "_original.id) IS NOT NULL"
	})
	for alias := range aliases {
		fields := regexp.MustCompile(`\b` + alias + `\.(` + strings.ReplaceAll(effectiveRowColumns, ",", "|") + `)\b`)
		statement = fields.ReplaceAllStringFunc(statement, func(field string) string {
			column := strings.TrimPrefix(field, alias+".")
			return "coalesce(" + alias + "_override." + column + "," + alias + "_original." + column + ")"
		})
	}
	return statement
}

// Sources remain separate and indexed: a compound source view otherwise makes
// SQLite rescan or materialize a million unrelated rows for a single case.
func businessSourceSQL(statement, key string) string {
	aliases := map[string]bool{}
	statement = businessSourceJoin.ReplaceAllStringFunc(statement, func(join string) string {
		m := businessSourceJoin.FindStringSubmatch(join)
		if m[1] != m[2] {
			return join
		}
		alias, row := m[1], m[3]
		aliases[alias] = true
		return "LEFT JOIN main.source_rows " + alias + "_original ON " + alias + "_original.id=" + row + ".source_id LEFT JOIN supplemental_sources " + alias + "_supplement ON " + alias + "_supplement.run_id=" + key + " AND " + alias + "_supplement.id=" + row + ".source_id"
	})
	for alias := range aliases {
		fields := regexp.MustCompile(`\b` + alias + `\.(id|source_hash|sheet_index|sheet|source_row|raw_json)\b`)
		statement = fields.ReplaceAllStringFunc(statement, func(field string) string {
			column := strings.TrimPrefix(field, alias+".")
			return "coalesce(" + alias + "_original." + column + "," + alias + "_supplement." + column + ")"
		})
	}
	return statement
}

// New runs store only changed rows, while every unchanged original row remains
// part of the effective input. Existing full projections use their old layout.
func businessSQL(ctx context.Context, q rowQuerier, run, statement string) string {
	_, active, sparse, err := remediationState(ctx, q, run)
	if err != nil || !active {
		return statement
	}
	key := "'" + strings.ReplaceAll(run, "'", "''") + "'"
	statement = businessSourceSQL(statement, key)
	if sparse {
		statement = businessLookupSQL(statement, key)
	}
	projection := func(originalIndex, businessIndex string) string {
		index := ""
		if businessIndex != "" {
			index = " INDEXED BY " + businessIndex
		}
		overrides := "SELECT " + effectiveRowColumns + " FROM business_rows" + index + " WHERE run_id=" + key
		if !sparse {
			return overrides
		}
		baseIndex := ""
		if originalIndex != "" {
			baseIndex = " INDEXED BY " + originalIndex
		}
		original := "SELECT base.* FROM main.normalized_rows base" + baseIndex + " WHERE base.dataset_id=(SELECT dataset_id FROM runs WHERE id=" + key + ") AND NOT EXISTS(SELECT 1 FROM business_rows changed WHERE changed.run_id=" + key + " AND changed.id=base.id)"
		return original + " UNION ALL " + overrides
	}
	extra := ""
	for _, pair := range businessIndexes {
		if strings.Contains(statement, " INDEXED BY "+pair[0]) {
			name := "projected_" + pair[0]
			statement = strings.ReplaceAll(statement, "normalized_rows n INDEXED BY "+pair[0], name+" n")
			statement = strings.ReplaceAll(statement, "normalized_rows INDEXED BY "+pair[0], name)
			extra += "," + name + " AS NOT MATERIALIZED (" + projection(pair[0], pair[1]) + ")"
		}
	}
	ctes := "normalized_rows AS NOT MATERIALIZED (" + projection("", "") + `),
 decisions AS NOT MATERIALIZED (SELECT d.run_id,d.cancel_id,coalesce(a.quantity,d.quantity) quantity,coalesce(a.classification,d.classification) classification,coalesce(a.reason,d.reason) reason,coalesce(a.candidate_lower_bound,d.candidate_lower_bound) candidate_lower_bound FROM main.decisions d LEFT JOIN current_assessments a ON a.run_id=d.run_id AND a.cancel_id=d.cancel_id)` + extra
	if strings.HasPrefix(statement, "WITH ") {
		return "WITH " + ctes + "," + strings.TrimPrefix(statement, "WITH ")
	}
	return "WITH " + ctes + " " + statement
}

func materializeBusinessRow(ctx context.Context, c *sql.Conn, run, id string) error {
	_, err := c.ExecContext(ctx, `INSERT INTO business_rows SELECT ?,n.* FROM main.normalized_rows n JOIN runs r ON r.dataset_id=n.dataset_id WHERE r.id=? AND n.id=? ON CONFLICT(run_id,id) DO NOTHING`, run, run, id)
	return err
}

func readBusinessRow(ctx context.Context, q rowQuerier, run, dataset, id string) (r model.Row, err error) {
	statement := `SELECT n.id,n.invoice,n.stock,n.description,n.quantity,n.source_time,n.price,n.customer,n.country,n.kind,n.excluded,n.flags,s.sheet,s.sheet_index,s.source_row,s.raw_json,n.duplicate_of FROM normalized_rows n JOIN source_rows s ON s.id=n.source_id WHERE n.dataset_id=? AND n.id=?`
	err = q.QueryRowContext(ctx, businessSQL(ctx, q, run, statement), dataset, id).Scan(&r.ID, &r.Invoice, &r.Stock, &r.Description, &r.Quantity, &r.Time, &r.Price, &r.Customer, &r.Country, &r.Kind, &r.Excluded, &r.Flags, &r.Sheet, &r.SheetIndex, &r.SourceRow, &r.RawJSON, &r.DuplicateOf)
	return
}

type RemediationRequest struct {
	Version          int               `json:"version"`
	RequestID        string            `json:"request_id"`
	Action           string            `json:"action"`
	Evidence         string            `json:"evidence"`
	Changes          map[string]string `json:"changes,omitempty"`
	Sale             *SupplementalSale `json:"sale,omitempty"`
	RowIDs           []string          `json:"row_ids,omitempty"`
	CanonicalID      string            `json:"canonical_id,omitempty"`
	Disposition      string            `json:"disposition,omitempty"`
	MissingMaterials string            `json:"missing_materials,omitempty"`
	NextStep         string            `json:"next_step,omitempty"`
}
type SupplementalSale struct {
	Invoice     string `json:"invoice"`
	Stock       string `json:"stock"`
	Description string `json:"description"`
	Customer    string `json:"customer"`
	Country     string `json:"country"`
	Quantity    string `json:"quantity"`
	Time        string `json:"time"`
	Price       string `json:"price"`
	DocumentRef string `json:"document_ref"`
	LineRef     string `json:"line_ref"`
}
type RemediationResult struct {
	Case               WorkflowCase   `json:"case"`
	Revision           int            `json:"revision"`
	CreatedCases       []WorkflowCase `json:"created_cases"`
	Matched            int            `json:"matched"`
	Reclassified       int            `json:"reclassified"`
	Excluded           int            `json:"excluded"`
	SupplementalSaleID string         `json:"supplemental_sale_id,omitempty"`
	Message            string         `json:"message"`
}
type RemediationEvent struct {
	ID         string          `json:"id"`
	Action     string          `json:"action"`
	Evidence   string          `json:"evidence"`
	Actor      string          `json:"actor"`
	OccurredAt string          `json:"occurred_at"`
	Before     json.RawMessage `json:"before"`
	After      json.RawMessage `json:"after"`
}

func validateBusinessRow(r *model.Row) error {
	if len(r.Invoice) > 256 || len(r.Stock) > 256 || len(r.Customer) > 256 || len(r.Country) > 256 || len(r.Description) > 4000 {
		return workflowError("invalid_input", "字段过长", WorkflowCase{})
	}
	if r.Invoice == "" || r.Stock == "" || r.Quantity == 0 || r.Quantity == math.MinInt64 || (r.Kind == "sale" && r.Quantity < 0) || (r.Kind == "cancel" && r.Quantity > 0) {
		return workflowError("invalid_input", "商品、单号或数量不合法", WorkflowCase{})
	}
	p, e := decimal.NewFromString(r.Price)
	// Official XML prices can contain more than twelve decimal places. Preserve
	// their exact value while bounding both input and expanded decimal lengths.
	if e != nil || !p.IsPositive() || len(r.Price) > 64 || p.Exponent() < -64 || p.Exponent() > 64 {
		return workflowError("invalid_input", "单价须为正的精确十进制数", WorkflowCase{})
	}
	r.Price = p.String()
	if len(r.Price) > 64 {
		return workflowError("invalid_input", "单价须为正的精确十进制数", WorkflowCase{})
	}
	normalized, e := normalizeDate(r.Time)
	if e != nil || normalized == "" {
		return workflowError("invalid_input", "请填写有效的来源时间", WorkflowCase{})
	}
	r.Time = normalized
	return nil
}

func updateBusinessRow(ctx context.Context, c *sql.Conn, run string, r model.Row) error {
	if e := materializeBusinessRow(ctx, c, run, r.ID); e != nil {
		return e
	}
	_, e := c.ExecContext(ctx, `UPDATE business_rows SET invoice=?,stock=?,description=?,quantity=?,source_time=?,price=?,customer=?,country=?,excluded=?,flags=?,duplicate_of=? WHERE run_id=? AND id=?`, r.Invoice, r.Stock, r.Description, r.Quantity, r.Time, r.Price, r.Customer, r.Country, r.Excluded, r.Flags, r.DuplicateOf, run, r.ID)
	return e
}
func withoutDuplicate(flags string) string {
	out := []string{}
	for _, f := range strings.Split(flags, "|") {
		if f != "" && f != "same_sheet_duplicate" {
			out = append(out, f)
		}
	}
	return strings.Join(out, "|")
}
func sameBusiness(a, b model.Row) bool {
	return a.Kind == b.Kind && a.Invoice == b.Invoice && a.Stock == b.Stock && a.Description == b.Description && a.Quantity == b.Quantity && a.Time == b.Time && a.Price == b.Price && a.Customer == b.Customer && a.Country == b.Country
}

func snapshotCase(ctx context.Context, c *sql.Conn, w WorkflowCase, r model.Row) error {
	b, e := json.Marshal(r)
	if e != nil {
		return e
	}
	_, e = c.ExecContext(ctx, "INSERT INTO case_snapshots(case_id,row_json) VALUES(?,?) ON CONFLICT(case_id) DO NOTHING", w.ID, string(b))
	if e != nil {
		return e
	}
	var present int
	if e = c.QueryRowContext(ctx, "SELECT count(*) FROM case_decision_snapshots WHERE case_id=?", w.ID).Scan(&present); e != nil || present > 0 {
		return e
	}
	d := model.Decision{CancelID: w.CancelID, Evidence: []model.Evidence{}}
	var raw string
	e = c.QueryRowContext(ctx, "SELECT quantity,classification,reason,candidate_lower_bound,evidence_json FROM current_assessments WHERE run_id=? AND cancel_id=?", w.RunID, w.CancelID).Scan(&d.Quantity, &d.Classification, &d.Reason, &d.CandidateLowerBound, &raw)
	if errors.Is(e, sql.ErrNoRows) {
		if e = c.QueryRowContext(ctx, "SELECT quantity,classification,reason,candidate_lower_bound FROM decisions WHERE run_id=? AND cancel_id=?", w.RunID, w.CancelID).Scan(&d.Quantity, &d.Classification, &d.Reason, &d.CandidateLowerBound); e != nil {
			return e
		}
		rows, err := c.QueryContext(ctx, "SELECT sale_id,remaining,eligible,reason FROM candidate_evidence WHERE run_id=? AND cancel_id=? ORDER BY position", w.RunID, w.CancelID)
		if err != nil {
			return err
		}
		for rows.Next() {
			var witness model.Evidence
			if err = rows.Scan(&witness.SaleID, &witness.Remaining, &witness.Eligible, &witness.Reason); err != nil {
				rows.Close()
				return err
			}
			d.Evidence = append(d.Evidence, witness)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
	} else if e != nil {
		return e
	} else if e = json.Unmarshal([]byte(raw), &d.Evidence); e != nil {
		return e
	}
	b, e = json.Marshal(d)
	if e != nil {
		return e
	}
	_, e = c.ExecContext(ctx, "INSERT INTO case_decision_snapshots VALUES(?,?) ON CONFLICT DO NOTHING", w.ID, string(b))
	return e
}
func appendRemediationAudit(ctx context.Context, c *sql.Conn, actor, action string, before, after WorkflowCase, detail any) error {
	b, e := json.Marshal(before)
	if e != nil {
		return e
	}
	a, e := json.Marshal(struct {
		WorkflowCase
		Remediation any `json:"remediation"`
	}{after, detail})
	if e != nil {
		return e
	}
	_, e = c.ExecContext(ctx, `INSERT INTO audit_events(case_id,actor,action,occurred_at,before_json,after_json) VALUES(?,?,?,?,?,?)`, before.ID, actor, action, time.Now().Format(time.RFC3339Nano), string(b), string(a))
	return e
}

func (s *Store) RemediateCase(ctx context.Context, actor WorkflowActor, id string, req RemediationRequest) (out RemediationResult, err error) {
	out.CreatedCases = []WorkflowCase{}
	if len(req.RequestID) < 8 || len(req.RequestID) > 128 || len(req.Evidence) > 8000 || strings.TrimSpace(req.Evidence) == "" {
		return out, workflowError("invalid_input", "需要核实依据和有效请求编号", WorkflowCase{})
	}
	if !actor.valid() {
		return out, workflowError("unauthenticated", "需要可信身份", WorkflowCase{})
	}
	body, e := json.Marshal(req)
	if e != nil {
		return out, e
	}
	err = s.immediate(ctx, func(c *sql.Conn) error {
		before, e := readWorkflowCase(ctx, c, id)
		if e != nil {
			return e
		}
		if e = authorizeWorkflowRead(ctx, actor, before); e != nil {
			return e
		}
		var saved, original, originalCase string
		e = c.QueryRowContext(ctx, "SELECT result_json,request_json,case_id FROM remediation_events WHERE actor=? AND request_id=?", actor.ID, req.RequestID).Scan(&saved, &original, &originalCase)
		if e == nil {
			if original != string(body) || originalCase != id {
				return workflowError("idempotency_conflict", "请求编号已用于其他操作", before)
			}
			if e = json.Unmarshal([]byte(saved), &out); e != nil {
				return e
			}
			visible := []WorkflowCase{}
			for _, created := range out.CreatedCases {
				if actor.Role != "warehouse" && access.Allows(ctx, created.Country) {
					visible = append(visible, created)
				}
			}
			out.CreatedCases = visible
			return nil
		}
		if !errors.Is(e, sql.ErrNoRows) {
			return e
		}
		if before.Assignee != actor.ID {
			return workflowError("forbidden", "仅当前责任人可以补充", before)
		}
		if before.Status != "in_progress" || !before.Current {
			return workflowError("invalid_transition", "仅当前轮次处理中可补充", before)
		}
		if before.Version != req.Version {
			return workflowError("version_conflict", "差异版本已变化，请刷新", before)
		}
		switch req.Action {
		case "correct_return", "supplement_sale", "verify_duplicate", "wait_materials", "recheck":
		default:
			return workflowError("invalid_input", "未知补充操作", before)
		}
		row, e := readBusinessRow(ctx, c, before.RunID, before.DatasetID, before.CancelID)
		if e != nil {
			return e
		}
		if e = snapshotCase(ctx, c, before, row); e != nil {
			return e
		}
		beforeRow := row
		if _, e = c.ExecContext(ctx, "INSERT INTO remediation_runs(run_id,sparse) VALUES(?,1) ON CONFLICT DO NOTHING", before.RunID); e != nil {
			return e
		}
		eventID := ID(actor.ID + "|" + req.RequestID)
		if req.Action == "wait_materials" {
			if strings.TrimSpace(req.MissingMaterials) == "" || strings.TrimSpace(req.NextStep) == "" || len(req.MissingMaterials) > 4000 || len(req.NextStep) > 4000 {
				return workflowError("invalid_input", "待补充须填写缺少材料和下一步", before)
			}
			if _, e = c.ExecContext(ctx, "UPDATE cases SET waiting=1,missing_materials=?,next_step=?,version=version+1 WHERE id=?", strings.TrimSpace(req.MissingMaterials), strings.TrimSpace(req.NextStep), id); e != nil {
				return e
			}
		} else {
			var allocated int
			if e = c.QueryRowContext(ctx, "SELECT count(*) FROM allocations WHERE run_id=? AND cancel_id=?", before.RunID, before.CancelID).Scan(&allocated); e != nil {
				return e
			}
			if allocated > 0 {
				return workflowError("already_allocated", "已有配对保留，请核查结论后办结", before)
			}
			_, active, e := remediationRevision(ctx, c, before.RunID)
			if e != nil {
				return e
			}
			if !active {
				if e = ensureBusinessIndexes(ctx, c); e != nil {
					return e
				}
				if _, e = c.ExecContext(ctx, "UPDATE remediation_runs SET initialized=1,sparse=1 WHERE run_id=?", before.RunID); e != nil {
					return e
				}
			}
			affected := map[string]model.Row{reviewKey(row): row}
			switch req.Action {
			case "correct_return":
				if len(req.Changes) == 0 {
					return workflowError("invalid_input", "请填写需核实修正的字段", before)
				}
				for k, v := range req.Changes {
					v = strings.TrimSpace(v)
					switch k {
					case "customer":
						row.Customer = v
					case "country":
						row.Country = v
					case "stock":
						row.Stock = v
					case "price":
						row.Price = v
					case "time":
						row.Time = v
					case "quantity":
						var n int64
						if e = json.Unmarshal([]byte(v), &n); e != nil || n <= 0 {
							return workflowError("invalid_input", "退货数量须为正整数", before)
						}
						row.Quantity = -n
					default:
						return workflowError("invalid_input", "不允许修正该字段", before)
					}
				}
				if e = validateBusinessRow(&row); e != nil {
					return e
				}
				if !access.Allows(ctx, row.Country) {
					return workflowError("forbidden", "修正后的国家不在当前权限范围", before)
				}
				if e = updateBusinessRow(ctx, c, before.RunID, row); e != nil {
					return e
				}
				affected[reviewKey(row)] = row
			case "supplement_sale":
				if req.Sale == nil {
					return workflowError("invalid_input", "请填写销售凭证", before)
				}
				v := req.Sale
				sale := model.Row{ID: "manual:" + eventID, Kind: "sale", Invoice: strings.TrimSpace(v.Invoice), Stock: strings.TrimSpace(v.Stock), Customer: strings.TrimSpace(v.Customer), Country: strings.TrimSpace(v.Country), Description: strings.TrimSpace(v.Description), Time: v.Time, Price: v.Price, SheetIndex: 2, SourceRow: 0}
				if e = json.Unmarshal([]byte(v.Quantity), &sale.Quantity); e != nil {
					return workflowError("invalid_input", "销售数量须为正整数", before)
				}
				if e = validateBusinessRow(&sale); e != nil {
					return e
				}
				if !sale.HasIdentity() || strings.HasPrefix(strings.ToUpper(sale.Invoice), "C") {
					return workflowError("invalid_input", "销售须有完整身份及正向单号", before)
				}
				if !access.Allows(ctx, sale.Country) || sale.Country != row.Country || sale.Stock != row.Stock || sale.Customer != row.Customer || sale.Price != row.Price {
					return workflowError("invalid_input", "先核实退货身份；补录销售须属于当前退货的同一身份", before)
				}
				document, line := strings.TrimSpace(v.DocumentRef), strings.TrimSpace(v.LineRef)
				if document == "" || line == "" || len(document) > 256 || len(line) > 256 {
					return workflowError("invalid_input", "需要凭证编号和明细号", before)
				}
				var exists int
				if e = c.QueryRowContext(ctx, "SELECT count(*) FROM supplemental_sources WHERE run_id=? AND document_ref=? AND line_ref=?", before.RunID, document, line).Scan(&exists); e != nil {
					return e
				}
				if exists > 0 {
					return workflowError("duplicate_document", "该凭证明细已补录", before)
				}
				sale.Sheet = "补录凭证 " + document + " · " + line
				raw, _ := json.Marshal([][]string{{"s", sale.Invoice}, {"s", sale.Stock}, {"s", sale.Description}, {"n", v.Quantity}, {"s", sale.Time}, {"n", sale.Price}, {"s", sale.Customer}, {"s", sale.Country}})
				sale.RawJSON = string(raw)
				if _, e = c.ExecContext(ctx, `INSERT INTO supplemental_sources VALUES(?,?,?,?,?,?,?,?,?)`, before.RunID, sale.ID, ID(string(raw)), sale.Sheet, 2, 0, string(raw), document, line); e != nil {
					return e
				}
				if _, e = c.ExecContext(ctx, `INSERT INTO business_rows VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, before.RunID, before.DatasetID, sale.ID, sale.ID, sale.Invoice, sale.Stock, sale.Description, sale.Quantity, sale.Time, sale.Price, sale.Customer, sale.Country, sale.Kind, "", "", ""); e != nil {
					return e
				}
				if _, e = c.ExecContext(ctx, "INSERT INTO sale_balances(run_id,sale_id,original,allocated,remaining) VALUES(?,?,?,0,?)", before.RunID, sale.ID, sale.Quantity, sale.Quantity); e != nil {
					return e
				}
				// An identical business sale is still a suspicion, never extra capacity.
				rows, e := c.QueryContext(ctx, businessSQL(ctx, c, before.RunID, `SELECT id FROM normalized_rows WHERE dataset_id=? AND kind='sale' AND id<>? AND excluded='' AND invoice=? AND stock=? AND description=? AND quantity=? AND source_time=? AND price=? AND customer=? AND country=?`), before.DatasetID, sale.ID, sale.Invoice, sale.Stock, sale.Description, sale.Quantity, sale.Time, sale.Price, sale.Customer, sale.Country)
				if e != nil {
					return e
				}
				ids := []string{}
				for rows.Next() {
					var rid string
					if e = rows.Scan(&rid); e != nil {
						rows.Close()
						return e
					}
					ids = append(ids, rid)
				}
				e = rows.Err()
				rows.Close()
				if e != nil {
					return e
				}
				if len(ids) > 0 {
					ids = append(ids, sale.ID)
					for _, rid := range ids {
						if e = materializeBusinessRow(ctx, c, before.RunID, rid); e != nil {
							return e
						}
						if _, e = c.ExecContext(ctx, "UPDATE business_rows SET flags=CASE WHEN instr(flags,'same_sheet_duplicate')>0 THEN flags ELSE flags||CASE WHEN flags='' THEN '' ELSE '|' END||'same_sheet_duplicate' END WHERE run_id=? AND id=?", before.RunID, rid); e != nil {
							return e
						}
					}
				}
				out.SupplementalSaleID = sale.ID
				affected[reviewKey(sale)] = sale
			case "verify_duplicate":
				if req.Disposition != "independent" && req.Disposition != "duplicate" {
					return workflowError("invalid_input", "请选择重复或独立业务", before)
				}
				if len(req.RowIDs) == 0 || len(req.RowIDs) > 100 {
					return workflowError("invalid_input", "请选择需核实的重复记录（最多100条）", before)
				}
				members := []model.Row{}
				seen := map[string]bool{}
				for _, rid := range req.RowIDs {
					if seen[rid] {
						continue
					}
					seen[rid] = true
					r, e := readBusinessRow(ctx, c, before.RunID, before.DatasetID, rid)
					if e != nil {
						return workflowError("invalid_input", "重复记录不存在", before)
					}
					if !access.Allows(ctx, r.Country) || reviewKey(r) != reviewKey(row) || r.Excluded != "" || !r.Duplicate() {
						return workflowError("invalid_input", "仅可核实当前身份的疑似重复记录", before)
					}
					if len(members) > 0 && !sameBusiness(members[0], r) {
						return workflowError("invalid_input", "所选重复记录字段不相同", before)
					}
					members = append(members, r)
				}
				sort.Slice(members, func(i, j int) bool { return sourceLess(members[i], members[j]) })
				if req.Disposition == "duplicate" && (len(members) < 2 || !seen[req.CanonicalID]) {
					return workflowError("invalid_input", "确认重复须选择至少两条并指定保留记录", before)
				}
				canonical := req.CanonicalID
				if len(members) > 0 && members[0].Kind == "cancel" && !seen[row.ID] {
					return workflowError("invalid_input", "必须包含当前退货", before)
				}
				var total int64
				for _, r := range members {
					var groupID string
					e = c.QueryRowContext(ctx, "SELECT group_id FROM duplicate_sale_members WHERE run_id=? AND sale_id=?", before.RunID, r.ID).Scan(&groupID)
					if e == nil {
						return workflowError("invalid_input", "已确认归组的销售保留，不能再次改变判定", before)
					}
					if !errors.Is(e, sql.ErrNoRows) {
						return e
					}
					if r.Kind == "sale" {
						var used int64
						if e = c.QueryRowContext(ctx, "SELECT allocated FROM sale_balances WHERE run_id=? AND sale_id=?", before.RunID, r.ID).Scan(&used); e != nil {
							return e
						}
						total += used
					}
					if r.Kind == "cancel" {
						var count int
						if e = c.QueryRowContext(ctx, "SELECT count(*) FROM allocations WHERE run_id=? AND cancel_id=?", before.RunID, r.ID).Scan(&count); e != nil {
							return e
						}
						if count > 0 {
							return workflowError("already_allocated", "已配对退货保留，不可排除或改判", before)
						}
						var closed int
						if e = c.QueryRowContext(ctx, "SELECT count(*) FROM cases WHERE run_id=? AND cancel_id=? AND is_current=1 AND status='closed'", before.RunID, r.ID).Scan(&closed); e != nil {
							return e
						}
						if closed > 0 {
							return workflowError("closed", "已关闭退货保留，不可改判", before)
						}
					}
				}
				if req.Disposition == "duplicate" && members[0].Kind == "sale" {
					// Existing allocations are immutable; grouping must not make them exceed capacity.
					if total > members[0].Quantity {
						return workflowError("duplicate_capacity_conflict", "已有配对总量超过重复组容量，请保留待补充并核查凭证", before)
					}
					// Use source order consistently with all existing duplicate write paths.
					canonical = members[0].ID
					gid := ID(before.RunID + "|remediation|" + eventID)
					if _, e = c.ExecContext(ctx, "INSERT INTO duplicate_sale_groups VALUES(?,?,?,?,?,?)", before.RunID, gid, canonical, members[0].Quantity, actor.ID, eventID); e != nil {
						return e
					}
					for _, r := range members {
						if _, e = c.ExecContext(ctx, "INSERT INTO duplicate_sale_members VALUES(?,?,?)", before.RunID, r.ID, gid); e != nil {
							return e
						}
					}
				}
				for _, r := range members {
					r.Flags = withoutDuplicate(r.Flags)
					if req.Disposition == "duplicate" && r.Kind == "cancel" && r.ID != canonical {
						r.Excluded = "verified_duplicate"
						r.DuplicateOf = canonical
					}
					if e = updateBusinessRow(ctx, c, before.RunID, r); e != nil {
						return e
					}
					if _, e = c.ExecContext(ctx, "INSERT INTO duplicate_verifications VALUES(?,?,?,?,?) ON CONFLICT(run_id,row_id) DO UPDATE SET disposition=excluded.disposition,canonical_id=excluded.canonical_id,event_id=excluded.event_id", before.RunID, r.ID, req.Disposition, canonical, eventID); e != nil {
						return e
					}
					affected[reviewKey(r)] = r
				}
			}
			if e = s.reassessAffected(ctx, c, actor, before, affected, req.Evidence, &out); e != nil {
				return e
			}
		}
		if _, e = c.ExecContext(ctx, "UPDATE remediation_runs SET revision=revision+1 WHERE run_id=?", before.RunID); e != nil {
			return e
		}
		out.Revision, _, e = remediationRevision(ctx, c, before.RunID)
		if e != nil {
			return e
		}
		out.Case, e = readWorkflowCase(ctx, c, id)
		if e != nil {
			return e
		}
		currentRow, e := readBusinessRow(ctx, c, before.RunID, before.DatasetID, before.CancelID)
		if e != nil {
			return e
		}
		if e = appendRemediationAudit(ctx, c, actor.ID, req.Action, before, out.Case, map[string]any{"evidence": req.Evidence, "before": beforeRow, "after": currentRow, "revision": out.Revision}); e != nil {
			return e
		}
		if req.Action == "wait_materials" {
			out.Message = "已记录待补充材料和下一步，问题继续保留。"
		} else {
			out.Message = fmt.Sprintf("补充已生效并重算：配对 %d 条，新问题 %d 条，核实重复排除 %d 条。", out.Matched, out.Reclassified, out.Excluded)
		}
		result, e := json.Marshal(out)
		if e != nil {
			return e
		}
		b, _ := json.Marshal(beforeRow)
		a, _ := json.Marshal(currentRow)
		_, e = c.ExecContext(ctx, `INSERT INTO remediation_events VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`, eventID, before.RunID, id, actor.ID, req.RequestID, string(body), string(result), req.Action, strings.TrimSpace(req.Evidence), string(b), string(a), time.Now().Format(time.RFC3339Nano))
		return e
	})
	if err != nil {
		return RemediationResult{}, err
	}
	return
}

func (s *Store) reassessAffected(ctx context.Context, c *sql.Conn, actor WorkflowActor, origin WorkflowCase, affected map[string]model.Row, evidence string, out *RemediationResult) error {
	// Collect before writing; a record changed from identity A to B is evaluated
	// once, against its final identity. Closed cases and every allocation stay fixed.
	ids := map[string]bool{}
	for _, key := range affected {
		rows, e := c.QueryContext(ctx, businessSQL(ctx, c, origin.RunID, `SELECT n.id FROM normalized_rows n INDEXED BY row_identity JOIN cases cc ON cc.run_id=? AND cc.cancel_id=n.id AND cc.is_current=1 AND cc.status<>'closed' LEFT JOIN allocations a ON a.run_id=cc.run_id AND a.cancel_id=n.id WHERE n.dataset_id=? AND n.kind='cancel' AND n.customer=? AND n.stock=? AND n.price=? AND n.country=? AND a.cancel_id IS NULL`), origin.RunID, origin.DatasetID, key.Customer, key.Stock, key.Price, key.Country)
		if e != nil {
			return e
		}
		for rows.Next() {
			var id string
			if e = rows.Scan(&id); e != nil {
				rows.Close()
				return e
			}
			ids[id] = true
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return e
		}
	}
	ids[origin.CancelID] = true
	cancels := []model.Row{}
	for id := range ids {
		r, e := readBusinessRow(ctx, c, origin.RunID, origin.DatasetID, id)
		if e != nil {
			return e
		}
		cancels = append(cancels, r)
	}
	sort.Slice(cancels, func(i, j int) bool {
		if cancels[i].Time != cancels[j].Time {
			return cancels[i].Time < cancels[j].Time
		}
		return sourceLess(cancels[i], cancels[j])
	})
	for _, r := range cancels {
		var caseID string
		if e := c.QueryRowContext(ctx, "SELECT id FROM cases WHERE run_id=? AND cancel_id=? AND is_current=1 AND status<>'closed'", origin.RunID, r.ID).Scan(&caseID); errors.Is(e, sql.ErrNoRows) {
			continue
		} else if e != nil {
			return e
		}
		w, e := readWorkflowCase(ctx, c, caseID)
		if e != nil {
			return e
		}
		if e = snapshotCase(ctx, c, w, r); e != nil {
			return e
		}
		d, sale, e := assessBusiness(ctx, c, origin.RunID, origin.DatasetID, r)
		if e != nil {
			return e
		}
		encoded, _ := json.Marshal(d.Evidence)
		if _, e = c.ExecContext(ctx, `INSERT INTO current_assessments VALUES(?,?,?,?,?,?,?) ON CONFLICT(run_id,cancel_id) DO UPDATE SET quantity=excluded.quantity,classification=excluded.classification,reason=excluded.reason,candidate_lower_bound=excluded.candidate_lower_bound,evidence_json=excluded.evidence_json`, origin.RunID, r.ID, -r.Quantity, d.Classification, d.Reason, d.CandidateLowerBound, string(encoded)); e != nil {
			return e
		}
		action := "reassess"
		after := w
		switch {
		case r.Excluded == "verified_duplicate":
			after.Status = "closed"
			after.Outcome = "verified_duplicate"
			out.Excluded++
			action = "verified_duplicate"
		case d.Classification == "matched":
			qty := -r.Quantity
			result, e := c.ExecContext(ctx, "UPDATE sale_balances SET allocated=allocated+?,remaining=remaining-? WHERE run_id=? AND sale_id=? AND remaining>=?", qty, qty, origin.RunID, sale.ID, qty)
			if e != nil {
				return e
			}
			n, e := result.RowsAffected()
			if e != nil {
				return e
			}
			if n != 1 {
				return workflowError("balance_conflict", "销售余量已经变化", w)
			}
			if e = checkDuplicateCapacity(ctx, c, origin.RunID, sale.ID, 0); e != nil {
				return e
			}
			p, _ := decimal.NewFromString(r.Price)
			if _, e = c.ExecContext(ctx, "INSERT INTO allocations VALUES(?,?,?,?,?,'remediation',?)", origin.RunID, r.ID, sale.ID, qty, p.Mul(decimal.NewFromInt(qty)).String(), actor.ID); e != nil {
				return e
			}
			after.Status = "closed"
			after.Outcome = "matched"
			out.Matched++
			action = "remediation_match"
		case d.Reason != w.Reason:
			after.Status = "closed"
			after.Outcome = "reclassified"
			after.Current = false
			action = "reclassified"
			out.Reclassified++
		}
		after.Version++
		after.Waiting = false
		after.MissingMaterials = ""
		after.NextStep = ""
		if after.Status == "closed" {
			after.Conclusion = evidence
		}
		if _, e = c.ExecContext(ctx, "UPDATE cases SET status=?,outcome=?,is_current=?,waiting=0,missing_materials='',next_step='',conclusion=?,version=version+1 WHERE id=?", after.Status, after.Outcome, after.Current, after.Conclusion, w.ID); e != nil {
			return e
		}
		if after.Outcome == "reclassified" {
			nextID := ID(origin.RunID + "|" + r.ID + fmt.Sprintf("|generation:%d", w.Generation+1))
			if _, e = c.ExecContext(ctx, "INSERT INTO cases(id,run_id,cancel_id,reason,generation,previous_case_id) VALUES(?,?,?,?,?,?)", nextID, origin.RunID, r.ID, d.Reason, w.Generation+1, w.ID); e != nil {
				return e
			}
			next, e := readWorkflowCase(ctx, c, nextID)
			if e != nil {
				return e
			}
			if e = snapshotCase(ctx, c, next, r); e != nil {
				return e
			}
			if e = appendRemediationAudit(ctx, c, actor.ID, "created_from_reassessment", next, next, map[string]any{"previous_case_id": w.ID, "decision": d}); e != nil {
				return e
			}
			if actor.Role != "warehouse" && access.Allows(ctx, next.Country) {
				out.CreatedCases = append(out.CreatedCases, next)
			}
		}
		if e = appendRemediationAudit(ctx, c, actor.ID, action, w, after, d); e != nil {
			return e
		}
	}
	return nil
}

func assessBusiness(ctx context.Context, c *sql.Conn, run, dataset string, r model.Row) (d model.Decision, chosen model.Row, err error) {
	d = model.Decision{CancelID: r.ID, Quantity: -r.Quantity, Classification: "suspected", Evidence: []model.Evidence{}}
	if r.Excluded == "verified_duplicate" {
		d.Classification = "verified_duplicate"
		d.Reason = "verified_duplicate"
		return
	}
	if !r.HasIdentity() {
		d.Reason = "missing_identity"
		return
	}
	rows, e := c.QueryContext(ctx, businessSQL(ctx, c, run, `SELECT n.id,b.remaining FROM normalized_rows n INDEXED BY row_identity JOIN sale_balances b ON b.run_id=? AND b.sale_id=n.id WHERE n.dataset_id=? AND n.kind='sale' AND n.excluded='' AND n.customer=? AND n.stock=? AND n.price=? AND n.country=? ORDER BY n.source_time,n.id`), run, dataset, r.Customer, r.Stock, r.Price, r.Country)
	if e != nil {
		err = e
		return
	}
	type bal struct {
		id        string
		remaining int64
	}
	sales := []bal{}
	for rows.Next() {
		var b bal
		if e = rows.Scan(&b.id, &b.remaining); e != nil {
			rows.Close()
			err = e
			return
		}
		sales = append(sales, b)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return
	}
	earlier := 0
	candidates := []model.Row{}
	dup := false
	for _, b := range sales {
		sale, e := readBusinessRow(ctx, c, run, dataset, b.id)
		if e != nil {
			err = e
			return
		}
		var gid, canonical string
		var cap, used int64
		e = c.QueryRowContext(ctx, `SELECT g.id,g.canonical_id,g.capacity,coalesce((SELECT sum(sb.allocated) FROM duplicate_sale_members m JOIN sale_balances sb ON sb.run_id=m.run_id AND sb.sale_id=m.sale_id WHERE m.run_id=g.run_id AND m.group_id=g.id),0) FROM duplicate_sale_members m JOIN duplicate_sale_groups g ON g.run_id=m.run_id AND g.id=m.group_id WHERE m.run_id=? AND m.sale_id=?`, run, sale.ID).Scan(&gid, &canonical, &cap, &used)
		if e == nil {
			if canonical != sale.ID {
				continue
			}
			b.remaining = min(b.remaining, cap-used)
		} else if !errors.Is(e, sql.ErrNoRows) {
			err = e
			return
		}
		eligible := sale.Time < r.Time && b.remaining >= -r.Quantity
		reason := "qualified"
		if sale.Time >= r.Time {
			reason = "sale_not_strictly_earlier"
		} else {
			earlier++
			if b.remaining < -r.Quantity {
				reason = "remaining_quantity_too_small"
			}
		}
		if eligible {
			candidates = append(candidates, sale)
			if sale.Duplicate() {
				dup = true
				reason = "suspected_duplicate_sale"
			}
			d.Evidence = append(d.Evidence, model.Evidence{SaleID: sale.ID, Remaining: b.remaining, Eligible: true, Reason: reason})
		} else if len(d.Evidence) < 2 {
			d.Evidence = append(d.Evidence, model.Evidence{SaleID: sale.ID, Remaining: b.remaining, Reason: reason})
		}
	}
	d.CandidateLowerBound = len(candidates)
	switch {
	case r.Duplicate():
		d.Reason = "suspected_duplicate_cancel"
	case dup:
		d.Reason = "suspected_duplicate_sale"
	case len(candidates) > 1:
		d.Reason = "ambiguous_candidates"
	case len(candidates) == 1:
		d.Classification = "matched"
		d.Reason = "unique_qualified_candidate"
		chosen = candidates[0]
		d.SaleID = chosen.ID
	default:
		d.Classification = "no_original"
		if len(sales) == 0 {
			d.Reason = "no_identity_match"
		} else if earlier == 0 {
			d.Reason = "no_strictly_earlier_sale"
		} else {
			d.Reason = "insufficient_remaining_quantity"
		}
	}
	return
}

func closeAllowed(ctx context.Context, q rowQuerier, w WorkflowCase) error {
	var count int
	if e := q.QueryRowContext(ctx, "SELECT count(*) FROM allocations WHERE run_id=? AND cancel_id=?", w.RunID, w.CancelID).Scan(&count); e != nil {
		return e
	}
	if count > 0 || w.Outcome == "verified_duplicate" {
		return nil
	}
	return workflowError("unresolved", "问题尚未解决，请补充资料并重算，或保存待补充信息", w)
}
