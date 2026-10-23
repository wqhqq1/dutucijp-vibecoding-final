// Package apicontract describes the existing wire protocol. Struct schemas are
// reflected from production DTOs; route declarations below are checked against Gin.
package apicontract

import (
	"encoding/json"
	"path"
	"reflect"
	"regexp"
	"returnsrecon/internal/access"
	"returnsrecon/internal/auth"
	"returnsrecon/internal/store"
	"strings"
)

type M = map[string]any
type Registry struct{ schemas M }

func (r *Registry) schema(t reflect.Type) M {
	if t == reflect.TypeOf(json.RawMessage{}) {
		return M{"description": "结构化原始 JSON 证据；形状随审计事件变化"}
	}
	switch t.Kind() {
	case reflect.Pointer:
		return M{"anyOf": []any{r.schema(t.Elem()), M{"type": "null"}}}
	case reflect.Slice, reflect.Array:
		return M{"anyOf": []any{M{"type": "array", "items": r.schema(t.Elem())}, M{"type": "null"}}}
	case reflect.Map:
		return M{"type": []string{"object", "null"}, "additionalProperties": r.schema(t.Elem())}
	case reflect.Interface:
		return M{}
	case reflect.String:
		return M{"type": "string"}
	case reflect.Bool:
		return M{"type": "boolean"}
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64, reflect.Uint, reflect.Uint8, reflect.Uint32, reflect.Uint64:
		return M{"type": "integer", "format": "int64"}
	case reflect.Float32, reflect.Float64:
		return M{"type": "number"}
	case reflect.Struct:
		name := path.Base(t.PkgPath()) + "_" + t.Name()
		if t.Name() == "" {
			return r.object(t)
		}
		if _, exists := r.schemas[name]; !exists {
			r.schemas[name] = M{}
			r.schemas[name] = r.object(t)
		}
		return M{"$ref": "#/components/schemas/" + name}
	}
	return M{}
}
func (r *Registry) object(t reflect.Type) M {
	properties := M{}
	required := []string{}
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if f.PkgPath != "" {
			continue
		}
		tag := f.Tag.Get("json")
		if tag == "-" {
			continue
		}
		if f.Anonymous && tag == "" {
			inner := r.object(f.Type)
			for k, v := range inner["properties"].(M) {
				properties[k] = v
			}
			required = append(required, inner["required"].([]string)...)
			continue
		}
		name := strings.Split(tag, ",")[0]
		if name == "" {
			name = f.Name
		}
		s := r.schema(f.Type)
		if name == "price" || name == "amount" || name == "exact_amount" {
			s = M{"type": "string", "description": "精确十进制字符串，不转换为浮点数，不按显示位数舍入", "examples": []string{"0.100000000000000001"}}
		}
		if name == "time" {
			s["description"] = "来源本地时间，保留原始秒/小数精度，不做时区换算"
		}
		properties[name] = s
		if !strings.Contains(tag, "omitempty") {
			required = append(required, name)
		}
	}
	return M{"type": "object", "properties": properties, "required": required}
}
func object(props M, required ...string) M {
	if required == nil {
		required = []string{}
	}
	return M{"type": "object", "properties": props, "required": required, "additionalProperties": false}
}
func str() M            { return M{"type": "string"} }
func num() M            { return M{"type": "integer"} }
func arr(s M) M         { return M{"type": "array", "items": s} }
func ref(name string) M { return M{"$ref": "#/components/schemas/" + name} }

type endpoint struct {
	method, url, title, role string
	request, response        M
	query                    []string
}

