package web

// HTTP integration tests provision synthetic identities through the actual
// bootstrap/password/TOTP/recovery-confirmation routes. No usable test-login
// endpoint or directly inserted business session is involved.
import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/pquerna/otp/totp"
	"returnsrecon/internal/auth"
	"returnsrecon/internal/model"
	"returnsrecon/internal/store"
)

const httpFixturePassword = "Amber waterfall birch 73!"
const httpFixtureOrigin = "http://localhost:8080"

type httpSecurityClient struct {
	id     string
	cookie *http.Cookie
	csrf   string
	codes  []string
}
type httpSecurityFixture struct {
	server    *Server
	handler   http.Handler
	bootstrap map[string]string
	run       string
	cases     []string
}

func httpSecurityTest(t *testing.T) *httpSecurityFixture {
	t.Helper()
	root := t.TempDir()
	s, e := store.Open(filepath.Join(root, "http-isolated.sqlite"))
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { s.Close() })
	a, e := auth.New(s.DB, auth.Config{SecretDir: filepath.Join(root, "secrets"), Origin: httpFixtureOrigin})
	if e != nil {
		t.Fatal("isolated HTTP auth initialization failed:", e)
	}
	bootstrap, e := a.Initialize(context.Background(), []auth.Identity{{ID: "http-finance", Role: "finance"}, {ID: "http-admin", Role: "admin"}, {ID: "http-owner", Role: "warehouse"}, {ID: "http-other", Role: "warehouse"}})
	if e != nil {
		t.Fatal(e)
	}
	prepared := filepath.Join(root, "prepared")
	if e = os.Mkdir(prepared, 0700); e != nil {
		t.Fatal(e)
	}
	rows := []model.Row{}
	for i := 2; i <= 7; i++ {
		quantity := int64(10)
		kind := "sale"
		hour := i + 6
		if i >= 4 && i <= 6 {
			quantity = -4
			kind = "cancel"
		}
		stock, customer := "A", "42"
		if i == 7 {
			stock, customer = "B", "99"
		}
		rows = append(rows, model.Row{ID: fmt.Sprintf("0:%d", i), SourceRow: i, Sheet: "isolated-http-sheet", Invoice: fmt.Sprintf("ISOLATED-%d", i), Stock: stock, Description: "isolated HTTP fixture only", Customer: customer, Country: "UK", Price: "0.1", Time: fmt.Sprintf("2010-01-01T%02d:00:00.000000", hour), Quantity: quantity, Kind: kind, RawJSON: `["isolated HTTP fixture only"]`})
	}
	path := filepath.Join(prepared, "records.csv")
	file, e := os.Create(path)
	if e != nil {
		t.Fatal(e)
	}
	writer := csv.NewWriter(file)
	header := []string{"id", "sheet_index", "sheet", "source_row", "raw_json", "invoice", "stock", "description", "quantity", "time", "price", "customer", "country", "kind", "excluded", "flags", "duplicate_of"}
	if e = writer.Write(header); e != nil {
		t.Fatal(e)
	}
	for _, r := range rows {
		if e = writer.Write([]string{r.ID, "0", r.Sheet, strconv.Itoa(r.SourceRow), r.RawJSON, r.Invoice, r.Stock, r.Description, strconv.FormatInt(r.Quantity, 10), r.Time, r.Price, r.Customer, r.Country, r.Kind, "", "", ""}); e != nil {
			t.Fatal(e)
		}
	}
	writer.Flush()
	if e = writer.Error(); e != nil {
		t.Fatal(e)
	}
	if e = file.Close(); e != nil {
		t.Fatal(e)
	}
	data, e := os.ReadFile(path)
	if e != nil {
		t.Fatal(e)
	}
	digest := sha256.Sum256(data)
	manifest := store.Manifest{Scope: "benchmark", CleaningVersion: "cr-http-isolated", SourceHash: store.ID("synthetic HTTP integration source only"), RecordsHash: hex.EncodeToString(digest[:]), Status: "prepared", Stats: store.Stats{Raw: 6, Accepted: 6, AcceptedSales: 3, AcceptedCancels: 3, RawCancels: 3}}
	manifest.DatasetID = store.ID(manifest.SourceHash + "|" + manifest.Scope + "|" + manifest.CleaningVersion)
	raw, e := json.Marshal(manifest)
	if e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(filepath.Join(prepared, "manifest.json"), raw, 0600); e != nil {
		t.Fatal(e)
	}
	if _, e = s.Import(context.Background(), prepared); e != nil {
		t.Fatal("isolated fixture Import failed:", e)
	}
	run, e := s.Run(context.Background(), manifest.DatasetID, 1)
	if e != nil {
		t.Fatal("isolated fixture actual engine failed:", e)
	}
	if run.Cases != 3 || run.Suspected != 3 || run.Matched != 0 {
		t.Fatal("isolated fixture did not produce three actual ambiguous cases")
	}
	server := &Server{Store: s, Auth: a, Origin: httpFixtureOrigin}
	f := &httpSecurityFixture{server: server, handler: server.Handler(), bootstrap: bootstrap, run: run.RunID}
	for _, id := range []string{"0:4", "0:5", "0:6"} {
		f.cases = append(f.cases, store.ID(run.RunID+"|"+id))
	}
	return f
}
func (f *httpSecurityFixture) request(t *testing.T, client *httpSecurityClient, method, path string, body any, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	var data []byte
	var e error
	if body != nil {
		data, e = json.Marshal(body)
		if e != nil {
			t.Fatal("serialize isolated HTTP request")
		}
	}
	r := httptest.NewRequest(method, httpFixtureOrigin+path, bytes.NewReader(data))
	r.Host = "localhost:8080"
	r.RemoteAddr = "127.0.0.1:43123"
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Origin", httpFixtureOrigin)
	if client != nil && client.cookie != nil {
		r.AddCookie(client.cookie)
		r.Header.Set("X-CSRF-Token", client.csrf)
	}
	for k, v := range headers {
		if k == "Host" {
			r.Host = v
		} else {
			r.Header.Set(k, v)
		}
	}
	w := httptest.NewRecorder()
	f.handler.ServeHTTP(w, r)
	if client != nil && w.Code == 200 {
		for _, cookie := range w.Result().Cookies() {
			if cookie.Name == cookieName {
				copy := *cookie
				client.cookie = &copy
				var result struct {
					Session auth.Session `json:"session"`
					CSRF    string       `json:"csrf"`
				}
				if e = json.Unmarshal(w.Body.Bytes(), &result); e == nil {
					client.csrf = result.Session.CSRF
					if result.CSRF != "" {
						client.csrf = result.CSRF
					}
				}
			}
		}
	}
	return w
}
func expectHTTPStatus(t *testing.T, w *httptest.ResponseRecorder, status int) {
	t.Helper()
	if w.Code != status {
		var result struct {
			Code string `json:"code"`
		}
		_ = json.Unmarshal(w.Body.Bytes(), &result)
		t.Fatalf("HTTP status=%d, want=%d, response code=%s (secret response body withheld)", w.Code, status, result.Code)
	}
}
func decodeHTTPResponse(t *testing.T, w *httptest.ResponseRecorder, out any) {
	t.Helper()
	if e := json.Unmarshal(w.Body.Bytes(), out); e != nil {
		t.Fatal("decode HTTP response failed (response withheld)")
	}
}
func httpSecurityCount(t *testing.T, db *sql.DB, query string, args ...any) int {
	t.Helper()
	var n int
	if e := db.QueryRow(query, args...).Scan(&n); e != nil {
		t.Fatal(e)
	}
	return n
}
func (f *httpSecurityFixture) configured(t *testing.T, id string) *httpSecurityClient {
	t.Helper()
	c := &httpSecurityClient{id: id}
	expectHTTPStatus(t, f.request(t, c, "POST", "/api/auth/bootstrap", map[string]any{"token": f.bootstrap[id]}, nil), 200)
	expectHTTPStatus(t, f.request(t, c, "GET", "/api/runs", nil, nil), 403)
	expectHTTPStatus(t, f.request(t, c, "POST", "/api/auth/setup/password", map[string]any{"password": httpFixturePassword}, nil), 200)
	expectHTTPStatus(t, f.request(t, c, "GET", "/api/runs", nil, nil), 403)
	enrollmentResponse := f.request(t, c, "POST", "/api/auth/totp/enroll", map[string]any{}, nil)
	expectHTTPStatus(t, enrollmentResponse, 200)
	var en auth.Enrollment
	decodeHTTPResponse(t, enrollmentResponse, &en)
	expectHTTPStatus(t, f.request(t, c, "GET", "/api/runs", nil, nil), 403)
	expectHTTPStatus(t, f.request(t, c, "POST", "/api/auth/setup/complete", map[string]any{"saved": true}, nil), 403)
	code, e := totp.GenerateCode(en.Secret, time.Now())
	if e != nil {
		t.Fatal("synthetic TOTP generation failed")
	}
	confirmed := f.request(t, c, "POST", "/api/auth/totp/confirm", map[string]any{"code": code}, nil)
	expectHTTPStatus(t, confirmed, 200)
	var grant auth.Grant
	decodeHTTPResponse(t, confirmed, &grant)
	c.codes = append([]string(nil), grant.RecoveryCodes...)
	expectHTTPStatus(t, f.request(t, c, "GET", "/api/runs", nil, nil), 403)
	expectHTTPStatus(t, f.request(t, c, "POST", "/api/auth/setup/complete", map[string]any{"saved": false}, nil), 422)
	expectHTTPStatus(t, f.request(t, c, "POST", "/api/auth/setup/complete", map[string]any{"saved": true}, nil), 200)
	expectHTTPStatus(t, f.request(t, c, "GET", "/api/runs", nil, nil), 200)
	return c
}
func (f *httpSecurityFixture) assignHTTP(t *testing.T, finance *httpSecurityClient, caseIndex int, assignee string) store.WorkflowCase {
	t.Helper()
	w := f.request(t, finance, "POST", "/api/cases/"+f.cases[caseIndex]+"/assign", map[string]any{"assignee": assignee}, nil)
	expectHTTPStatus(t, w, 200)
	var c store.WorkflowCase
	decodeHTTPResponse(t, w, &c)
	return c
}

