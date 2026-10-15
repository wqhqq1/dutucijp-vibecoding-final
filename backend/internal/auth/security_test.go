package auth

// These tests use isolated SQLite files and synthetic credentials only. They do
// not provision a real course account and never log any authentication secret.
import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-webauthn/webauthn/protocol/webauthncbor"
	"github.com/pquerna/otp/totp"
	_ "modernc.org/sqlite"
)

const isolatedPassword = "Violet river copper 72!"

type securityClock struct {
	mu sync.RWMutex
	at time.Time
}

func (c *securityClock) now() time.Time      { c.mu.RLock(); defer c.mu.RUnlock(); return c.at }
func (c *securityClock) add(d time.Duration) { c.mu.Lock(); c.at = c.at.Add(d); c.mu.Unlock() }

type securityFixture struct {
	svc       *Service
	db        *sql.DB
	clock     *securityClock
	dir       string
	bootstrap map[string]string
}

func securityTest(t *testing.T, roles ...string) *securityFixture {
	t.Helper()
	dir := t.TempDir()
	db, e := sql.Open("sqlite", filepath.ToSlash(filepath.Join(dir, "isolated.sqlite"))+"?_pragma=busy_timeout(10000)&_pragma=foreign_keys(1)")
	if e != nil {
		t.Fatal("open isolated database:", e)
	}
	db.SetMaxOpenConns(16)
	t.Cleanup(func() { db.Close() })
	c := &securityClock{at: time.Now().UTC()}
	s, e := New(db, Config{SecretDir: filepath.Join(dir, "secrets"), Origin: "http://localhost:8080", Now: c.now})
	if e != nil {
		t.Fatal("new isolated authentication service:", e)
	}
	if len(roles) == 0 {
		roles = []string{"finance"}
	}
	ids := make([]Identity, len(roles))
	for i, role := range roles {
		ids[i] = Identity{ID: fmt.Sprintf("fixture-%d", i), Role: role}
	}
	bootstrap, e := s.Initialize(context.Background(), ids)
	if e != nil {
		t.Fatal("initialize isolated identities:", e)
	}
	return &securityFixture{s, db, c, dir, bootstrap}
}
func (f *securityFixture) password(t *testing.T, id string) Grant {
	t.Helper()
	g, e := f.svc.Bootstrap(context.Background(), f.bootstrap[id])
	if e != nil {
		t.Fatal("bootstrap failed:", e)
	}
	g, e = f.svc.SetPassword(context.Background(), g.Token, isolatedPassword)
	if e != nil {
		t.Fatal("set test password failed:", e)
	}
	return g
}
func totpAt(t *testing.T, secret string, at time.Time) string {
	t.Helper()
	v, e := totp.GenerateCode(secret, at)
	if e != nil {
		t.Fatal("compute synthetic TOTP failed")
	}
	return v
}
func incorrectOTP(code string) string {
	if code[0] == '0' {
		return "1" + code[1:]
	}
	return "0" + code[1:]
}
func (f *securityFixture) totp(t *testing.T, id string) (Grant, Enrollment, []string) {
	t.Helper()
	g := f.password(t, id)
	en, e := f.svc.BeginTOTP(context.Background(), g.Token)
	if e != nil {
		t.Fatal("begin test TOTP failed:", e)
	}
	g, e = f.svc.ConfirmTOTP(context.Background(), g.Token, totpAt(t, en.Secret, f.clock.now()), "fixture")
	if e != nil {
		t.Fatal("confirm test TOTP failed:", e)
	}
	codes := append([]string(nil), g.RecoveryCodes...)
	g, e = f.svc.Complete(context.Background(), g.Token, true)
	if e != nil {
		t.Fatal("complete test configuration failed:", e)
	}
	return g, en, codes
}
func requireDenied(t *testing.T, e error) {
	t.Helper()
	if e == nil {
		t.Fatal("unauthorized action unexpectedly succeeded")
	}
}
func requireStatus(t *testing.T, e error, status int) {
	t.Helper()
	var ae *Error
	if !errors.As(e, &ae) || ae.Status != status {
		t.Fatalf("expected status %d, actual error=%v", status, e)
	}
}
func requireNoBusiness(t *testing.T, s *Service, token string) {
	t.Helper()
	_, e := s.Current(context.Background(), token, true)
	requireDenied(t, e)
}
func countSecurity(t *testing.T, db *sql.DB, q string, args ...any) int {
	t.Helper()
	var n int
	if e := db.QueryRow(q, args...).Scan(&n); e != nil {
		t.Fatal("query isolated state:", e)
	}
	return n
}
func execSecurity(t *testing.T, db *sql.DB, q string, args ...any) {
	t.Helper()
	if _, e := db.Exec(q, args...); e != nil {
		t.Fatal("inject isolated test condition:", e)
	}
}

func TestTC33_AllRolesRequirePasswordFactorAndRecovery(t *testing.T) {
	f := securityTest(t, "finance", "warehouse", "admin")
	ctx := context.Background()
	for i, role := range []string{"finance", "warehouse", "admin"} {
		t.Run(role, func(t *testing.T) {
			id := fmt.Sprintf("fixture-%d", i)
			g, e := f.svc.Bootstrap(ctx, f.bootstrap[id])
			if e != nil {
				t.Fatal(e)
			}
			requireNoBusiness(t, f.svc, g.Token)
			_, e = f.svc.BeginTOTP(ctx, g.Token)
			requireDenied(t, e)
			_, e = f.svc.Complete(ctx, g.Token, true)
			requireDenied(t, e)
			old := g.Token
			g, e = f.svc.SetPassword(ctx, g.Token, isolatedPassword)
			if e != nil {
				t.Fatal(e)
			}
			requireNoBusiness(t, f.svc, old)
			requireNoBusiness(t, f.svc, g.Token)
			en, e := f.svc.BeginTOTP(ctx, g.Token)
			if e != nil {
				t.Fatal(e)
			}
			requireNoBusiness(t, f.svc, g.Token)
			_, e = f.svc.Complete(ctx, g.Token, true)
			requireDenied(t, e)
			g, e = f.svc.ConfirmTOTP(ctx, g.Token, totpAt(t, en.Secret, f.clock.now()), "role-"+role)
			if e != nil {
				t.Fatal(e)
			}
			requireNoBusiness(t, f.svc, g.Token)
			_, e = f.svc.Complete(ctx, g.Token, false)
			requireDenied(t, e)
			old = g.Token
			g, e = f.svc.Complete(ctx, g.Token, true)
			if e != nil {
				t.Fatal(e)
			}
			v, e := f.svc.Current(ctx, g.Token, true)
			if e != nil || v.Role != role {
				t.Fatal("completed role not obtained from server state")
			}
			requireNoBusiness(t, f.svc, old)
		})
	}
	t.Log("actual: all three roles denied before password, factor verification and recovery confirmation; tokens rotated")
}

