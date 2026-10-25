package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"returnsrecon/internal/access"
	"strings"
	"time"
)

// WorkflowActor is a trusted internal identity. A future auth adapter must resolve
// it from a complete server session, never from request role/user fields.
// Today only isolated tests/verification supply simulated actors; no business
// command or HTTP endpoint exposes these mutators before FR-14 is implemented.
type WorkflowActor struct {
	ID   string `json:"id"`
	Role string `json:"role"`
}

type writeAuthorizationKey struct{}
type WriteAuthorizer func(context.Context, *sql.Conn) error

// WithWriteAuthorization binds a trusted HTTP session recheck to the eventual
// write transaction. Local isolated verification has no HTTP session to attach.
func WithWriteAuthorization(ctx context.Context, check WriteAuthorizer) context.Context {
	return context.WithValue(ctx, writeAuthorizationKey{}, check)
}

func (a WorkflowActor) valid() bool {
	return strings.TrimSpace(a.ID) != "" && (a.Role == "finance" || a.Role == "warehouse" || a.Role == "admin")
}

type WorkflowCase struct {
	Generation       int    `json:"generation"`
	Current          bool   `json:"is_current"`
	PreviousCaseID   string `json:"previous_case_id"`
	Outcome          string `json:"outcome"`
	Waiting          bool   `json:"waiting"`
	MissingMaterials string `json:"missing_materials"`
	NextStep         string `json:"next_step"`
	Country          string `json:"country,omitempty"`
	ID               string `json:"id"`
	RunID            string `json:"run_id"`
	DatasetID        string `json:"dataset_id"`
	CancelID         string `json:"cancel_id"`
	Reason           string `json:"reason"`
	Status           string `json:"status"`
	Assignee         string `json:"assignee"`
	Version          int    `json:"version"`
	Conclusion       string `json:"conclusion"`
	AssignedAt       string `json:"assigned_at"`
	AssignedBy       string `json:"assigned_by"`
	SourceHash       string `json:"source_hash"`
	Sheet            string `json:"sheet"`
	SourceRow        int    `json:"source_row"`
	RuleVersion      string `json:"rule_version"`
	CleaningVersion  string `json:"cleaning_version"`
}

type WorkflowError struct {
	Code             string `json:"code"`
	Message          string `json:"message"`
	ExistingAssignee string `json:"existing_assignee,omitempty"`
	CurrentVersion   int    `json:"current_version,omitempty"`
}

func (e *WorkflowError) Error() string { return e.Code + ": " + e.Message }
func workflowError(code, message string, current WorkflowCase) error {
	return &WorkflowError{code, message, current.Assignee, current.Version}
}

// Immediate transactions serialize short workflow writes even across processes.
// All statements use the reserved connection, including audit and COMMIT.
func (s *Store) immediate(ctx context.Context, work func(*sql.Conn) error) error {
	conn, err := s.DB.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	if _, err = conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return err
	}
	defer conn.ExecContext(context.Background(), "ROLLBACK")
	if check, ok := ctx.Value(writeAuthorizationKey{}).(WriteAuthorizer); ok {
		if err = check(ctx, conn); err != nil {
			return err
		}
	}
	if err = work(conn); err != nil {
		return err
	}
	_, err = conn.ExecContext(ctx, "COMMIT")
	return err
}

