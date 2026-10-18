package web

import (
	"net/url"
	"testing"

	"returnsrecon/internal/store"
)

type reviewHTTPMetadata struct {
	ID string `json:"id"`
}
type reviewHTTPBatch struct {
	ID        string `json:"id"`
	Status    string `json:"status"`
	Total     int    `json:"total"`
	Succeeded int    `json:"succeeded"`
	Skipped   int    `json:"skipped"`
}

func reviewHTTPGenerate(t *testing.T, f *httpSecurityFixture, c *httpSecurityClient) string {
	t.Helper()
	w := f.request(t, c, "POST", "/api/review-sets", map[string]any{"run_id": f.run}, nil)
	expectHTTPStatus(t, w, 200)
	var out reviewHTTPMetadata
	decodeHTTPResponse(t, w, &out)
	if out.ID == "" {
		t.Fatal("generated review metadata missing ID")
	}
	return out.ID
}

func TestHTTP_TC52_ReviewScopedByTrustedActorAndWarehouseOwnership(t *testing.T) {
	f := httpSecurityTest(t)
	finance := f.configured(t, "http-finance")
	admin := f.configured(t, "http-admin")
	owner := f.configured(t, "http-owner")
	f.assignHTTP(t, finance, 0, "http-owner")
	f.assignHTTP(t, finance, 1, "http-other")
	financeSet := reviewHTTPGenerate(t, f, finance)
	ownerSet := reviewHTTPGenerate(t, f, owner)
	adminSet := reviewHTTPGenerate(t, f, admin)
	for _, tc := range []struct {
		client *httpSecurityClient
		set    string
		total  int
	}{{finance, financeSet, 3}, {admin, adminSet, 3}, {owner, ownerSet, 1}} {
		w := f.request(t, tc.client, "GET", "/api/review-sets/"+tc.set+"?page_size=1", nil, nil)
		expectHTTPStatus(t, w, 200)
		var page store.ReviewPage
		decodeHTTPResponse(t, w, &page)
		if page.Total != tc.total || len(page.Items) != 1 {
			t.Fatalf("scoped review size=%d items=%d", page.Total, len(page.Items))
		}
		if tc.client == owner && page.Items[0].Case.ID != f.cases[0] {
			t.Fatal("warehouse saw unrelated recommendation")
		}
	}
	expectHTTPStatus(t, f.request(t, owner, "GET", "/api/review-sets/"+financeSet, nil, nil), 404)
	expectHTTPStatus(t, f.request(t, owner, "POST", "/api/case-batches/preview", map[string]any{"set_id": ownerSet, "action": "assign", "all": true, "assignee": "http-owner"}, map[string]string{"X-Role": "admin"}), 403)
	expectHTTPStatus(t, f.request(t, owner, "POST", "/api/case-batches/preview", map[string]any{"set_id": ownerSet, "action": "approve", "ids": []string{f.cases[1]}, "conclusion": "isolated fixture review"}, nil), 403)
	w := f.request(t, finance, "POST", "/api/case-batches/preview", map[string]any{"set_id": financeSet, "action": "approve", "ids": []string{f.cases[2]}, "conclusion": "isolated fixture review"}, nil)
	expectHTTPStatus(t, w, 200)
	var batch reviewHTTPBatch
	decodeHTTPResponse(t, w, &batch)
	expectHTTPStatus(t, f.request(t, owner, "GET", "/api/case-batches/"+batch.ID, nil, nil), 404)
	expectHTTPStatus(t, f.request(t, owner, "POST", "/api/case-batches/"+batch.ID+"/commit", map[string]any{}, nil), 404)
	if httpSecurityCount(t, f.server.Store.DB, "SELECT count(*) FROM allocations") != 0 || httpSecurityCount(t, f.server.Store.DB, "SELECT count(*) FROM audit_events") != 2 {
		t.Fatal("scope rejects or preview wrote business state")
	}
}

