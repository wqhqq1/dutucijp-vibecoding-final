package web

// Contract checks run actual HTTP handlers against disposable data. The schema
// source is the published OpenAPI document, not a second copy of response DTOs.
import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/goccy/go-yaml"
	"github.com/pquerna/otp/totp"
	"github.com/santhosh-tekuri/jsonschema/v6"
	"returnsrecon/internal/apicontract"
	"returnsrecon/internal/auth"
	"returnsrecon/internal/store"
)

const contractResource = "https://contracts.invalid/openapi.json"

// Enumerations are executable wire values, not merely documentation labels.
func TestAPIContract_DeclaredTransactionSorts(t *testing.T) {
	f := httpSecurityTest(t)
	client := f.configured(t, "http-finance")
	v := newContractValidator(t)
	_, op := v.operation(t, "GET", "/api/transactions")
	for i, raw := range op["parameters"].([]any) {
		parameter := raw.(map[string]any)
		if parameter["name"] != "sort" {
			continue
		}
		for _, value := range parameter["schema"].(map[string]any)["enum"].([]any) {
			sort := value.(string)
			v.validate(t, "/paths/~1api~1transactions/get/parameters/"+strconv.Itoa(i)+"/schema", sort)
			for _, direction := range []string{"asc", "desc"} {
				v.request(t, f, client, "GET", "/api/transactions?run_id="+f.run+"&sort="+sort+"&direction="+direction, nil, nil, 200)
			}
		}
		return
	}
	t.Fatal("sort enumeration missing from published operation")
}

type contractValidator struct {
	paths    map[string]any
	compiler *jsonschema.Compiler
	compiled map[string]*jsonschema.Schema
}

func contractJSON(t *testing.T, v any) any {
	t.Helper()
	raw, e := json.Marshal(v)
	if e != nil {
		t.Fatal(e)
	}
	out, e := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if e != nil {
		t.Fatal(e)
	}
	return out
}
func newContractValidator(t *testing.T) *contractValidator {
	t.Helper()
	doc := contractJSON(t, apicontract.Document()).(map[string]any)
	c := jsonschema.NewCompiler()
	c.DefaultDraft(jsonschema.Draft2020)
	if e := c.AddResource(contractResource, doc); e != nil {
		t.Fatal(e)
	}
	return &contractValidator{doc["paths"].(map[string]any), c, map[string]*jsonschema.Schema{}}
}
func contractPointer(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(s, "~", "~0"), "/", "~1")
}
func (v *contractValidator) schema(t *testing.T, pointer string) *jsonschema.Schema {
	t.Helper()
	if s := v.compiled[pointer]; s != nil {
		return s
	}
	s, e := v.compiler.Compile(contractResource + "#" + pointer)
	if e != nil {
		t.Fatalf("compile OpenAPI schema %s: %v", pointer, e)
	}
	v.compiled[pointer] = s
	return s
}
func (v *contractValidator) validate(t *testing.T, pointer string, value any) {
	t.Helper()
	e := v.schema(t, pointer).Validate(value)
	if e == nil {
		return
	}
	var failure *jsonschema.ValidationError
	if !errors.As(e, &failure) {
		t.Errorf("schema validation %s failed (%T); response withheld", pointer, e)
		return
	}
	var report func(*jsonschema.ValidationError)
	report = func(f *jsonschema.ValidationError) {
		if len(f.Causes) > 0 {
			for _, cause := range f.Causes {
				report(cause)
			}
			return
		}
		t.Errorf("schema violation at /%s, schema %s (%T); payload withheld", strings.Join(f.InstanceLocation, "/"), f.SchemaURL, f.ErrorKind)
	}
	report(failure)
}
func (v *contractValidator) operation(t *testing.T, method, path string) (string, map[string]any) {
	t.Helper()
	u, e := url.Parse(path)
	if e != nil {
		t.Fatal(e)
	}
	if item, ok := v.paths[u.Path].(map[string]any); ok {
		if op, ok := item[strings.ToLower(method)].(map[string]any); ok {
			return u.Path, op
		}
	}
	segments := strings.Split(u.Path, "/")
	for pattern, item := range v.paths {
		expected := strings.Split(pattern, "/")
		if len(expected) != len(segments) {
			continue
		}
		match := true
		for i, part := range expected {
			if part != segments[i] && !(strings.HasPrefix(part, "{") && strings.HasSuffix(part, "}") && segments[i] != "") {
				match = false
				break
			}
		}
		if match {
			if op, ok := item.(map[string]any)[strings.ToLower(method)].(map[string]any); ok {
				return pattern, op
			}
		}
	}
	t.Fatalf("undocumented HTTP operation %s %s", method, u.Path)
	return "", nil
}
func (v *contractValidator) request(t *testing.T, f *httpSecurityFixture, client *httpSecurityClient, method, path string, body any, headers map[string]string, status int) *httptest.ResponseRecorder {
	t.Helper()
	pattern, op := v.operation(t, method, path)
	root := "/paths/" + contractPointer(pattern) + "/" + strings.ToLower(method)
	if body != nil && status == 200 {
		if _, ok := op["requestBody"]; !ok {
			t.Errorf("successful request %s %s has no request schema", method, pattern)
		} else {
			v.validate(t, root+"/requestBody/content/application~1json/schema", contractJSON(t, body))
		}
	}
	w := f.request(t, client, method, path, body, headers)
	if w.Code != status {
		t.Fatalf("actual HTTP %s %s returned %d, want %d; response body withheld", method, path, w.Code, status)
	}
	responses := op["responses"].(map[string]any)
	response, ok := responses[strconv.Itoa(w.Code)].(map[string]any)
	if !ok {
		t.Fatalf("undocumented status %d for %s %s", w.Code, method, pattern)
	}
	content, ok := response["content"].(map[string]any)
	if !ok {
		t.Fatalf("missing response content for %s %s", method, pattern)
	}
	if _, ok := content["application/json"]; !ok {
		t.Fatalf("expected JSON response for %s %s", method, pattern)
	}
	instance, e := jsonschema.UnmarshalJSON(bytes.NewReader(w.Body.Bytes()))
	if e != nil {
		t.Fatal("actual HTTP response is invalid JSON; body withheld")
	}
	v.validate(t, root+"/responses/"+strconv.Itoa(w.Code)+"/content/application~1json/schema", instance)
	return w
}

