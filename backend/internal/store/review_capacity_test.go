package store

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"returnsrecon/internal/model"
)

func reviewSharedCapacity(t *testing.T) (*Store, []WorkflowCase) {
	t.Helper()
	sale := fixtureRow("0:2", 2, 6, "08:00")
	sale.Invoice = "SAME-ORIGINAL"
	sale.Flags = "same_sheet_duplicate"
	other := sale
	other.ID, other.SourceRow, other.DuplicateOf = "0:3", 3, sale.ID
	rows := []model.Row{reviewRaw(t, sale), reviewRaw(t, other), reviewRaw(t, fixtureRow("0:4", 4, -4, "10:00")), reviewRaw(t, fixtureRow("0:5", 5, -4, "11:00"))}
	s, run, _ := reviewFixture(t, rows)
	// This fixture starts after a human has approved the source duplicate group.
	// Source balances remain separate; the group is the shared capacity resource.
	if _, err := s.DB.Exec(`INSERT INTO duplicate_sale_groups(run_id,id,canonical_id,capacity,actor,batch_id) VALUES(?,'approved-duplicate','0:2',6,?,'isolated-fixture')`, run.RunID, testOwner.ID); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"0:2", "0:3"} {
		if _, err := s.DB.Exec(`INSERT INTO duplicate_sale_members(run_id,sale_id,group_id) VALUES(?,?,'approved-duplicate')`, run.RunID, id); err != nil {
			t.Fatal(err)
		}
	}
	return s, []WorkflowCase{reviewProcessing(t, s, run.RunID, "0:4"), reviewProcessing(t, s, run.RunID, "0:5")}
}

func TestTC49_SingleConfirmationUsesApprovedGroupCapacity(t *testing.T) {
	s, cases := reviewSharedCapacity(t)
	if _, err := s.ConfirmCandidate(context.Background(), testOwner, cases[0].ID, "0:2", cases[0].Version); err != nil {
		t.Fatal(err)
	}
	before := workflowCurrent(t, s, cases[1].ID)
	audits := scalar(t, s, "SELECT count(*) FROM audit_events")
	if _, err := s.ConfirmCandidate(context.Background(), testOwner, cases[1].ID, "0:3", cases[1].Version); err == nil {
		t.Fatal("second source member bypassed one-sale duplicate group capacity")
	}
	if scalar(t, s, "SELECT sum(quantity) FROM allocations") != 4 || scalar(t, s, "SELECT count(*) FROM allocations") != 1 || scalar(t, s, "SELECT remaining FROM sale_balances WHERE sale_id='0:3'") != 6 || workflowCurrent(t, s, cases[1].ID) != before || scalar(t, s, "SELECT count(*) FROM audit_events") != audits {
		t.Fatal("rejected shared capacity confirmation left a partial change")
	}
}

func TestTC49_SeparatePoolsCannotOverdrawDifferentDuplicateMembers(t *testing.T) {
	s, cases := reviewSharedCapacity(t)
	other := openTest(t, s.path)
	start := make(chan struct{})
	results := make(chan error, 2)
	var wait sync.WaitGroup
	for i := range cases {
		wait.Add(1)
		go func(i int) {
			defer wait.Done()
			<-start
			db, saleID := s, "0:2"
			if i == 1 {
				db, saleID = other, "0:3"
			}
			_, err := db.ConfirmCandidate(context.Background(), testOwner, cases[i].ID, saleID, cases[i].Version)
			results <- err
		}(i)
	}
	close(start)
	wait.Wait()
	close(results)
	success := 0
	for err := range results {
		if err == nil {
			success++
		}
	}
	if success != 1 || scalar(t, s, "SELECT sum(quantity) FROM allocations") != 4 || scalar(t, s, "SELECT count(*) FROM audit_events WHERE action='confirm_candidate'") != 1 {
		t.Fatalf("different duplicate member race spent capacity twice: success=%d", success)
	}
}

