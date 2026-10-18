package store

import (
	"context"
	"sync"
	"testing"
	"time"

	"returnsrecon/internal/model"
)

func TestReviewConcurrentSnapshotsReleaseReadConnections(t *testing.T) {
	rows := []model.Row{reviewRaw(t, fixtureRow("0:2", 2, 10, "08:00")), reviewRaw(t, fixtureRow("0:3", 3, 10, "09:00")), reviewRaw(t, fixtureRow("0:4", 4, -4, "10:00"))}
	s, run, _ := reviewFixture(t, rows)
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	start := make(chan struct{})
	result := make(chan error, 4)
	var wg sync.WaitGroup
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			set, e := s.GenerateReviewSet(ctx, testFinance, run.RunID)
			if e == nil {
				_, e = s.PreviewBatch(ctx, testFinance, BatchRequest{SetID: set.ID, Action: "approve", All: true, RecommendedOnly: true, Conclusion: "isolated concurrency verification"}, WorkflowActor{})
			}
			result <- e
		}()
	}
	close(start)
	wg.Wait()
	close(result)
	for e := range result {
		if e != nil {
			t.Fatal("four readers must release their reserved connections before persisting previews:", e)
		}
	}
	if scalar(t, s, "SELECT count(*) FROM review_sets") != 4 || scalar(t, s, "SELECT count(*) FROM review_batches") != 4 || scalar(t, s, "SELECT count(*) FROM audit_events") != 0 || scalar(t, s, "SELECT count(*) FROM allocations") != 0 {
		t.Fatal("preview concurrency wrote business state or lost a snapshot")
	}
}