func TestAPIContract_PublishedSnapshotsAndGinRoutes(t *testing.T) {
	doc := contractJSON(t, apicontract.Document())
	root := filepath.Join("..", "..", "..", "docs", "api")
	raw, e := os.ReadFile(filepath.Join(root, "openapi.json"))
	if e != nil {
		t.Fatal(e)
	}
	published, e := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if e != nil {
		t.Fatal(e)
	}
	if !reflect.DeepEqual(published, doc) {
		t.Error("published openapi.json differs from apicontract.Document(); regenerate with go run ./cmd/apispec")
	}
	raw, e = os.ReadFile(filepath.Join(root, "openapi.yaml"))
	if e != nil {
		t.Fatal(e)
	}
	var y map[string]any
	if e = yaml.Unmarshal(raw, &y); e != nil {
		t.Fatal(e)
	}
	if !reflect.DeepEqual(contractJSON(t, y), doc) {
		t.Error("published openapi.yaml differs from apicontract.Document(); regenerate with go run ./cmd/apispec")
	}
	f := httpSecurityTest(t)
	f.server.Jobs = &Jobs{}
	f.handler = f.server.Handler()
	router, ok := f.handler.(*gin.Engine)
	if !ok {
		t.Fatal("handler no longer exposes Gin routes")
	}
	actual := map[string]bool{}
	for _, route := range router.Routes() {
		if !strings.HasPrefix(route.Path, "/api/") {
			continue
		}
		parts := strings.Split(route.Path, "/")
		for i, part := range parts {
			if strings.HasPrefix(part, ":") {
				parts[i] = "{" + strings.TrimPrefix(part, ":") + "}"
			}
		}
		key := route.Method + " " + strings.Join(parts, "/")
		if actual[key] {
			t.Errorf("duplicate Gin route %s", key)
		}
		actual[key] = true
	}
	documented := map[string]bool{}
	for path, item := range doc.(map[string]any)["paths"].(map[string]any) {
		for method := range item.(map[string]any) {
			switch method {
			case "get", "post", "put", "patch", "delete", "head", "options", "trace":
				documented[strings.ToUpper(method)+" "+path] = true
			}
		}
	}
	for key := range actual {
		if !documented[key] {
			t.Errorf("Gin route absent from OpenAPI: %s", key)
		}
	}
	for key := range documented {
		if !actual[key] {
			t.Errorf("OpenAPI operation absent from Gin: %s", key)
		}
	}
	t.Logf("compared %d actual API routes and both published OpenAPI snapshots", len(actual))
}

