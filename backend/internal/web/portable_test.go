package web

import (
	"encoding/json"
	"net/http"
	"testing"

	"returnsrecon/internal/auth"
)

func TestPortableInstanceAndCookieIsolation(t *testing.T) {
	f := httpSecurityTest(t)
	f.server.CookieName = "recon_session_18080"
	f.server.RuntimeID = "isolated-teacher-instance"
	f.handler = f.server.Handler()
	health := f.request(t, nil, "GET", "/api/health", nil, nil)
	expectHTTPStatus(t, health, 200)
	var status map[string]string
	if err := json.Unmarshal(health.Body.Bytes(), &status); err != nil || status["application"] != "returns-recon" || status["runtime_id"] != f.server.RuntimeID {
		t.Fatal("instance identity missing")
	}
	w := f.request(t, nil, "POST", "/api/auth/bootstrap", map[string]any{"token": f.bootstrap["http-finance"]}, nil)
	expectHTTPStatus(t, w, 200)
	var grant auth.Grant
	decodeHTTPResponse(t, w, &grant)
	var isolated *http.Cookie
	for _, c := range w.Result().Cookies() {
		if c.Name == f.server.CookieName {
			isolated = c
		}
		if c.Name == cookieName {
			t.Fatal("portable login overwrote the default application cookie")
		}
	}
	if isolated == nil || !isolated.HttpOnly {
		t.Fatal("isolated cookie missing or unsafe")
	}
	expectHTTPStatus(t, f.request(t, &httpSecurityClient{cookie: isolated}, "GET", "/api/auth/me", nil, nil), 200)
	wrong := *isolated
	wrong.Name = cookieName
	expectHTTPStatus(t, f.request(t, &httpSecurityClient{cookie: &wrong}, "GET", "/api/auth/me", nil, nil), 401)
}
