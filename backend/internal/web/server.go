package web

import (
	"context"
	"database/sql"
	"embed"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"returnsrecon/internal/auth"
	"returnsrecon/internal/store"
)

//go:embed assets/*
var assets embed.FS

const cookieName = "recon_session"

type Server struct {
	Store  *store.Store
	Auth   *auth.Service
	Origin string
	Jobs   *Jobs
}

func (s *Server) Handler() http.Handler {
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.ContextWithFallback = true
	r.SetTrustedProxies(nil)
	r.Use(gin.RecoveryWithWriter(io.Discard, func(c *gin.Context, _ any) {
		c.AbortWithStatusJSON(500, gin.H{"code": "internal_error", "message": "请求未完成"})
	}), s.boundary)
	r.GET("/api/auth/me", func(c *gin.Context) { v, e := s.Auth.Current(c, token(c), false); respond(c, v, e) })
	r.POST("/api/auth/bootstrap", func(c *gin.Context) {
		var b struct {
			Token string `json:"token"`
		}
		if bind(c, &b) {
			g, e := s.Auth.Bootstrap(c, b.Token)
			s.grant(c, g, e)
		}
	})
	r.POST("/api/auth/setup/password", func(c *gin.Context) {
		var b struct {
			Password string `json:"password"`
		}
		if bind(c, &b) {
			g, e := s.Auth.SetPassword(c, token(c), b.Password)
			s.grant(c, g, e)
		}
	})
	r.POST("/api/auth/login", func(c *gin.Context) {
		var b struct {
			ID       string `json:"id"`
			Password string `json:"password"`
		}
		if bind(c, &b) {
			g, e := s.Auth.LoginPassword(c, b.ID, b.Password, c.ClientIP())
			s.grant(c, g, e)
		}
	})
	r.POST("/api/auth/totp/enroll", func(c *gin.Context) { v, e := s.Auth.BeginTOTP(c, token(c)); respond(c, v, e) })
	r.POST("/api/auth/totp/confirm", func(c *gin.Context) {
		var b struct {
			Code string `json:"code"`
		}
		if bind(c, &b) {
			g, e := s.Auth.ConfirmTOTP(c, token(c), b.Code, c.ClientIP())
			s.grant(c, g, e)
		}
	})
	r.POST("/api/auth/totp/login", func(c *gin.Context) {
		var b struct {
			Code string `json:"code"`
		}
		if bind(c, &b) {
			g, e := s.Auth.LoginTOTP(c, token(c), b.Code, c.ClientIP())
			s.grant(c, g, e)
		}
	})
	r.POST("/api/auth/setup/complete", func(c *gin.Context) {
		var b struct {
			Saved bool `json:"saved"`
		}
		if bind(c, &b) {
			g, e := s.Auth.Complete(c, token(c), b.Saved)
			s.grant(c, g, e)
		}
	})
	r.POST("/api/auth/recover", func(c *gin.Context) {
		var b struct {
			ID       string `json:"id"`
			Password string `json:"password"`
			Code     string `json:"code"`
		}
		if bind(c, &b) {
			g, e := s.Auth.Recover(c, b.ID, b.Password, b.Code, c.ClientIP())
			s.grant(c, g, e)
		}
	})
	r.POST("/api/auth/credentials/change", func(c *gin.Context) {
		var b struct {
			Password string `json:"password"`
			Remove   string `json:"remove"`
		}
		if !bind(c, &b) {
			return
		}
		var g auth.Grant
		var e error
		if b.Password != "" {
			g, e = s.Auth.ChangePassword(c, token(c), b.Password)
		} else {
			g, e = s.Auth.RemoveFactor(c, token(c), b.Remove)
		}
		s.grant(c, g, e)
	})
	for _, register := range []bool{true, false} {
		name := "login"
		if register {
			name = "register"
		}
		isRegister := register
		r.POST("/api/auth/passkeys/"+name+"/begin", func(c *gin.Context) {
			var b struct {
				ID string `json:"id"`
			}
			if !bind(c, &b) {
				return
			}
			out, e := s.Auth.BeginPasskey(c, token(c), b.ID, isRegister)
			if e == nil {
				s.cookie(c, out.Token)
			}
			respond(c, out, e)
		})
		r.POST("/api/auth/passkeys/"+name+"/finish", func(c *gin.Context) {
			g, e := s.Auth.FinishPasskey(c, token(c), c.Query("challenge_id"), c.Request, isRegister, c.ClientIP())
			s.grant(c, g, e)
		})
	}
	r.POST("/api/auth/logout", func(c *gin.Context) {
		e := s.Auth.Logout(c, token(c))
		c.SetCookie(cookieName, "", -1, "/", "", false, true)
		respond(c, map[string]bool{"logged_out": e == nil}, e)
	})
	r.POST("/api/auth/recovery/reissue", func(c *gin.Context) { g, e := s.Auth.ReissueRecovery(c, token(c)); s.grant(c, g, e) })
	business := r.Group("/api", s.requireBusiness)
	business.GET("/runs", s.runs)
	business.GET("/overview", s.overview)
	business.GET("/users", func(c *gin.Context) {
		if !s.finance(c) {
			return
		}
		v, e := s.Auth.Users(c)
		respond(c, v, e)
	})
	business.GET("/transactions", func(c *gin.Context) {
		f, e := filter(c)
		if e != nil {
			respond(c, nil, e)
			return
		}
		v, e := s.Store.ListTransactions(c, actor(c), f)
		respond(c, v, e)
	})
	business.GET("/allocations", func(c *gin.Context) {
		f, e := filter(c)
		if e != nil {
			respond(c, nil, e)
			return
		}
		v, e := s.Store.ListAllocations(c, actor(c), f)
		respond(c, v, e)
	})
	business.GET("/cases", func(c *gin.Context) {
		f, e := filter(c)
		if e != nil {
			respond(c, nil, e)
			return
		}
		v, e := s.Store.ListCases(c, actor(c), f)
		respond(c, v, e)
	})
	business.GET("/cases/:id", func(c *gin.Context) { v, e := s.Store.WorkflowCase(c, actor(c), c.Param("id")); respond(c, v, e) })
	business.GET("/cases/:id/evidence", func(c *gin.Context) {
		p, e := page(c)
		if e != nil {
			respond(c, nil, e)
			return
		}
		v, e := s.Store.CaseEvidence(c, actor(c), c.Param("id"), p)
		respond(c, v, e)
	})
	business.GET("/cases/:id/candidates", func(c *gin.Context) {
		p, e := page(c)
		if e != nil {
			respond(c, nil, e)
			return
		}
		v, e := s.Store.ListCandidates(c, actor(c), c.Param("id"), p)
		respond(c, v, e)
	})
	business.GET("/cases/:id/audit", func(c *gin.Context) { v, e := s.Store.WorkflowHistory(c, actor(c), c.Param("id")); respond(c, v, e) })
	business.POST("/cases/:id/assign", func(c *gin.Context) {
		if !s.finance(c) {
			return
		}
		var b struct {
			Assignee string `json:"assignee"`
		}
		if !bind(c, &b) {
			return
		}
		identities, e := s.Auth.Users(c)
		if e != nil {
			respond(c, nil, e)
			return
		}
		var target store.WorkflowActor
		for _, u := range identities {
			if u.ID == b.Assignee {
				target = store.WorkflowActor{ID: u.ID, Role: u.Role}
			}
		}
		v, e := s.Store.AssignCase(c, actor(c), target, c.Param("id"))
		respond(c, v, e)
	})
	business.POST("/cases/:id/start", func(c *gin.Context) {
		var b struct {
			Version int `json:"version"`
		}
		if bind(c, &b) {
			v, e := s.Store.TransitionCase(c, actor(c), c.Param("id"), "in_progress", b.Version, "")
			respond(c, v, e)
		}
	})
	business.POST("/cases/:id/close", func(c *gin.Context) {
		var b struct {
			Version    int    `json:"version"`
			Conclusion string `json:"conclusion"`
		}
		if bind(c, &b) {
			v, e := s.Store.TransitionCase(c, actor(c), c.Param("id"), "closed", b.Version, b.Conclusion)
			respond(c, v, e)
		}
	})
	business.POST("/cases/:id/confirm-match", func(c *gin.Context) {
		var b struct {
			Version int    `json:"version"`
			Sale    string `json:"sale_row_id"`
		}
		if bind(c, &b) {
			v, e := s.Store.ConfirmCandidate(c, actor(c), c.Param("id"), b.Sale, b.Version)
			respond(c, v, e)
		}
	})
	business.GET("/exports", func(c *gin.Context) {
		if !s.finance(c) {
			return
		}
		f, e := filter(c)
		if e != nil {
			respond(c, nil, e)
			return
		}
		if _, e = s.Store.ListTransactions(c, actor(c), f); e != nil {
			respond(c, nil, e)
			return
		}
		c.Header("Content-Type", "text/csv; charset=utf-8")
		c.Header("Content-Disposition", `attachment; filename="reconciliation.csv"`)
		if e = s.Store.ExportCSV(c, actor(c), f, c.Writer); e != nil {
			c.Abort()
		}
	})
	business.GET("/audit", s.audit)
	if s.Jobs != nil {
		business.POST("/jobs", func(c *gin.Context) {
			if !s.finance(c) {
				return
			}
			var b struct {
				Scope string `json:"scope"`
			}
			if bind(c, &b) {
				v, e := s.Jobs.Start(c, b.Scope)
				respond(c, v, e)
			}
		})
		business.GET("/jobs/:id", func(c *gin.Context) {
			if !s.finance(c) {
				return
			}
			v, e := s.Jobs.Get(c, c.Param("id"))
			respond(c, v, e)
		})
	}
	sub, _ := fs.Sub(assets, "assets")
	files := http.FileServer(http.FS(sub))
	r.NoRoute(func(c *gin.Context) {
		if strings.HasPrefix(c.Request.URL.Path, "/api/") {
			c.JSON(404, gin.H{"code": "not_found", "message": "接口不存在"})
			return
		}
		if c.Request.Method != "GET" && c.Request.Method != "HEAD" {
			c.Status(405)
			return
		}
		if strings.HasPrefix(c.Request.URL.Path, "/assets/") {
			files.ServeHTTP(c.Writer, c.Request)
			return
		}
		body, err := fs.ReadFile(sub, "index.html")
		if err != nil {
			c.JSON(503, gin.H{"code": "frontend_not_built", "message": "请先执行build.ps1构建前端资源"})
			return
		}
		c.Data(200, "text/html; charset=utf-8", body)
	})
	return r
}
func token(c *gin.Context) string { t, _ := c.Cookie(cookieName); return t }
func (s *Server) cookie(c *gin.Context, t string) {
	c.SetSameSite(http.SameSiteStrictMode)
	c.SetCookie(cookieName, t, 8*3600, "/", "", false, true)
}
func (s *Server) grant(c *gin.Context, g auth.Grant, e error) {
	if e == nil {
		s.cookie(c, g.Token)
	}
	respond(c, g, e)
}
func bind(c *gin.Context, out any) bool {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 1024*1024)
	d := json.NewDecoder(c.Request.Body)
	d.DisallowUnknownFields()
	if e := d.Decode(out); e != nil {
		c.JSON(422, gin.H{"code": "invalid_input", "message": "请求字段或格式不合法"})
		return false
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		c.JSON(422, gin.H{"code": "invalid_input", "message": "仅接受一个JSON对象"})
		return false
	}
	return true
}
func respond(c *gin.Context, value any, e error) {
	if e == nil {
		c.JSON(200, value)
		return
	}
	var ae *auth.Error
	if errors.As(e, &ae) {
		c.JSON(ae.Status, ae)
		return
	}
	var we *store.WorkflowError
	if errors.As(e, &we) {
		status := 409
		switch we.Code {
		case "unauthenticated":
			status = 401
		case "forbidden":
			status = 403
		case "not_found":
			status = 404
		case "invalid_input", "invalid_assignee", "empty_conclusion", "invalid_candidate":
			status = 422
		}
		c.JSON(status, we)
		return
	}
	c.JSON(500, gin.H{"code": "internal_error", "message": "操作未完成，请查看本地诊断并重试"})
}
func (s *Server) boundary(c *gin.Context) {
	day := time.Now().In(time.FixedZone("Asia/Shanghai", 8*3600)).Format("01-02")
	if c.Request.Method == "POST" && (day == "10-22" || day == "11-17") {
		blocked := map[string]bool{"/api/auth/bootstrap": true, "/api/auth/setup/password": true, "/api/auth/setup/complete": true, "/api/auth/totp/enroll": true, "/api/auth/totp/confirm": true, "/api/auth/passkeys/register/begin": true, "/api/auth/passkeys/register/finish": true, "/api/auth/recover": true, "/api/auth/credentials/change": true, "/api/auth/recovery/reissue": true}
		if blocked[c.Request.URL.Path] {
			c.AbortWithStatusJSON(403, gin.H{"code": "reserved_day", "message": "预留日不能首次配置、恢复重绑或更改凭据，请使用已配置的演示账户"})
			return
		}
	}
	expected, _ := url.Parse(s.Origin)
	host := strings.ToLower(c.Request.Host)
	if host != expected.Host {
		if host == "127.0.0.1:8080" && (c.Request.Method == "GET" || c.Request.Method == "HEAD") {
			c.Redirect(http.StatusTemporaryRedirect, s.Origin+c.Request.URL.RequestURI())
		} else {
			c.JSON(403, gin.H{"code": "invalid_host", "message": "请使用规范localhost入口"})
		}
		c.Abort()
		return
	}
	c.Header("Cache-Control", "no-store")
	c.Header("X-Content-Type-Options", "nosniff")
	c.Header("Referrer-Policy", "no-referrer")
	c.Header("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data:; connect-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'")
	if c.Request.Method != "GET" && c.Request.Method != "HEAD" && c.Request.Method != "OPTIONS" {
		if c.GetHeader("Origin") != s.Origin {
			c.AbortWithStatusJSON(403, gin.H{"code": "invalid_origin", "message": "拒绝跨来源操作"})
			return
		}
		if t := token(c); t != "" {
			_, valid := s.Auth.Current(c, t, false)
			if valid == nil && !s.Auth.CheckCSRF(t, c.GetHeader("X-CSRF-Token")) {
				c.AbortWithStatusJSON(403, gin.H{"code": "invalid_csrf", "message": "会话校验失败，请刷新页面"})
				return
			}
		}
	}
	c.Next()
}
func (s *Server) requireBusiness(c *gin.Context) {
	v, e := s.Auth.Current(c, token(c), true)
	if e != nil {
		respond(c, nil, e)
		c.Abort()
		return
	}
	c.Set("principal", v)
	trustedToken := token(c)
	c.Request = c.Request.WithContext(store.WithWriteAuthorization(c.Request.Context(), func(ctx context.Context, conn *sql.Conn) error {
		return s.Auth.ValidateWrite(ctx, conn, trustedToken, v)
	}))
	c.Next()
}
func actor(c *gin.Context) store.WorkflowActor {
	v := c.MustGet("principal").(auth.Session)
	return store.WorkflowActor{ID: v.ID, Role: v.Role}
}
func (s *Server) finance(c *gin.Context) bool {
	a := actor(c)
	if a.Role != "finance" && a.Role != "admin" {
		respond(c, nil, &auth.Error{Code: "forbidden", Message: "需要财务或管理员权限", Status: 403})
		return false
	}
	return true
}
func page(c *gin.Context) (store.PageRequest, error) {
	p := store.PageRequest{}
	for key, target := range map[string]*int{"page": &p.Page, "page_size": &p.PageSize} {
		if raw := c.Query(key); raw != "" {
			v, e := strconv.Atoi(raw)
			if e != nil {
				return p, &auth.Error{Code: "invalid_input", Message: "分页须为整数", Status: 422}
			}
			*target = v
		}
	}
	return p, nil
}
func filter(c *gin.Context) (store.QueryFilter, error) {
	p, e := page(c)
	f := store.QueryFilter{RunID: c.Query("run_id"), Status: c.Query("status"), Assignee: c.Query("assignee"), Customer: c.Query("customer"), Stock: c.Query("stock"), Invoice: c.Query("invoice"), Classification: c.Query("classification"), Kind: c.Query("kind"), DateFrom: c.Query("date_from"), DateTo: c.Query("date_to"), Page: p.Page, PageSize: p.PageSize}
	return f, e
}
func (s *Server) runs(c *gin.Context) {
	rows, e := s.Store.DB.QueryContext(c, "SELECT r.id,r.dataset_id,d.scope,d.cleaning_version,r.rule_version FROM runs r JOIN datasets d ON d.id=r.dataset_id WHERE r.status='published' AND d.status='published' ORDER BY r.completed_at DESC")
	if e != nil {
		respond(c, nil, e)
		return
	}
	defer rows.Close()
	out := []map[string]string{}
	for rows.Next() {
		var id, ds, scope, clean, rule string
		if e = rows.Scan(&id, &ds, &scope, &clean, &rule); e != nil {
			respond(c, nil, e)
			return
		}
		out = append(out, map[string]string{"run_id": id, "dataset_id": ds, "scope": scope, "cleaning_version": clean, "rule_version": rule})
	}
	respond(c, out, rows.Err())
}
func (s *Server) overview(c *gin.Context) {
	run := c.Query("run_id")
	a := actor(c)
	if run == "" {
		respond(c, nil, &auth.Error{Code: "invalid_input", Message: "必须指定当前运行版本", Status: 422})
		return
	}
	var published int
	if e := s.Store.DB.QueryRowContext(c, "SELECT 1 FROM runs r JOIN datasets d ON d.id=r.dataset_id WHERE r.id=? AND r.status='published' AND d.status='published'", run).Scan(&published); e != nil {
		if errors.Is(e, sql.ErrNoRows) {
			e = &auth.Error{Code: "not_found", Message: "运行不存在或尚未发布", Status: 404}
		}
		respond(c, nil, e)
		return
	}
	out := map[string]any{"run_id": run, "caveat": "规则推断；没有真实原订单关联号。人工处理记录为课程模拟。"}
	var total, pending, progress, closed int
	query := "SELECT count(*),coalesce(sum(status='pending'),0),coalesce(sum(status='in_progress'),0),coalesce(sum(status='closed'),0) FROM cases WHERE run_id=?"
	args := []any{run}
	if a.Role == "warehouse" {
		query += " AND assignee=?"
		args = append(args, a.ID)
	}
	e := s.Store.DB.QueryRowContext(c, query, args...).Scan(&total, &pending, &progress, &closed)
	if e != nil {
		respond(c, nil, e)
		return
	}
	out["cases"] = map[string]int{"total": total, "pending": pending, "in_progress": progress, "closed": closed}
	if a.Role != "warehouse" {
		var raw string
		if e = s.Store.DB.QueryRowContext(c, "SELECT summary_json FROM runs WHERE id=?", run).Scan(&raw); e == nil {
			var data any
			e = json.Unmarshal([]byte(raw), &data)
			out["initial_reconciliation"] = data
		}
	}
	respond(c, out, e)
}
func (s *Server) audit(c *gin.Context) {
	if id := c.Query("case_id"); id != "" {
		v, e := s.Store.WorkflowHistory(c, actor(c), id)
		respond(c, v, e)
		return
	}
	if !s.finance(c) {
		return
	}
	rows, e := s.Store.DB.QueryContext(c, "SELECT actor,action,method,result,occurred_at FROM auth_audit ORDER BY id DESC LIMIT 100")
	if e != nil {
		respond(c, nil, e)
		return
	}
	defer rows.Close()
	out := []map[string]string{}
	for rows.Next() {
		var a, b, d, e, f string
		if err := rows.Scan(&a, &b, &d, &e, &f); err != nil {
			respond(c, nil, err)
			return
		}
		out = append(out, map[string]string{"actor": a, "action": b, "method": d, "result": e, "occurred_at": f})
	}
	respond(c, out, rows.Err())
}
