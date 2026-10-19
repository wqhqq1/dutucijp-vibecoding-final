package web

// FR-19 exercises the real account-management and authentication boundaries.
// Only synthetic source countries are changed directly; permissions, business
// sessions, assignments, snapshots and batches always use their HTTP routes.
import (
	"encoding/csv"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/pquerna/otp/totp"
	"returnsrecon/internal/auth"
	"returnsrecon/internal/store"
)

func countryHTTPFixture(t *testing.T) (*httpSecurityFixture, *httpSecurityClient) {
	t.Helper()
	f := httpSecurityTest(t)
	if _, err := f.server.Store.DB.Exec("UPDATE normalized_rows SET country='DE' WHERE id IN ('0:3','0:6')"); err != nil {
		t.Fatal(err)
	}
	return f, f.configured(t, "http-admin")
}

func countryHTTPSet(t *testing.T, f *httpSecurityFixture, admin *httpSecurityClient, id string, countries ...string) {
	t.Helper()
	scope := "selected"
	if len(countries) == 0 {
		scope = "all"
		countries = []string{}
	}
	w := f.request(t, admin, "PATCH", "/api/admin/accounts/"+id, map[string]any{"display_name": "隔离国家测试账户", "country_scope": scope, "countries": countries}, nil)
	expectHTTPStatus(t, w, 200)
}

// Retain only this fixture's synthetic enrollment secret, in memory, so a scope
// change can be followed by a genuine password plus TOTP login without a bypass.
func countryHTTPConfigured(t *testing.T, f *httpSecurityFixture, id string) (*httpSecurityClient, string) {
	t.Helper()
	c := &httpSecurityClient{id: id}
	expectHTTPStatus(t, f.request(t, c, "POST", "/api/auth/setup/start", map[string]any{"id": id}, nil), 200)
	expectHTTPStatus(t, f.request(t, c, "POST", "/api/auth/setup/password", map[string]any{"password": httpFixturePassword}, nil), 200)
	w := f.request(t, c, "POST", "/api/auth/totp/enroll", map[string]any{}, nil)
	expectHTTPStatus(t, w, 200)
	var en auth.Enrollment
	decodeHTTPResponse(t, w, &en)
	code, err := totp.GenerateCode(en.Secret, time.Now())
	if err != nil {
		t.Fatal("synthetic country fixture TOTP generation failed")
	}
	expectHTTPStatus(t, f.request(t, c, "POST", "/api/auth/totp/confirm", map[string]any{"code": code}, nil), 200)
	expectHTTPStatus(t, f.request(t, c, "POST", "/api/auth/setup/complete", map[string]any{"saved": true}, nil), 200)
	return c, en.Secret
}

func countryHTTPLogin(t *testing.T, f *httpSecurityFixture, id, secret string) *httpSecurityClient {
	t.Helper()
	c := &httpSecurityClient{id: id}
	expectHTTPStatus(t, f.request(t, c, "POST", "/api/auth/login", map[string]any{"id": id, "password": httpFixturePassword}, nil), 200)
	// The accepted adjacent step avoids replay of enrollment's current step.
	code, err := totp.GenerateCode(secret, time.Now().Add(30*time.Second))
	if err != nil {
		t.Fatal("synthetic country fixture login TOTP generation failed")
	}
	expectHTTPStatus(t, f.request(t, c, "POST", "/api/auth/totp/login", map[string]any{"code": code}, nil), 200)
	return c
}

func countryHTTPDenied(t *testing.T, status int) {
	t.Helper()
	if status != 403 && status != 404 {
		t.Fatalf("out-of-country request status=%d, want 403 or 404", status)
	}
}

