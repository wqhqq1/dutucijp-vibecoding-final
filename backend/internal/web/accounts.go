package web

import (
	"github.com/gin-gonic/gin"
	"returnsrecon/internal/access"
	"returnsrecon/internal/auth"
)

func (s *Server) registerAccounts(r *gin.RouterGroup) {
	r.GET("/admin/passkey-domains", func(c *gin.Context) { v, e := s.Auth.PasskeyDomains(c, token(c)); respond(c, v, e) })
	r.PUT("/admin/passkey-domains", func(c *gin.Context) {
		var b auth.PasskeyDomains
		if bind(c, &b) {
			v, e := s.Auth.UpdatePasskeyDomains(c, token(c), b)
			respond(c, v, e)
		}
	})
	r.GET("/admin/accounts", func(c *gin.Context) { v, e := s.Auth.Accounts(c, token(c)); respond(c, v, e) })
	r.POST("/admin/accounts", func(c *gin.Context) {
		var b auth.AccountInput
		if bind(c, &b) {
			v, e := s.Auth.CreateAccount(c, token(c), b)
			respond(c, v, e)
		}
	})
	r.GET("/admin/accounts/:id/deletion-preview", func(c *gin.Context) {
		v, e := s.Auth.AccountDeletionPreview(c, token(c), c.Param("id"))
		respond(c, v, e)
	})
	r.DELETE("/admin/accounts/:id", func(c *gin.Context) {
		var b struct{}
		if bind(c, &b) {
			v, e := s.Auth.DeleteAccount(c, token(c), c.Param("id"))
			respond(c, v, e)
		}
	})
	r.PATCH("/admin/accounts/:id", func(c *gin.Context) {
		var b auth.AccountInput
		if bind(c, &b) {
			v, e := s.Auth.UpdateAccount(c, token(c), c.Param("id"), b)
			respond(c, v, e)
		}
	})
	r.GET("/admin/account-audit", func(c *gin.Context) { v, e := s.Auth.AccountAudit(c, token(c)); respond(c, v, e) })
	r.GET("/warehouse-requests", func(c *gin.Context) { v, e := s.Auth.WarehouseRequests(c, token(c)); respond(c, v, e) })
	r.POST("/warehouse-requests", func(c *gin.Context) {
		var b struct {
			auth.AccountInput
			Reason string `json:"reason"`
		}
		if bind(c, &b) {
			v, e := s.Auth.RequestWarehouse(c, token(c), b.AccountInput, b.Reason)
			respond(c, v, e)
		}
	})
	r.POST("/warehouse-requests/:id/approve", func(c *gin.Context) {
		var b struct{}
		if bind(c, &b) {
			v, e := s.Auth.ReviewWarehouse(c, token(c), c.Param("id"), true, "")
			respond(c, v, e)
		}
	})
	r.POST("/warehouse-requests/:id/reject", func(c *gin.Context) {
		var b struct {
			Reason string `json:"reason"`
		}
		if bind(c, &b) {
			v, e := s.Auth.ReviewWarehouse(c, token(c), c.Param("id"), false, b.Reason)
			respond(c, v, e)
		}
	})
	r.GET("/countries", func(c *gin.Context) {
		suffix, args := access.SQL(c, "n.country")
		rows, e := s.Store.DB.QueryContext(c, `SELECT DISTINCT n.country FROM normalized_rows n JOIN datasets d ON d.id=n.dataset_id WHERE d.status='published' AND n.country<>''`+suffix+` ORDER BY n.country`, args...)
		if e != nil {
			respond(c, nil, e)
			return
		}
		defer rows.Close()
		out := []string{}
		for rows.Next() {
			var country string
			if e = rows.Scan(&country); e != nil {
				respond(c, nil, e)
				return
			}
			out = append(out, country)
		}
		respond(c, out, rows.Err())
	})
}
func (s *Server) visibleUsers(c *gin.Context) ([]access.Profile, error) {
	users, e := s.Auth.Users(c)
	if e != nil {
		return nil, e
	}
	p := c.MustGet("principal").(auth.Session).Profile()
	out := []access.Profile{}
	for _, u := range users {
		visible := p.All() || u.All()
		for _, country := range p.Countries {
			visible = visible || u.Allows(country)
		}
		if country := c.Query("country"); country != "" {
			visible = visible && p.Allows(country) && u.Allows(country)
		}
		if visible {
			out = append(out, u)
		}
	}
	return out, nil
}