func TestTC34_PasswordBoundariesWeakListAndNFC(t *testing.T) {
	for _, tc := range []struct {
		name, p string
		ok      bool
	}{
		{"15", strings.Repeat("ab", 7) + "c", false}, {"16", strings.Repeat("ab", 8), true}, {"128", strings.Repeat("ab", 64), true}, {"129", strings.Repeat("ab", 64) + "c", false},
		{"weak", "password1234567890", false}, {"account", "fixture-0 long password 8!", false}, {"spaces", "  violet copper river  ", true}, {"unicode", strings.Repeat("é", 15) + "文", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, e := ValidatePassword("fixture-0", tc.p)
			if (e == nil) != tc.ok {
				t.Fatal("password policy boundary differs from specification")
			}
		})
	}
	decomposed := "e\u0301lan violet copper river 8"
	normalized, e := ValidatePassword("fixture-0", decomposed)
	if e != nil || normalized != "élan violet copper river 8" {
		t.Fatal("NFC normalization inconsistent")
	}
	f := securityTest(t)
	g, e := f.svc.Bootstrap(context.Background(), f.bootstrap["fixture-0"])
	if e != nil {
		t.Fatal(e)
	}
	g, e = f.svc.SetPassword(context.Background(), g.Token, decomposed)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = f.svc.LoginPassword(context.Background(), "fixture-0", normalized, "nfc"); e != nil {
		t.Fatal("normalized equivalent password did not authenticate")
	}
	t.Log("actual: 15/16/128/129 and weak/account/space/Unicode boundaries checked; NFC equivalent authenticated")
}

func TestTC35_PasswordHashesAreSaltedAndNoPlaintextAudit(t *testing.T) {
	f := securityTest(t, "finance", "admin")
	grant := f.password(t, "fixture-0")
	f.password(t, "fixture-1")
	raw, e := json.Marshal(grant)
	if e != nil || bytes.Contains(raw, []byte(grant.Token)) {
		t.Fatal("session token leaked through JSON serialization")
	}
	rows, e := f.db.Query("SELECT password_hash FROM auth_users ORDER BY id")
	if e != nil {
		t.Fatal(e)
	}
	var hashes []string
	for rows.Next() {
		var h string
		if e = rows.Scan(&h); e != nil {
			t.Fatal(e)
		}
		hashes = append(hashes, h)
	}
	rows.Close()
	if len(hashes) != 2 || hashes[0] == hashes[1] {
		t.Fatal("same password did not receive independent salt")
	}
	for _, h := range hashes {
		if !strings.HasPrefix(h, "argon2id$v=19$m=65536,t=3,p=1$") || strings.Contains(h, isolatedPassword) {
			t.Fatal("password storage is not salted Argon2id")
		}
	}
	if countSecurity(t, f.db, "SELECT count(*) FROM auth_audit WHERE actor LIKE ? OR method LIKE ? OR result LIKE ?", "%"+isolatedPassword+"%", "%"+isolatedPassword+"%", "%"+isolatedPassword+"%") != 0 {
		t.Fatal("plaintext in audit")
	}
	t.Log("actual: independent 16-byte salts, Argon2id m=64MiB/t=3/p=1; no plaintext in authentication audit; browser generator remains device acceptance")
}

func TestTC36_InterruptedSetupResumesAfterRestartWithoutBootstrapReuse(t *testing.T) {
	f := securityTest(t)
	g := f.password(t, "fixture-0")
	_, e := f.svc.BeginTOTP(context.Background(), g.Token)
	if e != nil {
		t.Fatal(e)
	}
	_, e = f.svc.Bootstrap(context.Background(), f.bootstrap["fixture-0"])
	requireDenied(t, e)
	f.clock.add(11 * time.Minute)
	requireNoBusiness(t, f.svc, g.Token)
	s, e := New(f.db, Config{SecretDir: filepath.Join(f.dir, "secrets"), Now: f.clock.now})
	if e != nil {
		t.Fatal(e)
	}
	g, e = s.LoginPassword(context.Background(), "fixture-0", isolatedPassword, "resume")
	if e != nil || g.Session.Scope != "setup" || g.Session.State != "password" {
		t.Fatal("password configuration did not survive restart")
	}
	requireNoBusiness(t, s, g.Token)
	_, e = s.Bootstrap(context.Background(), f.bootstrap["fixture-0"])
	requireDenied(t, e)
	en, e := s.BeginTOTP(context.Background(), g.Token)
	if e != nil {
		t.Fatal(e)
	}
	g, e = s.ConfirmTOTP(context.Background(), g.Token, totpAt(t, en.Secret, f.clock.now()), "interrupted-factor")
	if e != nil {
		t.Fatal(e)
	}
	f.clock.add(11 * time.Minute)
	passwordOnly, e := s.LoginPassword(context.Background(), "fixture-0", isolatedPassword, "interrupted-password")
	if e != nil {
		t.Fatal(e)
	}
	_, e = s.BeginTOTP(context.Background(), passwordOnly.Token)
	requireDenied(t, e)
	_, e = s.ReissueRecovery(context.Background(), passwordOnly.Token)
	requireDenied(t, e)
	verified, e := s.LoginTOTP(context.Background(), passwordOnly.Token, totpAt(t, en.Secret, f.clock.now()), "interrupted-reverify")
	if e != nil {
		t.Fatal(e)
	}
	requireNoBusiness(t, s, verified.Token)
	verified, e = s.ReissueRecovery(context.Background(), verified.Token)
	if e != nil || len(verified.RecoveryCodes) != 10 {
		t.Fatal("verified factor could not safely resume recovery-code saving")
	}
	if _, e = s.Complete(context.Background(), verified.Token, true); e != nil {
		t.Fatal(e)
	}
	t.Log("actual: setup interrupted/expired/restarted; password continuation remained restricted; old bootstrap rejected")
}

