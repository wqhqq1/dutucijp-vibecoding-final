package web

// All identities, factors and business rows here are isolated HTTP fixtures.
// Inert credential rows test cleanup/notification IDs only; they cannot sign in.
import (
	"context"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"returnsrecon/internal/auth"
	"returnsrecon/internal/store"
)

func deleteSnapshot(t *testing.T, f *httpSecurityFixture, query string, args ...any) string {
	t.Helper()
	var out string
	if err := f.server.Store.DB.QueryRow(query, args...).Scan(&out); err != nil {
		t.Fatal(err)
	}
	return out
}
func deleteHTTPCase(t *testing.T, f *httpSecurityFixture, client *httpSecurityClient, id string) store.WorkflowCase {
	t.Helper()
	w := f.request(t, client, "GET", "/api/cases/"+id, nil, nil)
	expectHTTPStatus(t, w, 200)
	var c store.WorkflowCase
	decodeHTTPResponse(t, w, &c)
	return c
}

func TestHTTP_TC63_DeletionBoundaryTombstoneAndNoReinitialization(t *testing.T) {
	f := httpSecurityTest(t)
	admin := f.configured(t, "http-admin")
	finance := f.configured(t, "http-finance")
	owner := f.configured(t, "http-owner")
	path := "/api/admin/accounts/http-other"
	for _, client := range []*httpSecurityClient{nil, finance, owner} {
		want := 403
		if client == nil {
			want = 401
		}
		expectHTTPStatus(t, f.request(t, client, "GET", path+"/deletion-preview", nil, nil), want)
		expectHTTPStatus(t, f.request(t, client, "DELETE", path, map[string]any{}, map[string]string{"X-Role": "admin"}), want)
	}
	for _, method := range []string{"GET", "DELETE"} {
		url := "/api/admin/accounts/http-admin"
		var body any = map[string]any{}
		if method == "GET" {
			url += "/deletion-preview"
			body = nil
		}
		expectHTTPStatus(t, f.request(t, admin, method, url, body, nil), 403)
	}
	expectHTTPStatus(t, f.request(t, admin, "DELETE", path, map[string]any{}, map[string]string{"X-CSRF-Token": "wrong"}), 403)
	w := f.request(t, admin, "GET", path+"/deletion-preview", nil, nil)
	expectHTTPStatus(t, w, 200)
	if strings.Contains(w.Body.String(), "revoked_credential_ids") {
		t.Fatal("preview exposed credential IDs")
	}
	expectHTTPStatus(t, f.request(t, admin, "DELETE", path, map[string]any{}, nil), 200)
	if httpSecurityCount(t, f.server.Store.DB, "SELECT count(*) FROM auth_users WHERE id='http-other' AND state='deleted' AND password_hash='' AND totp_secret IS NULL") != 1 {
		t.Fatal("missing inert historical identity")
	}
	expectHTTPStatus(t, f.request(t, admin, "DELETE", path, map[string]any{}, nil), 404)
	expectHTTPStatus(t, f.request(t, admin, "PATCH", path, accountPatch("all"), nil), 404)
	expectHTTPStatus(t, f.request(t, admin, "POST", "/api/admin/accounts", accountBody("http-other", "warehouse", "all"), nil), 409)
	for _, url := range []string{"/api/users", "/api/admin/accounts", "/api/auth/setup/accounts"} {
		w = f.request(t, admin, "GET", url, nil, nil)
		expectHTTPStatus(t, w, 200)
		if strings.Contains(w.Body.String(), `"id":"http-other"`) {
			t.Fatal("deleted identity remained selectable")
		}
	}
	if _, err := f.server.Auth.Initialize(context.Background(), []auth.Identity{{ID: "http-other", Role: "warehouse"}}); err != nil {
		t.Fatal(err)
	}
	expectHTTPStatus(t, f.request(t, &httpSecurityClient{id: "http-other"}, "POST", "/api/auth/setup/start", map[string]any{"id": "http-other"}, nil), 401)
	expectHTTPStatus(t, f.request(t, &httpSecurityClient{id: "http-other"}, "POST", "/api/auth/bootstrap", map[string]any{"token": f.bootstrap["http-other"]}, nil), 401)
	if httpSecurityCount(t, f.server.Store.DB, "SELECT count(*) FROM auth_users WHERE state='deleted'") != 1 {
		t.Fatal("initialization revived a deleted account")
	}
}

