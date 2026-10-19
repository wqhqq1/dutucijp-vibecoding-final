package web

import (
	"returnsrecon/internal/auth"
	"strings"
	"testing"
)

func TestHTTP_TC69_PasskeyDomainsEnforceRolesSameOriginAndSecureCookies(t *testing.T) {
	f := httpSecurityTest(t)
	admin := f.configured(t, "http-admin")
	finance := f.configured(t, "http-finance")
	owner := f.configured(t, "http-owner")
	path := "/api/admin/passkey-domains"
	for _, client := range []*httpSecurityClient{finance, owner} {
		expectHTTPStatus(t, f.request(t, client, "GET", path, nil, nil), 403)
		expectHTTPStatus(t, f.request(t, client, "PUT", path, map[string]any{"version": 1, "origins": []string{}}, nil), 403)
	}
	expectHTTPStatus(t, f.request(t, nil, "GET", path, nil, nil), 401)
	body := map[string]any{"version": 1, "origins": []string{"https://recon.example.com", "https://recon.example.com:8443"}}
	expectHTTPStatus(t, f.request(t, admin, "PUT", path, body, map[string]string{"X-CSRF-Token": ""}), 403)
	expectHTTPStatus(t, f.request(t, admin, "PUT", path, body, map[string]string{"Origin": "https://unapproved.example.com"}), 403)
	expectHTTPStatus(t, f.request(t, admin, "GET", path, nil, map[string]string{"Host": "recon.example.com"}), 403)
	expectHTTPStatus(t, f.request(t, admin, "PUT", path, body, nil), 200)
	expectHTTPStatus(t, f.request(t, admin, "PUT", path, body, nil), 409)
	headers := map[string]string{"Host": "recon.example.com", "Origin": "https://recon.example.com"}
	expectHTTPStatus(t, f.request(t, admin, "GET", path, nil, headers), 200)
	// Cross-entrance writes remain forbidden even when both origins are approved.
	expectHTTPStatus(t, f.request(t, admin, "PUT", path, map[string]any{"version": 2, "origins": []string{}}, map[string]string{"Host": "localhost:8080", "Origin": "https://recon.example.com"}), 403)
	expectHTTPStatus(t, f.request(t, admin, "PUT", path, map[string]any{"version": 2, "origins": []string{}}, headers), 422)
	expectHTTPStatus(t, f.request(t, admin, "POST", "/api/auth/passkeys/register/begin", map[string]any{}, map[string]string{"Host": "recon.example.com", "Origin": "http://recon.example.com"}), 403)
	w := f.request(t, admin, "POST", "/api/auth/passkeys/register/begin", map[string]any{}, headers)
	expectHTTPStatus(t, w, 200)
	if !strings.Contains(w.Body.String(), `"id":"recon.example.com"`) {
		t.Fatal("wrong registration RP", w.Body.String())
	}
	if cookies := w.Result().Cookies(); len(cookies) != 1 || !cookies[0].Secure || !cookies[0].HttpOnly || cookies[0].Domain != "" {
		t.Fatal("HTTPS cookie is not Secure host-only", cookies)
	}
	expectHTTPStatus(t, f.request(t, admin, "PUT", path, map[string]any{"version": 2, "origins": []string{}}, nil), 200)
	expectHTTPStatus(t, f.request(t, admin, "GET", path, nil, headers), 403)
	expectHTTPStatus(t, f.request(t, admin, "GET", path, nil, nil), 200)
}
func TestHTTP_TC69_DeletionSignalsOnlyCurrentDomainCredentials(t *testing.T) {
	f := httpSecurityTest(t)
	admin := f.configured(t, "http-admin")
	f.configured(t, "http-owner")
	accountSQL(t, f, `INSERT INTO auth_credentials VALUES('old-local-id','http-owner','{}'),('new-site-id','http-owner','{"app_rp_id":"recon.example.com"}')`)
	w := f.request(t, admin, "DELETE", "/api/admin/accounts/http-owner", map[string]any{}, nil)
	expectHTTPStatus(t, w, 200)
	var v auth.AccountDeletion
	decodeHTTPResponse(t, w, &v)
	if v.PasskeyCount != 2 || len(v.RevokedCredentialIDs) != 1 || v.RevokedCredentialIDs[0] != "old-local-id" {
		t.Fatal("wrong notification targets", v)
	}
}
