package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"
	"time"
)

func TestTC37_NewPasskeysRequireDiscoverableCredentials(t *testing.T) {
	f := securityTest(t)
	g := f.password(t, "fixture-0")
	b, err := f.svc.BeginPasskey(context.Background(), g.Token, "fixture-0", true)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(b.Options)
	var options struct {
		PublicKey struct {
			AuthenticatorSelection struct {
				ResidentKey      string `json:"residentKey"`
				RequireResident  bool   `json:"requireResidentKey"`
				UserVerification string `json:"userVerification"`
			} `json:"authenticatorSelection"`
		} `json:"publicKey"`
	}
	if err := json.Unmarshal(raw, &options); err != nil {
		t.Fatal(err)
	}
	selection := options.PublicKey.AuthenticatorSelection
	if selection.ResidentKey != "required" || !selection.RequireResident || selection.UserVerification != "required" {
		t.Fatal("new credentials must be discoverable and require device user verification")
	}
}

func TestTC37_PasskeySelectionLogsInChosenCredentialOwner(t *testing.T) {
	f := securityTest(t, "finance", "warehouse", "admin")
	ctx := context.Background()
	devices := []*virtualAuthenticator{}
	for i := range 3 {
		g, v := f.passkey(t, fmt.Sprintf("fixture-%d", i))
		devices = append(devices, v)
		if err := f.svc.Logout(ctx, g.Token); err != nil {
			t.Fatal(err)
		}
	}
	for i, role := range []string{"finance", "warehouse", "admin"} {
		b, err := f.svc.BeginPasskey(ctx, "", "", false)
		if err != nil {
			t.Fatal(err)
		}
		raw, _ := json.Marshal(b.Options)
		var options struct {
			PublicKey map[string]json.RawMessage `json:"publicKey"`
		}
		if err = json.Unmarshal(raw, &options); err != nil {
			t.Fatal(err)
		}
		if _, leaked := options.PublicKey["allowCredentials"]; leaked {
			t.Fatal("selection must not restrict or disclose account credential IDs")
		}
		if _, err = f.svc.Current(ctx, b.Token, false); err == nil {
			t.Fatal("anonymous selection cookie became an account session")
		}
		id := fmt.Sprintf("fixture-%d", i)
		g, err := f.svc.FinishPasskey(ctx, b.Token, b.ChallengeID, devices[i].response(t, passkeyChallenge(t, b), f.svc.Origin, "localhost", id, false, 5, false), false, "chosen-owner")
		if err != nil || g.Session.ID != id || g.Session.Role != role || g.Session.Scope != "business" || g.Token == b.Token {
			t.Fatalf("selected credential did not determine the account/role: %+v %v", g.Session, err)
		}
		if _, err = f.svc.Current(ctx, g.Token, true); err != nil {
			t.Fatal(err)
		}
		requireNoBusiness(t, f.svc, b.Token)
	}
}

