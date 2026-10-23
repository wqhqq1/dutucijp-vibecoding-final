package auth

// FR-21 uses only temporary databases and real, synthetic WebAuthn signatures.
import (
	"context"
	"net/http"
	"path/filepath"
	"testing"
	"time"
)

func nativeTestContext(t *testing.T, s *Service, origin, binding string) context.Context {
	t.Helper()
	ctx, e := s.WithNativePasskey(WithPasskeyOrigin(context.Background(), origin), binding)
	if e != nil {
		t.Fatal("native binding:", e)
	}
	return ctx
}

func TestFR21_NativeIdentityPersistsAndIsUniqueToDatabase(t *testing.T) {
	f := securityTest(t)
	cfg, e := f.svc.ClientConfiguration(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	if cfg.APIVersion == "" || cfg.BackendID == "" || cfg.NativePasskey.ID != "default" || cfg.NativePasskey.RPID != "localhost" || cfg.NativePasskey.Origin != f.svc.Origin {
		t.Fatal("invalid default native identity", cfg)
	}
	restarted, e := New(f.db, Config{SecretDir: filepath.Join(f.dir, "secrets"), Origin: "http://127.0.0.1:19099", Now: f.clock.now})
	if e != nil {
		t.Fatal(e)
	}
	again, e := restarted.ClientConfiguration(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	if again.BackendID != cfg.BackendID || again.NativePasskey != cfg.NativePasskey {
		t.Fatal("changing launch URL changed persistent native identity")
	}
	other := securityTest(t)
	second, e := other.svc.ClientConfiguration(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	if second.BackendID == cfg.BackendID {
		t.Fatal("different databases share backend identity")
	}
	if _, e = f.svc.WithNativePasskey(context.Background(), "unknown-binding"); e == nil {
		t.Fatal("unknown binding accepted")
	}
	if _, e = f.svc.WithNativePasskey(WithPasskeyOrigin(context.Background(), "http://192.0.2.99:8080"), ""); e == nil {
		t.Fatal("unapproved transport accepted")
	}
}

func TestFR21_NativeCredentialUsesFreshChallengesAcrossApprovedEntrances(t *testing.T) {
	f := securityTest(t, "admin", "finance")
	admin, _, _ := f.totp(t, "fixture-0")
	origins := []string{f.svc.Origin, "http://127.0.0.1:18089", "http://[::1]:18089", "http://recon.example.com:18089", "https://recon.example.com"}
	if _, e := f.svc.UpdatePasskeyDomains(context.Background(), admin.Token, PasskeyDomains{Version: 1, Origins: origins[1:]}); e != nil {
		t.Fatal(e)
	}
	setup := f.password(t, "fixture-1")
	ctx := nativeTestContext(t, f.svc, origins[1], "")
	b, e := f.svc.BeginPasskey(ctx, setup.Token, "fixture-1", true)
	if e != nil {
		t.Fatal(e)
	}
	v := newVirtualAuthenticator(t)
	g, e := f.svc.FinishPasskey(ctx, setup.Token, b.ChallengeID, v.response(t, passkeyChallenge(t, b), f.svc.Origin, "localhost", "fixture-1", true, 0x45, false), true, "native-register")
	if e != nil {
		t.Fatal(e)
	}
	requireNoBusiness(t, f.svc, g.Token)
	g, e = f.svc.Complete(ctx, g.Token, true)
	if e != nil {
		t.Fatal(e)
	}
	for _, origin := range origins {
		for _, id := range []string{"fixture-1", ""} {
			ctx = nativeTestContext(t, f.svc, origin, "default")
			b, e = f.svc.BeginPasskey(ctx, "", id, false)
			if e != nil {
				t.Fatal(origin, id, e)
			}
			g, e = f.svc.FinishPasskey(ctx, b.Token, b.ChallengeID, v.response(t, passkeyChallenge(t, b), f.svc.Origin, "localhost", "fixture-1", false, 5, false), false, "native-new-entrance")
			if e != nil || g.Session.Scope != "business" || g.Session.ID != "fixture-1" {
				t.Fatal("same credential failed at approved entrance", origin, id, e)
			}
		}
	}
	if n := countSecurity(t, f.db, `SELECT count(*) FROM auth_credentials WHERE user_id='fixture-1'`); n != 1 {
		t.Fatal("entrance switch duplicated credential", n)
	}
	// The old browser flow continues to accept this localhost credential.
	b, e = f.svc.BeginPasskey(context.Background(), "", "", false)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = f.svc.FinishPasskey(context.Background(), b.Token, b.ChallengeID, v.response(t, passkeyChallenge(t, b), f.svc.Origin, "localhost", "fixture-1", false, 5, false), false, "legacy-browser"); e != nil {
		t.Fatal(e)
	}
}

func TestFR21_NativeChallengesRejectMovedAndStaleProofs(t *testing.T) {
	for _, name := range []string{"cross-entrance", "native-to-browser", "browser-to-native", "cross-instance", "replay", "expired", "version-revoked", "no-UP", "no-UV", "bad-signature", "wrong-logical-origin", "wrong-RP", "removed-entrance"} {
		t.Run(name, func(t *testing.T) {
			f := securityTest(t, "admin", "finance")
			admin, _, _ := f.totp(t, "fixture-0")
			_, v := f.passkey(t, "fixture-1")
			remote := "http://127.0.0.1:18089"
			if _, e := f.svc.UpdatePasskeyDomains(context.Background(), admin.Token, PasskeyDomains{Version: 1, Origins: []string{remote}}); e != nil {
				t.Fatal(e)
			}
			native := nativeTestContext(t, f.svc, f.svc.Origin, "")
			start, finish := native, native
			if name == "browser-to-native" {
				start = context.Background()
			}
			b, e := f.svc.BeginPasskey(start, "", "", false)
			if e != nil {
				t.Fatal(e)
			}
			flags := byte(5)
			origin, rp := f.svc.Origin, "localhost"
			switch name {
			case "cross-entrance":
				finish = nativeTestContext(t, f.svc, remote, "")
			case "native-to-browser":
				finish = context.Background()
			case "cross-instance":
				other := securityTest(t)
				finish = nativeTestContext(t, other.svc, f.svc.Origin, "")
			case "expired":
				f.clock.add(121 * time.Second)
			case "version-revoked":
				execSecurity(t, f.db, `UPDATE auth_users SET version=version+1 WHERE id='fixture-1'`)
			case "no-UP":
				flags = 4
			case "no-UV":
				flags = 1
			case "wrong-logical-origin":
				origin = remote
			case "wrong-RP":
				rp = "127.0.0.1"
			case "removed-entrance":
				start = nativeTestContext(t, f.svc, remote, "")
				finish = start
				b, e = f.svc.BeginPasskey(start, "", "", false)
				if e != nil {
					t.Fatal(e)
				}
				if _, e = f.svc.UpdatePasskeyDomains(context.Background(), admin.Token, PasskeyDomains{Version: 2}); e != nil {
					t.Fatal(e)
				}
			}
			if name == "replay" {
				if _, e = f.svc.FinishPasskey(finish, b.Token, b.ChallengeID, v.response(t, passkeyChallenge(t, b), origin, rp, "fixture-1", false, flags, false), false, "native-first"); e != nil {
					t.Fatal(e)
				}
			}
			_, e = f.svc.FinishPasskey(finish, b.Token, b.ChallengeID, v.response(t, passkeyChallenge(t, b), origin, rp, "fixture-1", false, flags, name == "bad-signature"), false, "native-rejected-"+name)
			requireDenied(t, e)
		})
	}
}

func TestFR21_NativeLegacyDomainBindingUsesExistingCredential(t *testing.T) {
	f := securityTest(t, "admin", "finance")
	admin, _, _ := f.totp(t, "fixture-0")
	_, v := f.passkey(t, "fixture-1")
	remote := "https://recon.example.com"
	if _, e := f.svc.UpdatePasskeyDomains(context.Background(), admin.Token, PasskeyDomains{Version: 1, Origins: []string{remote, "http://127.0.0.1:18089"}}); e != nil {
		t.Fatal(e)
	}
	// Model an already registered domain key, retaining its actual public key.
	execSecurity(t, f.db, `UPDATE auth_credentials SET credential_json=json_set(credential_json,'$.app_rp_id','recon.example.com') WHERE user_id='fixture-1'`)
	cfg, e := f.svc.ClientConfiguration(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	if len(cfg.LegacyBindings) != 1 || cfg.LegacyBindings[0].RPID != "recon.example.com" || cfg.LegacyBindings[0].Origin != remote {
		t.Fatal("legacy binding missing or raw IP advertised", cfg.LegacyBindings)
	}
	ctx := nativeTestContext(t, f.svc, "http://127.0.0.1:18089", cfg.LegacyBindings[0].ID)
	b, e := f.svc.BeginPasskey(ctx, "", "", false)
	if e != nil {
		t.Fatal(e)
	}
	g, e := f.svc.FinishPasskey(ctx, b.Token, b.ChallengeID, v.response(t, passkeyChallenge(t, b), remote, "recon.example.com", "fixture-1", false, 5, false), false, "native-legacy-domain")
	if e != nil || g.Session.Scope != "business" {
		t.Fatal("legacy domain credential unavailable through native binding", e)
	}
}

func TestFR21_NativeRegistrationBoundToEntranceAndFlow(t *testing.T) {
	for _, name := range []string{"cross-entrance", "native-to-browser", "browser-to-native", "no-UP", "no-UV", "expired", "version-revoked"} {
		t.Run(name, func(t *testing.T) {
			f := securityTest(t, "admin", "finance")
			admin, _, _ := f.totp(t, "fixture-0")
			remote := "http://127.0.0.1:18089"
			if _, e := f.svc.UpdatePasskeyDomains(context.Background(), admin.Token, PasskeyDomains{Version: 1, Origins: []string{remote}}); e != nil {
				t.Fatal(e)
			}
			setup := f.password(t, "fixture-1")
			native := nativeTestContext(t, f.svc, f.svc.Origin, "")
			start, finish := native, native
			if name == "browser-to-native" {
				start = context.Background()
			}
			if name == "native-to-browser" {
				finish = context.Background()
			}
			if name == "cross-entrance" {
				finish = nativeTestContext(t, f.svc, remote, "")
			}
			b, e := f.svc.BeginPasskey(start, setup.Token, "fixture-1", true)
			if e != nil {
				t.Fatal(e)
			}
			flags := byte(0x45)
			switch name {
			case "no-UP":
				flags = 0x44
			case "no-UV":
				flags = 0x41
			case "expired":
				f.clock.add(121 * time.Second)
			case "version-revoked":
				execSecurity(t, f.db, `UPDATE auth_users SET version=version+1 WHERE id='fixture-1'`)
			}
			v := newVirtualAuthenticator(t)
			_, e = f.svc.FinishPasskey(finish, setup.Token, b.ChallengeID, v.response(t, passkeyChallenge(t, b), f.svc.Origin, "localhost", "fixture-1", true, flags, false), true, "native-register-reject")
			requireDenied(t, e)
			if countSecurity(t, f.db, `SELECT count(*) FROM auth_credentials WHERE user_id='fixture-1'`) != 0 {
				t.Fatal("rejected registration persisted credential")
			}
			requireNoBusiness(t, f.svc, setup.Token)
		})
	}
}

func TestFR21_NativeChallengeConcurrentReplayExactlyOnce(t *testing.T) {
	for _, id := range []string{"fixture-0", ""} {
		t.Run("account-"+id, func(t *testing.T) {
			f := securityTest(t)
			_, v := f.passkey(t, "fixture-0")
			ctx := nativeTestContext(t, f.svc, f.svc.Origin, "")
			b, e := f.svc.BeginPasskey(ctx, "", id, false)
			if e != nil {
				t.Fatal(e)
			}
			// Prepare independent request bodies before starting both consumers.
			requests := []*http.Request{v.response(t, passkeyChallenge(t, b), f.svc.Origin, "localhost", "fixture-0", false, 5, false), v.response(t, passkeyChallenge(t, b), f.svc.Origin, "localhost", "fixture-0", false, 5, false)}
			start := make(chan struct{})
			results := make(chan error, 2)
			for _, request := range requests {
				go func(request *http.Request) {
					<-start
					_, err := f.svc.FinishPasskey(ctx, b.Token, b.ChallengeID, request, false, "native-concurrent-replay")
					results <- err
				}(request)
			}
			close(start)
			success := 0
			for i := 0; i < 2; i++ {
				if <-results == nil {
					success++
				}
			}
			if success != 1 {
				t.Fatalf("concurrent challenge consumers succeeded %d times", success)
			}
			if countSecurity(t, f.db, `SELECT count(*) FROM auth_challenges WHERE id=?`, b.ChallengeID) != 0 {
				t.Fatal("consumed native challenge remains")
			}
		})
	}
}