func TestTC40_TOTPOnlyVerifiedCurrentEnrollmentBecomesActive(t *testing.T) {
	f := securityTest(t)
	ctx := context.Background()
	g := f.password(t, "fixture-0")
	first, e := f.svc.BeginTOTP(ctx, g.Token)
	if e != nil {
		t.Fatal(e)
	}
	firstCode := totpAt(t, first.Secret, f.clock.now())
	var second Enrollment
	for {
		second, e = f.svc.BeginTOTP(ctx, g.Token)
		if e != nil {
			t.Fatal(e)
		}
		if totpAt(t, second.Secret, f.clock.now()) != firstCode {
			break
		}
	}
	_, e = f.svc.ConfirmTOTP(ctx, g.Token, firstCode, "enroll")
	requireDenied(t, e)
	correct := totpAt(t, second.Secret, f.clock.now())
	_, e = f.svc.ConfirmTOTP(ctx, g.Token, incorrectOTP(correct), "enroll")
	requireDenied(t, e)
	if countSecurity(t, f.db, "SELECT count(*) FROM auth_users WHERE totp_secret IS NOT NULL") != 0 {
		t.Fatal("unverified enrollment became active")
	}
	g, e = f.svc.ConfirmTOTP(ctx, g.Token, correct, "enroll")
	if e != nil {
		t.Fatal(e)
	}
	if len(g.RecoveryCodes) != 10 {
		t.Fatal("recovery group not generated")
	}
	requireNoBusiness(t, f.svc, g.Token)
	var cipher []byte
	if e = f.db.QueryRow("SELECT totp_secret FROM auth_users").Scan(&cipher); e != nil {
		t.Fatal(e)
	}
	if bytes.Contains(cipher, []byte(second.Secret)) {
		t.Fatal("TOTP stored in plaintext")
	}
	if countSecurity(t, f.db, "SELECT count(*) FROM auth_users WHERE pending_totp IS NOT NULL") != 0 {
		t.Fatal("pending enrollment retained after verification")
	}
	t.Log("actual: replaced pending enrollment and wrong OTP rejected; actual test OTP activated AES-GCM encrypted factor only")
}

func TestTC41_TOTPReplayWindowAndConcurrentConsumption(t *testing.T) {
	f := securityTest(t)
	ctx := context.Background()
	business, en, _ := f.totp(t, "fixture-0")
	_ = business
	login, e := f.svc.LoginPassword(ctx, "fixture-0", isolatedPassword, "replay")
	if e != nil {
		t.Fatal(e)
	}
	requireNoBusiness(t, f.svc, login.Token)
	_, e = f.svc.LoginTOTP(ctx, login.Token, totpAt(t, en.Secret, f.clock.now()), "replay")
	requireDenied(t, e)
	_, e = f.svc.LoginTOTP(ctx, login.Token, incorrectOTP(totpAt(t, en.Secret, f.clock.now())), "replay")
	requireDenied(t, e)
	_, e = f.svc.LoginTOTP(ctx, login.Token, totpAt(t, en.Secret, f.clock.now().Add(-2*time.Minute)), "replay")
	requireDenied(t, e)
	f.clock.add(30 * time.Second)
	code := totpAt(t, en.Secret, f.clock.now())
	var wg sync.WaitGroup
	out := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := f.svc.LoginTOTP(ctx, login.Token, code, "parallel-totp")
			out <- err
		}()
	}
	wg.Wait()
	close(out)
	success := 0
	for e := range out {
		if e == nil {
			success++
		}
	}
	if success != 1 {
		t.Fatalf("same-code concurrency success=%d, want 1", success)
	}
	f.clock.add(30 * time.Second)
	login, e = f.svc.LoginPassword(ctx, "fixture-0", isolatedPassword, "boundary")
	if e != nil {
		t.Fatal(e)
	}
	g, e := f.svc.LoginTOTP(ctx, login.Token, totpAt(t, en.Secret, f.clock.now().Add(30*time.Second)), "boundary")
	if e != nil {
		t.Fatal("allowed +1 window denied:", e)
	}
	if _, e = f.svc.Current(ctx, g.Token, true); e != nil {
		t.Fatal(e)
	}
	login, e = f.svc.LoginPassword(ctx, "fixture-0", isolatedPassword, "past")
	if e != nil {
		t.Fatal(e)
	}
	_, e = f.svc.LoginTOTP(ctx, login.Token, totpAt(t, en.Secret, f.clock.now()), "past")
	requireDenied(t, e)
	t.Log("actual: binding-code, wrong/expired/earlier steps rejected; concurrent same-code exactly one success; +1 boundary accepted once")
}

func TestTC42_PasswordCannotConfigureNewFactorForCompletedAccount(t *testing.T) {
	f := securityTest(t)
	_, _, _ = f.totp(t, "fixture-0")
	ctx := context.Background()
	g, e := f.svc.LoginPassword(ctx, "fixture-0", isolatedPassword, "fallback")
	if e != nil {
		t.Fatal(e)
	}
	requireNoBusiness(t, f.svc, g.Token)
	_, e = f.svc.BeginTOTP(ctx, g.Token)
	requireDenied(t, e)
	_, e = f.svc.Complete(ctx, g.Token, true)
	requireDenied(t, e)
	_, e = f.svc.BeginPasskey(ctx, g.Token, "fixture-0", true)
	requireDenied(t, e)
	t.Log("actual: password-only session cannot enter business, complete setup, enroll TOTP or enroll Passkey")
}

