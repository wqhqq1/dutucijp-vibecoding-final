package web

import (
	"fmt"
	"testing"

	"github.com/gin-gonic/gin"
	"returnsrecon/internal/store"
)

func batchViewFixture(status string) store.ReviewBatch {
	item := func(id string) store.BatchItem {
		return store.BatchItem{Item: store.ReviewItem{Case: store.WorkflowCase{ID: id, CancelID: id}}}
	}
	b := store.ReviewBatch{ID: "b", Status: status, Total: 6, ReadOnly: 2}
	b.Groups = []store.BatchGroup{
		{ID: 1, Status: "skipped", Code: "incomplete_basket", Message: "整单", Items: []store.BatchItem{item("s1"), item("s2")}},
		{ID: 2, Status: "ready", Items: []store.BatchItem{item("r1")}},
		{ID: 3, Status: "skipped", Code: "incomplete_basket", Message: "整单", Items: []store.BatchItem{item("s3")}},
		{ID: 4, Status: "ready", Items: []store.BatchItem{item("r2"), item("r3")}},
		{ID: 5, Status: "failed", Code: "conflict", Message: "冲突", Items: []store.BatchItem{item("f1")}},
	}
	return b
}

func viewIDs(v gin.H) string {
	out := ""
	for _, it := range v["items"].([]gin.H) {
		out += fmt.Sprint(it["case_id"], ",")
	}
	return out
}

func TestTC55_BatchViewSeparatesReadyFromSkippedRows(t *testing.T) {
	v := batchView(batchViewFixture("preview"), 1, "")
	if v["view"] != "ready" || v["view_total"] != 3 || viewIDs(v) != "r1,r2,r3," {
		t.Fatalf("preview default mixed statuses: %v %v %s", v["view"], v["view_total"], viewIDs(v))
	}
	if v["ready"] != 3 || v["skipped"] != 3 || v["failed"] != 1 || v["readonly"] != 2 || v["groups"] != 5 || v["total"] != 6 {
		t.Fatalf("counts changed: %+v", v)
	}
	skipped := batchView(batchViewFixture("preview"), 1, "skipped")
	if skipped["view_total"] != 3 || viewIDs(skipped) != "s1,s2,s3," {
		t.Fatalf("skipped view: %s", viewIDs(skipped))
	}
	reasons := v["reasons"].([]*batchReason)
	if len(reasons) != 2 || *reasons[0] != (batchReason{"incomplete_basket", "整单", 3}) || *reasons[1] != (batchReason{"conflict", "冲突", 1}) {
		t.Fatalf("reasons not aggregated: %+v", reasons)
	}
	done := batchViewFixture("completed")
	done.Groups[1].Status, done.Groups[3].Status = "succeeded", "succeeded"
	if r := batchView(done, 1, ""); r["view"] != "succeeded" || r["view_total"] != 3 {
		t.Fatalf("result default view: %v", r["view"])
	}
	if r := batchView(done, 2, "skipped"); len(r["items"].([]gin.H)) != 0 || r["view_total"] != 3 {
		t.Fatal("paging past filtered rows returned items")
	}
}
