package web

import (
	"context"
	"net/url"
	"testing"

	"returnsrecon/internal/store"
)

func TestHTTPFR20_TrustedRemediationFlowsToNewUnassignedProblem(t *testing.T) {
	f := httpSecurityTest(t)
	finance := f.configured(t, "http-finance")
	owner := f.configured(t, "http-owner")
	other := f.configured(t, "http-other")
	f.assignHTTP(t, finance, 0, "http-owner")
	id := f.cases[0]
	expectHTTPStatus(t, f.request(t, owner, "POST", "/api/cases/"+id+"/start", map[string]any{"version": 2}, nil), 200)
	request := store.RemediationRequest{Version: 3, RequestID: "http-fr20-correct-quantity", Action: "correct_return", Evidence: "隔离HTTP夹具：核实退货数量为11", Changes: map[string]string{"quantity": "11"}}
	path := "/api/cases/" + id + "/remediations"
	expectHTTPStatus(t, f.request(t, other, "POST", path, request, nil), 403)
	expectHTTPStatus(t, f.request(t, owner, "POST", path, request, map[string]string{"X-CSRF-Token": ""}), 403)
	expectHTTPStatus(t, f.request(t, owner, "POST", "/api/cases/"+id+"/close", map[string]any{"version": 3, "conclusion": "资料不足直接结案"}, nil), 409)
	w := f.request(t, owner, "POST", path, request, nil)
	expectHTTPStatus(t, w, 200)
	var result store.RemediationResult
	decodeHTTPResponse(t, w, &result)
	if result.Case.Outcome != "reclassified" || result.Reclassified != 1 || result.Case.Current || len(result.CreatedCases) != 0 {
		t.Fatalf("warehouse response: %+v", result)
	}
	w = f.request(t, owner, "GET", "/api/cases/"+id+"/evidence", nil, nil)
	expectHTTPStatus(t, w, 200)
	var evidence store.CaseEvidencePage
	decodeHTTPResponse(t, w, &evidence)
	if evidence.Cancel.Quantity != -4 || evidence.EffectiveCancel.Quantity != -11 || len(evidence.Remediations) != 1 {
		t.Fatalf("historical/effective evidence mismatch: %+v", evidence)
	}
	w = f.request(t, finance, "GET", "/api/cases?run_id="+url.QueryEscape(f.run), nil, nil)
	expectHTTPStatus(t, w, 200)
	var cases store.CasePage
	decodeHTTPResponse(t, w, &cases)
	var next store.WorkflowCase
	for _, c := range cases.Items {
		if c.CancelID == result.Case.CancelID {
			next = c
		}
	}
	if next.Generation != 2 || next.Status != "pending" || next.Assignee != "" || next.Reason != "insufficient_remaining_quantity" || next.PreviousCaseID != id || cases.Total != 3 {
		t.Fatalf("new problem: %+v total=%d", next, cases.Total)
	}
	// Same request and identity replay the saved result; no second generation.
	expectHTTPStatus(t, f.request(t, owner, "POST", path, request, nil), 200)
	expectHTTPStatus(t, f.request(t, owner, "GET", "/api/cases/"+next.ID+"/evidence", nil, nil), 403)
	expectHTTPStatus(t, f.request(t, finance, "GET", "/api/overview?run_id="+url.QueryEscape(f.run), nil, nil), 200)
	expectHTTPStatus(t, f.request(t, finance, "GET", "/api/transactions?run_id="+url.QueryEscape(f.run)+"&status=pending", nil, nil), 200)
}

func TestHTTPFR20_WaitingFilterAndTransactionBoundRevocation(t *testing.T) {
	f := httpSecurityTest(t)
	finance := f.configured(t, "http-finance")
	owner := f.configured(t, "http-owner")
	f.assignHTTP(t, finance, 0, "http-owner")
	id := f.cases[0]
	expectHTTPStatus(t, f.request(t, owner, "POST", "/api/cases/"+id+"/start", map[string]any{"version": 2}, nil), 200)
	req := store.RemediationRequest{Version: 3, RequestID: "http-fr20-wait-materials", Action: "wait_materials", Evidence: "隔离核查，暂无原销售凭证", MissingMaterials: "原单凭证", NextStep: "联系仓库核对"}
	w := f.request(t, owner, "POST", "/api/cases/"+id+"/remediations", req, nil)
	expectHTTPStatus(t, w, 200)
	var result store.RemediationResult
	decodeHTTPResponse(t, w, &result)
	if !result.Case.Waiting || result.Case.Status != "in_progress" {
		t.Fatalf("waiting=%+v", result)
	}
	w = f.request(t, finance, "GET", "/api/cases?run_id="+url.QueryEscape(f.run)+"&waiting=1", nil, nil)
	expectHTTPStatus(t, w, 200)
	var page store.CasePage
	decodeHTTPResponse(t, w, &page)
	if page.Total != 1 || page.Items[0].ID != id {
		t.Fatal("waiting filter mismatch")
	}
	ctx, _ := f.capturedRequestContext(t, owner)
	expectHTTPStatus(t, f.request(t, owner, "POST", "/api/auth/logout", map[string]any{}, nil), 200)
	if _, e := f.server.Store.RemediateCase(ctx, store.WorkflowActor{ID: "http-owner", Role: "warehouse"}, id, req); e == nil {
		t.Fatal("revoked admitted context replayed a write")
	}
	current, e := f.server.Store.WorkflowCase(context.Background(), store.WorkflowActor{ID: "http-finance", Role: "finance"}, id)
	if e != nil || current.Version != result.Case.Version {
		t.Fatal("revoked request changed state")
	}
}