func TestHTTP_TC59_CountryListsPaginationAndCSV(t *testing.T) {
	f, admin := countryHTTPFixture(t)
	countryHTTPSet(t, f, admin, "http-finance", "UK")
	finance, _ := countryHTTPConfigured(t, f, "http-finance")
	seen := map[string]bool{}
	for page := 1; page <= 4; page++ {
		w := f.request(t, finance, "GET", fmt.Sprintf("/api/transactions?run_id=%s&page_size=1&page=%d", f.run, page), nil, map[string]string{"X-Role": "admin", "X-Countries": "DE"})
		expectHTTPStatus(t, w, 200)
		var out store.TransactionPage
		decodeHTTPResponse(t, w, &out)
		if out.Total != 4 || len(out.Items) != 1 || out.Items[0].Country != "UK" || seen[out.Items[0].ID] {
			t.Fatal("country transaction pagination leaked or omitted records")
		}
		seen[out.Items[0].ID] = true
	}
	for _, tc := range []struct {
		client *httpSecurityClient
		query  string
		total  int
	}{{finance, "", 2}, {finance, "&country=DE", 0}, {admin, "", 3}, {admin, "&country=DE", 1}} {
		w := f.request(t, tc.client, "GET", "/api/cases?run_id="+f.run+"&page_size=1"+tc.query, nil, nil)
		expectHTTPStatus(t, w, 200)
		var out store.CasePage
		decodeHTTPResponse(t, w, &out)
		if out.Total != tc.total || len(out.Items) != min(1, tc.total) {
			t.Fatalf("country case total=%d, want=%d", out.Total, tc.total)
		}
		if tc.client == finance && len(out.Items) != 0 && out.Items[0].CancelID == "0:6" {
			t.Fatal("country case page leaked the unauthorized cancellation")
		}
	}
	for _, country := range []string{"", "&country=DE"} {
		w := f.request(t, finance, "GET", "/api/exports?run_id="+f.run+"&page_size=1"+country, nil, nil)
		expectHTTPStatus(t, w, 200)
		rows, err := csv.NewReader(strings.NewReader(w.Body.String())).ReadAll()
		want := 5
		if country != "" {
			want = 1
		}
		if err != nil || len(rows) != want {
			t.Fatal("country CSV did not export exactly the authorized filtered records")
		}
		for _, row := range rows[1:] {
			if row[16] != "UK" {
				t.Fatal("country CSV leaked an unauthorized country")
			}
		}
	}
}

func TestHTTP_TC59_CountryByIDCandidatesEvidenceAndAudit(t *testing.T) {
	f, admin := countryHTTPFixture(t)
	countryHTTPSet(t, f, admin, "http-finance", "UK")
	finance, _ := countryHTTPConfigured(t, f, "http-finance")
	for _, suffix := range []string{"", "/evidence", "/candidates", "/audit"} {
		w := f.request(t, finance, "GET", "/api/cases/"+f.cases[2]+suffix, nil, nil)
		countryHTTPDenied(t, w.Code)
		expectHTTPStatus(t, f.request(t, finance, "GET", "/api/cases/"+f.cases[0]+suffix, nil, nil), 200)
	}
	countryHTTPDenied(t, f.request(t, finance, "GET", "/api/audit?case_id="+f.cases[2], nil, nil).Code)
	w := f.request(t, finance, "GET", "/api/audit", nil, nil)
	expectHTTPStatus(t, w, 200)
	var audit []struct {
		Actor string `json:"actor"`
	}
	decodeHTTPResponse(t, w, &audit)
	for _, event := range audit {
		if event.Actor != finance.id {
			t.Fatal("selected-country audit leaked another account's authentication activity")
		}
	}
	w = f.request(t, finance, "GET", "/api/cases/"+f.cases[0]+"/evidence", nil, nil)
	expectHTTPStatus(t, w, 200)
	var evidence store.CaseEvidencePage
	decodeHTTPResponse(t, w, &evidence)
	if evidence.Cancel.Country != "UK" || evidence.Candidates.Total != 1 {
		t.Fatal("country evidence did not restrict the cancellation and candidate count")
	}
	for _, candidate := range evidence.Candidates.Items {
		if candidate.Country != "UK" {
			t.Fatal("country evidence leaked another country's candidate")
		}
	}
	for _, decision := range evidence.DecisionEvidence {
		if decision.Country != "UK" {
			t.Fatal("country decision evidence leaked another country's source")
		}
	}
	before := httpSecurityCount(t, f.server.Store.DB, "SELECT count(*) FROM audit_events")
	countryHTTPDenied(t, f.request(t, finance, "POST", "/api/cases/"+f.cases[2]+"/assign", map[string]any{"assignee": "http-owner"}, nil).Code)
	if httpSecurityCount(t, f.server.Store.DB, "SELECT count(*) FROM audit_events") != before {
		t.Fatal("out-of-country mutation wrote an audit event")
	}
}