func TestHTTP_TC64_DeletePreservesClosedAndConfirmedResultsReleasesOtherCases(t *testing.T) {
	f := httpSecurityTest(t)
	admin := f.configured(t, "http-admin")
	finance := f.configured(t, "http-finance")
	owner := f.configured(t, "http-owner")
	for i := 0; i < 3; i++ {
		f.assignHTTP(t, finance, i, owner.id)
	}
	for _, i := range []int{0, 1} {
		c := deleteHTTPCase(t, f, finance, f.cases[i])
		w := f.request(t, owner, "POST", "/api/cases/"+c.ID+"/start", map[string]any{"version": c.Version}, nil)
		expectHTTPStatus(t, w, 200)
		decodeHTTPResponse(t, w, &c)
		expectHTTPStatus(t, f.request(t, owner, "POST", "/api/cases/"+c.ID+"/confirm-match", map[string]any{"version": c.Version, "sale_row_id": "0:2"}, nil), 200)
		if i == 0 {
			c = deleteHTTPCase(t, f, finance, c.ID)
			expectHTTPStatus(t, f.request(t, owner, "POST", "/api/cases/"+c.ID+"/close", map[string]any{"version": c.Version, "conclusion": "已核对并办结"}, nil), 200)
		}
	}
	closed := deleteHTTPCase(t, f, finance, f.cases[0])
	unfinished := deleteHTTPCase(t, f, finance, f.cases[1])
	pending := deleteHTTPCase(t, f, finance, f.cases[2])
	alloc := deleteSnapshot(t, f, `SELECT json_group_array(json_object('cancel',cancel_id,'sale',sale_id,'quantity',quantity,'amount',amount,'actor',actor)) FROM allocations`)
	balances := deleteSnapshot(t, f, `SELECT json_group_array(json_object('sale',sale_id,'allocated',allocated,'remaining',remaining)) FROM sale_balances`)
	closedAudit := httpSecurityCount(t, f.server.Store.DB, "SELECT count(*) FROM audit_events WHERE case_id=?", closed.ID)
	ctx, actor := f.capturedRequestContext(t, owner)
	accountSQL(t, f, `INSERT INTO auth_credentials(id,user_id,credential_json) VALUES('isolated-revoked-key','http-owner','{}'),('isolated-untouched-key','http-other','{}')`)
	w := f.request(t, admin, "GET", "/api/admin/accounts/http-owner/deletion-preview", nil, nil)
	expectHTTPStatus(t, w, 200)
	var impact auth.AccountDeletion
	decodeHTTPResponse(t, w, &impact)
	if impact.OpenCases != 2 || impact.ClosedCases != 1 || impact.PasskeyCount != 1 || len(impact.RevokedCredentialIDs) != 0 {
		t.Fatal("incorrect deletion preview")
	}
	w = f.request(t, admin, "DELETE", "/api/admin/accounts/http-owner", map[string]any{}, nil)
	expectHTTPStatus(t, w, 200)
	decodeHTTPResponse(t, w, &impact)
	if len(impact.RevokedCredentialIDs) != 1 || impact.RevokedCredentialIDs[0] != "isolated-revoked-key" {
		t.Fatal("notification IDs include another account or miss revoked credential")
	}
	if got := deleteHTTPCase(t, f, finance, closed.ID); got != closed {
		t.Fatal("closed workflow record changed")
	}
	for _, old := range []store.WorkflowCase{unfinished, pending} {
		got := deleteHTTPCase(t, f, finance, old.ID)
		if got.Status != "pending" || got.Assignee != "" || got.Version != old.Version+1 || got.AssignedAt != "" || got.AssignedBy != "" || got.Conclusion != "" {
			t.Fatal("unfinished case did not return to the unassigned queue")
		}
	}
	if alloc != deleteSnapshot(t, f, `SELECT json_group_array(json_object('cancel',cancel_id,'sale',sale_id,'quantity',quantity,'amount',amount,'actor',actor)) FROM allocations`) || balances != deleteSnapshot(t, f, `SELECT json_group_array(json_object('sale',sale_id,'allocated',allocated,'remaining',remaining)) FROM sale_balances`) {
		t.Fatal("deletion changed saved allocations or balance quantities")
	}
	if httpSecurityCount(t, f.server.Store.DB, "SELECT count(*) FROM audit_events WHERE case_id=?", closed.ID) != closedAudit || httpSecurityCount(t, f.server.Store.DB, "SELECT count(*) FROM audit_events WHERE action='account_deleted_unassign'") != 2 {
		t.Fatal("workflow deletion audits missing or closed audit modified")
	}
	for _, table := range []string{"auth_sessions", "auth_credentials", "auth_recovery", "auth_bootstrap", "auth_challenges"} {
		if httpSecurityCount(t, f.server.Store.DB, "SELECT count(*) FROM "+table+" WHERE user_id='http-owner'") != 0 {
			t.Fatal("deleted account retained active authentication material")
		}
	}
	if httpSecurityCount(t, f.server.Store.DB, "SELECT count(*) FROM auth_credentials WHERE id='isolated-untouched-key'") != 1 {
		t.Fatal("another account's credential was removed")
	}
	if strings.Contains(deleteSnapshot(t, f, `SELECT after_json FROM account_audit WHERE action='delete'`), "isolated-revoked-key") {
		t.Fatal("management history retained notification IDs")
	}
	expectHTTPStatus(t, f.request(t, owner, "GET", "/api/runs", nil, nil), 401)
	if _, err := f.server.Store.TransitionCase(ctx, actor, pending.ID, "in_progress", pending.Version, ""); err == nil {
		t.Fatal("an admitted deleted-user request still wrote")
	}
	for _, url := range []string{"/api/auth/login", "/api/auth/recover"} {
		body := map[string]any{"id": owner.id, "password": httpFixturePassword}
		if url == "/api/auth/recover" {
			body["code"] = owner.codes[0]
		}
		expectHTTPStatus(t, f.request(t, &httpSecurityClient{id: owner.id}, "POST", url, body, nil), 401)
	}
	// Another active worker can continue the returned case without recreating
	// its saved allocation or consuming the sale's quantity a second time.
	resumed := f.assignHTTP(t, finance, 1, finance.id)
	w = f.request(t, finance, "POST", "/api/cases/"+resumed.ID+"/start", map[string]any{"version": resumed.Version}, nil)
	expectHTTPStatus(t, w, 200)
	decodeHTTPResponse(t, w, &resumed)
	expectHTTPStatus(t, f.request(t, finance, "POST", "/api/cases/"+resumed.ID+"/close", map[string]any{"version": resumed.Version, "conclusion": "接手原已确认配对后办结"}, nil), 200)
	if alloc != deleteSnapshot(t, f, `SELECT json_group_array(json_object('cancel',cancel_id,'sale',sale_id,'quantity',quantity,'amount',amount,'actor',actor)) FROM allocations`) {
		t.Fatal("resumed work reallocated a previously confirmed match")
	}
}