func TestTC43_RecoveryCodeAtomicAndRestrictedUntilReenrolled(t *testing.T) {
	f := securityTest(t)
	old, _, codes := f.totp(t, "fixture-0")
	ctx := context.Background()
	_, e := f.svc.Recover(ctx, "fixture-0", "wrong-password-here", codes[0], "recovery")
	requireDenied(t, e)
	_, e = f.svc.Recover(ctx, "fixture-0", isolatedPassword, "invalid-test-code", "recovery")
	requireDenied(t, e)
	var wg sync.WaitGroup
	out := make(chan Grant, 2)
	errs := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			g, err := f.svc.Recover(ctx, "fixture-0", isolatedPassword, codes[0], "recovery-parallel")
			out <- g
			errs <- err
		}()
	}
	wg.Wait()
	close(out)
	close(errs)
	success := 0
	for err := range errs {
		if err == nil {
			success++
		}
	}
	if success != 1 {
		t.Fatalf("same recovery code succeeded %d times", success)
	}
	var recovered Grant
	for g := range out {
		if g.Token != "" {
			recovered = g
		}
	}
	requireNoBusiness(t, f.svc, recovered.Token)
	requireNoBusiness(t, f.svc, old.Token)
	// A different client that knows only the password must not borrow the
	// recovery proof already supplied by this client's one-time recovery code.
	passwordOnly, passwordError := f.svc.LoginPassword(ctx, "fixture-0", isolatedPassword, "recovery-password-only")
	if passwordError == nil {
		requireNoBusiness(t, f.svc, passwordOnly.Token)
		_, e = f.svc.BeginTOTP(ctx, passwordOnly.Token)
		requireDenied(t, e)
		_, e = f.svc.BeginPasskey(ctx, passwordOnly.Token, "fixture-0", true)
		requireDenied(t, e)
		_, e = f.svc.Complete(ctx, passwordOnly.Token, true)
		requireDenied(t, e)
	}
	if countSecurity(t, f.db, "SELECT count(*) FROM auth_recovery") != 0 || countSecurity(t, f.db, "SELECT count(*) FROM auth_users WHERE totp_secret IS NOT NULL") != 0 || countSecurity(t, f.db, "SELECT count(*) FROM auth_credentials") != 0 {
		t.Fatal("recovery failed to revoke old codes or factors")
	}
	_, e = f.svc.Complete(ctx, recovered.Token, true)
	requireDenied(t, e)
	_, e = f.svc.Recover(ctx, "fixture-0", isolatedPassword, codes[1], "recovery-old-group")
	requireDenied(t, e)
	en, e := f.svc.BeginTOTP(ctx, recovered.Token)
	if e != nil {
		t.Fatal(e)
	}
	g, e := f.svc.ConfirmTOTP(ctx, recovered.Token, totpAt(t, en.Secret, f.clock.now()), "rebound")
	if e != nil {
		t.Fatal(e)
	}
	g, e = f.svc.Complete(ctx, g.Token, true)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = f.svc.Current(ctx, g.Token, true); e != nil {
		t.Fatal(e)
	}
	t.Log("actual: wrong recovery combinations rejected; same-code concurrency exactly one success; old sessions/factors/all old codes revoked; no business before new factor")
}

func TestTC44_LastFactorAndPasswordChangeRevokeOldSessions(t *testing.T) {
	f := securityTest(t)
	g, enrollment, oldCodes := f.totp(t, "fixture-0")
	ctx := context.Background()
	_, e := f.svc.RemoveFactor(ctx, g.Token, "totp")
	requireDenied(t, e)
	reissued, e := f.svc.ReissueRecovery(ctx, g.Token)
	if e != nil || len(reissued.RecoveryCodes) != 10 {
		t.Fatal("fresh factor authentication could not reissue recovery codes")
	}
	requireNoBusiness(t, f.svc, g.Token)
	requireNoBusiness(t, f.svc, reissued.Token)
	_, e = f.svc.Recover(ctx, "fixture-0", isolatedPassword, oldCodes[0], "old-recovery-group")
	requireDenied(t, e)
	g, e = f.svc.Complete(ctx, reissued.Token, true)
	if e != nil {
		t.Fatal(e)
	}
	f.clock.add(301 * time.Second)
	_, e = f.svc.ChangePassword(ctx, g.Token, "Cobalt island silver 83!")
	requireDenied(t, e)
	login, e := f.svc.LoginPassword(ctx, "fixture-0", isolatedPassword, "change")
	if e != nil {
		t.Fatal(e)
	}
	_, e = f.svc.ChangePassword(ctx, login.Token, "Cobalt island silver 83!")
	requireDenied(t, e)
	_, e = f.svc.ReissueRecovery(ctx, login.Token)
	requireDenied(t, e)
	fresh, e := f.svc.LoginTOTP(ctx, login.Token, totpAt(t, enrollment.Secret, f.clock.now()), "change")
	if e != nil {
		t.Fatal(e)
	}
	changed, e := f.svc.ChangePassword(ctx, fresh.Token, "Cobalt island silver 83!")
	if e != nil {
		t.Fatal(e)
	}
	requireNoBusiness(t, f.svc, g.Token)
	requireNoBusiness(t, f.svc, fresh.Token)
	requireNoBusiness(t, f.svc, changed.Token)
	_, e = f.svc.LoginPassword(ctx, "fixture-0", isolatedPassword, "old-password")
	requireDenied(t, e)
	if _, e = f.svc.Complete(ctx, changed.Token, true); e != nil {
		t.Fatal(e)
	}
	t.Run("remove-one-of-two", func(t *testing.T) {
		dual := securityTest(t)
		business, _ := dual.passkey(t, "fixture-0")
		en, e := dual.svc.BeginTOTP(ctx, business.Token)
		if e != nil {
			t.Fatal(e)
		}
		verified, e := dual.svc.ConfirmTOTP(ctx, business.Token, totpAt(t, en.Secret, dual.clock.now()), "second-factor")
		if e != nil {
			t.Fatal(e)
		}
		requireNoBusiness(t, dual.svc, business.Token)
		business, e = dual.svc.Complete(ctx, verified.Token, true)
		if e != nil {
			t.Fatal(e)
		}
		prior := business.Token
		removed, e := dual.svc.RemoveFactor(ctx, business.Token, "totp")
		if e != nil {
			t.Fatal(e)
		}
		requireNoBusiness(t, dual.svc, prior)
		requireNoBusiness(t, dual.svc, removed.Token)
		if countSecurity(t, dual.db, "SELECT count(*) FROM auth_credentials") != 1 || countSecurity(t, dual.db, "SELECT count(*) FROM auth_users WHERE totp_secret IS NOT NULL") != 0 {
			t.Fatal("removal did not retain exactly one usable passkey factor")
		}
		business, e = dual.svc.Complete(ctx, removed.Token, true)
		if e != nil {
			t.Fatal(e)
		}
		_, e = dual.svc.RemoveFactor(ctx, business.Token, "passkey")
		requireDenied(t, e)
	})
	t.Log("actual: last-factor removal, stale session and password-only change denied; fresh factor-authorized password change revoked old sessions")
}