func TestHTTP_TC25_TrustedSessionRejectsMissingOrForgedIdentity(t *testing.T) {
	f := httpSecurityTest(t)
	finance := f.configured(t, "http-finance")
	owner := f.configured(t, "http-owner")
	for _, path := range []string{"/api/runs", "/api/cases?run_id=" + f.run, "/api/users", "/api/audit"} {
		expectHTTPStatus(t, f.request(t, nil, "GET", path, nil, map[string]string{"X-Role": "admin", "X-User-ID": "http-finance", "X-Trusted-Role": "admin", "X-Trusted-User": "http-admin"}), 401)
	}
	assign := "/api/cases/" + f.cases[0] + "/assign"
	before := httpSecurityCount(t, f.server.Store.DB, "SELECT count(*) FROM audit_events")
	expectHTTPStatus(t, f.request(t, nil, "POST", assign, map[string]any{"assignee": "http-owner", "role": "admin", "user_id": "http-finance"}, nil), 401)
	expectHTTPStatus(t, f.request(t, owner, "POST", assign, map[string]any{"assignee": "http-owner"}, map[string]string{"X-Role": "admin", "X-User-ID": "http-finance", "X-Trusted-Role": "admin"}), 403)
	expectHTTPStatus(t, f.request(t, finance, "POST", assign, map[string]any{"assignee": "http-owner", "role": "admin", "user_id": "http-admin"}, nil), 422)
	if httpSecurityCount(t, f.server.Store.DB, "SELECT count(*) FROM audit_events") != before || httpSecurityCount(t, f.server.Store.DB, "SELECT count(*) FROM cases WHERE assignee IS NOT NULL AND assignee<>''") != 0 {
		t.Fatal("forged identity wrote business state or audit")
	}
	var me auth.Session
	w := f.request(t, owner, "GET", "/api/auth/me", nil, map[string]string{"X-Role": "admin"})
	expectHTTPStatus(t, w, 200)
	decodeHTTPResponse(t, w, &me)
	if me.ID != "http-owner" || me.Role != "warehouse" {
		t.Fatal("trusted session identity was overridden by client headers")
	}
	t.Log("TC-25 actual HTTP: unauthenticated requests=401; forged trusted headers with warehouse Cookie=403; unknown role/user body fields=422; zero business writes")
}

