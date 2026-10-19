package web

// FR-19 uses only isolated synthetic data and the real first-use/password/TOTP
// HTTP routes. These tests never insert a usable session or change a live DB.
import (
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pquerna/otp/totp"
	"returnsrecon/internal/auth"
)

func accountBody(id, role, scope string, countries ...string) map[string]any {
	if countries == nil {
		countries = []string{}
	}
	return map[string]any{"id": id, "role": role, "display_name": "隔离测试账户 " + id, "country_scope": scope, "countries": countries}
}
func accountPatch(scope string, countries ...string) map[string]any {
	b := accountBody("", "", scope, countries...)
	delete(b, "id")
	delete(b, "role")
	return b
}
func accountSQL(t *testing.T, f *httpSecurityFixture, query string, args ...any) {
	t.Helper()
	if _, err := f.server.Store.DB.Exec(query, args...); err != nil {
		t.Fatal(err)
	}
}
func accountRequest(t *testing.T, f *httpSecurityFixture, finance *httpSecurityClient, id string, countries ...string) auth.WarehouseRequest {
	t.Helper()
	scope := "all"
	if len(countries) > 0 {
		scope = "selected"
	}
	b := accountBody(id, "warehouse", scope, countries...)
	delete(b, "role")
	b["reason"] = "隔离测试申请"
	w := f.request(t, finance, "POST", "/api/warehouse-requests", b, nil)
	expectHTTPStatus(t, w, 200)
	var v auth.WarehouseRequest
	decodeHTTPResponse(t, w, &v)
	if v.ID == "" || v.AccountID != id || v.Status != "pending" {
		t.Fatal("invalid pending warehouse request")
	}
	return v
}
func accountSetup(t *testing.T, f *httpSecurityFixture, id, role string) *httpSecurityClient {
	t.Helper()
	c := &httpSecurityClient{id: id}
	w := f.request(t, c, "POST", "/api/auth/setup/start", map[string]any{"id": id}, nil)
	expectHTTPStatus(t, w, 200)
	var grant auth.Grant
	decodeHTTPResponse(t, w, &grant)
	if grant.Session.Role != role || grant.Session.Scope != "setup" {
		t.Fatal("new account received wrong role or unrestricted session")
	}
	expectHTTPStatus(t, f.request(t, c, "GET", "/api/runs", nil, nil), 403)
	expectHTTPStatus(t, f.request(t, c, "POST", "/api/auth/setup/password", map[string]any{"password": "weak"}, nil), 422)
	expectHTTPStatus(t, f.request(t, c, "POST", "/api/auth/setup/password", map[string]any{"password": httpFixturePassword}, nil), 200)
	expectHTTPStatus(t, f.request(t, c, "POST", "/api/auth/setup/complete", map[string]any{"saved": true}, nil), 403)
	w = f.request(t, c, "POST", "/api/auth/totp/enroll", map[string]any{}, nil)
	expectHTTPStatus(t, w, 200)
	var en auth.Enrollment
	decodeHTTPResponse(t, w, &en)
	code, err := totp.GenerateCode(en.Secret, time.Now())
	if err != nil {
		t.Fatal("synthetic factor generation failed")
	}
	w = f.request(t, c, "POST", "/api/auth/totp/confirm", map[string]any{"code": code}, nil)
	expectHTTPStatus(t, w, 200)
	decodeHTTPResponse(t, w, &grant)
	c.codes = append([]string(nil), grant.RecoveryCodes...)
	expectHTTPStatus(t, f.request(t, c, "GET", "/api/runs", nil, nil), 403)
	expectHTTPStatus(t, f.request(t, c, "POST", "/api/auth/setup/complete", map[string]any{"saved": false}, nil), 422)
	expectHTTPStatus(t, f.request(t, c, "POST", "/api/auth/setup/complete", map[string]any{"saved": true}, nil), 200)
	expectHTTPStatus(t, f.request(t, c, "GET", "/api/runs", nil, nil), 200)
	return c
}
func TestHTTP_TC56_AccountManagementRoleBoundary(t *testing.T) {
	f := httpSecurityTest(t)
	admin := f.configured(t, "http-admin")
	finance := f.configured(t, "http-finance")
	owner := f.configured(t, "http-owner")
	for _, path := range []string{"/api/admin/accounts", "/api/admin/account-audit", "/api/warehouse-requests", "/api/countries"} {
		expectHTTPStatus(t, f.request(t, nil, "GET", path, nil, map[string]string{"X-Role": "admin"}), 401)
	}
	for _, c := range []*httpSecurityClient{finance, owner} {
		for _, path := range []string{"/api/admin/accounts", "/api/admin/account-audit"} {
			expectHTTPStatus(t, f.request(t, c, "GET", path, nil, nil), 403)
		}
		expectHTTPStatus(t, f.request(t, c, "POST", "/api/admin/accounts", accountBody("forbidden-new", "warehouse", "all"), map[string]string{"X-Role": "admin"}), 403)
		expectHTTPStatus(t, f.request(t, c, "PATCH", "/api/admin/accounts/http-other", accountPatch("all"), nil), 403)
		for _, action := range []string{"approve", "reject"} {
			body := map[string]any{}
			if action == "reject" {
				body["reason"] = "isolated"
			}
			expectHTTPStatus(t, f.request(t, c, "POST", "/api/warehouse-requests/unknown/"+action, body, nil), 403)
		}
	}
	for _, c := range []*httpSecurityClient{admin, owner} {
		expectHTTPStatus(t, f.request(t, c, "POST", "/api/warehouse-requests", accountBody("forbidden-req", "warehouse", "all"), nil), 403)
	}
	expectHTTPStatus(t, f.request(t, owner, "GET", "/api/warehouse-requests", nil, nil), 403)
	for _, c := range []*httpSecurityClient{admin, finance, owner} {
		expectHTTPStatus(t, f.request(t, c, "GET", "/api/countries", nil, nil), 200)
	}
	if httpSecurityCount(t, f.server.Store.DB, "SELECT count(*) FROM account_audit") != 0 {
		t.Fatal("unauthorized management wrote audit")
	}
}
func TestHTTP_TC56_AccountWritesRequireOriginAndCSRF(t *testing.T) {
	f := httpSecurityTest(t)
	admin := f.configured(t, "http-admin")
	finance := f.configured(t, "http-finance")
	r := accountRequest(t, f, finance, "csrf-request")
	before := httpSecurityCount(t, f.server.Store.DB, "SELECT count(*) FROM account_audit")
	for _, op := range []struct {
		c            *httpSecurityClient
		method, path string
		body         any
	}{
		{admin, "POST", "/api/admin/accounts", accountBody("csrf-create", "warehouse", "all")},
		{admin, "PATCH", "/api/admin/accounts/http-other", accountPatch("all")},
		{finance, "POST", "/api/warehouse-requests", accountBody("csrf-apply", "warehouse", "all")},
		{admin, "POST", "/api/warehouse-requests/" + r.ID + "/approve", map[string]any{}},
		{admin, "POST", "/api/warehouse-requests/" + r.ID + "/reject", map[string]any{"reason": "isolated"}},
	} {
		for _, h := range []map[string]string{{"Origin": ""}, {"Origin": "http://evil.invalid"}, {"X-CSRF-Token": ""}, {"X-CSRF-Token": "forged"}} {
			expectHTTPStatus(t, f.request(t, op.c, op.method, op.path, op.body, h), 403)
		}
	}
	if httpSecurityCount(t, f.server.Store.DB, "SELECT count(*) FROM account_audit") != before || httpSecurityCount(t, f.server.Store.DB, "SELECT count(*) FROM warehouse_requests WHERE status='pending'") != 1 || httpSecurityCount(t, f.server.Store.DB, "SELECT count(*) FROM auth_users") != 4 {
		t.Fatal("boundary rejection mutated account state")
	}
}
func TestHTTP_TC56_AccountValidationAndImmutableIdentity(t *testing.T) {
	f := httpSecurityTest(t)
	admin := f.configured(t, "http-admin")
	for _, b := range []map[string]any{accountBody("bad-admin", "admin", "all"), accountBody("BAD NAME", "finance", "all"), accountBody("empty-selected", "warehouse", "selected"), accountBody("unknown-country", "warehouse", "selected", "UNKNOWN"), accountBody("mixed-all", "finance", "all", "UK")} {
		expectHTTPStatus(t, f.request(t, admin, "POST", "/api/admin/accounts", b, nil), 422)
	}
	b := accountBody("valid-account", "finance", "selected", "UK", "UK")
	expectHTTPStatus(t, f.request(t, admin, "POST", "/api/admin/accounts", b, nil), 200)
	expectHTTPStatus(t, f.request(t, admin, "POST", "/api/admin/accounts", b, nil), 409)
	if httpSecurityCount(t, f.server.Store.DB, "SELECT count(*) FROM auth_user_countries WHERE user_id='valid-account'") != 1 {
		t.Fatal("country canonicalization failed")
	}
	for _, key := range []string{"id", "role"} {
		patch := accountPatch("all")
		patch[key] = "changed"
		expectHTTPStatus(t, f.request(t, admin, "PATCH", "/api/admin/accounts/valid-account", patch, nil), 422)
	}
	expectHTTPStatus(t, f.request(t, admin, "PATCH", "/api/admin/accounts/http-admin", accountPatch("all"), nil), 403)
}
func TestHTTP_TC56_NewAccountUsesActualFirstConfiguration(t *testing.T) {
	f := httpSecurityTest(t)
	admin := f.configured(t, "http-admin")
	w := f.request(t, admin, "POST", "/api/admin/accounts", accountBody("fresh-warehouse", "warehouse", "selected", "UK"), nil)
	expectHTTPStatus(t, w, 200)
	for _, secret := range []string{"password_hash", "totp_secret", "recovery_codes", "bootstrap", "token"} {
		if strings.Contains(w.Body.String(), secret) {
			t.Fatal("account creation response exposed authentication material")
		}
	}
	if httpSecurityCount(t, f.server.Store.DB, "SELECT count(*) FROM auth_users WHERE id='fresh-warehouse' AND state='new' AND password_hash=''") != 1 || httpSecurityCount(t, f.server.Store.DB, "SELECT count(*) FROM auth_sessions WHERE user_id='fresh-warehouse'") != 0 {
		t.Fatal("account creation bypassed first-use setup")
	}
	w = f.request(t, nil, "GET", "/api/auth/setup/accounts", nil, nil)
	expectHTTPStatus(t, w, 200)
	var identities []auth.Identity
	decodeHTTPResponse(t, w, &identities)
	found := false
	for _, id := range identities {
		if id.ID == "fresh-warehouse" && id.Role == "warehouse" {
			found = true
		}
	}
	if !found {
		t.Fatal("new account absent from first-use selection")
	}
	c := accountSetup(t, f, "fresh-warehouse", "warehouse")
	expectHTTPStatus(t, f.request(t, c, "POST", "/api/auth/setup/start", map[string]any{"id": c.id}, nil), 401)
}
func TestHTTP_TC57_ScopeChangeRevokesSessionAndBumpsBothVersions(t *testing.T) {
	f := httpSecurityTest(t)
	admin := f.configured(t, "http-admin")
	owner := f.configured(t, "http-owner")
	version := httpSecurityCount(t, f.server.Store.DB, "SELECT version FROM auth_users WHERE id='http-owner'")
	w := f.request(t, admin, "PATCH", "/api/admin/accounts/http-owner", accountPatch("all"), nil)
	expectHTTPStatus(t, w, 200)
	expectHTTPStatus(t, f.request(t, owner, "GET", "/api/runs", nil, nil), 200)
	w = f.request(t, admin, "PATCH", "/api/admin/accounts/http-owner", accountPatch("selected", "UK"), nil)
	expectHTTPStatus(t, w, 200)
	if httpSecurityCount(t, f.server.Store.DB, "SELECT version FROM auth_users WHERE id='http-owner'") != version+1 || httpSecurityCount(t, f.server.Store.DB, "SELECT permissions_version FROM auth_account_profiles WHERE user_id='http-owner'") != 2 {
		t.Fatal("scope change did not advance authentication and permission versions")
	}
	expectHTTPStatus(t, f.request(t, owner, "GET", "/api/runs", nil, nil), 401)
}