func TestTC45_RateLimitAccountSourceWindowAndPrehash(t *testing.T) {
	f := securityTest(t)
	f.password(t, "fixture-0")
	ctx := context.Background()
	for i := 0; i < 5; i++ {
		_, e := f.svc.LoginPassword(ctx, "fixture-0", "incorrect fixture password", fmt.Sprintf("source-%d", i))
		requireStatus(t, e, 401)
	}
	// Occupying the hash semaphore models two expensive computations already in
	// flight. A request known to be rate limited must not wait for either one.
	for i := 0; i < cap(f.svc.hashGate); i++ {
		f.svc.hashGate <- struct{}{}
	}
	done := make(chan error, 1)
	go func() { _, e := f.svc.LoginPassword(ctx, "fixture-0", isolatedPassword, "sixth"); done <- e }()
	select {
	case e := <-done:
		requireStatus(t, e, 429)
	case <-time.After(500 * time.Millisecond):
		for i := 0; i < cap(f.svc.hashGate); i++ {
			<-f.svc.hashGate
		}
		<-done
		t.Fatal("already limited request waited on high-cost hash gate")
	}
	for i := 0; i < cap(f.svc.hashGate); i++ {
		<-f.svc.hashGate
	}
	f.clock.add(301 * time.Second)
	if _, e := f.svc.LoginPassword(ctx, "fixture-0", isolatedPassword, "after-window"); e != nil {
		t.Fatal("limit failed to expire")
	}
	for i := 0; i < 5; i++ {
		_, e := f.svc.LoginPassword(ctx, fmt.Sprintf("unknown-%d", i), "incorrect fixture password", "one-source")
		requireStatus(t, e, 401)
	}
	_, e := f.svc.LoginPassword(ctx, "another-unknown", "incorrect fixture password", "one-source")
	requireStatus(t, e, 429)
	if cap(f.svc.hashGate) != 2 {
		t.Fatal("expensive hash concurrency is not bounded at two")
	}
	t.Log("actual: account/source sixth failure 429, unknown accounts limited, window expired, limited request bypassed occupied hash gate")
}

func TestTC46_SessionRotationCSRFIdleAndAbsoluteExpiry(t *testing.T) {
	f := securityTest(t)
	g, en, _ := f.totp(t, "fixture-0")
	ctx := context.Background()
	if !f.svc.CheckCSRF(g.Token, g.Session.CSRF) || f.svc.CheckCSRF(g.Token, "forged") {
		t.Fatal("CSRF token validation incorrect")
	}
	if e := f.svc.Logout(ctx, g.Token); e != nil {
		t.Fatal(e)
	}
	requireNoBusiness(t, f.svc, g.Token)
	f.clock.add(30 * time.Second)
	login, e := f.svc.LoginPassword(ctx, "fixture-0", isolatedPassword, "expiry")
	if e != nil {
		t.Fatal(e)
	}
	business, e := f.svc.LoginTOTP(ctx, login.Token, totpAt(t, en.Secret, f.clock.now()), "expiry")
	if e != nil {
		t.Fatal(e)
	}
	requireNoBusiness(t, f.svc, login.Token)
	if f.svc.CheckCSRF(business.Token, login.Session.CSRF) {
		t.Fatal("old session CSRF accepted for rotated token")
	}
	f.clock.add(30 * time.Minute)
	requireNoBusiness(t, f.svc, business.Token)
	login, e = f.svc.LoginPassword(ctx, "fixture-0", isolatedPassword, "absolute")
	if e != nil {
		t.Fatal(e)
	}
	business, e = f.svc.LoginTOTP(ctx, login.Token, totpAt(t, en.Secret, f.clock.now()), "absolute")
	if e != nil {
		t.Fatal(e)
	}
	for i := 0; i < 31; i++ {
		f.clock.add(15 * time.Minute)
		if _, e = f.svc.Current(ctx, business.Token, true); e != nil {
			t.Fatal("session expired before eight hours despite activity")
		}
	}
	f.clock.add(15 * time.Minute)
	requireNoBusiness(t, f.svc, business.Token)
	t.Log("actual: logout/old token/old CSRF refused; 30-minute idle and eight-hour absolute expiration enforced; HTTP Origin/Host checks covered separately")
}