func TestAPIContract_AllDeclaredSchemasCompile(t *testing.T) {
	v := newContractValidator(t)
	paths := make([]string, 0, len(v.paths))
	for path := range v.paths {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	for _, path := range paths {
		for method, item := range v.paths[path].(map[string]any) {
			op := item.(map[string]any)
			root := "/paths/" + contractPointer(path) + "/" + method
			if _, ok := op["requestBody"]; ok {
				v.schema(t, root+"/requestBody/content/application~1json/schema")
			}
			for status, response := range op["responses"].(map[string]any) {
				for media := range response.(map[string]any)["content"].(map[string]any) {
					v.schema(t, root+"/responses/"+status+"/content/"+contractPointer(media)+"/schema")
				}
			}
		}
	}
	t.Logf("compiled %d OpenAPI request/response schemas with JSON Schema 2020-12", len(v.compiled))
}

func TestAPIContract_ActualHTTPResponses(t *testing.T) {
	f := httpSecurityTest(t)
	v := newContractValidator(t)
	call := func(client *httpSecurityClient, method, path string, body any, status int) *httptest.ResponseRecorder {
		t.Helper()
		return v.request(t, f, client, method, path, body, nil, status)
	}
	call(nil, "GET", "/api/health", nil, 200)
	call(nil, "GET", "/api/auth/client-config", nil, 200)
	call(nil, "GET", "/api/auth/setup/accounts", nil, 200)
	call(nil, "GET", "/api/auth/me", nil, 401)
	// Provision through real HTTP setup routes while validating every wire shape.
	secrets := map[string]string{}
	configure := func(id string, bootstrap bool) *httpSecurityClient {
		t.Helper()
		client := &httpSecurityClient{id: id}
		if bootstrap {
			call(client, "POST", "/api/auth/bootstrap", map[string]any{"token": f.bootstrap[id]}, 200)
		} else {
			call(client, "POST", "/api/auth/setup/start", map[string]any{"id": id}, 200)
		}
		call(client, "GET", "/api/auth/me", nil, 200)
		call(client, "POST", "/api/auth/setup/password", map[string]any{"password": httpFixturePassword}, 200)
		w := call(client, "POST", "/api/auth/totp/enroll", map[string]any{}, 200)
		var en auth.Enrollment
		decodeHTTPResponse(t, w, &en)
		secrets[id] = en.Secret
		code, e := totp.GenerateCode(en.Secret, time.Now())
		if e != nil {
			t.Fatal(e)
		}
		w = call(client, "POST", "/api/auth/totp/confirm", map[string]any{"code": code}, 200)
		var grant auth.Grant
		decodeHTTPResponse(t, w, &grant)
		client.codes = append([]string(nil), grant.RecoveryCodes...)
		call(client, "POST", "/api/auth/setup/complete", map[string]any{"saved": true}, 200)
		call(client, "GET", "/api/auth/me", nil, 200)
		return client
	}
	admin := configure("http-admin", true)
	finance := configure("http-finance", false)
	owner := configure("http-owner", false)
	call(nil, "GET", "/api/auth/setup/accounts", nil, 200)
	login := &httpSecurityClient{}
	call(login, "POST", "/api/auth/login", map[string]any{"id": "http-finance", "password": httpFixturePassword}, 200)
	code, e := totp.GenerateCode(secrets["http-finance"], time.Now().Add(30*time.Second))
	if e != nil {
		t.Fatal(e)
	}
	call(login, "POST", "/api/auth/totp/login", map[string]any{"code": code}, 200)
	call(login, "POST", "/api/auth/logout", map[string]any{}, 200)
	call(admin, "GET", "/api/admin/accounts", nil, 200)
	call(finance, "GET", "/api/users", nil, 200)
	call(finance, "GET", "/api/countries", nil, 200)
	call(admin, "GET", "/api/admin/passkey-domains", nil, 200)
	call(admin, "PUT", "/api/admin/passkey-domains", map[string]any{"version": 1, "origins": []string{"http://127.0.0.1:18089", "https://recon.example.com"}}, 200)
	call(admin, "GET", "/api/auth/client-config", nil, 200)
	call(admin, "POST", "/api/admin/accounts", accountBody("contract-new", "warehouse", "all"), 200)
	call(admin, "PATCH", "/api/admin/accounts/contract-new", accountPatch("all"), 200)
	call(admin, "GET", "/api/admin/accounts/contract-new/deletion-preview", nil, 200)
	call(admin, "DELETE", "/api/admin/accounts/contract-new", map[string]any{}, 200)
	call(admin, "GET", "/api/admin/account-audit", nil, 200)
	call(finance, "GET", "/api/warehouse-requests", nil, 200)
	call(finance, "GET", "/api/runs", nil, 200)
	for _, path := range []string{"/api/overview?run_id=" + f.run, "/api/transactions?run_id=" + f.run + "&page_size=2", "/api/transactions?run_id=" + f.run + "&customer=absent", "/api/allocations?run_id=" + f.run, "/api/cases?run_id=" + f.run, "/api/cases/" + f.cases[0], "/api/cases/" + f.cases[0] + "/evidence?page_size=1", "/api/cases/" + f.cases[0] + "/candidates?page_size=1", "/api/cases/" + f.cases[0] + "/audit", "/api/audit", "/api/review-sets/latest?run_id=" + f.run, "/api/case-batches/latest?run_id=" + f.run} {
		call(finance, "GET", path, nil, 200)
	}
	call(finance, "POST", "/api/cases/"+f.cases[0]+"/assign", map[string]any{"assignee": "http-owner"}, 200)
	call(owner, "GET", "/api/overview?run_id="+f.run, nil, 200)
	call(owner, "POST", "/api/cases/"+f.cases[0]+"/start", map[string]any{"version": 1}, 409)
	call(owner, "POST", "/api/cases/"+f.cases[0]+"/start", map[string]any{"version": 2}, 200)
	w := call(owner, "POST", "/api/cases/"+f.cases[0]+"/remediations", store.RemediationRequest{Version: 3, RequestID: "contract-wait", Action: "wait_materials", Evidence: "isolated contract fixture", MissingMaterials: "source document", NextStep: "verify source"}, 200)
	var remediation store.RemediationResult
	decodeHTTPResponse(t, w, &remediation)
	call(owner, "GET", "/api/cases/"+f.cases[0]+"/evidence", nil, 200)
	call(finance, "GET", "/api/audit?case_id="+f.cases[0], nil, 200)
	w = call(finance, "POST", "/api/review-sets", map[string]any{"run_id": f.run}, 200)
	var metadata reviewHTTPMetadata
	decodeHTTPResponse(t, w, &metadata)
	call(finance, "GET", "/api/review-sets/latest?run_id="+f.run, nil, 200)
	call(finance, "GET", "/api/review-sets/"+metadata.ID+"?page_size=1", nil, 200)
	call(finance, "GET", "/api/review-sets/"+metadata.ID+"/groups?group_by=invoice", nil, 200)
	w = call(finance, "POST", "/api/case-batches/preview", map[string]any{"set_id": metadata.ID, "action": "approve", "ids": []string{f.cases[2]}, "conclusion": "isolated contract approval"}, 200)
	var batch reviewHTTPBatch
	decodeHTTPResponse(t, w, &batch)
	call(finance, "GET", "/api/case-batches/"+batch.ID, nil, 200)
	call(finance, "POST", "/api/case-batches/preview", map[string]any{"set_id": metadata.ID, "retry_batch_id": batch.ID}, 200)
	call(finance, "POST", "/api/case-batches/"+batch.ID+"/commit", map[string]any{}, 200)
	call(finance, "GET", "/api/case-batches/"+batch.ID+"?status=succeeded", nil, 200)
	call(finance, "GET", "/api/case-batches/latest?run_id="+f.run, nil, 200)
	call(finance, "GET", "/api/allocations?run_id="+f.run, nil, 200)
	// Include real WebAuthn options and proof responses; no mock login endpoint
	// or manually inserted business session participates in the contract check.
	keyClient := &httpSecurityClient{id: "http-other"}
	call(keyClient, "POST", "/api/auth/setup/start", map[string]any{"id": keyClient.id}, 200)
	call(keyClient, "POST", "/api/auth/setup/password", map[string]any{"password": httpFixturePassword}, 200)
	w = call(nil, "GET", "/api/auth/client-config", nil, 200)
	var clientConfig auth.ClientConfig
	decodeHTTPResponse(t, w, &clientConfig)
	device := newNativeHTTPAuthenticator(t)
	w = call(keyClient, "POST", "/api/auth/native-passkeys/register/begin", map[string]any{"binding_id": "default"}, 200)
	var challenge auth.PasskeyBegin
	decodeHTTPResponse(t, w, &challenge)
	call(keyClient, "POST", "/api/auth/native-passkeys/register/finish?challenge_id="+challenge.ChallengeID+"&binding_id=default", device.response(t, challenge, clientConfig.NativePasskey, keyClient.id, true, 0x45), 200)
	call(keyClient, "POST", "/api/auth/setup/complete", map[string]any{"saved": true}, 200)
	for _, family := range []string{"passkeys", "native-passkeys"} {
		for _, id := range []string{keyClient.id, ""} {
			picker := &httpSecurityClient{}
			w = call(picker, "POST", "/api/auth/"+family+"/login/begin", map[string]any{"id": id}, 200)
			decodeHTTPResponse(t, w, &challenge)
			call(picker, "POST", "/api/auth/"+family+"/login/finish?challenge_id="+challenge.ChallengeID, device.response(t, challenge, clientConfig.NativePasskey, keyClient.id, false, 5), 200)
		}
	}
	// Real failure paths exercise documented problem responses, including handlers
	// whose pagination errors intentionally contain only a message.
	call(nil, "GET", "/api/overview?run_id="+f.run, nil, 401)
	call(owner, "GET", "/api/users", nil, 403)
	call(finance, "GET", "/api/cases/does-not-exist", nil, 404)
	call(finance, "GET", "/api/cases?page=bad&run_id="+f.run, nil, 422)
	call(finance, "GET", "/api/review-sets/"+metadata.ID+"/groups?page=-1", nil, 422)
	v.request(t, f, finance, "POST", "/api/review-sets", map[string]any{"run_id": f.run}, map[string]string{"X-CSRF-Token": ""}, 403)
	accountSQL(t, f, `CREATE TRIGGER fail_contract_audit BEFORE INSERT ON account_audit WHEN NEW.action='passkey_domains' BEGIN SELECT RAISE(ABORT,'isolated contract failure');END`)
	call(admin, "PUT", "/api/admin/passkey-domains", map[string]any{"version": 2, "origins": []string{}}, 500)
	accountSQL(t, f, `DROP TRIGGER fail_contract_audit`)
	for i := 0; i < 12; i++ {
		response := f.request(t, nil, "POST", "/api/auth/login", map[string]any{"id": "contract-absent", "password": httpFixturePassword}, nil)
		if response.Code != 401 && response.Code != 429 {
			t.Fatalf("unexpected authentication failure status %d", response.Code)
		}
		pattern, _ := v.operation(t, "POST", "/api/auth/login")
		instance, e := jsonschema.UnmarshalJSON(bytes.NewReader(response.Body.Bytes()))
		if e != nil {
			t.Fatal("invalid error JSON")
		}
		v.validate(t, "/paths/"+contractPointer(pattern)+"/post/responses/"+strconv.Itoa(response.Code)+"/content/application~1json/schema", instance)
		if response.Code == 429 {
			break
		}
		if i == 11 {
			t.Fatal("isolated repeated failures did not produce rate-limit response")
		}
	}
	t.Logf("validated %d distinct request/response schemas against actual isolated HTTP traffic", len(v.compiled))
}
