package auth

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"returnsrecon/internal/access"
)

type AccountInput struct {
	ID           string   `json:"id"`
	Role         string   `json:"role"`
	DisplayName  string   `json:"display_name"`
	CountryScope string   `json:"country_scope"`
	Countries    []string `json:"countries"`
}
type AccountView struct {
	access.Profile
	State string `json:"state"`
}
type WarehouseRequest struct {
	ID              string   `json:"id"`
	Requester       string   `json:"requester"`
	AccountID       string   `json:"account_id"`
	DisplayName     string   `json:"display_name"`
	CountryScope    string   `json:"country_scope"`
	Countries       []string `json:"countries"`
	Reason          string   `json:"reason"`
	Status          string   `json:"status"`
	CreatedAt       string   `json:"created_at"`
	ReviewedBy      string   `json:"reviewed_by"`
	ReviewedAt      string   `json:"reviewed_at"`
	RejectionReason string   `json:"rejection_reason"`
	AccountState    string   `json:"account_state,omitempty"`
}

func conflict(message string) error { return &Error{"conflict", message, 409} }
func forbidden() error              { return &Error{"forbidden", "当前账户无权执行此操作", 403} }
func (s *Service) manager(ctx context.Context, c *sql.Conn, token, role string) (Session, error) {
	v, e := s.current(ctx, c, token)
	if e != nil {
		return v, e
	}
	if v.Scope != "business" || v.State != "complete" {
		return v, restricted()
	}
	if v.Role != role {
		return v, forbidden()
	}
	return v, nil
}

var accountIDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_.-]{2,63}$`)

func validateAccount(ctx context.Context, q access.Querier, in AccountInput) (access.Profile, error) {
	p := access.Profile{ID: in.ID, Role: in.Role, DisplayName: strings.TrimSpace(in.DisplayName), CountryScope: in.CountryScope, Countries: access.Canonical(in.Countries), PermissionsVersion: 1}
	if !accountIDPattern.MatchString(p.ID) {
		return p, invalid("账户名须为3至64位小写字母、数字、点、下划线或连字符")
	}
	if p.Role != "finance" && p.Role != "warehouse" {
		return p, invalid("只能创建财务或仓库账户")
	}
	if p.DisplayName == "" || utf8.RuneCountInString(p.DisplayName) > 64 || strings.IndexFunc(p.DisplayName, unicode.IsControl) >= 0 {
		return p, invalid("显示名称须为1至64个字符且不含控制字符")
	}
	if p.CountryScope == "" {
		if len(p.Countries) == 0 {
			p.CountryScope = "all"
		} else {
			p.CountryScope = "selected"
		}
	}
	if p.CountryScope == "all" {
		if len(p.Countries) > 0 {
			return p, invalid("全部国家范围不能同时指定国家")
		}
		return p, nil
	}
	if p.CountryScope != "selected" || len(p.Countries) == 0 || len(p.Countries) > 256 {
		return p, invalid("请选择至少一个负责国家，或选择全部国家")
	}
	for _, country := range p.Countries {
		var exists bool
		if country == "" {
			return p, invalid("负责国家不能为空")
		}
		e := q.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM normalized_rows n JOIN datasets d ON d.id=n.dataset_id WHERE d.status='published' AND n.country=?)`, country).Scan(&exists)
		if e != nil {
			return p, e
		}
		if !exists {
			return p, invalid("请选择已发布数据中的国家；尚未导入数据时可先使用全部国家")
		}
	}
	return p, nil
}
func (s *Service) managementAudit(ctx context.Context, c *sql.Conn, actor, action, target, request string, before, after any) error {
	_, e := c.ExecContext(ctx, `INSERT INTO account_audit(actor,action,target,request_id,before_json,after_json,occurred_at) VALUES(?,?,?,?,?,?,?)`, actor, action, target, request, encode(before), encode(after), s.now().Format(time.RFC3339Nano))
	return e
}
func saveProfile(ctx context.Context, c *sql.Conn, p access.Profile) error {
	_, e := c.ExecContext(ctx, `INSERT INTO auth_account_profiles(user_id,display_name,country_scope,permissions_version) VALUES(?,?,?,?) ON CONFLICT(user_id) DO UPDATE SET display_name=excluded.display_name,country_scope=excluded.country_scope,permissions_version=excluded.permissions_version`, p.ID, p.DisplayName, p.CountryScope, p.PermissionsVersion)
	if e != nil {
		return e
	}
	if _, e = c.ExecContext(ctx, `DELETE FROM auth_user_countries WHERE user_id=?`, p.ID); e != nil {
		return e
	}
	for _, country := range p.Countries {
		if _, e = c.ExecContext(ctx, `INSERT INTO auth_user_countries VALUES(?,?)`, p.ID, country); e != nil {
			return e
		}
	}
	return nil
}
func availableAccount(ctx context.Context, c *sql.Conn, id, request string) error {
	var count int
	e := c.QueryRowContext(ctx, `SELECT (SELECT count(*) FROM auth_users WHERE id=?)+(SELECT count(*) FROM warehouse_requests WHERE account_id=? AND status='pending' AND id<>?)`, id, id, request).Scan(&count)
	if e != nil {
		return e
	}
	if count > 0 {
		return conflict("该账户名已存在或已有待审批申请")
	}
	return nil
}
func createAccount(ctx context.Context, c *sql.Conn, p access.Profile) error {
	if _, e := c.ExecContext(ctx, `INSERT INTO auth_users(id,role) VALUES(?,?)`, p.ID, p.Role); e != nil {
		return e
	}
	return saveProfile(ctx, c, p)
}
func (s *Service) CreateAccount(ctx context.Context, token string, in AccountInput) (out AccountView, err error) {
	err = s.atomic(ctx, func(c *sql.Conn) error {
		actor, e := s.manager(ctx, c, token, "admin")
		if e != nil {
			return e
		}
		p, e := validateAccount(ctx, c, in)
		if e != nil {
			return e
		}
		if e = availableAccount(ctx, c, p.ID, ""); e != nil {
			return e
		}
		if e = createAccount(ctx, c, p); e != nil {
			return e
		}
		out = AccountView{p, "new"}
		return s.managementAudit(ctx, c, actor.ID, "create", p.ID, "", nil, out)
	})
	if err != nil {
		out = AccountView{}
	}
	return
}
func (s *Service) UpdateAccount(ctx context.Context, token, id string, in AccountInput) (out AccountView, err error) {
	err = s.atomic(ctx, func(c *sql.Conn) error {
		actor, e := s.manager(ctx, c, token, "admin")
		if e != nil {
			return e
		}
		old, e := access.Load(ctx, c, id)
		if errors.Is(e, sql.ErrNoRows) {
			return &Error{"not_found", "账户不存在", 404}
		}
		if e != nil {
			return e
		}
		if old.Role == "admin" {
			return forbidden()
		}
		if in.ID != "" && in.ID != id || in.Role != "" && in.Role != old.Role {
			return invalid("不能修改登录账户名或角色")
		}
		in.ID, in.Role = id, old.Role
		p, e := validateAccount(ctx, c, in)
		if e != nil {
			return e
		}
		p.PermissionsVersion = old.PermissionsVersion
		changed := old.CountryScope != p.CountryScope || encode(old.Countries) != encode(p.Countries)
		if changed {
			if !p.All() {
				suffix, args := p.SQL("n.country")
				suffix = strings.Replace(suffix, " AND n.country IN (", " AND n.country NOT IN (", 1)
				args = append([]any{id}, args...)
				var count int
				e = c.QueryRowContext(ctx, `SELECT count(*) FROM cases c JOIN runs r ON r.id=c.run_id JOIN normalized_rows n ON n.dataset_id=r.dataset_id AND n.id=c.cancel_id WHERE c.assignee=? AND c.status<>'closed'`+suffix, args...).Scan(&count)
				if e != nil {
					return e
				}
				if count > 0 {
					return conflict("仍有新国家范围之外的未结案派单，请先完成这些工作")
				}
			}
			p.PermissionsVersion++
			if _, e = c.ExecContext(ctx, `UPDATE auth_users SET version=version+1 WHERE id=?`, id); e != nil {
				return e
			}
			if _, e = c.ExecContext(ctx, `DELETE FROM auth_sessions WHERE user_id=?`, id); e != nil {
				return e
			}
		}
		if e = saveProfile(ctx, c, p); e != nil {
			return e
		}
		out.Profile = p
		if e = c.QueryRowContext(ctx, `SELECT state FROM auth_users WHERE id=?`, id).Scan(&out.State); e != nil {
			return e
		}
		return s.managementAudit(ctx, c, actor.ID, "update", id, "", old, out)
	})
	if err != nil {
		out = AccountView{}
	}
	return
}
func (s *Service) Accounts(ctx context.Context, token string) (out []AccountView, err error) {
	v, e := s.Current(ctx, token, true)
	if e != nil {
		return nil, e
	}
	if v.Role != "admin" {
		return nil, forbidden()
	}
	profiles, e := s.Users(ctx)
	if e != nil {
		return nil, e
	}
	out = []AccountView{}
	for _, p := range profiles {
		var state string
		if e = s.DB.QueryRowContext(ctx, `SELECT state FROM auth_users WHERE id=?`, p.ID).Scan(&state); e != nil {
			return nil, e
		}
		if state == "deleted" {
			continue
		}
		out = append(out, AccountView{p, state})
	}
	return out, nil
}
func (s *Service) RequestWarehouse(ctx context.Context, token string, in AccountInput, reason string) (out WarehouseRequest, err error) {
	err = s.atomic(ctx, func(c *sql.Conn) error {
		actor, e := s.manager(ctx, c, token, "finance")
		if e != nil {
			return e
		}
		if in.Role != "" && in.Role != "warehouse" {
			return invalid("只能申请仓库账户")
		}
		in.Role = "warehouse"
		p, e := validateAccount(ctx, c, in)
		if e != nil {
			return e
		}
		owner, e := access.Load(ctx, c, actor.ID)
		if e != nil {
			return e
		}
		if !owner.Contains(p) {
			return forbidden()
		}
		if utf8.RuneCountInString(reason) > 1000 {
			return invalid("申请理由不能超过1000字符")
		}
		if e = availableAccount(ctx, c, p.ID, ""); e != nil {
			return e
		}
		id, e := RandomToken()
		if e != nil {
			return e
		}
		out = WarehouseRequest{ID: id, Requester: actor.ID, AccountID: p.ID, DisplayName: p.DisplayName, CountryScope: p.CountryScope, Countries: p.Countries, Reason: strings.TrimSpace(reason), Status: "pending", CreatedAt: s.now().Format(time.RFC3339Nano)}
		_, e = c.ExecContext(ctx, `INSERT INTO warehouse_requests(id,requester,account_id,display_name,country_scope,countries_json,reason,status,created_at) VALUES(?,?,?,?,?,?,?,'pending',?)`, out.ID, out.Requester, out.AccountID, out.DisplayName, out.CountryScope, encode(out.Countries), out.Reason, out.CreatedAt)
		if e != nil {
			return e
		}
		return s.managementAudit(ctx, c, actor.ID, "request", out.AccountID, out.ID, nil, out)
	})
	if err != nil {
		out = WarehouseRequest{}
	}
	return
}