func TestHTTP_TC65_DeletingFinanceRejectsOnlyPendingApplications(t *testing.T) {
	f := httpSecurityTest(t)
	admin := f.configured(t, "http-admin")
	finance := f.configured(t, "http-finance")
	pending := accountRequest(t, f, finance, "released-request-name")
	approved := accountRequest(t, f, finance, "opened-before-delete")
	expectHTTPStatus(t, f.request(t, admin, "POST", "/api/warehouse-requests/"+approved.ID+"/approve", map[string]any{}, nil), 200)
	f.assignHTTP(t, admin, 0, finance.id)
	expectHTTPStatus(t, f.request(t, admin, "DELETE", "/api/admin/accounts/"+finance.id, map[string]any{}, nil), 200)
	w := f.request(t, admin, "GET", "/api/warehouse-requests", nil, nil)
	expectHTTPStatus(t, w, 200)
	var list []auth.WarehouseRequest
	decodeHTTPResponse(t, w, &list)
	for _, r := range list {
		if r.ID == pending.ID && (r.Status != "rejected" || r.RejectionReason != "申请人账户已删除，申请自动驳回。" || r.ReviewedBy != admin.id) {
			t.Fatal("pending request was not explicitly rejected")
		}
		if r.ID == approved.ID && (r.Status != "approved" || r.AccountState != "new") {
			t.Fatal("approved history or its warehouse was deleted")
		}
	}
	expectHTTPStatus(t, f.request(t, admin, "POST", "/api/warehouse-requests/"+pending.ID+"/approve", map[string]any{}, nil), 409)
	expectHTTPStatus(t, f.request(t, admin, "POST", "/api/admin/accounts", accountBody(pending.AccountID, "warehouse", "all"), nil), 200)
	if c := deleteHTTPCase(t, f, admin, f.cases[0]); c.Assignee != "" || c.Status != "pending" {
		t.Fatal("finance work was not released")
	}
}