func TestHTTP_TC57_ApprovedRequestTracksActualAccountSetupState(t *testing.T) {
	f := httpSecurityTest(t)
	admin := f.configured(t, "http-admin")
	finance := f.configured(t, "http-finance")
	r := accountRequest(t, f, finance, "requested-warehouse")
	check := func(state string) {
		t.Helper()
		for _, client := range []*httpSecurityClient{finance, admin} {
			w := f.request(t, client, "GET", "/api/warehouse-requests", nil, nil)
			expectHTTPStatus(t, w, 200)
			var list []auth.WarehouseRequest
			decodeHTTPResponse(t, w, &list)
			if len(list) != 1 || list[0].ID != r.ID || list[0].AccountState != state {
				t.Fatalf("request setup state must reflect current account state %q", state)
			}
		}
	}
	check("") // A pending request has no opened account or factor status.
	w := f.request(t, admin, "POST", "/api/warehouse-requests/"+r.ID+"/approve", map[string]any{}, nil)
	expectHTTPStatus(t, w, 200)
	var approved auth.WarehouseRequest
	decodeHTTPResponse(t, w, &approved)
	if approved.AccountState != "new" {
		t.Fatal("approval must report an unconfigured account")
	}
	check("new")
	c := &httpSecurityClient{id: r.AccountID}
	expectHTTPStatus(t, f.request(t, c, "POST", "/api/auth/setup/start", map[string]any{"id": c.id}, nil), 200)
	expectHTTPStatus(t, f.request(t, c, "POST", "/api/auth/setup/password", map[string]any{"password": httpFixturePassword}, nil), 200)
	check("password")
	w = f.request(t, c, "POST", "/api/auth/totp/enroll", map[string]any{}, nil)
	expectHTTPStatus(t, w, 200)
	var en auth.Enrollment
	decodeHTTPResponse(t, w, &en)
	check("password") // Merely obtaining a QR code has not verified a factor.
	code, err := totp.GenerateCode(en.Secret, time.Now())
	if err != nil {
		t.Fatal("synthetic request fixture TOTP generation failed")
	}
	expectHTTPStatus(t, f.request(t, c, "POST", "/api/auth/totp/confirm", map[string]any{"code": code}, nil), 200)
	check("factor")
	expectHTTPStatus(t, f.request(t, c, "POST", "/api/auth/setup/complete", map[string]any{"saved": true}, nil), 200)
	check("complete")
}
func TestHTTP_TC57_UnfinishedAssigneePreventsScopeShrink(t *testing.T) {
	f := httpSecurityTest(t)
	admin := f.configured(t, "http-admin")
	finance := f.configured(t, "http-finance")
	owner := f.configured(t, "http-owner")
	accountSQL(t, f, "UPDATE normalized_rows SET country='DE' WHERE id='0:4'")
	f.assignHTTP(t, finance, 0, "http-owner")
	before := httpSecurityCount(t, f.server.Store.DB, "SELECT count(*) FROM account_audit")
	version := httpSecurityCount(t, f.server.Store.DB, "SELECT version FROM auth_users WHERE id='http-owner'")
	expectHTTPStatus(t, f.request(t, admin, "PATCH", "/api/admin/accounts/http-owner", accountPatch("selected", "UK"), nil), 409)
	expectHTTPStatus(t, f.request(t, owner, "GET", "/api/runs", nil, nil), 200)
	if httpSecurityCount(t, f.server.Store.DB, "SELECT count(*) FROM account_audit") != before || httpSecurityCount(t, f.server.Store.DB, "SELECT version FROM auth_users WHERE id='http-owner'") != version {
		t.Fatal("rejected scope shrink mutated account or audit")
	}
}
func TestHTTP_TC58_FinanceSubsetAndPendingNameUniqueness(t *testing.T) {
	f := httpSecurityTest(t)
	admin := f.configured(t, "http-admin")
	global := f.configured(t, "http-finance")
	accountSQL(t, f, "UPDATE normalized_rows SET country='DE' WHERE id='0:4'")
	expectHTTPStatus(t, f.request(t, admin, "POST", "/api/admin/accounts", accountBody("limited-finance", "finance", "selected", "UK"), nil), 200)
	finance := accountSetup(t, f, "limited-finance", "finance")
	w := f.request(t, finance, "GET", "/api/countries", nil, nil)
	expectHTTPStatus(t, w, 200)
	var countries []string
	decodeHTTPResponse(t, w, &countries)
	if len(countries) != 1 || countries[0] != "UK" {
		t.Fatal("country selector exceeded finance scope")
	}
	for _, b := range []map[string]any{accountBody("scope-all", "warehouse", "all"), accountBody("scope-de", "warehouse", "selected", "DE")} {
		expectHTTPStatus(t, f.request(t, finance, "POST", "/api/warehouse-requests", b, nil), 403)
	}
	b := accountBody("role-spoof", "finance", "selected", "UK")
	expectHTTPStatus(t, f.request(t, finance, "POST", "/api/warehouse-requests", b, nil), 422)
	r := accountRequest(t, f, finance, "pending-name", "UK")
	expectHTTPStatus(t, f.request(t, global, "POST", "/api/warehouse-requests", accountBody("pending-name", "warehouse", "all"), nil), 409)
	expectHTTPStatus(t, f.request(t, admin, "POST", "/api/admin/accounts", accountBody("pending-name", "warehouse", "all"), nil), 409)
	for _, check := range []struct {
		client *httpSecurityClient
		want   int
	}{{finance, 1}, {global, 0}, {admin, 1}} {
		w := f.request(t, check.client, "GET", "/api/warehouse-requests", nil, nil)
		expectHTTPStatus(t, w, 200)
		var requests []auth.WarehouseRequest
		decodeHTTPResponse(t, w, &requests)
		if len(requests) != check.want || check.want == 1 && requests[0].ID != r.ID {
			t.Fatal("warehouse request list crossed requester boundary")
		}
	}
	// Pending requests retain the originally requested scope. Review must recheck
	// the requester's current authority rather than trusting the old snapshot.
	expectHTTPStatus(t, f.request(t, admin, "PATCH", "/api/admin/accounts/limited-finance", accountPatch("selected", "DE"), nil), 200)
	expectHTTPStatus(t, f.request(t, admin, "POST", "/api/warehouse-requests/"+r.ID+"/approve", map[string]any{}, nil), 409)
	if httpSecurityCount(t, f.server.Store.DB, "SELECT count(*) FROM auth_users WHERE id='pending-name'") != 0 || httpSecurityCount(t, f.server.Store.DB, "SELECT count(*) FROM warehouse_requests WHERE id=? AND status='pending'", r.ID) != 1 {
		t.Fatal("stale requester authority was approved or partially changed")
	}
}
func TestHTTP_TC58_ConcurrentApprovalCreatesExactlyOneUntouchedAccount(t *testing.T) {
	f := httpSecurityTest(t)
	admin := f.configured(t, "http-admin")
	finance := f.configured(t, "http-finance")
	r := accountRequest(t, f, finance, "approved-warehouse", "UK")
	var wg sync.WaitGroup
	responses := make(chan *httptest.ResponseRecorder, 2)
	start := make(chan struct{})
	for i := 0; i < 2; i++ {
		c := *admin
		cookie := *admin.cookie
		c.cookie = &cookie
		wg.Add(1)
		go func(client httpSecurityClient) {
			defer wg.Done()
			<-start
			responses <- f.request(t, &client, "POST", "/api/warehouse-requests/"+r.ID+"/approve", map[string]any{}, nil)
		}(c)
	}
	close(start)
	wg.Wait()
	close(responses)
	statuses := map[int]int{}
	for w := range responses {
		statuses[w.Code]++
	}
	if statuses[200] != 1 || statuses[409] != 1 {
		t.Fatalf("concurrent review status counts=%v, want one 200 and one 409", statuses)
	}
	if httpSecurityCount(t, f.server.Store.DB, "SELECT count(*) FROM auth_users WHERE id='approved-warehouse' AND role='warehouse' AND state='new' AND password_hash=''") != 1 || httpSecurityCount(t, f.server.Store.DB, "SELECT count(*) FROM account_audit WHERE action='approved' AND request_id=?", r.ID) != 1 {
		t.Fatal("concurrent approval duplicated account or audit")
	}
	accountSetup(t, f, "approved-warehouse", "warehouse")
	r = accountRequest(t, f, finance, "rejected-warehouse")
	expectHTTPStatus(t, f.request(t, admin, "POST", "/api/warehouse-requests/"+r.ID+"/reject", map[string]any{"reason": " "}, nil), 422)
	w := f.request(t, admin, "POST", "/api/warehouse-requests/"+r.ID+"/reject", map[string]any{"reason": "范围不适用"}, nil)
	expectHTTPStatus(t, w, 200)
	var rejected auth.WarehouseRequest
	decodeHTTPResponse(t, w, &rejected)
	if rejected.Status != "rejected" || rejected.RejectionReason != "范围不适用" || rejected.ReviewedBy != admin.id {
		t.Fatal("rejection lost review metadata")
	}
	expectHTTPStatus(t, f.request(t, admin, "POST", "/api/warehouse-requests/"+r.ID+"/approve", map[string]any{}, nil), 409)
	if httpSecurityCount(t, f.server.Store.DB, "SELECT count(*) FROM auth_users WHERE id='rejected-warehouse'") != 0 {
		t.Fatal("rejection created an account")
	}
	// A direct admin creation and a finance application compete for one name;
	// neither transaction may leave the name both provisioned and pending.
	responses = make(chan *httptest.ResponseRecorder, 2)
	start = make(chan struct{})
	for _, op := range []struct {
		client *httpSecurityClient
		path   string
	}{{admin, "/api/admin/accounts"}, {finance, "/api/warehouse-requests"}} {
		c := *op.client
		cookie := *op.client.cookie
		c.cookie = &cookie
		wg.Add(1)
		go func(client httpSecurityClient, path string) {
			defer wg.Done()
			<-start
			responses <- f.request(t, &client, "POST", path, accountBody("raced-name", "warehouse", "all"), nil)
		}(c, op.path)
	}
	close(start)
	wg.Wait()
	close(responses)
	statuses = map[int]int{}
	for w := range responses {
		statuses[w.Code]++
	}
	if statuses[200] != 1 || statuses[409] != 1 {
		t.Fatalf("concurrent name reservation status counts=%v, want one 200 and one 409", statuses)
	}
	if httpSecurityCount(t, f.server.Store.DB, "SELECT (SELECT count(*) FROM auth_users WHERE id='raced-name')+(SELECT count(*) FROM warehouse_requests WHERE account_id='raced-name' AND status='pending')") != 1 {
		t.Fatal("concurrent create/request violated account name uniqueness")
	}
}
func TestHTTP_TC61_AccountAuditFailureRollsBackAllManagementWrites(t *testing.T) {
	f := httpSecurityTest(t)
	admin := f.configured(t, "http-admin")
	finance := f.configured(t, "http-finance")
	owner := f.configured(t, "http-owner")
	r := accountRequest(t, f, finance, "rollback-approve")
	before := httpSecurityCount(t, f.server.Store.DB, "SELECT count(*) FROM account_audit")
	version := httpSecurityCount(t, f.server.Store.DB, "SELECT version FROM auth_users WHERE id='http-owner'")
	profiles := httpSecurityCount(t, f.server.Store.DB, "SELECT count(*) FROM auth_account_profiles WHERE user_id='http-owner'")
	accountSQL(t, f, "CREATE TRIGGER isolated_account_audit_failure BEFORE INSERT ON account_audit BEGIN SELECT RAISE(ABORT,'synthetic audit failure'); END")
	for _, op := range []struct {
		c            *httpSecurityClient
		method, path string
		body         any
	}{
		{admin, "POST", "/api/admin/accounts", accountBody("rollback-create", "warehouse", "all")},
		{admin, "PATCH", "/api/admin/accounts/http-owner", accountPatch("selected", "UK")},
		{finance, "POST", "/api/warehouse-requests", accountBody("rollback-request", "warehouse", "all")},
		{admin, "POST", "/api/warehouse-requests/" + r.ID + "/approve", map[string]any{}},
		{admin, "POST", "/api/warehouse-requests/" + r.ID + "/reject", map[string]any{"reason": "isolated rejection"}},
	} {
		expectHTTPStatus(t, f.request(t, op.c, op.method, op.path, op.body, nil), 500)
	}
	if httpSecurityCount(t, f.server.Store.DB, "SELECT count(*) FROM auth_users") != 4 || httpSecurityCount(t, f.server.Store.DB, "SELECT count(*) FROM warehouse_requests WHERE status='pending'") != 1 || httpSecurityCount(t, f.server.Store.DB, "SELECT count(*) FROM account_audit") != before || httpSecurityCount(t, f.server.Store.DB, "SELECT version FROM auth_users WHERE id='http-owner'") != version || httpSecurityCount(t, f.server.Store.DB, "SELECT count(*) FROM auth_account_profiles WHERE user_id='http-owner'") != profiles {
		t.Fatal("audit failure left partial account/request/profile state")
	}
	expectHTTPStatus(t, f.request(t, owner, "GET", "/api/runs", nil, nil), 200)
}
func TestHTTP_TC61_AppendOnlyAuditAndLegacyCredentialSessionCompatibility(t *testing.T) {
	f := httpSecurityTest(t)
	admin := f.configured(t, "http-admin")
	finance := f.configured(t, "http-finance")
	var password string
	if err := f.server.Store.DB.QueryRow("SELECT password_hash FROM auth_users WHERE id='http-finance'").Scan(&password); err != nil {
		t.Fatal("snapshot legacy password failed")
	}
	recovery := httpSecurityCount(t, f.server.Store.DB, "SELECT count(*) FROM auth_recovery WHERE user_id='http-finance'")
	w := f.request(t, admin, "GET", "/api/admin/accounts", nil, nil)
	expectHTTPStatus(t, w, 200)
	var accounts []auth.AccountView
	decodeHTTPResponse(t, w, &accounts)
	for _, a := range accounts {
		if a.CountryScope != "all" || a.PermissionsVersion != 1 {
			t.Fatal("legacy account failed global-scope migration defaults")
		}
	}
	expectHTTPStatus(t, f.request(t, admin, "PATCH", "/api/admin/accounts/http-finance", accountPatch("all"), nil), 200)
	expectHTTPStatus(t, f.request(t, finance, "GET", "/api/runs", nil, nil), 200)
	var after string
	if err := f.server.Store.DB.QueryRow("SELECT password_hash FROM auth_users WHERE id='http-finance'").Scan(&after); err != nil {
		t.Fatal("read legacy password failed")
	}
	if password != after || httpSecurityCount(t, f.server.Store.DB, "SELECT count(*) FROM auth_recovery WHERE user_id='http-finance'") != recovery {
		t.Fatal("display-only account update changed legacy credentials")
	}
	for _, query := range []string{"UPDATE account_audit SET actor='tampered'", "DELETE FROM account_audit"} {
		if _, err := f.server.Store.DB.Exec(query); err == nil {
			t.Fatal("account audit allowed historical rewrite")
		}
	}
	w = f.request(t, admin, "GET", "/api/admin/account-audit", nil, nil)
	expectHTTPStatus(t, w, 200)
	for _, field := range []string{"password_hash", "totp_secret", "recovery_codes", "credential_json"} {
		if strings.Contains(w.Body.String(), field) {
			t.Fatal("management audit exposed authentication material")
		}
	}
	if httpSecurityCount(t, f.server.Store.DB, "SELECT count(*) FROM account_audit") != 1 {
		t.Fatal("audit history changed after rejected rewrites")
	}
}