func TestHTTP_TC54_ReviewCSRFStrictBodiesAndRevokedCommitGuard(t *testing.T) {
	f := httpSecurityTest(t)
	finance := f.configured(t, "http-finance")
	for _, path := range []string{"/api/review-sets/latest?run_id=" + f.run, "/api/case-batches/latest?run_id=" + f.run} {
		expectHTTPStatus(t, f.request(t, nil, "GET", path, nil, map[string]string{"X-Role": "admin"}), 401)
	}
	for _, headers := range []map[string]string{{"X-CSRF-Token": ""}, {"Origin": "http://evil.invalid"}} {
		expectHTTPStatus(t, f.request(t, finance, "POST", "/api/review-sets", map[string]any{"run_id": f.run}, headers), 403)
	}
	expectHTTPStatus(t, f.request(t, finance, "POST", "/api/review-sets", map[string]any{"run_id": f.run, "role": "admin"}, nil), 422)
	set := reviewHTTPGenerate(t, f, finance)
	expectHTTPStatus(t, f.request(t, finance, "POST", "/api/case-batches/preview", map[string]any{"set_id": set, "action": "approve", "ids": []string{f.cases[0]}, "conclusion": "isolated fixture review", "actor": "http-admin"}, nil), 422)
	w := f.request(t, finance, "POST", "/api/case-batches/preview", map[string]any{"set_id": set, "action": "approve", "ids": []string{f.cases[0]}, "conclusion": "isolated fixture review"}, nil)
	expectHTTPStatus(t, w, 200)
	var batch reviewHTTPBatch
	decodeHTTPResponse(t, w, &batch)
	commit := "/api/case-batches/" + batch.ID + "/commit"
	expectHTTPStatus(t, f.request(t, finance, "POST", commit, map[string]any{}, map[string]string{"X-CSRF-Token": "forged"}), 403)
	expectHTTPStatus(t, f.request(t, finance, "POST", commit, map[string]any{"ids": []string{f.cases[1]}, "version": 99}, nil), 422)
	if httpSecurityCount(t, f.server.Store.DB, "SELECT count(*) FROM allocations") != 0 {
		t.Fatal("invalid commit body changed frozen selection")
	}
	w = f.request(t, finance, "POST", commit, map[string]any{}, nil)
	expectHTTPStatus(t, w, 200)
	decodeHTTPResponse(t, w, &batch)
	if batch.Status != "completed" || batch.Succeeded != 1 || batch.Total != 1 {
		t.Fatalf("HTTP approval result: %+v", batch)
	}
	for _, view := range []string{"bogus", "closed", "' OR 1=1 --"} {
		expectHTTPStatus(t, f.request(t, finance, "GET", "/api/case-batches/"+batch.ID+"?status="+url.QueryEscape(view), nil, nil), 422)
	}
	expectHTTPStatus(t, f.request(t, finance, "GET", "/api/case-batches/"+batch.ID+"?status=succeeded", nil, nil), 200)
	if httpSecurityCount(t, f.server.Store.DB, "SELECT count(*) FROM audit_events") != 4 || httpSecurityCount(t, f.server.Store.DB, "SELECT count(*) FROM allocations") != 1 {
		t.Fatal("HTTP approval failed four-step atomic workflow")
	}
	ctx, actor := f.capturedRequestContext(t, finance)
	expectHTTPStatus(t, f.request(t, finance, "POST", "/api/auth/recover", map[string]any{"id": finance.id, "password": httpFixturePassword, "code": finance.codes[0]}, nil), 200)
	if _, err := f.server.Store.CommitBatch(ctx, actor, batch.ID); err == nil {
		t.Fatal("admitted then revoked HTTP context replayed completed batch")
	}
	expectHTTPStatus(t, f.request(t, finance, "POST", commit, map[string]any{}, nil), 403)
	if httpSecurityCount(t, f.server.Store.DB, "SELECT count(*) FROM audit_events") != 4 || httpSecurityCount(t, f.server.Store.DB, "SELECT count(*) FROM allocations") != 1 {
		t.Fatal("revoked replay wrote business state")
	}
}