func TestHTTP_TC66_DeletionFailureRollsBackEverySideEffect(t *testing.T) {
	for _, fault := range []string{"workflow_audit", "credentials", "request_audit", "management_audit"} {
		t.Run(fault, func(t *testing.T) {
			f := httpSecurityTest(t)
			admin := f.configured(t, "http-admin")
			finance := f.configured(t, "http-finance")
			f.assignHTTP(t, admin, 0, finance.id)
			r := accountRequest(t, f, finance, "rollback-delete-request")
			before := deleteHTTPCase(t, f, admin, f.cases[0])
			version := httpSecurityCount(t, f.server.Store.DB, "SELECT version FROM auth_users WHERE id=?", finance.id)
			audit := httpSecurityCount(t, f.server.Store.DB, "SELECT count(*) FROM account_audit")
			accountSQL(t, f, `INSERT INTO auth_credentials(id,user_id,credential_json) VALUES('rollback-notify-key','http-finance','{}')`)
			trigger := map[string]string{
				"workflow_audit":   `CREATE TRIGGER isolated_delete_fault BEFORE INSERT ON audit_events WHEN NEW.action='account_deleted_unassign' BEGIN SELECT RAISE(ABORT,'isolated deletion fault'); END`,
				"credentials":      `CREATE TRIGGER isolated_delete_fault BEFORE DELETE ON auth_recovery WHEN OLD.user_id='http-finance' BEGIN SELECT RAISE(ABORT,'isolated deletion fault'); END`,
				"request_audit":    `CREATE TRIGGER isolated_delete_fault BEFORE INSERT ON account_audit WHEN NEW.action='rejected' BEGIN SELECT RAISE(ABORT,'isolated deletion fault'); END`,
				"management_audit": `CREATE TRIGGER isolated_delete_fault BEFORE INSERT ON account_audit WHEN NEW.action='delete' BEGIN SELECT RAISE(ABORT,'isolated deletion fault'); END`,
			}[fault]
			accountSQL(t, f, trigger)
			w := f.request(t, admin, "DELETE", "/api/admin/accounts/"+finance.id, map[string]any{}, nil)
			expectHTTPStatus(t, w, 500)
			if strings.Contains(w.Body.String(), "rollback-notify-key") {
				t.Fatal("failed deletion returned notification IDs")
			}
			if got := deleteHTTPCase(t, f, admin, before.ID); got != before {
				t.Fatal("failed deletion changed workflow state")
			}
			if httpSecurityCount(t, f.server.Store.DB, "SELECT version FROM auth_users WHERE id=?", finance.id) != version || httpSecurityCount(t, f.server.Store.DB, "SELECT count(*) FROM auth_users WHERE id=? AND state='complete' AND password_hash<>''", finance.id) != 1 || httpSecurityCount(t, f.server.Store.DB, "SELECT count(*) FROM auth_recovery WHERE user_id=?", finance.id) != 10 || httpSecurityCount(t, f.server.Store.DB, "SELECT count(*) FROM auth_credentials WHERE id='rollback-notify-key'") != 1 || httpSecurityCount(t, f.server.Store.DB, "SELECT count(*) FROM warehouse_requests WHERE id=? AND status='pending'", r.ID) != 1 || httpSecurityCount(t, f.server.Store.DB, "SELECT count(*) FROM account_audit") != audit || httpSecurityCount(t, f.server.Store.DB, "SELECT count(*) FROM audit_events WHERE action='account_deleted_unassign'") != 0 {
				t.Fatal("failed deletion left partial authentication, approval or audit state")
			}
			expectHTTPStatus(t, f.request(t, finance, "GET", "/api/runs", nil, nil), 200)
		})
	}
}