func Document() M {
	r := &Registry{schemas: M{}}
	schema := func(v any) M { return r.schema(reflect.TypeOf(v)) }
	session := schema(auth.Session{})
	grant := schema(auth.Grant{})
	account := schema(auth.AccountView{})
	caseSchema := schema(store.WorkflowCase{})
	empty := object(M{})
	idBody := object(M{"id": str()}, "id")
	password := object(M{"password": M{"type": "string", "minLength": 16, "maxLength": 128}}, "password")
	code := object(M{"code": str()}, "code")
	version := object(M{"version": num()}, "version")
	credential := object(M{"id": str(), "rawId": str(), "type": M{"const": "public-key"}, "authenticatorAttachment": M{"type": []string{"string", "null"}}, "clientExtensionResults": M{"type": "object", "additionalProperties": true}, "response": M{"type": "object", "properties": M{"clientDataJSON": str(), "attestationObject": str(), "authenticatorData": str(), "signature": str(), "userHandle": M{"type": []string{"string", "null"}}, "transports": arr(str())}, "required": []string{"clientDataJSON"}, "additionalProperties": true}}, "id", "rawId", "type", "response")
	credential["additionalProperties"] = true
	r.schemas["PublicKeyCredential"] = credential
	r.schemas["WebAuthnOptions"] = M{"type": "object", "properties": M{"publicKey": M{"type": "object", "properties": M{"challenge": str(), "timeout": num(), "rp": object(M{"id": str(), "name": str()}, "id", "name"), "rpId": str(), "user": object(M{"id": str(), "name": str(), "displayName": str()}, "id", "name", "displayName"), "pubKeyCredParams": arr(object(M{"type": str(), "alg": num()}, "type", "alg")), "allowCredentials": arr(object(M{"type": str(), "id": str(), "transports": arr(str())}, "type", "id")), "excludeCredentials": arr(object(M{"type": str(), "id": str(), "transports": arr(str())}, "type", "id")), "authenticatorSelection": M{"type": "object", "additionalProperties": true}, "userVerification": str(), "attestation": str(), "extensions": M{"type": "object", "additionalProperties": true}}, "required": []string{"challenge"}, "additionalProperties": true}}, "required": []string{"publicKey"}}
	begin := schema(auth.PasskeyBegin{})
	r.schemas["auth_PasskeyBegin"].(M)["properties"].(M)["options"] = ref("WebAuthnOptions")
	run := object(M{"run_id": str(), "dataset_id": str(), "scope": M{"enum": []string{"benchmark", "year1", "all"}}, "cleaning_version": str(), "rule_version": str()}, "run_id", "dataset_id", "scope", "cleaning_version", "rule_version")
	r.schemas["Run"] = run
	counts := M{"type": "object", "additionalProperties": num()}
	overview := object(M{"run_id": str(), "cases": object(M{"total": num(), "pending": num(), "in_progress": num(), "closed": num(), "waiting": num()}, "total", "pending", "in_progress", "closed", "waiting"), "currently_unallocated": num(), "reviewed_allocations": num(), "supplemental_sales": num(), "remediation": counts, "initial_reconciliation": M{"type": "object", "additionalProperties": true, "description": "全局财务/管理员可见的首次对账统计，包含 input/matched/suspected/no_original 等历史口径"}, "caveat": str()}, "run_id", "cases", "currently_unallocated", "reviewed_allocations", "remediation", "caveat")
	r.schemas["Overview"] = overview
	batchItem := object(M{"case_id": str(), "cancel_id": str(), "invoice": str(), "customer": str(), "stock": str(), "version": num(), "option": M{"anyOf": []any{schema(store.ReviewOption{}), M{"type": "null"}}}, "status": str(), "code": str(), "message": str(), "group_id": num(), "group_size": num()}, "case_id", "cancel_id", "invoice", "customer", "stock", "version", "option", "status", "code", "message", "group_id", "group_size")
	batch := object(M{"id": str(), "set_id": str(), "action": str(), "status": str(), "created_at": str(), "total": num(), "succeeded": num(), "skipped": num(), "failed": num(), "ready": num(), "readonly": num(), "groups": num(), "reasons": M{"anyOf": []any{arr(object(M{"code": str(), "message": str(), "count": num()}, "code", "message", "count")), M{"type": "null"}}}, "view": str(), "view_total": num(), "items": arr(batchItem), "page": num(), "page_size": num()}, "id", "set_id", "action", "status", "created_at", "total", "succeeded", "skipped", "failed", "ready", "readonly", "groups", "reasons", "view", "view_total", "items", "page", "page_size")
	r.schemas["BatchView"] = batch
	audit := object(M{"actor": str(), "action": str(), "method": str(), "result": str(), "occurred_at": str()}, "actor", "action", "method", "result", "occurred_at")
	job := object(M{"id": str(), "scope": str(), "status": M{"enum": []string{"queued", "running", "succeeded", "failed", "interrupted"}}, "stage": str(), "run_id": str(), "error": str(), "progress": M{"type": []string{"object", "null"}, "additionalProperties": true}}, "id", "scope", "status", "stage", "run_id", "progress")
	r.schemas["Job"] = job
	pageQuery := []string{"page", "page_size"}
	filterQuery := []string{"run_id", "page", "page_size", "waiting", "status", "reason", "assignee", "customer", "stock", "invoice", "country", "sort", "direction", "classification", "kind", "date_from", "date_to"}
	endpoints := []endpoint{
		{"get", "/health", "服务标识与运行实例", "public", nil, object(M{"application": M{"const": "returns-recon"}, "runtime_id": str()}, "application", "runtime_id"), nil},
		{"get", "/auth/client-config", "原生客户端配置及稳定后端身份", "public", nil, schema(auth.ClientConfig{}), nil},
		{"get", "/auth/me", "当前会话及 CSRF", "session", nil, session, nil},
		{"get", "/auth/setup/accounts", "尚未配置密码的账户", "public", nil, schema([]access.Profile{}), nil},
		{"post", "/auth/setup/start", "选择未设置密码账户", "public", idBody, grant, nil},
		{"post", "/auth/bootstrap", "兼容一次性初始化令牌", "public", object(M{"token": str()}, "token"), grant, nil},
		{"post", "/auth/setup/password", "设置首次强密码", "setup", password, grant, nil},
		{"post", "/auth/login", "密码验证取得因素验证会话", "public", object(M{"id": str(), "password": str()}, "id", "password"), grant, nil},
		{"post", "/auth/totp/enroll", "生成待验证 TOTP 绑定资料", "configure", empty, schema(auth.Enrollment{}), nil},
		{"post", "/auth/totp/confirm", "验证绑定 TOTP", "configure", code, grant, nil},
		{"post", "/auth/totp/login", "密码后验证 TOTP", "factor", code, grant, nil},
		{"post", "/auth/setup/complete", "确认保存恢复码并完成配置", "setup", object(M{"saved": M{"const": true}}, "saved"), grant, nil},
		{"post", "/auth/recover", "恢复码验证取得受限配置会话", "public", object(M{"id": str(), "password": str(), "code": str()}, "id", "password", "code"), grant, nil},
		{"post", "/auth/credentials/change", "更改密码或移除因素；password 非空时优先", "fresh", object(M{"password": str(), "remove": M{"enum": []string{"passkey", "totp"}}}), grant, nil},
		{"post", "/auth/logout", "注销当前会话及挑战", "session", empty, object(M{"logged_out": M{"type": "boolean"}}, "logged_out"), nil},
		{"post", "/auth/recovery/reissue", "生成新恢复码并作废旧码", "fresh", empty, grant, nil},
		{"get", "/runs", "已发布数据运行", "business", nil, arr(ref("Run")), nil},
		{"get", "/overview", "当前负责范围的统计", "business", nil, ref("Overview"), []string{"run_id"}},
		{"get", "/users", "可见派单人员", "finance", nil, schema([]access.Profile{}), nil},
		{"get", "/countries", "可见国家列表", "business", nil, arr(str()), nil},
		{"get", "/transactions", "交易筛选排序分页", "business", nil, schema(store.TransactionPage{}), filterQuery},
		{"get", "/allocations", "配对明细筛选分页", "business", nil, schema(store.AllocationPage{}), filterQuery},
		{"get", "/cases", "当前差异筛选分页", "business", nil, schema(store.CasePage{}), filterQuery},
		{"get", "/cases/{id}", "差异工作流状态", "business", nil, caseSchema, nil},
		{"get", "/cases/{id}/evidence", "原始及有效值、候选、决策和补充证据", "business", nil, schema(store.CaseEvidencePage{}), pageQuery},
		{"get", "/cases/{id}/candidates", "当前候选分页", "business", nil, schema(store.CandidatePage{}), pageQuery},
		{"get", "/cases/{id}/audit", "差异操作历史", "business", nil, schema([]store.WorkflowAudit{}), nil},
		{"post", "/cases/{id}/assign", "派给有国家权限的责任人", "finance", object(M{"assignee": str()}, "assignee"), caseSchema, nil},
		{"post", "/cases/{id}/start", "开始自己负责的差异", "owner", version, caseSchema, nil},
		{"post", "/cases/{id}/close", "已解决差异填写结论并关闭", "owner", object(M{"version": num(), "conclusion": str()}, "version", "conclusion"), caseSchema, nil},
		{"post", "/cases/{id}/confirm-match", "人工确认候选及数量分配", "owner", object(M{"version": num(), "sale_row_id": str()}, "version", "sale_row_id"), schema(store.ManualConfirmation{}), nil},
		{"post", "/cases/{id}/remediations", "补充资料并重新核查", "owner", schema(store.RemediationRequest{}), schema(store.RemediationResult{}), nil},
		{"get", "/exports", "导出全部筛选交易 CSV（不局限当前页）", "finance", nil, nil, filterQuery},
		{"get", "/audit", "认证审计或指定差异历史", "finance", nil, M{"anyOf": []any{arr(audit), schema([]store.WorkflowAudit{})}}, []string{"case_id"}},
		{"post", "/jobs", "创建数据准备/导入/对账任务", "data-manager", object(M{"scope": M{"enum": []string{"benchmark", "year1", "all"}}}, "scope"), ref("Job"), nil},
		{"get", "/jobs/{id}", "轮询数据任务", "data-manager", nil, ref("Job"), nil},
		{"get", "/admin/accounts", "账户名录", "admin", nil, arr(account), nil},
		{"post", "/admin/accounts", "创建待首次配置的账户", "admin", schema(auth.AccountInput{}), account, nil},
		{"patch", "/admin/accounts/{id}", "更新显示名和国家权限", "admin", object(M{"display_name": str(), "country_scope": M{"enum": []string{"all", "selected"}}, "countries": arr(str())}, "display_name", "country_scope", "countries"), account, nil},
		{"get", "/admin/accounts/{id}/deletion-preview", "删除影响预览", "admin", nil, schema(auth.AccountDeletion{}), nil},
		{"delete", "/admin/accounts/{id}", "删除账户、撤销凭据并退回未办结派单", "admin", empty, schema(auth.AccountDeletion{}), nil},
		{"get", "/admin/account-audit", "账户管理审计", "admin", nil, M{"type": "array", "items": M{"type": "object", "additionalProperties": true}}, nil},
		{"get", "/admin/passkey-domains", "统一许可入口（保留旧 API 名称）", "admin", nil, schema(auth.PasskeyDomains{}), nil},
		{"put", "/admin/passkey-domains", "按版本更新许可入口并撤销在途挑战", "admin", object(M{"version": num(), "origins": arr(str()), "base_origin": str()}, "version", "origins"), schema(auth.PasskeyDomains{}), nil},
		{"get", "/warehouse-requests", "可见仓库开户申请", "finance", nil, schema([]auth.WarehouseRequest{}), nil},
		{"post", "/warehouse-requests", "申请负责范围内的仓库账户", "finance", object(M{"id": str(), "role": M{"const": "warehouse"}, "display_name": str(), "country_scope": str(), "countries": arr(str()), "reason": str()}, "id", "display_name", "country_scope", "countries", "reason"), schema(auth.WarehouseRequest{}), nil},
		{"post", "/warehouse-requests/{id}/approve", "批准并创建仓库账户", "admin", empty, schema(auth.WarehouseRequest{}), nil},
		{"post", "/warehouse-requests/{id}/reject", "驳回申请", "admin", object(M{"reason": str()}, "reason"), schema(auth.WarehouseRequest{}), nil},
		{"post", "/review-sets", "生成复核快照及推荐", "business", object(M{"run_id": str()}, "run_id"), object(M{"id": str(), "run_id": str(), "created_at": str(), "policy_version": str(), "counts": counts}, "id", "run_id", "created_at", "policy_version", "counts"), nil},
		{"get", "/review-sets/latest", "最近可用复核快照，无记录时 id 为空", "business", nil, object(M{"id": str(), "policy_version": str(), "counts": counts}, "id"), []string{"run_id"}},
		{"get", "/review-sets/{id}", "复核项目分页", "business", nil, schema(store.ReviewPage{}), append(append([]string{}, filterQuery...), "recommended_only", "group_by", "group_key")},
		{"get", "/review-sets/{id}/groups", "复核分组分页", "business", nil, object(M{"items": arr(schema(store.ReviewGroup{})), "total": num(), "page": num(), "page_size": num()}, "items", "total", "page", "page_size"), append(append([]string{}, filterQuery...), "recommended_only", "group_by")},
		{"post", "/case-batches/preview", "批量预览，不占余量", "business", batchInput(schema(store.BatchRequest{}), r.schemas), ref("BatchView"), nil},
		{"get", "/case-batches/latest", "最近批量结果，无记录时 id 为空", "business", nil, ref("BatchView"), []string{"run_id"}},
		{"get", "/case-batches/{id}", "按结果状态查看批量分页", "business", nil, ref("BatchView"), []string{"page", "status"}},
		{"post", "/case-batches/{id}/commit", "提交预览，重验版本/权限/余量", "business", empty, ref("BatchView"), nil},
	}
	for _, family := range []string{"passkeys", "native-passkeys"} {
		for _, purpose := range []string{"register", "login"} {
			body := object(M{"id": str()})
			queries := []string{"challenge_id"}
			if family == "native-passkeys" {
				body["properties"].(M)["binding_id"] = str()
				queries = append(queries, "binding_id")
			}
			role := "public"
			if purpose == "register" {
				role = "configure"
			}
			endpoints = append(endpoints, endpoint{"post", "/auth/" + family + "/" + purpose + "/begin", family + " " + purpose + "：开始挑战", role, body, begin, nil}, endpoint{"post", "/auth/" + family + "/" + purpose + "/finish", family + " " + purpose + "：提交凭据证明", role, ref("PublicKeyCredential"), grant, queries})
		}
	}
	paths := M{}
	pathRE := regexp.MustCompile(`\{([^}]+)\}`)
	for _, e := range endpoints {
		full := "/api" + e.url
		if paths[full] == nil {
			paths[full] = M{}
		}
		op := M{"summary": e.title, "operationId": e.method + "_" + strings.NewReplacer("/", "_", "{", "", "}", "", "-", "_").Replace(strings.TrimPrefix(e.url, "/")), "tags": []string{strings.Split(strings.TrimPrefix(e.url, "/"), "/")[0]}, "x-role": e.role}
		parameters := []any{}
		for _, match := range pathRE.FindAllStringSubmatch(e.url, -1) {
			parameters = append(parameters, M{"name": match[1], "in": "path", "required": true, "schema": str()})
		}
		seen := map[string]bool{}
		for _, name := range e.query {
			if seen[name] {
				continue
			}
			seen[name] = true
			s := str()
			if name == "page" {
				s = M{"type": "integer", "minimum": 1, "maximum": 10000000, "default": 1}
			}
			if name == "page_size" {
				s = M{"type": "integer", "minimum": 1, "maximum": 100, "default": 50}
			}
			if name == "sort" {
				s = M{"enum": []string{"source", "date", "invoice", "customer", "stock", "country"}}
			}
			if name == "direction" {
				s = M{"enum": []string{"asc", "desc"}}
			}
			parameters = append(parameters, M{"name": name, "in": "query", "required": name == "challenge_id" || name == "run_id", "schema": s})
		}
		if e.method != "get" {
			parameters = append(parameters, M{"name": "Origin", "in": "header", "required": true, "schema": str(), "description": "必须与实际连接入口的获准 Origin 完全一致"}, M{"name": "X-CSRF-Token", "in": "header", "required": false, "schema": str(), "description": "有会话时必填；Passkey login finish 必须使用 begin 返回的 csrf，包括匿名 selection Cookie"})
		}
		if len(parameters) > 0 {
			op["parameters"] = parameters
		}
		if e.request != nil {
			op["requestBody"] = M{"required": true, "content": M{"application/json": M{"schema": e.request}}}
		}
		success := M{"description": "成功；当前协议所有成功操作使用 HTTP 200"}
		if e.url == "/exports" {
			success["content"] = M{"text/csv": M{"schema": str()}}
			success["headers"] = M{"Content-Disposition": M{"schema": str()}}
		} else {
			success["content"] = M{"application/json": M{"schema": e.response}}
		}
		if e.request != nil && strings.HasPrefix(e.url, "/auth/") {
			success["headers"] = M{"Set-Cookie": M{"schema": str(), "description": "动态名称的 host-only HttpOnly SameSite=Strict Cookie；HTTPS 下 Secure"}}
		}
		responses := M{"200": success}
		for _, status := range []string{"401", "403", "404", "409", "422", "429", "500"} {
			responses[status] = M{"description": map[string]string{"401": "认证失败/会话失效", "403": "权限、配置、Host、Origin 或 CSRF 不符", "404": "资源不存在", "409": "版本、数量、材料或任务冲突", "422": "输入无效", "429": "认证尝试限流", "500": "服务内部错误"}[status], "content": M{"application/json": M{"schema": ref("Problem")}}}
		}
		op["responses"] = responses
		if e.role == "public" {
			op["security"] = []any{}
		} else {
			op["security"] = []any{M{"SessionCookie": []any{}}}
		}
		paths[full].(M)[e.method] = op
	}
	r.schemas["Problem"] = M{"type": "object", "properties": M{"code": str(), "message": str(), "existing_assignee": str(), "current_version": num()}, "required": []string{"message"}, "additionalProperties": true}
	return M{"openapi": "3.1.0", "jsonSchemaDialect": "https://json-schema.org/draft/2020-12/schema", "info": M{"title": "退货对账与差异处理 API", "version": "1.0.0", "description": "Go 共用后端的现行协议。金额为精确字符串，来源时间不转换时区。角色及国家范围由服务端执行。原生认证的逻辑身份与连接入口分别绑定。"}, "servers": []any{M{"url": "http://localhost:8080", "description": "示例；客户端使用管理员批准的实际完整入口"}}, "paths": paths, "components": M{"schemas": r.schemas, "securitySchemes": M{"SessionCookie": M{"type": "apiKey", "in": "cookie", "name": "recon_session", "description": "默认8080端口名称；其他端口为 recon_session_<port>。应接收实际 Set-Cookie，禁止硬编码或从JSON提取会话。"}}}}
}

func batchInput(s M, schemas M) M {
	name := strings.TrimPrefix(s["$ref"].(string), "#/components/schemas/")
	value := schemas[name].(M)
	b := M{}
	for k, v := range value {
		b[k] = v
	}
	b["required"] = []string{"set_id"}
	b["anyOf"] = []any{M{"required": []string{"action"}}, M{"required": []string{"retry_batch_id"}}}
	return b
}