func TestHTTP_TC26_WarehouseScopeIncludesOnlyOwnCaseAndRelatedEvidence(t *testing.T) {
	f := httpSecurityTest(t)
	finance := f.configured(t, "http-finance")
	owner := f.configured(t, "http-owner")
	f.assignHTTP(t, finance, 0, "http-owner")
	f.assignHTTP(t, finance, 1, "http-other")
	w := f.request(t, owner, "GET", "/api/cases?run_id="+f.run, nil, nil)
	expectHTTPStatus(t, w, 200)
	var page store.CasePage
	decodeHTTPResponse(t, w, &page)
	if page.Total != 1 || len(page.Items) != 1 || page.Items[0].ID != f.cases[0] {
		t.Fatal("warehouse case list leaked another owner or unassigned case")
	}
	for _, id := range f.cases[1:] {
		for _, suffix := range []string{"", "/evidence", "/candidates", "/audit"} {
			expectHTTPStatus(t, f.request(t, owner, "GET", "/api/cases/"+id+suffix, nil, nil), 403)
		}
		expectHTTPStatus(t, f.request(t, owner, "GET", "/api/audit?case_id="+id, nil, nil), 403)
	}
	for _, suffix := range []string{"", "/evidence", "/candidates", "/audit"} {
		expectHTTPStatus(t, f.request(t, owner, "GET", "/api/cases/"+f.cases[0]+suffix, nil, nil), 200)
	}
	w = f.request(t, owner, "GET", "/api/transactions?run_id="+f.run, nil, nil)
	expectHTTPStatus(t, w, 200)
	var transactions store.TransactionPage
	decodeHTTPResponse(t, w, &transactions)
	if transactions.Total != 3 {
		t.Fatal("warehouse related transaction scope did not contain one cancellation and its two candidate sales")
	}
	for _, r := range transactions.Items {
		if r.ID != "0:2" && r.ID != "0:3" && r.ID != "0:4" {
			t.Fatal("warehouse transaction search leaked unrelated source row")
		}
	}
	w = f.request(t, owner, "GET", "/api/cases?run_id="+f.run+"&assignee=http-other", nil, nil)
	expectHTTPStatus(t, w, 200)
	decodeHTTPResponse(t, w, &page)
	if page.Total != 0 {
		t.Fatal("client assignee filter broadened warehouse ownership")
	}
	w = f.request(t, finance, "GET", "/api/cases?run_id="+f.run, nil, nil)
	expectHTTPStatus(t, w, 200)
	decodeHTTPResponse(t, w, &page)
	if page.Total != 3 {
		t.Fatal("finance did not retain global case scope")
	}
	t.Log("TC-26 actual HTTP: warehouse own list=1, related source rows=3; direct other/unassigned case/evidence/candidate/audit=403; finance global cases=3")
}