// Only approved requests expose the created account's current setup state.
// The caller's request ownership check still governs access to this projection.
const requestColumns = `id,requester,account_id,display_name,country_scope,countries_json,reason,status,created_at,reviewed_by,reviewed_at,rejection_reason,coalesce((SELECT state FROM auth_users WHERE auth_users.id=warehouse_requests.account_id AND warehouse_requests.status='approved'),'')`

func scanRequest(row interface{ Scan(...any) error }) (v WarehouseRequest, e error) {
	var countries string
	e = row.Scan(&v.ID, &v.Requester, &v.AccountID, &v.DisplayName, &v.CountryScope, &countries, &v.Reason, &v.Status, &v.CreatedAt, &v.ReviewedBy, &v.ReviewedAt, &v.RejectionReason, &v.AccountState)
	if e == nil {
		e = json.Unmarshal([]byte(countries), &v.Countries)
	}
	return
}
func (s *Service) WarehouseRequests(ctx context.Context, token string) ([]WarehouseRequest, error) {
	v, e := s.Current(ctx, token, true)
	if e != nil {
		return nil, e
	}
	if v.Role != "admin" && v.Role != "finance" {
		return nil, forbidden()
	}
	query := `SELECT ` + requestColumns + ` FROM warehouse_requests`
	args := []any{}
	if v.Role != "admin" {
		query += ` WHERE requester=?`
		args = append(args, v.ID)
	}
	rows, e := s.DB.QueryContext(ctx, query+` ORDER BY created_at DESC,id`, args...)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []WarehouseRequest{}
	for rows.Next() {
		r, e := scanRequest(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
func (s *Service) ReviewWarehouse(ctx context.Context, token, id string, approve bool, reason string) (out WarehouseRequest, err error) {
	err = s.atomic(ctx, func(c *sql.Conn) error {
		actor, e := s.manager(ctx, c, token, "admin")
		if e != nil {
			return e
		}
		old, e := scanRequest(c.QueryRowContext(ctx, `SELECT `+requestColumns+` FROM warehouse_requests WHERE id=?`, id))
		if errors.Is(e, sql.ErrNoRows) {
			return &Error{"not_found", "申请不存在", 404}
		}
		if e != nil {
			return e
		}
		if old.Status != "pending" {
			return conflict("申请已经审批，不能重复操作")
		}
		out = old
		out.ReviewedBy = actor.ID
		out.ReviewedAt = s.now().Format(time.RFC3339Nano)
		if approve {
			p, e := validateAccount(ctx, c, AccountInput{ID: old.AccountID, Role: "warehouse", DisplayName: old.DisplayName, CountryScope: old.CountryScope, Countries: old.Countries})
			if e != nil {
				return e
			}
			owner, e := access.Load(ctx, c, old.Requester)
			if e != nil {
				return e
			}
			if owner.Role != "finance" || !owner.Contains(p) {
				return conflict("申请人当前国家权限已不再覆盖申请范围，请驳回后重新申请")
			}
			if e = availableAccount(ctx, c, p.ID, id); e != nil {
				return e
			}
			if e = createAccount(ctx, c, p); e != nil {
				return e
			}
			out.Status = "approved"
			out.AccountState = "new"
		} else {
			out.RejectionReason = strings.TrimSpace(reason)
			if out.RejectionReason == "" || utf8.RuneCountInString(out.RejectionReason) > 1000 {
				return invalid("驳回原因须为1至1000字符")
			}
			out.Status = "rejected"
		}
		_, e = c.ExecContext(ctx, `UPDATE warehouse_requests SET status=?,reviewed_by=?,reviewed_at=?,rejection_reason=? WHERE id=? AND status='pending'`, out.Status, out.ReviewedBy, out.ReviewedAt, out.RejectionReason, id)
		if e != nil {
			return e
		}
		return s.managementAudit(ctx, c, actor.ID, out.Status, out.AccountID, id, old, out)
	})
	if err != nil {
		out = WarehouseRequest{}
	}
	return
}
func (s *Service) AccountAudit(ctx context.Context, token string) ([]map[string]any, error) {
	v, e := s.Current(ctx, token, true)
	if e != nil {
		return nil, e
	}
	if v.Role != "admin" {
		return nil, forbidden()
	}
	rows, e := s.DB.QueryContext(ctx, `SELECT id,actor,action,target,request_id,before_json,after_json,occurred_at FROM account_audit ORDER BY id DESC LIMIT 200`)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id int64
		var actor, action, target, request, before, after, at string
		if e = rows.Scan(&id, &actor, &action, &target, &request, &before, &after, &at); e != nil {
			return nil, e
		}
		out = append(out, map[string]any{"id": id, "actor": actor, "action": action, "target": target, "request_id": request, "before": json.RawMessage(before), "after": json.RawMessage(after), "occurred_at": at})
	}
	return out, rows.Err()
}