func TestTC47_SetupCompetitionAndAuditFailureRollback(t *testing.T) {
	f := securityTest(t)
	ctx := context.Background()
	g := f.password(t, "fixture-0")
	en, e := f.svc.BeginTOTP(ctx, g.Token)
	if e != nil {
		t.Fatal(e)
	}
	execSecurity(t, f.db, `CREATE TRIGGER isolated_auth_failure BEFORE INSERT ON auth_audit WHEN NEW.action='factor_verified' BEGIN SELECT RAISE(ABORT,'isolated audit failure'); END`)
	_, e = f.svc.ConfirmTOTP(ctx, g.Token, totpAt(t, en.Secret, f.clock.now()), "rollback")
	requireDenied(t, e)
	if countSecurity(t, f.db, "SELECT count(*) FROM auth_users WHERE state='password' AND totp_secret IS NULL AND pending_totp IS NOT NULL") != 1 || countSecurity(t, f.db, "SELECT count(*) FROM auth_recovery") != 0 || countSecurity(t, f.db, "SELECT count(*) FROM auth_sessions WHERE scope='business'") != 0 {
		t.Fatal("audit failure left partial factor/configuration")
	}
	execSecurity(t, f.db, "DROP TRIGGER isolated_auth_failure")
	g, e = f.svc.ConfirmTOTP(ctx, g.Token, totpAt(t, en.Secret, f.clock.now()), "retry")
	if e != nil {
		t.Fatal(e)
	}
	codes := append([]string(nil), g.RecoveryCodes...)
	var wg sync.WaitGroup
	out := make(chan Grant, 2)
	errs := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); result, e := f.svc.Complete(ctx, g.Token, true); out <- result; errs <- e }()
	}
	wg.Wait()
	close(out)
	close(errs)
	success := 0
	for e := range errs {
		if e == nil {
			success++
		}
	}
	if success != 1 || countSecurity(t, f.db, "SELECT count(*) FROM auth_sessions WHERE scope='business'") != 1 {
		t.Fatal("concurrent completion did not publish exactly one business session")
	}
	var business Grant
	for result := range out {
		if result.Token != "" {
			business = result
		}
	}
	version := countSecurity(t, f.db, "SELECT version FROM auth_users")
	execSecurity(t, f.db, `CREATE TRIGGER isolated_auth_failure BEFORE INSERT ON auth_audit WHEN NEW.action='recover' BEGIN SELECT RAISE(ABORT,'isolated recovery audit failure'); END`)
	failed, e := f.svc.Recover(ctx, "fixture-0", isolatedPassword, codes[0], "rollback-recovery")
	requireDenied(t, e)
	if failed.Token != "" || countSecurity(t, f.db, "SELECT count(*) FROM auth_recovery") != 10 || countSecurity(t, f.db, "SELECT version FROM auth_users") != version {
		t.Fatal("failed recovery consumed code/version or returned token")
	}
	if _, e = f.svc.Current(ctx, business.Token, true); e != nil {
		t.Fatal("failed recovery revoked prior business session")
	}
	execSecurity(t, f.db, "DROP TRIGGER isolated_auth_failure")
	execSecurity(t, f.db, `CREATE TRIGGER isolated_auth_failure BEFORE INSERT ON auth_audit WHEN NEW.action='factor_verified' BEGIN SELECT RAISE(ABORT,'isolated credential audit failure'); END`)
	failed, e = f.svc.ChangePassword(ctx, business.Token, "Cobalt island silver 83!")
	requireDenied(t, e)
	if failed.Token != "" || countSecurity(t, f.db, "SELECT version FROM auth_users") != version {
		t.Fatal("failed credential change returned token or changed version")
	}
	if _, e = f.svc.Current(ctx, business.Token, true); e != nil {
		t.Fatal("failed credential change revoked prior session")
	}
	execSecurity(t, f.db, "DROP TRIGGER isolated_auth_failure")
	if _, e = f.svc.LoginPassword(ctx, "fixture-0", isolatedPassword, "rollback-retained-password"); e != nil {
		t.Fatal("failed credential change altered password")
	}
	t.Log("actual: injected audit write failures rolled back factor, recovery and credential change; concurrent configuration completion exactly one business session")
}

func TestTC48_MissingMasterKeyFailsAndSecretsAreNotAuditFields(t *testing.T) {
	f := securityTest(t)
	_, en, codes := f.totp(t, "fixture-0")
	rows, e := f.db.Query("SELECT actor,action,method,result,occurred_at FROM auth_audit")
	if e != nil {
		t.Fatal(e)
	}
	defer rows.Close()
	for rows.Next() {
		var fields [5]string
		if e = rows.Scan(&fields[0], &fields[1], &fields[2], &fields[3], &fields[4]); e != nil {
			t.Fatal(e)
		}
		for _, field := range fields {
			for _, secret := range append([]string{isolatedPassword, en.Secret, en.URI}, codes...) {
				if strings.Contains(field, secret) {
					t.Fatal("secret found in authentication audit")
				}
			}
		}
	}
	rows.Close()
	path := filepath.Join(f.dir, "secrets", "master.key")
	if e = os.Remove(path); e != nil {
		t.Fatal(e)
	}
	_, e = New(f.db, Config{SecretDir: filepath.Join(f.dir, "secrets"), Now: f.clock.now})
	if e == nil || !strings.Contains(e.Error(), "master key missing") {
		t.Fatal("missing master key did not fail explicitly")
	}
	if _, e = os.Stat(path); !os.IsNotExist(e) {
		t.Fatal("missing master key silently regenerated")
	}
	t.Log("actual: no password/TOTP/URI/recovery material in audit; existing-account master-key loss failed explicitly without regeneration; no public reset path exercised")
}

// virtualAuthenticator produces actual P-256 WebAuthn CBOR attestations and
// ASN.1 ECDSA signatures. The server still performs its full library checks.
type virtualAuthenticator struct {
	key *ecdsa.PrivateKey
	id  []byte
}