func TestTC38_PasskeySelectionRejectsInvalidProofAndRevocation(t *testing.T) {
	for _, name := range []string{"wrong-challenge", "wrong-origin", "wrong-rp", "no-up", "no-uv", "bad-signature", "missing-handle", "other-handle", "unknown-credential", "wrong-cookie", "expired", "wrong-purpose", "revoked-version", "deleted-credential", "logout", "superseded"} {
		t.Run(name, func(t *testing.T) {
			f := securityTest(t)
			ctx := context.Background()
			g, device := f.passkey(t, "fixture-0")
			if err := f.svc.Logout(ctx, g.Token); err != nil {
				t.Fatal(err)
			}
			b, err := f.svc.BeginPasskey(ctx, "", "", false)
			if err != nil {
				t.Fatal(err)
			}
			challenge, origin, rp, handle, token := passkeyChallenge(t, b), f.svc.Origin, "localhost", "fixture-0", b.Token
			flags := byte(5)
			switch name {
			case "wrong-challenge":
				challenge = "wrong-challenge"
			case "wrong-origin":
				origin = "https://untrusted.invalid"
			case "wrong-rp":
				rp = "untrusted.invalid"
			case "no-up":
				flags = 4
			case "no-uv":
				flags = 1
			case "missing-handle":
				handle = ""
			case "other-handle":
				handle = "fixture-other"
			case "unknown-credential":
				device = newVirtualAuthenticator(t)
			case "wrong-cookie":
				token += "wrong"
			case "expired":
				f.clock.add(121 * time.Second)
			case "revoked-version":
				if _, err = f.db.Exec("UPDATE auth_users SET version=version+1"); err != nil {
					t.Fatal(err)
				}
			case "deleted-credential":
				if _, err = f.db.Exec("DELETE FROM auth_credentials"); err != nil {
					t.Fatal(err)
				}
			case "logout":
				if err = f.svc.Logout(ctx, b.Token); err != nil {
					t.Fatal(err)
				}
			case "superseded":
				if _, err = f.svc.BeginPasskey(ctx, b.Token, "", false); err != nil {
					t.Fatal(err)
				}
			}
			_, err = f.svc.FinishPasskey(ctx, token, b.ChallengeID, device.response(t, challenge, origin, rp, handle, false, flags, name == "bad-signature"), name == "wrong-purpose", name)
			requireDenied(t, err)
			requireNoBusiness(t, f.svc, b.Token)
			var business int
			if err := f.db.QueryRow("SELECT count(*) FROM auth_sessions WHERE scope='business'").Scan(&business); err != nil || business != 0 {
				t.Fatal("rejected proof issued a business session")
			}
		})
	}
}

func TestTC38_PasskeySelectionConcurrentConsumptionAndReplay(t *testing.T) {
	f := securityTest(t)
	ctx := context.Background()
	g, device := f.passkey(t, "fixture-0")
	if err := f.svc.Logout(ctx, g.Token); err != nil {
		t.Fatal(err)
	}
	b, err := f.svc.BeginPasskey(ctx, "", "", false)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	results := make(chan error, 2)
	start := make(chan struct{})
	for range 2 {
		wg.Go(func() {
			request := device.response(t, passkeyChallenge(t, b), f.svc.Origin, "localhost", "fixture-0", false, 5, false)
			<-start
			_, e := f.svc.FinishPasskey(ctx, b.Token, b.ChallengeID, request, false, "concurrent-selection")
			results <- e
		})
	}
	close(start)
	wg.Wait()
	close(results)
	succeeded := 0
	for err := range results {
		if err == nil {
			succeeded++
		} else {
			requireDenied(t, err)
		}
	}
	if succeeded != 1 {
		t.Fatalf("one challenge succeeded %d times", succeeded)
	}
	_, err = f.svc.FinishPasskey(ctx, b.Token, b.ChallengeID, device.response(t, passkeyChallenge(t, b), f.svc.Origin, "localhost", "fixture-0", false, 5, false), false, "replay-selection")
	requireDenied(t, err)
}

func TestTC36_PasskeySelectionCannotSkipIncompleteSetup(t *testing.T) {
	f := securityTest(t)
	ctx := context.Background()
	g := f.password(t, "fixture-0")
	b, err := f.svc.BeginPasskey(ctx, g.Token, "fixture-0", true)
	if err != nil {
		t.Fatal(err)
	}
	device := newVirtualAuthenticator(t)
	if _, err = f.svc.FinishPasskey(ctx, b.Token, b.ChallengeID, device.response(t, passkeyChallenge(t, b), f.svc.Origin, "localhost", "fixture-0", true, 0x45, false), true, "incomplete-registration"); err != nil {
		t.Fatal(err)
	}
	b, err = f.svc.BeginPasskey(ctx, "", "", false)
	if err != nil {
		t.Fatal(err)
	}
	g, err = f.svc.FinishPasskey(ctx, b.Token, b.ChallengeID, device.response(t, passkeyChallenge(t, b), f.svc.Origin, "localhost", "fixture-0", false, 5, false), false, "incomplete-selection")
	if err != nil || g.Session.Scope != "setup" || g.Session.State != "factor" {
		t.Fatalf("incomplete selection must only resume setup: %+v %v", g.Session, err)
	}
	requireNoBusiness(t, f.svc, g.Token)
}