func TestHTTP_TC27_FilteredCSVOnlyFinanceAndAdmin(t *testing.T) {
	f := httpSecurityTest(t)
	finance := f.configured(t, "http-finance")
	admin := f.configured(t, "http-admin")
	owner := f.configured(t, "http-owner")
	path := "/api/exports?run_id=" + f.run + "&customer=42&stock=A&kind=cancel&page_size=1"
	for _, client := range []*httpSecurityClient{finance, admin} {
		w := f.request(t, client, "GET", path, nil, nil)
		expectHTTPStatus(t, w, 200)
		if !strings.HasPrefix(w.Header().Get("Content-Type"), "text/csv;") || !strings.Contains(w.Header().Get("Content-Disposition"), "attachment;") {
			t.Fatal("CSV response headers are missing")
		}
		rows, e := csv.NewReader(strings.NewReader(w.Body.String())).ReadAll()
		if e != nil || len(rows) != 4 {
			t.Fatal("CSV did not contain the three complete filtered rows regardless of page_size=1")
		}
		for _, r := range rows[1:] {
			if len(r) != 21 || r[0] != f.run || r[10] != "A" || r[15] != "42" || r[17] != "cancel" || r[6] != model.RuleVersion || r[7] != "cr-http-isolated" {
				t.Fatal("CSV lost active filter or source/rule version columns")
			}
		}
	}
	expectHTTPStatus(t, f.request(t, owner, "GET", path, nil, map[string]string{"X-Role": "admin"}), 403)
	w := f.request(t, finance, "GET", "/api/exports?run_id="+f.run+"&customer=absent", nil, nil)
	expectHTTPStatus(t, w, 200)
	rows, e := csv.NewReader(strings.NewReader(w.Body.String())).ReadAll()
	if e != nil || len(rows) != 1 {
		t.Fatal("empty CSV filter fabricated detail rows")
	}
	t.Log("TC-27 actual HTTP: finance/admin customer+stock+kind filtered CSV=3 detail rows plus header, page_size=1 does not truncate export; warehouse=403; empty filter header only")
}