func TestHTTP_TC66_ConcurrentDeletionAppliesOnce(t *testing.T) {
	f := httpSecurityTest(t)
	admin := f.configured(t, "http-admin")
	finance := f.configured(t, "http-finance")
	f.assignHTTP(t, finance, 0, "http-other")
	var wg sync.WaitGroup
	start := make(chan struct{})
	results := make(chan *httptest.ResponseRecorder, 2)
	for i := 0; i < 2; i++ {
		client := *admin
		cookie := *admin.cookie
		client.cookie = &cookie
		wg.Add(1)
		go func(c httpSecurityClient) {
			defer wg.Done()
			<-start
			results <- f.request(t, &c, "DELETE", "/api/admin/accounts/http-other", map[string]any{}, nil)
		}(client)
	}
	close(start)
	wg.Wait()
	close(results)
	statuses := map[int]int{}
	for w := range results {
		statuses[w.Code]++
	}
	if statuses[200] != 1 || statuses[404] != 1 || httpSecurityCount(t, f.server.Store.DB, "SELECT count(*) FROM account_audit WHERE action='delete'") != 1 || httpSecurityCount(t, f.server.Store.DB, "SELECT count(*) FROM audit_events WHERE action='account_deleted_unassign'") != 1 {
		t.Fatal("concurrent delete repeated side effects")
	}
	if c := deleteHTTPCase(t, f, finance, f.cases[0]); c.Status != "pending" || c.Assignee != "" || c.Version != 3 {
		t.Fatal("concurrent delete did not release exactly once")
	}
}

func TestHTTP_TC66_DeletedTargetCannotReceiveAnAlreadyAdmittedAssignment(t *testing.T) {
	f := httpSecurityTest(t)
	admin := f.configured(t, "http-admin")
	finance := f.configured(t, "http-finance")
	ctx, actor := f.capturedRequestContext(t, finance)
	expectHTTPStatus(t, f.request(t, admin, "DELETE", "/api/admin/accounts/http-other", map[string]any{}, nil), 200)
	if _, err := f.server.Store.AssignCase(ctx, actor, store.WorkflowActor{ID: "http-other", Role: "warehouse"}, f.cases[0]); err == nil {
		t.Fatal("stale assignee was not rechecked in the write transaction")
	}
	if c := deleteHTTPCase(t, f, finance, f.cases[0]); c.Assignee != "" || c.Version != 1 {
		t.Fatal("deleted target received a case")
	}
}