func TestTC44_PasskeySelectionRetainsAccountAndSourceRateLimits(t *testing.T) {
	for _, mode := range []string{"account", "source"} {
		t.Run(mode, func(t *testing.T) {
			f := securityTest(t)
			ctx := context.Background()
			_, device := f.passkey(t, "fixture-0")
			invalidDevice := device
			if mode == "source" {
				invalidDevice = newVirtualAuthenticator(t)
			}
			for i := range 6 {
				b, err := f.svc.BeginPasskey(ctx, "", "", false)
				if err != nil {
					t.Fatal(err)
				}
				source := "rotating-selection-source"
				if mode == "account" {
					source = fmt.Sprintf("independent-source-%d", i)
				}
				_, err = f.svc.FinishPasskey(ctx, b.Token, b.ChallengeID, invalidDevice.response(t, passkeyChallenge(t, b), f.svc.Origin, "localhost", "fixture-0", false, 5, true), false, source)
				if i < 5 {
					requireDenied(t, err)
				} else {
					requireStatus(t, err, 429)
				}
			}
			f.clock.add(301 * time.Second)
			b, err := f.svc.BeginPasskey(ctx, "", "", false)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = f.svc.FinishPasskey(ctx, b.Token, b.ChallengeID, device.response(t, passkeyChallenge(t, b), f.svc.Origin, "localhost", "fixture-0", false, 5, false), false, "rotating-selection-source"); err != nil {
				t.Fatal("expired rate limit did not allow verified login", err)
			}
		})
	}
}

func TestTC38_PasskeySelectionAuditFailureRollsBackAndAllowsRetry(t *testing.T) {
	f := securityTest(t)
	ctx := context.Background()
	_, device := f.passkey(t, "fixture-0")
	b, err := f.svc.BeginPasskey(ctx, "", "", false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.db.Exec(`CREATE TRIGGER selection_audit_failure BEFORE INSERT ON auth_audit WHEN NEW.action='login' BEGIN SELECT RAISE(ABORT,'isolated audit failure'); END`); err != nil {
		t.Fatal(err)
	}
	var beforeCredential string
	var beforeSessions int
	if err = f.db.QueryRow("SELECT credential_json FROM auth_credentials").Scan(&beforeCredential); err != nil {
		t.Fatal(err)
	}
	if err = f.db.QueryRow("SELECT count(*) FROM auth_sessions").Scan(&beforeSessions); err != nil {
		t.Fatal(err)
	}
	_, err = f.svc.FinishPasskey(ctx, b.Token, b.ChallengeID, device.response(t, passkeyChallenge(t, b), f.svc.Origin, "localhost", "fixture-0", false, 5, false), false, "audit-fault")
	if err == nil {
		t.Fatal("audit fault allowed a grant")
	}
	var challenges int
	if err = f.db.QueryRow("SELECT count(*) FROM auth_challenges WHERE id=?", b.ChallengeID).Scan(&challenges); err != nil || challenges != 1 {
		t.Fatal("failed transaction consumed the challenge")
	}
	var afterCredential string
	var afterSessions int
	if err = f.db.QueryRow("SELECT credential_json FROM auth_credentials").Scan(&afterCredential); err != nil || afterCredential != beforeCredential {
		t.Fatal("failed transaction changed the stored credential")
	}
	if err = f.db.QueryRow("SELECT count(*) FROM auth_sessions").Scan(&afterSessions); err != nil || afterSessions != beforeSessions {
		t.Fatal("failed transaction issued a session")
	}
	if _, err = f.db.Exec("DROP TRIGGER selection_audit_failure"); err != nil {
		t.Fatal(err)
	}
	if _, err = f.svc.FinishPasskey(ctx, b.Token, b.ChallengeID, device.response(t, passkeyChallenge(t, b), f.svc.Origin, "localhost", "fixture-0", false, 5, false), false, "audit-retry"); err != nil {
		t.Fatal("rolled back challenge could not be retried", err)
	}
}
