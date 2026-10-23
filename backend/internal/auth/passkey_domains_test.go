package auth

import (
	"context"
	"path/filepath"
	"testing"
)

func TestTC67_PasskeyOriginValidation(t *testing.T) {
	good := map[string]string{"Recon.Example.com": "https://recon.example.com", "https://recon.example.com:443/": "https://recon.example.com", "http://localhost:18089": "http://localhost:18089", "https://recon.example.com:8443": "https://recon.example.com:8443", "https://仓库.example.com": "https://xn--5mqv79a.example.com", "http://recon.example.com": "http://recon.example.com", "https://127.0.0.1": "https://127.0.0.1", "http://192.0.2.12:8080/": "http://192.0.2.12:8080", "https://[::1]": "https://[::1]", "http://[2001:db8::1]:80": "http://[2001:db8::1]"}
	for raw, want := range good {
		got, e := NormalizePasskeyOrigin(raw)
		if e != nil || got != want {
			t.Errorf("%q got %q %v want %q", raw, got, e, want)
		}
	}
	for _, raw := range []string{"ftp://192.0.2.12", "http://[::gg]", "http://127.0.0.999", "http://[::1]:0", "https://*.example.com", "https://com", "https://co.uk", "https://example.com/path", "https://a@example.com", "https://example.com?x=1", "https://example.com#x", "https://example.com:0", "https://example.com:65536", "https://example.com:", "https://example.com.", "https://bad_domain.example", "https://-bad.example.com", "javascript:alert(1)", "https://example.com\\@evil.example"} {
		if _, e := NormalizePasskeyOrigin(raw); e == nil {
			t.Errorf("accepted invalid %q", raw)
		}
	}
}
func TestTC67_PasskeyDomainSettingsAtomicPersistentAndAdminOnly(t *testing.T) {
	f := securityTest(t, "admin", "finance")
	ctx := context.Background()
	admin, _, _ := f.totp(t, "fixture-0")
	finance, _, _ := f.totp(t, "fixture-1")
	if _, e := f.svc.PasskeyDomains(ctx, finance.Token); e == nil {
		t.Fatal("finance read settings")
	}
	if _, e := f.svc.UpdatePasskeyDomains(ctx, finance.Token, PasskeyDomains{Version: 1}); e == nil {
		t.Fatal("finance changed settings")
	}
	b, e := f.svc.BeginPasskey(ctx, "", "", false)
	if e != nil {
		t.Fatal(e)
	}
	p, e := f.svc.UpdatePasskeyDomains(ctx, admin.Token, PasskeyDomains{Version: 1, Origins: []string{"recon.example.com", "https://recon.example.com/", f.svc.Origin}})
	if e != nil || p.Version != 2 || len(p.Origins) != 1 {
		t.Fatal(p, e)
	}
	if countSecurity(t, f.db, `SELECT count(*) FROM auth_challenges WHERE id=?`, b.ChallengeID) != 0 {
		t.Fatal("stale challenge retained")
	}
	if _, e = f.svc.UpdatePasskeyDomains(ctx, admin.Token, PasskeyDomains{Version: 1}); e == nil {
		t.Fatal("stale version accepted")
	}
	restarted, e := New(f.db, Config{SecretDir: filepath.Join(f.dir, "secrets"), Origin: f.svc.Origin, Now: f.clock.now})
	if e != nil {
		t.Fatal(e)
	}
	p, e = restarted.PasskeyDomains(ctx, admin.Token)
	if e != nil || p.Version != 2 || len(p.Origins) != 1 {
		t.Fatal("restart lost config", p, e)
	}
	remote := WithPasskeyOrigin(ctx, "https://recon.example.com")
	if _, e = f.svc.UpdatePasskeyDomains(remote, admin.Token, PasskeyDomains{Version: 2}); e == nil {
		t.Fatal("active origin removed")
	}
	b, e = f.svc.BeginPasskey(ctx, "", "", false)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = f.db.Exec(`CREATE TRIGGER fail_domain_audit BEFORE INSERT ON account_audit WHEN NEW.action='passkey_domains' BEGIN SELECT RAISE(ABORT,'isolated failure');END`); e != nil {
		t.Fatal(e)
	}
	if _, e = f.svc.UpdatePasskeyDomains(ctx, admin.Token, PasskeyDomains{Version: 2}); e == nil {
		t.Fatal("audit failure committed")
	}
	p, e = f.svc.PasskeyDomains(ctx, admin.Token)
	if e != nil || p.Version != 2 || len(p.Origins) != 1 || countSecurity(t, f.db, `SELECT count(*) FROM auth_challenges WHERE id=?`, b.ChallengeID) != 1 {
		t.Fatal("partial update", p, e)
	}
	if _, e = f.db.Exec(`DROP TRIGGER fail_domain_audit`); e != nil {
		t.Fatal(e)
	}
	start := make(chan struct{})
	results := make(chan error, 2)
	for _, origin := range []string{"https://one.example.com", "https://two.example.com"} {
		go func(origin string) {
			<-start
			_, e := f.svc.UpdatePasskeyDomains(ctx, admin.Token, PasskeyDomains{Version: 2, Origins: []string{origin}})
			results <- e
		}(origin)
	}
	close(start)
	success := 0
	for i := 0; i < 2; i++ {
		if e := <-results; e == nil {
			success++
		} else {
			requireStatus(t, e, 409)
		}
	}
	if success != 1 || countSecurity(t, f.db, `SELECT count(*) FROM account_audit WHERE action='passkey_domains'`) != 2 {
		t.Fatal("concurrent settings committed twice")
	}
}
func TestTC68_NewAndLegacyPasskeysCoexistAndStayInTheirDomains(t *testing.T) {
	f := securityTest(t, "admin", "finance")
	ctx := context.Background()
	admin, _, _ := f.totp(t, "fixture-0")
	g, old := f.passkey(t, "fixture-1")
	// Remove the new metadata to represent an actual pre-upgrade credential.
	if _, e := f.db.Exec(`UPDATE auth_credentials SET credential_json=json_remove(credential_json,'$.app_rp_id') WHERE user_id='fixture-1'`); e != nil {
		t.Fatal(e)
	}
	p, e := f.svc.UpdatePasskeyDomains(ctx, admin.Token, PasskeyDomains{Version: 1, Origins: []string{"https://recon.example.com", "https://recon.example.com:8443"}})
	if e != nil {
		t.Fatal(e)
	}
	remote := WithPasskeyOrigin(ctx, "https://recon.example.com")
	b, e := f.svc.BeginPasskey(remote, g.Token, "fixture-1", true)
	if e != nil {
		t.Fatal(e)
	}
	fresh := newVirtualAuthenticator(t)
	g, e = f.svc.FinishPasskey(remote, g.Token, b.ChallengeID, fresh.response(t, passkeyChallenge(t, b), "https://recon.example.com", "recon.example.com", "fixture-1", true, 0x45, false), true, "new-domain-register")
	if e != nil {
		t.Fatal(e)
	}
	g, e = f.svc.Complete(remote, g.Token, true)
	if e != nil {
		t.Fatal(e)
	}
	if n := countSecurity(t, f.db, `SELECT count(*) FROM auth_credentials WHERE user_id='fixture-1'`); n != 2 {
		t.Fatal("old key lost", n)
	}
	for _, tc := range []struct {
		ctx        context.Context
		origin, rp string
		device     *virtualAuthenticator
	}{{ctx, f.svc.Origin, "localhost", old}, {remote, "https://recon.example.com", "recon.example.com", fresh}} {
		for _, id := range []string{"fixture-1", ""} {
			b, e = f.svc.BeginPasskey(tc.ctx, "", id, false)
			if e != nil {
				t.Fatal(e)
			}
			g, e = f.svc.FinishPasskey(tc.ctx, b.Token, b.ChallengeID, tc.device.response(t, passkeyChallenge(t, b), tc.origin, tc.rp, "fixture-1", false, 5, false), false, "domain-login")
			if e != nil || g.Session.Scope != "business" {
				t.Fatal("compatible login failed", id, e)
			}
		}
	}
	// Even a valid synthetic signature for the new RP cannot use the old key.
	b, e = f.svc.BeginPasskey(remote, "", "", false)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = f.svc.FinishPasskey(remote, b.Token, b.ChallengeID, old.response(t, passkeyChallenge(t, b), "https://recon.example.com", "recon.example.com", "fixture-1", false, 5, false), false, "cross-rp"); e == nil {
		t.Fatal("cross-RP key accepted")
	}
	// Challenges cannot be moved to another approved port on the same hostname.
	b, e = f.svc.BeginPasskey(remote, "", "", false)
	if e != nil {
		t.Fatal(e)
	}
	otherPort := WithPasskeyOrigin(ctx, "https://recon.example.com:8443")
	if _, e = f.svc.FinishPasskey(otherPort, b.Token, b.ChallengeID, fresh.response(t, passkeyChallenge(t, b), "https://recon.example.com:8443", "recon.example.com", "fixture-1", false, 5, false), false, "cross-origin"); e == nil {
		t.Fatal("cross-origin challenge accepted")
	}
	// Removing and restoring a domain preserves both keys and the localhost path.
	p, e = f.svc.UpdatePasskeyDomains(ctx, admin.Token, PasskeyDomains{Version: p.Version})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = f.svc.BeginPasskey(remote, "", "", false); e == nil {
		t.Fatal("removed origin accepted")
	}
	p, e = f.svc.UpdatePasskeyDomains(ctx, admin.Token, PasskeyDomains{Version: p.Version, Origins: []string{"https://recon.example.com"}})
	if e != nil {
		t.Fatal(e)
	}
	b, e = f.svc.BeginPasskey(remote, "", "", false)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = f.svc.FinishPasskey(remote, b.Token, b.ChallengeID, fresh.response(t, passkeyChallenge(t, b), "https://recon.example.com", "recon.example.com", "fixture-1", false, 5, false), false, "restored-origin"); e != nil {
		t.Fatal(e)
	}
}