func newVirtualAuthenticator(t *testing.T) *virtualAuthenticator {
	t.Helper()
	key, e := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if e != nil {
		t.Fatal(e)
	}
	id := make([]byte, 32)
	if _, e = rand.Read(id); e != nil {
		t.Fatal(e)
	}
	return &virtualAuthenticator{key, id}
}
func b64Security(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }
func passkeyChallenge(t *testing.T, b PasskeyBegin) string {
	t.Helper()
	raw, e := json.Marshal(b.Options)
	if e != nil {
		t.Fatal(e)
	}
	var v struct {
		PublicKey struct {
			Challenge string `json:"challenge"`
		} `json:"publicKey"`
		Challenge string `json:"challenge"`
	}
	if e = json.Unmarshal(raw, &v); e != nil {
		t.Fatal(e)
	}
	if v.PublicKey.Challenge != "" {
		return v.PublicKey.Challenge
	}
	if v.Challenge != "" {
		return v.Challenge
	}
	t.Fatal("Passkey options lack challenge")
	return ""
}
func (v *virtualAuthenticator) response(t *testing.T, challenge, origin, rp, user string, register bool, flags byte, badSignature bool) *http.Request {
	t.Helper()
	// Backup eligibility/state model a synced passkey whose counter stays zero.
	// Preserve requested UP/UV absence so the negative protocol cases stay real.
	flags |= 0x18
	kind := "webauthn.get"
	if register {
		kind = "webauthn.create"
	}
	client, e := json.Marshal(map[string]any{"type": kind, "challenge": challenge, "origin": origin, "crossOrigin": false})
	if e != nil {
		t.Fatal(e)
	}
	rpHash := sha256.Sum256([]byte(rp))
	authData := append([]byte(nil), rpHash[:]...)
	authData = append(authData, flags)
	authData = append(authData, 0, 0, 0, 0)
	response := map[string]any{"clientDataJSON": b64Security(client)}
	if register {
		cose, e := webauthncbor.Marshal(map[int]any{1: 2, 3: -7, -1: 1, -2: v.key.PublicKey.X.FillBytes(make([]byte, 32)), -3: v.key.PublicKey.Y.FillBytes(make([]byte, 32))})
		if e != nil {
			t.Fatal(e)
		}
		authData = append(authData, make([]byte, 16)...)
		length := make([]byte, 2)
		binary.BigEndian.PutUint16(length, uint16(len(v.id)))
		authData = append(authData, length...)
		authData = append(authData, v.id...)
		authData = append(authData, cose...)
		attestation, e := webauthncbor.Marshal(map[string]any{"fmt": "none", "authData": authData, "attStmt": map[string]any{}})
		if e != nil {
			t.Fatal(e)
		}
		response["attestationObject"] = b64Security(attestation)
		response["transports"] = []string{"internal"}
	} else {
		clientHash := sha256.Sum256(client)
		signed := append(append([]byte(nil), authData...), clientHash[:]...)
		digest := sha256.Sum256(signed)
		sig, e := ecdsa.SignASN1(rand.Reader, v.key, digest[:])
		if e != nil {
			t.Fatal(e)
		}
		if badSignature {
			sig[len(sig)-1] ^= 1
		}
		response["authenticatorData"] = b64Security(authData)
		response["signature"] = b64Security(sig)
		response["userHandle"] = b64Security([]byte(user))
	}
	data, e := json.Marshal(map[string]any{"id": b64Security(v.id), "rawId": b64Security(v.id), "type": "public-key", "response": response, "clientExtensionResults": map[string]any{}})
	if e != nil {
		t.Fatal(e)
	}
	r := httptest.NewRequest("POST", "http://localhost:8080/auth/passkey/finish", bytes.NewReader(data))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Origin", origin)
	return r
}

func (f *securityFixture) passkey(t *testing.T, id string) (Grant, *virtualAuthenticator) {
	t.Helper()
	ctx := context.Background()
	setup := f.password(t, id)
	b, e := f.svc.BeginPasskey(ctx, setup.Token, id, true)
	if e != nil {
		t.Fatal(e)
	}
	v := newVirtualAuthenticator(t)
	g, e := f.svc.FinishPasskey(ctx, setup.Token, b.ChallengeID, v.response(t, passkeyChallenge(t, b), f.svc.Origin, "localhost", id, true, 0x45, false), true, "virtual-register")
	if e != nil {
		t.Fatal("virtual registration failed:", e)
	}
	requireNoBusiness(t, f.svc, g.Token)
	resume, e := f.svc.BeginPasskey(ctx, "", id, false)
	if e != nil {
		t.Fatal(e)
	}
	g, e = f.svc.FinishPasskey(ctx, resume.Token, resume.ChallengeID, v.response(t, passkeyChallenge(t, resume), f.svc.Origin, "localhost", id, false, 5, false), false, "incomplete-passkey-login")
	if e != nil {
		t.Fatal(e)
	}
	requireNoBusiness(t, f.svc, g.Token)
	g, e = f.svc.Complete(ctx, g.Token, true)
	if e != nil {
		t.Fatal(e)
	}
	return g, v
}

func TestTC37_PasskeyRealProtocolRegistrationLoginAndZeroCounter(t *testing.T) {
	f := securityTest(t)
	g, v := f.passkey(t, "fixture-0")
	ctx := context.Background()
	if e := f.svc.Logout(ctx, g.Token); e != nil {
		t.Fatal(e)
	}
	for i := 0; i < 2; i++ {
		b, e := f.svc.BeginPasskey(ctx, "", "fixture-0", false)
		if e != nil {
			t.Fatal(e)
		}
		g, e = f.svc.FinishPasskey(ctx, b.Token, b.ChallengeID, v.response(t, passkeyChallenge(t, b), f.svc.Origin, "localhost", "fixture-0", false, 0x05, false), false, "virtual-login")
		if e != nil {
			t.Fatal("real ES256 assertion rejected:", e)
		}
		if _, e = f.svc.Current(ctx, g.Token, true); e != nil {
			t.Fatal(e)
		}
	}
	t.Log("actual: real CBOR none attestation and ES256 signed UV assertions verified; two zero-counter logins accepted; physical device acceptance remains pending")
}