func (s *Store) migrateWorkflow(ctx context.Context) error {
	return s.immediate(ctx, func(conn *sql.Conn) error {
		rows, err := conn.QueryContext(ctx, "PRAGMA table_info(cases)")
		if err != nil {
			return err
		}
		columns := map[string]bool{}
		for rows.Next() {
			var cid, notnull, pk int
			var name, typ string
			var defaultValue any
			if err = rows.Scan(&cid, &name, &typ, &notnull, &defaultValue, &pk); err != nil {
				rows.Close()
				return err
			}
			columns[name] = true
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		// Fixed identifiers only; do not infer old assignment metadata.
		for _, name := range []string{"assigned_at", "assigned_by"} {
			if !columns[name] {
				if _, err = conn.ExecContext(ctx, "ALTER TABLE cases ADD COLUMN "+name+" TEXT NOT NULL DEFAULT ''"); err != nil {
					return err
				}
			}
		}
		return nil
	})
}

type rowQuerier interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func readWorkflowCase(ctx context.Context, q rowQuerier, id string) (c WorkflowCase, err error) {
	err = q.QueryRowContext(ctx, `SELECT c.id,c.run_id,r.dataset_id,c.cancel_id,c.reason,
 c.status,coalesce(c.assignee,''),c.version,c.conclusion,c.assigned_at,c.assigned_by,
	 d.source_hash,s.sheet,s.source_row,r.rule_version,d.cleaning_version,
 CASE WHEN c.is_current=0 THEN coalesce(json_extract(cs.row_json,'$.country'),n.country) ELSE coalesce(br.country,n.country) END,
 c.generation,c.is_current,c.previous_case_id,c.outcome,c.waiting,c.missing_materials,c.next_step
 FROM cases c JOIN runs r ON r.id=c.run_id JOIN datasets d ON d.id=r.dataset_id
 JOIN normalized_rows n ON n.dataset_id=d.id AND n.id=c.cancel_id
 JOIN source_rows s ON s.id=n.source_id
 LEFT JOIN business_rows br ON br.run_id=c.run_id AND br.id=c.cancel_id
 LEFT JOIN case_snapshots cs ON cs.case_id=c.id
 WHERE c.id=? AND r.status='published' AND d.status='published'`, id).Scan(
		&c.ID, &c.RunID, &c.DatasetID, &c.CancelID, &c.Reason, &c.Status,
		&c.Assignee, &c.Version, &c.Conclusion, &c.AssignedAt, &c.AssignedBy,
		&c.SourceHash, &c.Sheet, &c.SourceRow, &c.RuleVersion, &c.CleaningVersion, &c.Country,
		&c.Generation, &c.Current, &c.PreviousCaseID, &c.Outcome, &c.Waiting, &c.MissingMaterials, &c.NextStep)
	if errors.Is(err, sql.ErrNoRows) {
		err = workflowError("not_found", "差异不存在或运行尚未发布", WorkflowCase{})
	}
	return c, err
}

func authorizeWorkflowRead(ctx context.Context, actor WorkflowActor, c WorkflowCase) error {
	if !actor.valid() {
		return workflowError("unauthenticated", "需要可信服务端身份", WorkflowCase{})
	}
	if !access.Allows(ctx, c.Country) {
		return workflowError("not_found", "差异不存在或不在当前权限范围", WorkflowCase{})
	}
	if actor.Role == "warehouse" && actor.ID != c.Assignee {
		return workflowError("forbidden", "只能查看本人差异及操作历史", WorkflowCase{})
	}
	return nil
}

func (s *Store) WorkflowCase(ctx context.Context, actor WorkflowActor, id string) (WorkflowCase, error) {
	c, err := readWorkflowCase(ctx, s.DB, id)
	if err == nil {
		err = authorizeWorkflowRead(ctx, actor, c)
	}
	if err != nil {
		return WorkflowCase{}, err
	}
	return c, nil
}

func (s *Store) AssignCase(ctx context.Context, actor, assignee WorkflowActor, id string) (out WorkflowCase, err error) {
	err = s.immediate(ctx, func(conn *sql.Conn) error {
		out, err = assignCaseTx(ctx, conn, actor, assignee, id)
		return err
	})
	if err != nil {
		out = WorkflowCase{}
	}
	return
}

func assignCaseTx(ctx context.Context, conn *sql.Conn, actor, assignee WorkflowActor, id string) (out WorkflowCase, err error) {
	if !actor.valid() {
		return out, workflowError("unauthenticated", "需要可信服务端身份", out)
	}
	if actor.Role != "finance" && actor.Role != "admin" {
		return out, workflowError("forbidden", "只有财务或管理员可以派单", out)
	}
	if !assignee.valid() {
		return out, workflowError("invalid_assignee", "需要有效的处理人身份", out)
	}
	err = func() error {
		before, err := readWorkflowCase(ctx, conn, id)
		if err != nil {
			return err
		}
		if err = authorizeCaseCountry(ctx, conn, actor, assignee, before); err != nil {
			return err
		}
		if before.Assignee != "" {
			return workflowError("already_assigned", "已有责任人："+before.Assignee, before)
		}
		if before.Status != "pending" {
			return workflowError("invalid_transition", "只能对待处理差异初始派单", before)
		}
		after := before
		after.Assignee, after.AssignedBy = assignee.ID, actor.ID
		after.AssignedAt = time.Now().Format(time.RFC3339Nano)
		after.Version++
		result, err := conn.ExecContext(ctx, `UPDATE cases SET assignee=?,assigned_at=?,assigned_by=?,version=version+1
 WHERE id=? AND status='pending' AND (assignee IS NULL OR assignee='') AND version=?`, assignee.ID, after.AssignedAt, actor.ID, id, before.Version)
		if err != nil {
			return err
		}
		if err = recordWorkflowAudit(ctx, conn, actor.ID, "assign", after.AssignedAt, before, after, result); err != nil {
			return err
		}
		out = after
		return nil
	}()
	if err != nil {
		out = WorkflowCase{}
	}
	return out, err
}

// TransitionCase deliberately has no arbitrary edit operation or reopen path.
func (s *Store) TransitionCase(ctx context.Context, actor WorkflowActor, id, target string, version int, conclusion string) (out WorkflowCase, err error) {
	err = s.immediate(ctx, func(conn *sql.Conn) error {
		out, err = transitionCaseTx(ctx, conn, actor, id, target, version, conclusion)
		return err
	})
	if err != nil {
		out = WorkflowCase{}
	}
	return
}

func transitionCaseTx(ctx context.Context, conn *sql.Conn, actor WorkflowActor, id, target string, version int, conclusion string) (out WorkflowCase, err error) {
	if !actor.valid() {
		return out, workflowError("unauthenticated", "需要可信服务端身份", out)
	}
	err = func() error {
		before, err := readWorkflowCase(ctx, conn, id)
		if err != nil {
			return err
		}
		if err = authorizeWorkflowRead(ctx, actor, before); err != nil {
			return err
		}
		if before.Assignee == "" || before.Assignee != actor.ID {
			return workflowError("forbidden", "仅当前责任人可以处理", WorkflowCase{})
		}
		if before.Status == "closed" {
			return workflowError("closed", "已关闭差异不能修改", before)
		}
		if version != before.Version {
			return workflowError("version_conflict", "差异版本已变化，请刷新", before)
		}
		action := ""
		switch {
		case before.Status == "pending" && target == "in_progress":
			action = "start"
			if conclusion != "" {
				return workflowError("invalid_input", "开始处理不能填写关闭结论", before)
			}
		case before.Status == "in_progress" && target == "closed":
			action = "close"
			if strings.TrimSpace(conclusion) == "" {
				return workflowError("empty_conclusion", "关闭前必须填写非空白结论", before)
			}
			if err = closeAllowed(ctx, conn, before); err != nil {
				return err
			}
		default:
			return workflowError("invalid_transition", "只能待处理→处理中→已关闭，禁止跳级或逆向流转", before)
		}
		after := before
		after.Status, after.Version = target, before.Version+1
		if action == "close" {
			after.Conclusion = strings.TrimSpace(conclusion)
			after.Outcome = "matched"
		}
		result, err := conn.ExecContext(ctx, `UPDATE cases SET status=?,version=version+1,conclusion=?,outcome=?,waiting=0
 WHERE id=? AND status=? AND assignee=? AND version=?`, target, after.Conclusion, after.Outcome, id, before.Status, actor.ID, before.Version)
		if err != nil {
			return err
		}
		if err = recordWorkflowAudit(ctx, conn, actor.ID, action, time.Now().Format(time.RFC3339Nano), before, after, result); err != nil {
			return err
		}
		out = after
		return nil
	}()
	if err != nil {
		out = WorkflowCase{}
	}
	return out, err
}

// Resolve targets again under the write lock; a stale drop-down is not authority.
func authorizeCaseCountry(ctx context.Context, conn *sql.Conn, actor, target WorkflowActor, c WorkflowCase) error {
	if !access.Allows(ctx, c.Country) {
		return workflowError("not_found", "差异不存在或不在当前权限范围", WorkflowCase{})
	}
	if _, trusted := access.From(ctx); !trusted {
		return nil
	}
	p, e := access.Load(ctx, conn, target.ID)
	if e != nil || p.Role != target.Role || !p.Allows(c.Country) {
		return workflowError("forbidden", "责任人的负责国家/地区不覆盖此差异", WorkflowCase{})
	}
	return nil
}

func recordWorkflowAudit(ctx context.Context, conn *sql.Conn, actor, action, at string, before, after WorkflowCase, result sql.Result) error {
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return workflowError("version_conflict", "差异已变化，请刷新", before)
	}
	b, err := json.Marshal(before)
	if err != nil {
		return err
	}
	a, err := json.Marshal(after)
	if err != nil {
		return err
	}
	_, err = conn.ExecContext(ctx, `INSERT INTO audit_events(case_id,actor,action,occurred_at,before_json,after_json) VALUES(?,?,?,?,?,?)`, after.ID, actor, action, at, string(b), string(a))
	return err
}

