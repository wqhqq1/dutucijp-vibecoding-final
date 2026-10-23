package web

import (
	"github.com/gin-gonic/gin"
	"net/http"
)

func (s *Server) registerNativePasskeys(r *gin.Engine) {
	r.GET("/api/auth/client-config", func(c *gin.Context) { v, e := s.Auth.ClientConfiguration(c); respond(c, v, e) })
	for _, register := range []bool{true, false} {
		name := "login"
		if register {
			name = "register"
		}
		isRegister := register
		r.POST("/api/auth/native-passkeys/"+name+"/begin", func(c *gin.Context) {
			var b struct {
				ID        string `json:"id"`
				BindingID string `json:"binding_id"`
			}
			if !bind(c, &b) {
				return
			}
			ctx, e := s.Auth.WithNativePasskey(c.Request.Context(), b.BindingID)
			if e != nil {
				respond(c, nil, e)
				return
			}
			v, e := s.Auth.BeginPasskey(ctx, token(c), b.ID, isRegister)
			if e == nil {
				s.cookie(c, v.Token)
			}
			respond(c, v, e)
		})
		r.POST("/api/auth/native-passkeys/"+name+"/finish", func(c *gin.Context) {
			if !isRegister && !s.Auth.CheckCSRF(token(c), c.GetHeader("X-CSRF-Token")) {
				c.AbortWithStatusJSON(403, gin.H{"code": "invalid_csrf", "message": "会话校验失败，请重新选择Passkey"})
				return
			}
			ctx, e := s.Auth.WithNativePasskey(c.Request.Context(), c.Query("binding_id"))
			if e != nil {
				respond(c, nil, e)
				return
			}
			c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 1024*1024)
			v, e := s.Auth.FinishPasskey(ctx, token(c), c.Query("challenge_id"), c.Request, isRegister, c.ClientIP())
			s.grant(c, v, e)
		})
	}
}
