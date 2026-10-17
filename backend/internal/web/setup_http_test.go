package web

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/pquerna/otp/totp"
	"returnsrecon/internal/auth"
)

func TestTC36_FirstSetupHTTPBoundaryAndAccountSelection(t *testing.T) {
	f := httpSecurityTest(t)
	list := f.request(t, nil, "GET", "/api/auth/setup/accounts", nil, nil)
	expectHTTPStatus(t, list, http.StatusOK)
	var accounts []auth.Identity
	decodeHTTPResponse(t, list, &accounts)
	if len(accounts) != 4 || list.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("invalid first-use account response")
	}
	for _, forbidden := range []string{"password", "token", "csrf", "secret", "credential"} {
		if strings.Contains(list.Body.String(), forbidden) {
			t.Fatal("secret field in account list")
		}
	}
	for _, headers := range []map[string]string{{"Origin": ""}, {"Origin": "https://untrusted.invalid"}, {"Host": "untrusted.invalid"}} {
		expectHTTPStatus(t, f.request(t, nil, "POST", "/api/auth/setup/start", map[string]any{"id": "http-finance"}, headers), http.StatusForbidden)
	}
	expectHTTPStatus(t, f.request(t, nil, "POST", "/api/auth/setup/start", map[string]any{"id": "unknown"}, nil), http.StatusUnauthorized)
	c := &httpSecurityClient{id: "http-finance"}
	expectHTTPStatus(t, f.request(t, c, "POST", "/api/auth/setup/start", map[string]any{"id": c.id, "role": "admin"}, nil), http.StatusUnprocessableEntity)
	w := f.request(t, c, "POST", "/api/auth/setup/start", map[string]any{"id": c.id}, nil)
	expectHTTPStatus(t, w, http.StatusOK)
	var grant auth.Grant
	decodeHTTPResponse(t, w, &grant)
	if grant.Session.Role != "finance" || grant.Session.Scope != "setup" {
		t.Fatal("client selected role or business scope")
	}
	expectHTTPStatus(t, f.request(t, c, "GET", "/api/runs", nil, nil), http.StatusForbidden)
	expectHTTPStatus(t, f.request(t, c, "POST", "/api/auth/setup/start", map[string]any{"id": "http-admin"}, map[string]string{"X-CSRF-Token": "wrong"}), http.StatusForbidden)
	expectHTTPStatus(t, f.request(t, c, "POST", "/api/auth/setup/complete", map[string]any{"saved": true}, nil), http.StatusForbidden)
	expectHTTPStatus(t, f.request(t, c, "POST", "/api/auth/setup/password", map[string]any{"password": "weak"}, nil), http.StatusUnprocessableEntity)
	expectHTTPStatus(t, f.request(t, c, "POST", "/api/auth/setup/password", map[string]any{"password": httpFixturePassword}, nil), http.StatusOK)
	expectHTTPStatus(t, f.request(t, c, "POST", "/api/auth/setup/start", map[string]any{"id": c.id}, nil), http.StatusUnauthorized)
	list = f.request(t, nil, "GET", "/api/auth/setup/accounts", nil, nil)
	decodeHTTPResponse(t, list, &accounts)
	if len(accounts) != 3 {
		t.Fatal("password account still listed")
	}
	// Finish the new path through real factor and recovery confirmation routes.
	enrolled := f.request(t, c, "POST", "/api/auth/totp/enroll", map[string]any{}, nil)
	expectHTTPStatus(t, enrolled, http.StatusOK)
	var en auth.Enrollment
	decodeHTTPResponse(t, enrolled, &en)
	code, err := totp.GenerateCode(en.Secret, time.Now())
	if err != nil {
		t.Fatal("fixture factor failed")
	}
	expectHTTPStatus(t, f.request(t, c, "POST", "/api/auth/totp/confirm", map[string]any{"code": code}, nil), http.StatusOK)
	expectHTTPStatus(t, f.request(t, c, "GET", "/api/runs", nil, nil), http.StatusForbidden)
	expectHTTPStatus(t, f.request(t, c, "POST", "/api/auth/setup/complete", map[string]any{"saved": false}, nil), http.StatusUnprocessableEntity)
	expectHTTPStatus(t, f.request(t, c, "POST", "/api/auth/setup/complete", map[string]any{"saved": true}, nil), http.StatusOK)
	expectHTTPStatus(t, f.request(t, c, "GET", "/api/runs", nil, nil), http.StatusOK)
	complete := f.configured(t, "http-admin")
	expectHTTPStatus(t, f.request(t, complete, "POST", "/api/auth/setup/start", map[string]any{"id": complete.id}, nil), http.StatusUnauthorized)
}
