package auth

import (
	"context"
	"sync"
	"testing"
	"time"
)

func TestSessionActivityDoesNotBlockReadersOrBypassRevocation(t *testing.T) {
	f := securityTest(t)
	ctx := context.Background()
	execSecurity(t, f.db, "PRAGMA journal_mode=WAL")
	g, _, _ := f.totp(t, "fixture-0")
	before := countSecurity(t, f.db, "SELECT seen FROM auth_sessions WHERE hash=?", Digest(g.Token))
	f.clock.add(61 * time.Second)
	writer, err := f.db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	if _, err = writer.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		t.Fatal(err)
	}
	defer writer.ExecContext(context.Background(), "ROLLBACK")
	// Each real database read must complete while another connection holds the
	// write lock, including when its activity timestamp is due for renewal.
	deadline, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	results := make(chan error, 10)
	var wg sync.WaitGroup
	for range 10 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, e := f.svc.Current(deadline, g.Token, true)
			results <- e
		}()
	}
	wg.Wait()
	close(results)
	for e := range results {
		if e != nil {
			t.Fatalf("reader waited for active writer: %v", e)
		}
	}
	if seen := countSecurity(t, f.db, "SELECT seen FROM auth_sessions WHERE hash=?", Digest(g.Token)); seen != before {
		t.Fatal("busy renewal extended persisted lifetime")
	}
	if _, err = writer.ExecContext(ctx, "COMMIT"); err != nil {
		t.Fatal(err)
	}
	if _, err = f.svc.Current(ctx, g.Token, true); err != nil {
		t.Fatal(err)
	}
	if seen := countSecurity(t, f.db, "SELECT seen FROM auth_sessions WHERE hash=?", Digest(g.Token)); int64(seen) != f.clock.now().Unix() {
		t.Fatal("next request did not retry the activity renewal")
	}
	execSecurity(t, f.db, "UPDATE auth_users SET version=version+1 WHERE id='fixture-0'")
	_, err = f.svc.Current(ctx, g.Token, true)
	requireStatus(t, err, 401)
}

func TestSessionBusyRenewalCannotReviveIdleExpiredSession(t *testing.T) {
	f := securityTest(t)
	ctx := context.Background()
	execSecurity(t, f.db, "PRAGMA journal_mode=WAL")
	g, _, _ := f.totp(t, "fixture-0")
	f.clock.add(30 * time.Minute)
	_, err := f.svc.Current(ctx, g.Token, true)
	requireStatus(t, err, 401)
}