func TestHTTP_TC59_CountryOverviewReviewCountsAndGroups(t *testing.T) {
	f, admin := countryHTTPFixture(t)
	countryHTTPSet(t, f, admin, "http-finance", "UK")
	finance, _ := countryHTTPConfigured(t, f, "http-finance")
	w := f.request(t, finance, "GET", "/api/overview?run_id="+f.run, nil, nil)
	expectHTTPStatus(t, w, 200)
	var overview struct {
		Cases       map[string]int `json:"cases"`
		Unallocated int            `json:"currently_unallocated"`
		Reviewed    int            `json:"reviewed_allocations"`
		Initial     any            `json:"initial_reconciliation"`
	}
	decodeHTTPResponse(t, w, &overview)
	if overview.Cases["total"] != 2 || overview.Cases["pending"] != 2 || overview.Unallocated != 2 || overview.Reviewed != 0 {
		t.Fatal("country overview used global business counts")
	}
	if overview.Initial != nil {
		t.Fatal("selected-country overview exposed the global initial summary")
	}
	set := reviewHTTPGenerate(t, f, finance)
	for page := 1; page <= 2; page++ {
		w = f.request(t, finance, "GET", fmt.Sprintf("/api/review-sets/%s?page_size=1&page=%d", set, page), nil, nil)
		expectHTTPStatus(t, w, 200)
		var review store.ReviewPage
		decodeHTTPResponse(t, w, &review)
		if review.Total != 2 || review.Counts["total"] != 2 || len(review.Items) != 1 || review.Items[0].Cancel.Country != "UK" {
			t.Fatal("country review page/counts leaked another country")
		}
		for _, option := range review.Items[0].Options {
			if option.SaleID == "0:3" {
				t.Fatal("country recommendation offered an unauthorized sale")
			}
		}
	}
	w = f.request(t, finance, "GET", "/api/review-sets/"+set+"/groups?group_by=reason", nil, nil)
	expectHTTPStatus(t, w, 200)
	var groups struct {
		Items []store.ReviewGroup `json:"items"`
	}
	decodeHTTPResponse(t, w, &groups)
	total := 0
	for _, group := range groups.Items {
		total += group.Count
	}
	if total != 2 {
		t.Fatal("country review group counts included hidden cases")
	}
}

