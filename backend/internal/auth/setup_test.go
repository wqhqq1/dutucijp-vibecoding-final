package auth

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestTC36_FirstSetupWithoutLinkSurvivesDeadlineAndRestart(t *testing.T) {
	f := securityTest(t, "finance", "warehouse", "admin")
	ctx := context.Background()
	// Include a real legacy deadline, rather than only testing newly generated
	// expires=0 links. This is a fixture, never a user's authentication database.
	execSecurity(t, f.db, "UPDATE auth_bootstrap SET expires=?", f.clock.now().Unix()+900)
	f.clock.add(24 * time.Hour)
	s, err := New(f.db, Config{SecretDir: filepath.Join(f.dir, "secrets"), Now: f.clock.now})
	if err != nil {
		t.Fatal(err)
	}
	accounts, err := s.SetupAccounts(ctx)
	if err != nil || len(accounts) != 3 {
		t.Fatal("untouched accounts unavailable after restart/deadline")
	}
	for _, a := range accounts {
		g, e := s.StartSetup(ctx, a.ID)
		if e != nil || g.Session.Scope != "setup" || g.Session.Role != a.Role {
			t.Fatal("local first setup failed")
		}
		requireNoBusiness(t, s, g.Token)
		_, e = s.Complete(ctx, g.Token, true)
		requireDenied(t, e)
	}
	legacy, err := s.Bootstrap(ctx, f.bootstrap["fixture-0"])
	if err != nil {
		t.Fatal("legacy deadline still applied")
	}
	_, err = s.Bootstrap(ctx, f.bootstrap["fixture-0"])
	requireDenied(t, err)
	g, err := s.SetPassword(ctx, legacy.Token, isolatedPassword)
	if err != nil {
		t.Fatal(err)
	}
	requireNoBusiness(t, s, g.Token)
	_, err = s.StartSetup(ctx, "fixture-0")
	requireDenied(t, err)
	accounts, err = s.SetupAccounts(ctx)
	if err != nil || len(accounts) != 2 {
		t.Fatal("password account remained in first-use list")
	}
	resume, err := s.LoginPassword(ctx, "fixture-0", isolatedPassword, "setup-resume")
	if err != nil || resume.Session.Scope != "setup" {
		t.Fatal("password continuation failed")
	}
}

func TestTC36_FirstSetupCannotResetOrRaceExistingPassword(t *testing.T) {
	f := securityTest(t, "finance", "warehouse", "admin")
	ctx := context.Background()
	g, err := f.svc.StartSetup(ctx, "fixture-0")
	if err != nil {
		t.Fatal(err)
	}
	newer, err := f.svc.StartSetup(ctx, "fixture-0")
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.svc.SetPassword(ctx, g.Token, isolatedPassword)
	requireDenied(t, err)
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _, e := f.svc.SetPassword(ctx, newer.Token, isolatedPassword); results <- e }()
	}
	wg.Wait()
	close(results)
	success := 0
	for e := range results {
		if e == nil {
			success++
		}
	}
	if success != 1 {
		t.Fatal("password setup race did not have exactly one winner")
	}
	_, _, _ = f.totp(t, "fixture-1")
	execSecurity(t, f.db, "UPDATE auth_users SET state='recovering' WHERE id='fixture-2'")
	accounts, err := f.svc.SetupAccounts(ctx)
	if err != nil || len(accounts) != 0 {
		t.Fatal("configured/recovering account exposed for initialization")
	}
	for _, id := range []string{"fixture-0", "fixture-1", "fixture-2", "unknown"} {
		_, err = f.svc.StartSetup(ctx, id)
		requireDenied(t, err)
		_, err = f.svc.RenewBootstrap(ctx, id)
		requireDenied(t, err)
	}
	// Defense against inconsistent imported states: a nonempty password is
	// sufficient to block initialization even if state was accidentally 'new'.
	execSecurity(t, f.db, "UPDATE auth_users SET state='new' WHERE id='fixture-0'")
	accounts, err = f.svc.SetupAccounts(ctx)
	if err != nil || len(accounts) != 0 {
		t.Fatal("nonempty password exposed")
	}
	_, err = f.svc.StartSetup(ctx, "fixture-0")
	requireDenied(t, err)
	_, err = f.svc.RenewBootstrap(ctx, "fixture-0")
	requireDenied(t, err)
	_, err = f.svc.Bootstrap(ctx, f.bootstrap["fixture-0"])
	requireDenied(t, err)
}
