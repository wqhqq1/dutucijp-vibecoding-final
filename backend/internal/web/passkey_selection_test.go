package web

import (
	"net/http"
	"strings"
	"testing"

	"returnsrecon/internal/auth"
)

func TestTC37_PasskeySelectionHTTPAnonymousChallengeAndCSRF(t *testing.T) {
	f := httpSecurityTest(t)
	client := &httpSecurityClient{}
	w := f.request(t, client, "POST", "/api/auth/passkeys/login/begin", map[string]any{}, nil)
	expectHTTPStatus(t, w, http.StatusOK)
	var begin auth.PasskeyBegin
	decodeHTTPResponse(t, w, &begin)
	if client.cookie == nil || client.csrf == "" || begin.ChallengeID == "" || strings.Contains(w.Body.String(), "allowCredentials") || strings.Contains(w.Body.String(), "http-finance") {
		t.Fatal("anonymous begin must issue only a challenge cookie/CSRF and no account allowlist")
	}
	expectHTTPStatus(t, f.request(t, client, "GET", "/api/auth/me", nil, nil), http.StatusUnauthorized)
	expectHTTPStatus(t, f.request(t, client, "GET", "/api/runs", nil, nil), http.StatusUnauthorized)
	target := "/api/auth/passkeys/login/finish?challenge_id=" + begin.ChallengeID
	for _, headers := range []map[string]string{{"X-CSRF-Token": ""}, {"X-CSRF-Token": "wrong"}, {"Origin": "https://untrusted.invalid"}} {
		expectHTTPStatus(t, f.request(t, client, "POST", target, map[string]any{}, headers), http.StatusForbidden)
	}
	expectHTTPStatus(t, f.request(t, client, "POST", target, map[string]any{}, nil), http.StatusUnauthorized)
	expectHTTPStatus(t, f.request(t, client, "POST", "/api/auth/passkeys/login/begin", map[string]any{"role": "admin"}, nil), http.StatusUnprocessableEntity)
	// A refresh or cancelled picker does not leave an anonymous cookie blocking
	// another login attempt. It still cannot authorize registration or setup.
	expectHTTPStatus(t, f.request(t, client, "POST", "/api/auth/passkeys/register/begin", map[string]any{}, nil), http.StatusUnauthorized)
	expectHTTPStatus(t, f.request(t, client, "POST", "/api/auth/passkeys/login/begin", map[string]any{}, map[string]string{"X-CSRF-Token": ""}), http.StatusOK)
	if client.cookie.HttpOnly != true || client.cookie.SameSite != http.SameSiteStrictMode {
		t.Fatal("selection cookie lost existing security attributes")
	}
}