func TestTC38_PasskeyRejectsMalformedAssertionsAndChallengeReplay(t *testing.T) {
	f := securityTest(t)
	_, v := f.passkey(t, "fixture-0")
	ctx := context.Background()
	for _, tc := range []struct {
		name, origin, rp, challenge string
		flags                       byte
		signature, expire, purpose  bool
	}{
		{name: "wrong-challenge", challenge: "invalid-challenge", flags: 5}, {name: "wrong-origin", origin: "http://evil.invalid", flags: 5}, {name: "wrong-rp", rp: "evil.invalid", flags: 5},
		{name: "no-up", flags: 4}, {name: "no-uv", flags: 1}, {name: "signature", flags: 5, signature: true}, {name: "expired", flags: 5, expire: true}, {name: "purpose", flags: 5, purpose: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b, e := f.svc.BeginPasskey(ctx, "", "fixture-0", false)
			if e != nil {
				t.Fatal(e)
			}
			challenge := passkeyChallenge(t, b)
			if tc.challenge != "" {
				challenge = tc.challenge
			}
			origin := f.svc.Origin
			if tc.origin != "" {
				origin = tc.origin
			}
			rp := "localhost"
			if tc.rp != "" {
				rp = tc.rp
			}
			if tc.expire {
				f.clock.add(121 * time.Second)
			}
			_, e = f.svc.FinishPasskey(ctx, b.Token, b.ChallengeID, v.response(t, challenge, origin, rp, "fixture-0", false, tc.flags, tc.signature), tc.purpose, "virtual-negative")
			requireDenied(t, e)
			requireNoBusiness(t, f.svc, b.Token)
			f.clock.add(301 * time.Second)
		})
	}
	b, e := f.svc.BeginPasskey(ctx, "", "fixture-0", false)
	if e != nil {
		t.Fatal(e)
	}
	challenge := passkeyChallenge(t, b)
	var wg sync.WaitGroup
	out := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, e := f.svc.FinishPasskey(ctx, b.Token, b.ChallengeID, v.response(t, challenge, f.svc.Origin, "localhost", "fixture-0", false, 5, false), false, "parallel-passkey")
			out <- e
		}()
	}
	wg.Wait()
	close(out)
	n := 0
	for e := range out {
		if e == nil {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("challenge consumed successfully %d times", n)
	}
	_, e = f.svc.FinishPasskey(ctx, b.Token, b.ChallengeID, v.response(t, challenge, f.svc.Origin, "localhost", "fixture-0", false, 5, false), false, "replay-passkey")
	requireDenied(t, e)
	for _, tc := range []struct {
		name, origin, rp, challenge string
		flags                       byte
	}{
		{name: "register-origin", origin: "http://evil.invalid", flags: 0x45},
		{name: "register-rp", rp: "evil.invalid", flags: 0x45},
		{name: "register-up", flags: 0x44},
		{name: "register-uv", flags: 0x41},
		{name: "register-challenge", challenge: "invalid-registration-challenge", flags: 0x45},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rf := securityTest(t)
			setup := rf.password(t, "fixture-0")
			registration, e := rf.svc.BeginPasskey(ctx, setup.Token, "fixture-0", true)
			if e != nil {
				t.Fatal(e)
			}
			origin := rf.svc.Origin
			if tc.origin != "" {
				origin = tc.origin
			}
			rp := "localhost"
			if tc.rp != "" {
				rp = tc.rp
			}
			challenge := passkeyChallenge(t, registration)
			if tc.challenge != "" {
				challenge = tc.challenge
			}
			_, e = rf.svc.FinishPasskey(ctx, setup.Token, registration.ChallengeID, newVirtualAuthenticator(t).response(t, challenge, origin, rp, "fixture-0", true, tc.flags, false), true, "invalid-register")
			requireDenied(t, e)
			if countSecurity(t, rf.db, "SELECT count(*) FROM auth_credentials") != 0 || countSecurity(t, rf.db, "SELECT count(*) FROM auth_users WHERE state='password'") != 1 {
				t.Fatal("rejected registration persisted a credential or factor state")
			}
		})
	}
	t.Run("register-concurrent", func(t *testing.T) {
		rf := securityTest(t)
		setup := rf.password(t, "fixture-0")
		registration, e := rf.svc.BeginPasskey(ctx, setup.Token, "fixture-0", true)
		if e != nil {
			t.Fatal(e)
		}
		authenticator := newVirtualAuthenticator(t)
		challenge := passkeyChallenge(t, registration)
		out := make(chan error, 2)
		var wg sync.WaitGroup
		for i := 0; i < 2; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				_, e := rf.svc.FinishPasskey(ctx, setup.Token, registration.ChallengeID, authenticator.response(t, challenge, rf.svc.Origin, "localhost", "fixture-0", true, 0x45, false), true, "concurrent-register")
				out <- e
			}()
		}
		wg.Wait()
		close(out)
		success := 0
		for e := range out {
			if e == nil {
				success++
			}
		}
		if success != 1 || countSecurity(t, rf.db, "SELECT count(*) FROM auth_credentials") != 1 {
			t.Fatal("registration challenge did not consume exactly once")
		}
	})
	t.Log("actual: wrong/expired/replayed challenge, Origin/RP, purpose, UP/UV and signature rejected; concurrent valid challenge exactly one success")
}

func TestTC39_PasskeyRejectsOtherAccountHandleAndDuplicateCredential(t *testing.T) {
	f := securityTest(t, "finance", "admin")
	_, v := f.passkey(t, "fixture-0")
	ctx := context.Background()
	second := f.password(t, "fixture-1")
	b, e := f.svc.BeginPasskey(ctx, "", "fixture-0", false)
	if e != nil {
		t.Fatal(e)
	}
	_, e = f.svc.FinishPasskey(ctx, b.Token, b.ChallengeID, v.response(t, passkeyChallenge(t, b), f.svc.Origin, "localhost", "fixture-1", false, 5, false), false, "cross-handle")
	requireDenied(t, e)
	b, e = f.svc.BeginPasskey(ctx, second.Token, "fixture-1", true)
	if e != nil {
		t.Fatal(e)
	}
	_, e = f.svc.FinishPasskey(ctx, second.Token, b.ChallengeID, v.response(t, passkeyChallenge(t, b), f.svc.Origin, "localhost", "fixture-1", true, 0x45, false), true, "duplicate-id")
	requireDenied(t, e)
	if countSecurity(t, f.db, "SELECT count(*) FROM auth_credentials") != 1 {
		t.Fatal("duplicate credential crossed account ownership")
	}
	_, e = f.svc.BeginPasskey(ctx, second.Token, "fixture-0", true)
	requireDenied(t, e)
	b, e = f.svc.BeginPasskey(ctx, second.Token, "fixture-1", true)
	if e != nil {
		t.Fatal(e)
	}
	other := newVirtualAuthenticator(t)
	second, e = f.svc.FinishPasskey(ctx, second.Token, b.ChallengeID, other.response(t, passkeyChallenge(t, b), f.svc.Origin, "localhost", "fixture-1", true, 0x45, false), true, "second-register")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = f.svc.Complete(ctx, second.Token, true); e != nil {
		t.Fatal(e)
	}
	b, e = f.svc.BeginPasskey(ctx, "", "fixture-1", false)
	if e != nil {
		t.Fatal(e)
	}
	_, e = f.svc.FinishPasskey(ctx, b.Token, b.ChallengeID, v.response(t, passkeyChallenge(t, b), f.svc.Origin, "localhost", "fixture-1", false, 5, false), false, "other-account-credential")
	requireDenied(t, e)
	t.Log("actual: cross-account user_handle, mismatched registration account and duplicate credential_id rejected; original credential remains unique")
}