func TestHTTP_TC59_CountryScopeIntersectsWarehouseOwnership(t *testing.T) {
	f, admin := countryHTTPFixture(t)
	countryHTTPSet(t, f, admin, "http-owner", "UK")
	owner, _ := countryHTTPConfigured(t, f, "http-owner")
	f.assignHTTP(t, admin, 0, "http-owner")
	f.assignHTTP(t, admin, 1, "http-other")
	f.assignHTTP(t, admin, 2, "http-other")
	w := f.request(t, owner, "GET", "/api/cases?run_id="+f.run, nil, nil)
	expectHTTPStatus(t, w, 200)
	var cases store.CasePage
	decodeHTTPResponse(t, w, &cases)
	if cases.Total != 1 || len(cases.Items) != 1 || cases.Items[0].ID != f.cases[0] {
		t.Fatal("warehouse country scope broadened ownership")
	}
	w = f.request(t, owner, "GET", "/api/transactions?run_id="+f.run, nil, nil)
	expectHTTPStatus(t, w, 200)
	var rows store.TransactionPage
	decodeHTTPResponse(t, w, &rows)
	if rows.Total != 2 {
		t.Fatal("warehouse related rows did not intersect authorized countries")
	}
	for _, row := range rows.Items {
		if row.Country != "UK" || row.ID != "0:2" && row.ID != "0:4" {
			t.Fatal("warehouse related rows leaked country or ownership")
		}
	}
	for _, id := range f.cases[1:] {
		countryHTTPDenied(t, f.request(t, owner, "GET", "/api/cases/"+id+"/evidence", nil, nil).Code)
	}
	set := reviewHTTPGenerate(t, f, owner)
	w = f.request(t, owner, "GET", "/api/review-sets/"+set, nil, nil)
	expectHTTPStatus(t, w, 200)
	var review store.ReviewPage
	decodeHTTPResponse(t, w, &review)
	if review.Total != 1 || review.Counts["total"] != 1 || review.Items[0].Case.ID != f.cases[0] {
		t.Fatal("warehouse review lost country-and-owner intersection")
	}
}

func TestHTTP_TC60_AssigneeCountrySingleRejectAndBatchSkip(t *testing.T) {
	f, admin := countryHTTPFixture(t)
	countryHTTPSet(t, f, admin, "http-owner", "UK")
	before := httpSecurityCount(t, f.server.Store.DB, "SELECT count(*) FROM audit_events")
	countryHTTPDenied(t, f.request(t, admin, "POST", "/api/cases/"+f.cases[2]+"/assign", map[string]any{"assignee": "http-owner"}, nil).Code)
	if httpSecurityCount(t, f.server.Store.DB, "SELECT count(*) FROM audit_events") != before || httpSecurityCount(t, f.server.Store.DB, "SELECT count(*) FROM cases WHERE assignee='http-owner'") != 0 {
		t.Fatal("out-of-country assignee rejection wrote business state")
	}
	set := reviewHTTPGenerate(t, f, admin)
	w := f.request(t, admin, "POST", "/api/case-batches/preview", map[string]any{"set_id": set, "action": "assign", "all": true, "assignee": "http-owner"}, nil)
	expectHTTPStatus(t, w, 200)
	var batch reviewHTTPBatch
	decodeHTTPResponse(t, w, &batch)
	if batch.Total != 3 || batch.Skipped != 1 {
		t.Fatal("batch preview did not skip just the uncovered country")
	}
	w = f.request(t, admin, "POST", "/api/case-batches/"+batch.ID+"/commit", map[string]any{}, nil)
	expectHTTPStatus(t, w, 200)
	decodeHTTPResponse(t, w, &batch)
	if batch.Succeeded != 2 || batch.Skipped != 1 || httpSecurityCount(t, f.server.Store.DB, "SELECT count(*) FROM cases WHERE assignee='http-owner'") != 2 || httpSecurityCount(t, f.server.Store.DB, "SELECT count(*) FROM audit_events") != before+2 {
		t.Fatal("batch assignment country skip wrote the hidden case or lost allowed work")
	}
	if httpSecurityCount(t, f.server.Store.DB, "SELECT count(*) FROM cases WHERE id=? AND coalesce(assignee,'')<>''", f.cases[2]) != 0 {
		t.Fatal("batch assigned the country not covered by its target")
	}
}

