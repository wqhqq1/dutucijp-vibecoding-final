package web

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"net/url"
	"strings"
	"testing"

	"github.com/go-webauthn/webauthn/protocol/webauthncbor"
	"returnsrecon/internal/auth"
)

// This is an isolated authenticator; the HTTP routes still verify real CBOR,
// RP hashes, clientDataJSON, P-256 signatures, UP and UV through WebAuthn.
type nativeHTTPAuthenticator struct {
	key *ecdsa.PrivateKey
	id  []byte
}

func newNativeHTTPAuthenticator(t *testing.T) *nativeHTTPAuthenticator {
	t.Helper()
	key, e := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if e != nil {
		t.Fatal(e)
	}
	id := make([]byte, 32)
	if _, e = rand.Read(id); e != nil {
		t.Fatal(e)
	}
	return &nativeHTTPAuthenticator{key, id}
}
func nativeHTTPChallenge(t *testing.T, b auth.PasskeyBegin) string {
	t.Helper()
	raw, e := json.Marshal(b.Options)
	if e != nil {
		t.Fatal(e)
	}
	var v struct {
		PublicKey struct {
			Challenge string `json:"challenge"`
		} `json:"publicKey"`
	}
	if e = json.Unmarshal(raw, &v); e != nil {
		t.Fatal(e)
	}
	if v.PublicKey.Challenge == "" {
		t.Fatal("missing native challenge")
	}
	return v.PublicKey.Challenge
}
func (v *nativeHTTPAuthenticator) response(t *testing.T, b auth.PasskeyBegin, binding auth.NativeBinding, user string, register bool, flags byte) map[string]any {
	t.Helper()
	b64 := base64.RawURLEncoding.EncodeToString
	kind := "webauthn.get"
	if register {
		kind = "webauthn.create"
	}
	client, e := json.Marshal(map[string]any{"type": kind, "challenge": nativeHTTPChallenge(t, b), "origin": binding.Origin, "crossOrigin": false})
	if e != nil {
		t.Fatal(e)
	}
	rp := sha256.Sum256([]byte(binding.RPID))
	data := append([]byte(nil), rp[:]...)
	data = append(data, flags|0x18, 0, 0, 0, 0)
	response := map[string]any{"clientDataJSON": b64(client)}
	if register {
		cose, e := webauthncbor.Marshal(map[int]any{1: 2, 3: -7, -1: 1, -2: v.key.X.FillBytes(make([]byte, 32)), -3: v.key.Y.FillBytes(make([]byte, 32))})
		if e != nil {
			t.Fatal(e)
		}
		data = append(data, make([]byte, 16)...)
		size := make([]byte, 2)
		binary.BigEndian.PutUint16(size, uint16(len(v.id)))
		data = append(data, size...)
		data = append(data, v.id...)
		data = append(data, cose...)
		att, e := webauthncbor.Marshal(map[string]any{"fmt": "none", "authData": data, "attStmt": map[string]any{}})
		if e != nil {
			t.Fatal(e)
		}
		response["attestationObject"] = b64(att)
		response["transports"] = []string{"internal"}
	} else {
		hash := sha256.Sum256(client)
		signed := append(append([]byte(nil), data...), hash[:]...)
		digest := sha256.Sum256(signed)
		sig, e := ecdsa.SignASN1(rand.Reader, v.key, digest[:])
		if e != nil {
			t.Fatal(e)
		}
		response["authenticatorData"] = b64(data)
		response["signature"] = b64(sig)
		response["userHandle"] = b64([]byte(user))
	}
	return map[string]any{"id": b64(v.id), "rawId": b64(v.id), "type": "public-key", "response": response, "clientExtensionResults": map[string]any{}}
}
func nativeHTTPHeaders(origin string) map[string]string {
	u, _ := url.Parse(origin)
	return map[string]string{"Host": u.Host, "Origin": origin}
}

