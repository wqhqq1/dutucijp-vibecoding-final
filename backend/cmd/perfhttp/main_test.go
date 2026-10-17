package main

import (
	"encoding/json"
	"net/url"
	"reflect"
	"testing"

	"returnsrecon/internal/store"
	"returnsrecon/internal/web"
)

func TestWorkloadSelectionPreservesDefaultAndBalancesMatrix(t *testing.T) {
	legacy := queryWorkloads("run+id", "case-id", false)
	want := []workload{
		{Category: "cases", Path: "/api/cases?run_id=run%2Bid&page_size=50"},
		{Category: "transactions", Path: "/api/transactions?run_id=run%2Bid&page_size=50"},
		{Category: "overview", Path: "/api/overview?run_id=run%2Bid"},
		{Category: "allocations", Path: "/api/allocations?run_id=run%2Bid&page_size=50"},
		{Category: "case_evidence", Path: "/api/cases/case-id/evidence?page_size=50"},
		{Category: "case_candidates", Path: "/api/cases/case-id/candidates?page_size=50"},
	}
	if !reflect.DeepEqual(legacy, want) {
		t.Fatal("default workload changed")
	}
	mat := queryWorkloads("run+id", "case-id", true)
	counts := map[string]int{}
	for worker := 0; worker < 10; worker++ {
		for seq := 0; seq < 30; seq++ {
			counts[mat[(worker+seq)%len(mat)].Category]++
		}
	}
	if len(counts) != 10 {
		t.Fatal("matrix must cover ten conditions")
	}
	for _, work := range mat {
		if counts[work.Category] != 30 {
			t.Fatal("matrix must send 30 real requests per condition")
		}
		parsed, err := url.Parse(work.Path)
		if err != nil {
			t.Fatal(err)
		}
		q := parsed.Query()
		if parsed.Path != "/api/transactions" || q.Get("run_id") != "run+id" || work.Filter == nil || work.ExpectedTotal < 1 {
			t.Fatal("matrix needs explicit all-run transaction paths and reference totals")
		}
	}
	if mat[6].Filter.Page != 10000 || mat[6].Filter.PageSize != 50 || mat[9].Filter.Page != 10000 || mat[9].Filter.PageSize != 100 || mat[9].Filter.Sort != "date" {
		t.Fatal("deep-page conditions changed")
	}
}

func TestAcceptanceRequiresEveryRequestInRealHeavyWindow(t *testing.T) {
	for _, job := range []web.Job{{Status: "queued", Stage: "importing"}, {Status: "running", Stage: "preparing"}, {Status: "running", Stage: "published"}, {Status: "succeeded", Stage: "reconciling"}} {
		if heavyJobActive(job) {
			t.Fatalf("non-heavy state counted active: %+v", job)
		}
	}
	for _, stage := range []string{"importing", "reconciling"} {
		if !heavyJobActive(web.Job{Status: "running", Stage: stage}) {
			t.Fatal("real heavy stage rejected")
		}
	}
	passed := report{Workers: 10, PerWorker: 30, Requests: 300, InWindow: 300, P95: 500, SourceBefore: "hash", SourceAfter: "hash", JobFinal: web.Job{Status: "succeeded"}}
	if !baselineSatisfied(passed) {
		t.Fatal("valid boundary result rejected")
	}
	for _, alter := range []func(*report){func(r *report) { r.InWindow = 299 }, func(r *report) { r.Failures = 1 }, func(r *report) { r.P95 = 500.001 }, func(r *report) { r.Requests = 299 }, func(r *report) { r.JobFinal.Status = "failed" }, func(r *report) { r.SourceAfter = "changed" }} {
		r := passed
		alter(&r)
		if baselineSatisfied(r) {
			t.Fatal("invalid measurement marked passed")
		}
	}
}

func TestTransactionMatrixRejectsWrongPageFilterOrderOrCounts(t *testing.T) {
	f := store.QueryFilter{Country: "France", Page: 1, PageSize: 50}
	work := workload{Filter: &f, ExpectedTotal: 2, ExpectedReturned: 2}
	first := store.TransactionRecord{Scope: "all", SourceHash: "bcbe73b35f5b7babf197fb0cb983a11f5d9ff929078d4aa53d171b1f2df2e980"}
	first.ID, first.SheetIndex, first.SourceRow, first.Country = "0:2", 0, 2, "France"
	second := first
	second.ID, second.SourceRow = "0:10", 10
	page := store.TransactionPage{Page: 1, PageSize: 50, Total: 2, Items: []store.TransactionRecord{first, second}}
	check := func(p store.TransactionPage) bool {
		data, err := json.Marshal(p)
		if err != nil {
			t.Fatal(err)
		}
		return validTransactionResponse(work, data)
	}
	if !check(page) {
		t.Fatal("correct page rejected")
	}
	wrong := page
	wrong.Total = 3
	if check(wrong) {
		t.Fatal("wrong total accepted")
	}
	wrong = page
	wrong.Page = 2
	if check(wrong) {
		t.Fatal("wrong page accepted")
	}
	wrong = page
	wrong.Items = []store.TransactionRecord{second, first}
	if check(wrong) {
		t.Fatal("wrong numeric source order accepted")
	}
	wrong = page
	wrong.Items = []store.TransactionRecord{first, first}
	if check(wrong) {
		t.Fatal("duplicate source accepted")
	}
	wrong = page
	wrong.Items = append([]store.TransactionRecord{}, page.Items...)
	wrong.Items[0].Country = "UK"
	if check(wrong) {
		t.Fatal("wrong filter accepted")
	}
}