func TestHTTP_TC33_AllRolesRestrictedUntilActualFactorAndRecoveryConfirmation(t *testing.T) {
	f := httpSecurityTest(t)
	for _, id := range []string{"http-finance", "http-owner", "http-admin"} {
		t.Run(id, func(t *testing.T) {
			c := f.configured(t, id)
			if c.cookie == nil || !c.cookie.HttpOnly || c.cookie.SameSite != http.SameSiteStrictMode || c.cookie.Path != "/" || c.cookie.Secure {
				t.Fatal("local HTTP session Cookie lacks required HttpOnly/Strict/path settings")
			}
			w := f.request(t, c, "GET", "/api/auth/me", nil, nil)
			expectHTTPStatus(t, w, 200)
			var v auth.Session
			decodeHTTPResponse(t, w, &v)
			if v.Scope != "business" || v.State != "complete" {
				t.Fatal("completed HTTP account did not obtain full trusted session")
			}
		})
	}
	t.Log("TC-33 actual HTTP: finance/warehouse/admin each denied business before password, after password, after enrollment data, and after factor before saved confirmation; complete granted fresh Cookie")
}

func TestHTTP_TC46_OriginCSRFHostCookieAndRecoveryRevocation(t *testing.T) {
	f := httpSecurityTest(t)
	finance := f.configured(t, "http-finance")
	target := "/api/cases/" + f.cases[0] + "/assign"
	body := map[string]any{"assignee": "http-owner"}
	expectHTTPStatus(t, f.request(t, finance, "POST", target, body, map[string]string{"Origin": "http://evil.invalid"}), 403)
	expectHTTPStatus(t, f.request(t, finance, "POST", target, body, map[string]string{"X-CSRF-Token": "forged"}), 403)
	expectHTTPStatus(t, f.request(t, finance, "POST", target, body, map[string]string{"X-CSRF-Token": ""}), 403)
	expectHTTPStatus(t, f.request(t, finance, "GET", "/api/runs", nil, map[string]string{"Host": "evil.invalid:8080"}), 403)
	w := f.request(t, finance, "GET", "/api/runs", nil, map[string]string{"Host": "127.0.0.1:8080"})
	expectHTTPStatus(t, w, 307)
	if w.Header().Get("Location") != httpFixtureOrigin+"/api/runs" {
		t.Fatal("IP access did not redirect to canonical localhost")
	}
	if httpSecurityCount(t, f.server.Store.DB, "SELECT count(*) FROM audit_events") != 0 {
		t.Fatal("boundary rejects wrote business audit")
	}
	old := *finance
	oldCookie := *finance.cookie
	old.cookie = &oldCookie
	expectHTTPStatus(t, f.request(t, finance, "POST", "/api/auth/recover", map[string]any{"id": finance.id, "password": httpFixturePassword, "code": finance.codes[0]}, nil), 200)
	expectHTTPStatus(t, f.request(t, &old, "GET", "/api/runs", nil, nil), 401)
	expectHTTPStatus(t, f.request(t, finance, "GET", "/api/runs", nil, nil), 403)
	expectHTTPStatus(t, f.request(t, finance, "POST", "/api/auth/setup/complete", map[string]any{"saved": true, "mfa": true, "factor": "verified"}, nil), 422)
	expectHTTPStatus(t, f.request(t, finance, "POST", "/api/auth/setup/complete", map[string]any{"saved": true}, nil), 403)
	t.Log("TC-46 actual HTTP: bad Origin/CSRF/Host=403, canonical IP redirect=307; HttpOnly Strict Cookie used; recovery old Cookie=401, new recovery Cookie business=403, forged completion=422")
}