func TestHTTP_FR21_NativeRegistrationAndSameCredentialAcrossEntrances(t *testing.T) {
	f := httpSecurityTest(t)
	admin := f.configured(t, "http-admin")
	origins := []string{httpFixtureOrigin, "http://127.0.0.1:18089", "http://[::1]:18089", "http://recon.example.com:18089", "https://recon.example.com"}
	expectHTTPStatus(t, f.request(t, admin, "PUT", "/api/admin/passkey-domains", map[string]any{"version": 1, "origins": origins[1:]}, nil), 200)
	var cfg auth.ClientConfig
	w := f.request(t, nil, "GET", "/api/auth/client-config", nil, nil)
	expectHTTPStatus(t, w, 200)
	decodeHTTPResponse(t, w, &cfg)
	if cfg.BackendID == "" || cfg.NativePasskey.RPID != "localhost" || cfg.NativePasskey.Origin != httpFixtureOrigin || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("invalid public native configuration")
	}
	client := &httpSecurityClient{id: "http-finance"}
	expectHTTPStatus(t, f.request(t, client, "POST", "/api/auth/bootstrap", map[string]any{"token": f.bootstrap[client.id]}, nil), 200)
	expectHTTPStatus(t, f.request(t, client, "POST", "/api/auth/setup/password", map[string]any{"password": httpFixturePassword}, nil), 200)
	remote := nativeHTTPHeaders(origins[1])
	v := newNativeHTTPAuthenticator(t)
	w = f.request(t, client, "POST", "/api/auth/native-passkeys/register/begin", map[string]any{"id": client.id, "binding_id": "default"}, remote)
	expectHTTPStatus(t, w, 200)
	var b auth.PasskeyBegin
	decodeHTTPResponse(t, w, &b)
	if !strings.Contains(w.Body.String(), `"id":"localhost"`) {
		t.Fatal("native registration did not use stable RP")
	}
	target := "/api/auth/native-passkeys/register/finish?challenge_id=" + b.ChallengeID + "&binding_id=default"
	expectHTTPStatus(t, f.request(t, client, "POST", target, v.response(t, b, cfg.NativePasskey, client.id, true, 0x45), remote), 200)
	expectHTTPStatus(t, f.request(t, client, "GET", "/api/runs", nil, remote), 403)
	expectHTTPStatus(t, f.request(t, client, "POST", "/api/auth/setup/complete", map[string]any{"saved": true}, remote), 200)
	for _, origin := range origins {
		for _, id := range []string{"http-finance", ""} {
			headers := nativeHTTPHeaders(origin)
			login := &httpSecurityClient{}
			var current auth.ClientConfig
			w = f.request(t, nil, "GET", "/api/auth/client-config", nil, headers)
			expectHTTPStatus(t, w, 200)
			decodeHTTPResponse(t, w, &current)
			if current.BackendID != cfg.BackendID || current.NativePasskey != cfg.NativePasskey {
				t.Fatal("transport changed logical identity")
			}
			w = f.request(t, login, "POST", "/api/auth/native-passkeys/login/begin", map[string]any{"id": id, "binding_id": "default"}, headers)
			expectHTTPStatus(t, w, 200)
			decodeHTTPResponse(t, w, &b)
			if login.cookie == nil || !login.cookie.HttpOnly || login.cookie.Domain != "" || login.cookie.Secure != strings.HasPrefix(origin, "https:") {
				t.Fatal("invalid entrance cookie flags")
			}
			if id == "" && strings.Contains(w.Body.String(), "allowCredentials") {
				t.Fatal("selection leaked credential allowlist")
			}
			beforeLogin := 401
			if id != "" {
				beforeLogin = 403
			}
			expectHTTPStatus(t, f.request(t, login, "GET", "/api/runs", nil, headers), beforeLogin)
			target = "/api/auth/native-passkeys/login/finish?challenge_id=" + b.ChallengeID + "&binding_id=default"
			w = f.request(t, login, "POST", target, v.response(t, b, cfg.NativePasskey, "http-finance", false, 5), headers)
			expectHTTPStatus(t, w, 200)
			var grant auth.Grant
			decodeHTTPResponse(t, w, &grant)
			if grant.Session.ID != "http-finance" || grant.Session.Scope != "business" {
				t.Fatal("native credential resolved wrong account")
			}
			expectHTTPStatus(t, f.request(t, login, "GET", "/api/runs", nil, headers), 200)
		}
	}
	if n := httpSecurityCount(t, f.server.Store.DB, `SELECT count(*) FROM auth_credentials WHERE user_id='http-finance'`); n != 1 {
		t.Fatal("duplicated credential across entrances", n)
	}
}