func TestHTTP_TC60_CountryCrossPageBatchFreezesOnlyVisibleCases(t *testing.T) {
	f, admin := countryHTTPFixture(t)
	countryHTTPSet(t, f, admin, "http-finance", "UK")
	finance, _ := countryHTTPConfigured(t, f, "http-finance")
	set := reviewHTTPGenerate(t, f, finance)
	w := f.request(t, finance, "GET", "/api/review-sets/"+set+"?page_size=1", nil, nil)
	expectHTTPStatus(t, w, 200)
	var review store.ReviewPage
	decodeHTTPResponse(t, w, &review)
	if len(review.Items) != 1 || review.Total != 2 {
		t.Fatal("country batch test requires a visible second page")
	}
	expectHTTPStatus(t, f.request(t, finance, "POST", "/api/case-batches/preview", map[string]any{"set_id": set, "action": "assign", "ids": []string{f.cases[2]}, "assignee": "http-owner"}, nil), 403)
	w = f.request(t, finance, "POST", "/api/case-batches/preview", map[string]any{"set_id": set, "action": "assign", "all": true, "filter": map[string]any{"page_size": 1}, "assignee": "http-owner"}, nil)
	expectHTTPStatus(t, w, 200)
	var batch reviewHTTPBatch
	decodeHTTPResponse(t, w, &batch)
	if batch.Total != 2 {
		t.Fatal("country cross-page batch froze a global or page-only selection")
	}
	w = f.request(t, finance, "POST", "/api/case-batches/"+batch.ID+"/commit", map[string]any{}, nil)
	expectHTTPStatus(t, w, 200)
	decodeHTTPResponse(t, w, &batch)
	if batch.Succeeded != 2 || batch.Skipped != 0 || httpSecurityCount(t, f.server.Store.DB, "SELECT count(*) FROM cases WHERE assignee='http-owner'") != 2 || httpSecurityCount(t, f.server.Store.DB, "SELECT count(*) FROM audit_events") != 2 {
		t.Fatal("country cross-page batch did not mutate exactly both authorized cases")
	}
	if httpSecurityCount(t, f.server.Store.DB, "SELECT count(*) FROM cases WHERE id=? AND coalesce(assignee,'')<>''", f.cases[2]) != 0 {
		t.Fatal("cross-page batch assigned the hidden country")
	}
}