func (f *httpSecurityFixture) capturedRequestContext(t *testing.T, client *httpSecurityClient) (context.Context, store.WorkflowActor) {
	t.Helper()
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	request := httptest.NewRequest("POST", httpFixtureOrigin+"/api/cases/placeholder", nil)
	request.Host = "localhost:8080"
	request.AddCookie(client.cookie)
	c.Request = request
	f.server.requireBusiness(c)
	if c.IsAborted() {
		t.Fatal("legitimate HTTP business middleware did not admit test request")
	}
	return c.Request.Context(), actor(c)
}
func TestHTTP_TC43_AlreadyAdmittedWriteRechecksRevokedSessionInsideTransaction(t *testing.T) {
	f := httpSecurityTest(t)
	finance := f.configured(t, "http-finance")
	owner := f.configured(t, "http-owner")
	c := f.assignHTTP(t, finance, 0, "http-owner")
	w := f.request(t, owner, "POST", "/api/cases/"+c.ID+"/start", map[string]any{"version": c.Version}, nil)
	expectHTTPStatus(t, w, 200)
	decodeHTTPResponse(t, w, &c)
	financeCtx, financeActor := f.capturedRequestContext(t, finance)
	ownerCtx, ownerActor := f.capturedRequestContext(t, owner)
	beforeAudit := httpSecurityCount(t, f.server.Store.DB, "SELECT count(*) FROM audit_events")
	beforeBalance := httpSecurityCount(t, f.server.Store.DB, "SELECT sum(remaining) FROM sale_balances")
	beforeVersion := httpSecurityCount(t, f.server.Store.DB, "SELECT sum(version) FROM cases")
	// Recover before entering either business BEGIN IMMEDIATE: this reproduces
	// admission-then-revocation without constructing an artificial write deadlock.
	expectHTTPStatus(t, f.request(t, finance, "POST", "/api/auth/recover", map[string]any{"id": finance.id, "password": httpFixturePassword, "code": finance.codes[0]}, nil), 200)
	expectHTTPStatus(t, f.request(t, owner, "POST", "/api/auth/recover", map[string]any{"id": owner.id, "password": httpFixturePassword, "code": owner.codes[0]}, nil), 200)
	_, e := f.server.Store.AssignCase(financeCtx, financeActor, store.WorkflowActor{ID: "http-other", Role: "warehouse"}, f.cases[2])
	if e == nil {
		t.Fatal("already-admitted revoked finance request assigned a case")
	}
	_, e = f.server.Store.ConfirmCandidate(ownerCtx, ownerActor, c.ID, "0:2", c.Version)
	if e == nil {
		t.Fatal("already-admitted revoked owner request allocated a candidate")
	}
	if httpSecurityCount(t, f.server.Store.DB, "SELECT count(*) FROM audit_events") != beforeAudit || httpSecurityCount(t, f.server.Store.DB, "SELECT sum(remaining) FROM sale_balances") != beforeBalance || httpSecurityCount(t, f.server.Store.DB, "SELECT sum(version) FROM cases") != beforeVersion || httpSecurityCount(t, f.server.Store.DB, "SELECT count(*) FROM allocations") != 0 {
		t.Fatal("revoked write guard failed to roll back business state/audit")
	}
	t.Log("TC-43/46 transaction race: admitted finance/owner contexts captured by real middleware; recovery revoked both before BEGIN; old Assign/Confirm rejected, audit/version/remaining unchanged and allocations=0")
}