func TestTC49_BatchAndSingleEntryShareDuplicateCapacity(t *testing.T) {
	s, cases := reviewSharedCapacity(t)
	ctx := context.Background()
	set, err := s.GenerateReviewSet(ctx, testOwner, cases[0].RunID)
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.PreviewBatch(ctx, testOwner, BatchRequest{SetID: set.ID, Action: "confirm", IDs: []string{cases[0].ID}}, WorkflowActor{})
	if err != nil {
		t.Fatal(err)
	}
	other := openTest(t, s.path)
	start := make(chan struct{})
	var batch ReviewBatch
	var batchErr, manualErr error
	var wait sync.WaitGroup
	wait.Add(2)
	go func() { defer wait.Done(); <-start; batch, batchErr = s.CommitBatch(ctx, testOwner, b.ID) }()
	go func() {
		defer wait.Done()
		<-start
		_, manualErr = other.ConfirmCandidate(ctx, testOwner, cases[1].ID, "0:3", cases[1].Version)
	}()
	close(start)
	wait.Wait()
	if batchErr != nil {
		t.Fatal(batchErr)
	}
	success := batch.Succeeded
	if manualErr == nil {
		success++
	}
	if success != 1 || scalar(t, s, "SELECT sum(quantity) FROM allocations") != 4 || scalar(t, s, "SELECT count(*) FROM audit_events WHERE action='confirm_candidate'") != 1 {
		t.Fatalf("single and batch entry race spent capacity twice: batch=%+v manual=%v", batch, manualErr)
	}
}

func TestTC49_BatchDuplicateActivationAndAuditRollback(t *testing.T) {
	for _, fault := range []bool{false, true} {
		t.Run(fmt.Sprint(fault), func(t *testing.T) {
			a := fixtureRow("0:2", 2, 6, "08:00")
			a.Invoice = "SOURCE-SAME"
			a.Flags = "same_sheet_duplicate"
			b := a
			b.ID, b.SourceRow = "0:3", 3
			rows := []model.Row{reviewRaw(t, a), reviewRaw(t, b), reviewRaw(t, fixtureRow("0:4", 4, -4, "10:00")), reviewRaw(t, fixtureRow("0:5", 5, -4, "11:00"))}
			s, run, _ := reviewFixture(t, rows)
			set, err := s.GenerateReviewSet(context.Background(), testFinance, run.RunID)
			if err != nil {
				t.Fatal(err)
			}
			batch, err := s.PreviewBatch(context.Background(), testFinance, BatchRequest{SetID: set.ID, Action: "approve", IDs: []string{ID(run.RunID + "|0:4")}, Conclusion: "隔离重复组核查"}, WorkflowActor{})
			if err != nil {
				t.Fatal(err)
			}
			if fault {
				if _, err = s.DB.Exec(`CREATE TRIGGER isolated_duplicate_audit_fault BEFORE INSERT ON audit_events WHEN NEW.action='close' BEGIN SELECT RAISE(ABORT,'isolated duplicate activation failure'); END;`); err != nil {
					t.Fatal(err)
				}
			}
			out, err := s.CommitBatch(context.Background(), testFinance, batch.ID)
			if err != nil {
				t.Fatal(err)
			}
			if fault {
				if out.Failed != 1 || scalar(t, s, "SELECT count(*) FROM duplicate_sale_groups") != 0 || scalar(t, s, "SELECT count(*) FROM duplicate_sale_members") != 0 || scalar(t, s, "SELECT count(*) FROM allocations") != 0 || scalar(t, s, "SELECT count(*) FROM audit_events") != 0 {
					t.Fatal("failed approval left activated group, allocation or audit")
				}
				return
			}
			if out.Succeeded != 1 || scalar(t, s, "SELECT count(*) FROM duplicate_sale_groups") != 1 || scalar(t, s, "SELECT count(*) FROM duplicate_sale_members") != 2 || scalar(t, s, "SELECT count(*) FROM review_confirmations WHERE recommendation_kind='duplicate'") != 1 {
				t.Fatalf("duplicate activation lost evidence: %+v", out)
			}
			c := reviewProcessing(t, s, run.RunID, "0:5")
			if _, err = s.ConfirmCandidate(context.Background(), testOwner, c.ID, "0:3", c.Version); err == nil {
				t.Fatal("single entry bypassed batch-activated group")
			}
			if scalar(t, s, "SELECT sum(quantity) FROM allocations") != 4 {
				t.Fatal("batch-activated duplicate capacity exceeded")
			}
		})
	}
}