func TestHTTP_FR21_NativeEndpointsEnforceOriginCSRFBindingAndFlow(t *testing.T) {
	f := httpSecurityTest(t)
	admin := f.configured(t, "http-admin")
	remote := "http://127.0.0.1:18089"
	expectHTTPStatus(t, f.request(t, admin, "PUT", "/api/admin/passkey-domains", map[string]any{"version": 1, "origins": []string{remote}}, nil), 200)
	var cfg auth.ClientConfig
	w := f.request(t, nil, "GET", "/api/auth/client-config", nil, nil)
	expectHTTPStatus(t, w, 200)
	decodeHTTPResponse(t, w, &cfg)
	v := newNativeHTTPAuthenticator(t)
	// Register via actual authenticated, CSRF-protected HTTP setup routes.
	setup := &httpSecurityClient{id: "http-finance"}
	expectHTTPStatus(t, f.request(t, setup, "POST", "/api/auth/bootstrap", map[string]any{"token": f.bootstrap[setup.id]}, nil), 200)
	expectHTTPStatus(t, f.request(t, setup, "POST", "/api/auth/setup/password", map[string]any{"password": httpFixturePassword}, nil), 200)
	w = f.request(t, setup, "POST", "/api/auth/native-passkeys/register/begin", map[string]any{}, nil)
	expectHTTPStatus(t, w, 200)
	var b auth.PasskeyBegin
	decodeHTTPResponse(t, w, &b)
	expectHTTPStatus(t, f.request(t, setup, "POST", "/api/auth/native-passkeys/register/finish?challenge_id="+b.ChallengeID, v.response(t, b, cfg.NativePasskey, setup.id, true, 0x45), nil), 200)
	expectHTTPStatus(t, f.request(t, setup, "POST", "/api/auth/setup/complete", map[string]any{"saved": true}, nil), 200)
	expectHTTPStatus(t, f.request(t, nil, "GET", "/api/auth/client-config", nil, map[string]string{"Host": "unapproved.example.com"}), 403)
	expectHTTPStatus(t, f.request(t, nil, "POST", "/api/auth/native-passkeys/register/begin", map[string]any{}, nil), 401)
	expectHTTPStatus(t, f.request(t, nil, "POST", "/api/auth/native-passkeys/login/begin", map[string]any{"binding_id": "unknown"}, nil), 422)
	expectHTTPStatus(t, f.request(t, nil, "POST", "/api/auth/native-passkeys/login/begin", map[string]any{"rp_id": "evil.example"}, nil), 422)
	expectHTTPStatus(t, f.request(t, nil, "POST", "/api/auth/native-passkeys/login/begin", map[string]any{}, map[string]string{"Origin": remote}), 403)
	for _, name := range []string{"missing-CSRF", "wrong-CSRF", "cross-entrance", "native-to-browser", "browser-to-native", "wrong-binding", "missing-UP", "missing-UV", "replay"} {
		t.Run(name, func(t *testing.T) {
			// These independent protocol cases share a disposable database; clear
			// only its failure counters so throttling cannot mask the next check.
			accountSQL(t, f, `DELETE FROM auth_limits`)
			client := &httpSecurityClient{}
			begin := "/api/auth/native-passkeys/login/begin"
			finish := "/api/auth/native-passkeys/login/finish"
			if name == "browser-to-native" {
				begin = "/api/auth/passkeys/login/begin"
			}
			if name == "native-to-browser" {
				finish = "/api/auth/passkeys/login/finish"
			}
			w := f.request(t, client, "POST", begin, map[string]any{}, nil)
			expectHTTPStatus(t, w, 200)
			var b auth.PasskeyBegin
			decodeHTTPResponse(t, w, &b)
			finish += "?challenge_id=" + b.ChallengeID
			headers := map[string]string{}
			status := 401
			flags := byte(5)
			switch name {
			case "missing-CSRF":
				headers["X-CSRF-Token"] = ""
				status = 403
			case "wrong-CSRF":
				headers["X-CSRF-Token"] = "wrong"
				status = 403
			case "cross-entrance":
				headers = nativeHTTPHeaders(remote)
			case "wrong-binding":
				finish += "&binding_id=unknown"
				status = 422
			case "missing-UP":
				flags = 4
			case "missing-UV":
				flags = 1
			case "replay":
				expectHTTPStatus(t, f.request(t, client, "POST", finish, v.response(t, b, cfg.NativePasskey, setup.id, false, 5), nil), 200)
			}
			expectHTTPStatus(t, f.request(t, client, "POST", finish, v.response(t, b, cfg.NativePasskey, setup.id, false, flags), headers), status)
		})
	}
}