func TestHTTP_TC61_CountryChangeRevokesSessionSnapshotBatchAndAdmittedWrite(t *testing.T) {
	f, admin := countryHTTPFixture(t)
	finance, secret := countryHTTPConfigured(t, f, "http-finance")
	set := reviewHTTPGenerate(t, f, finance)
	w := f.request(t, finance, "POST", "/api/case-batches/preview", map[string]any{"set_id": set, "action": "assign", "all": true, "assignee": "http-owner"}, nil)
	expectHTTPStatus(t, w, 200)
	var batch reviewHTTPBatch
	decodeHTTPResponse(t, w, &batch)
	ctx, actor := f.capturedRequestContext(t, finance)
	countryHTTPSet(t, f, admin, "http-finance", "UK")
	expectHTTPStatus(t, f.request(t, finance, "GET", "/api/auth/me", nil, nil), 401)
	expectHTTPStatus(t, f.request(t, finance, "POST", "/api/case-batches/"+batch.ID+"/commit", map[string]any{}, nil), 401)
	finance = countryHTTPLogin(t, f, "http-finance", secret)
	w = f.request(t, finance, "GET", "/api/auth/me", nil, nil)
	expectHTTPStatus(t, w, 200)
	var me auth.Session
	decodeHTTPResponse(t, w, &me)
	if me.CountryScope != "selected" || len(me.Countries) != 1 || me.Countries[0] != "UK" || me.PermissionsVersion < 2 || me.CanManageDataJobs {
		t.Fatal("relogin did not expose current country permissions")
	}
	for _, path := range []string{"/api/review-sets/" + set, "/api/case-batches/" + batch.ID} {
		expectHTTPStatus(t, f.request(t, finance, "GET", path, nil, nil), 409)
	}
	expectHTTPStatus(t, f.request(t, finance, "POST", "/api/case-batches/"+batch.ID+"/commit", map[string]any{}, nil), 409)
	if _, err := f.server.Store.CommitBatch(ctx, actor, batch.ID); err == nil {
		t.Fatal("admitted old country context committed a batch")
	}
	if httpSecurityCount(t, f.server.Store.DB, "SELECT count(*) FROM audit_events") != 0 || httpSecurityCount(t, f.server.Store.DB, "SELECT count(*) FROM cases WHERE assignee IS NOT NULL AND assignee<>''") != 0 {
		t.Fatal("revoked country scope wrote state or audit")
	}
	newSet := reviewHTTPGenerate(t, f, finance)
	w = f.request(t, finance, "GET", "/api/review-sets/"+newSet, nil, nil)
	expectHTTPStatus(t, w, 200)
	var review store.ReviewPage
	decodeHTTPResponse(t, w, &review)
	if review.Total != 2 {
		t.Fatal("new country scope did not create a fresh restricted snapshot")
	}
	// A valid freshly generated workbench must not be blocked while restoring
	// optional history from an earlier permission version. Old ID access above
	// remains rejected, and the underlying batch is retained.
	w = f.request(t, finance, "GET", "/api/case-batches/latest?run_id="+f.run, nil, nil)
	expectHTTPStatus(t, w, 200)
	var latest reviewHTTPBatch
	decodeHTTPResponse(t, w, &latest)
	if latest.ID != "" || latest.Total != 0 || httpSecurityCount(t, f.server.Store.DB, "SELECT count(*) FROM review_batches WHERE id=?", batch.ID) != 1 {
		t.Fatal("latest exposed stale batch data or removed historical records")
	}
	w = f.request(t, finance, "POST", "/api/case-batches/preview", map[string]any{"set_id": newSet, "action": "assign", "all": true, "assignee": "http-owner"}, nil)
	expectHTTPStatus(t, w, 200)
	var fresh reviewHTTPBatch
	decodeHTTPResponse(t, w, &fresh)
	w = f.request(t, finance, "GET", "/api/case-batches/latest?run_id="+f.run, nil, nil)
	expectHTTPStatus(t, w, 200)
	decodeHTTPResponse(t, w, &latest)
	if latest.ID != fresh.ID || latest.Total != 2 {
		t.Fatal("latest did not restore the current permission version's batch")
	}
}

func TestHTTP_TC61_GlobalJobsRequireAllCountryFinanceOrAdmin(t *testing.T) {
	f, admin := countryHTTPFixture(t)
	finance, secret := countryHTTPConfigured(t, f, "http-finance")
	owner, _ := countryHTTPConfigured(t, f, "http-owner")
	jobs, err := NewJobs(f.server.Store, t.TempDir(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(jobs.Close)
	jobs.gate <- struct{}{} // Isolated queue only; never launch data preparation.
	f.server.Jobs = jobs
	f.handler = f.server.Handler()
	var job Job
	for _, client := range []*httpSecurityClient{finance, admin} {
		w := f.request(t, client, "POST", "/api/jobs", map[string]any{"scope": "all"}, nil)
		expectHTTPStatus(t, w, 200)
		decodeHTTPResponse(t, w, &job)
		if job.Status != "queued" {
			t.Fatal("all-country manager could not queue an isolated job")
		}
	}
	countryHTTPSet(t, f, admin, "http-finance", "UK")
	finance = countryHTTPLogin(t, f, "http-finance", secret)
	for _, client := range []*httpSecurityClient{finance, owner} {
		expectHTTPStatus(t, f.request(t, client, "POST", "/api/jobs", map[string]any{"scope": "all"}, map[string]string{"X-Role": "admin"}), 403)
		expectHTTPStatus(t, f.request(t, client, "GET", "/api/jobs/"+job.ID, nil, nil), 403)
	}
	expectHTTPStatus(t, f.request(t, admin, "GET", "/api/jobs/"+job.ID, nil, nil), 200)
	if httpSecurityCount(t, f.server.Store.DB, "SELECT count(*) FROM web_jobs") != 2 {
		t.Fatal("restricted country account queued an extra global job")
	}
}
