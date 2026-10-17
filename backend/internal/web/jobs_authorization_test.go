package web

import (
	"testing"
)

func TestHTTP_TC43_AlreadyAdmittedJobRejectsRevokedSession(t *testing.T) {
	for _, action := range []string{"logout", "recovery"} {
		t.Run(action, func(t *testing.T) {
			f := httpSecurityTest(t)
			finance := f.configured(t, "http-finance")
			jobs, err := NewJobs(f.server.Store, t.TempDir(), t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(jobs.Close)
			ctx, _ := f.capturedRequestContext(t, finance)
			if action == "logout" {
				expectHTTPStatus(t, f.request(t, finance, "POST", "/api/auth/logout", map[string]any{}, nil), 200)
			} else {
				expectHTTPStatus(t, f.request(t, finance, "POST", "/api/auth/recover", map[string]any{"id": finance.id, "password": httpFixturePassword, "code": finance.codes[0]}, nil), 200)
			}
			if _, err = jobs.Start(ctx, "all"); err == nil {
				t.Fatal("already-admitted revoked session queued a data job")
			}
			if jobs.active != 0 || httpSecurityCount(t, f.server.Store.DB, "SELECT count(*) FROM web_jobs") != 0 {
				t.Fatal("revoked job changed queue or launched a worker")
			}
		})
	}
}

func TestHTTP_TC43_ValidAdmittedJobCanStillQueue(t *testing.T) {
	f := httpSecurityTest(t)
	finance := f.configured(t, "http-finance")
	jobs, err := NewJobs(f.server.Store, t.TempDir(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(jobs.Close)
	// Occupy only this isolated test worker gate: enqueue succeeds without
	// invoking a real Python data preparation process.
	jobs.gate <- struct{}{}
	ctx, _ := f.capturedRequestContext(t, finance)
	job, err := jobs.Start(ctx, "all")
	if err != nil || job.Status != "queued" || job.Scope != "all" {
		t.Fatalf("valid session could not enqueue: %+v %v", job, err)
	}
	if httpSecurityCount(t, f.server.Store.DB, "SELECT count(*) FROM web_jobs") != 1 {
		t.Fatal("valid job was not persisted")
	}
}