type WorkflowAudit struct {
	ID             int64           `json:"id"`
	Actor          string          `json:"actor"`
	Action         string          `json:"action"`
	OccurredAt     string          `json:"occurred_at"`
	Before         WorkflowCase    `json:"before"`
	After          WorkflowCase    `json:"after"`
	BeforeEvidence json.RawMessage `json:"before_evidence,omitempty"`
	AfterEvidence  json.RawMessage `json:"after_evidence,omitempty"`
}

func (s *Store) WorkflowHistory(ctx context.Context, actor WorkflowActor, id string) ([]WorkflowAudit, error) {
	if _, err := s.WorkflowCase(ctx, actor, id); err != nil {
		return nil, err
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT id,actor,action,occurred_at,before_json,after_json FROM audit_events WHERE case_id=? ORDER BY id`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []WorkflowAudit{}
	for rows.Next() {
		var a WorkflowAudit
		var before, after string
		if err = rows.Scan(&a.ID, &a.Actor, &a.Action, &a.OccurredAt, &before, &after); err != nil {
			return nil, err
		}
		if !auditJSONVisible(ctx, []byte(before)) || !auditJSONVisible(ctx, []byte(after)) {
			continue
		}
		if err = json.Unmarshal([]byte(before), &a.Before); err != nil {
			return nil, fmt.Errorf("decode audit before: %w", err)
		}
		if err = json.Unmarshal([]byte(after), &a.After); err != nil {
			return nil, fmt.Errorf("decode audit after: %w", err)
		}
		a.BeforeEvidence = json.RawMessage(before)
		a.AfterEvidence = json.RawMessage(after)
		out = append(out, a)
	}
	return out, rows.Err()
}
