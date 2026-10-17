package store

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"testing"

	"returnsrecon/internal/model"
)

func TestComputeLoadPreservesMatchingFieldsAndRejectsBadSourceIDs(t *testing.T) {
	for _, bad := range []string{"missing", ":2", "-1:2", "2:2", "0:1", "0:02", "00:2", "0:2:3", "0:99999999999999999999999999", "+0:2", "0:+2", "0:-2", "0:2 ", "0:2a"} {
		t.Run(bad, func(t *testing.T) {
			s := openTest(t, filepath.Join(t.TempDir(), "compute.sqlite"))
			r := fixtureRow("1:10", 10, 6, "09:00")
			r.SheetIndex, r.Sheet, r.Price, r.Flags = 1, "Year 2010-2011", "0.100000000000000000000000001", "same_sheet_duplicate"
			dir, m := preparedFixture(t, []model.Row{r})
			if _, err := s.Import(context.Background(), dir); err != nil {
				t.Fatal(err)
			}
			rows, err := s.loadComputeRows(context.Background(), m.DatasetID, 1)
			if err != nil || len(rows) != 1 {
				t.Fatalf("load=%+v %v", rows, err)
			}
			got := rows[0]
			if got.ID != r.ID || got.SheetIndex != r.SheetIndex || got.SourceRow != r.SourceRow || got.Price != r.Price || got.Flags != r.Flags || got.Customer != r.Customer || got.Stock != r.Stock || got.Country != r.Country || got.Time != r.Time || got.Kind != r.Kind || got.Quantity != r.Quantity {
				t.Fatal("compute fields changed")
			}
			if _, err = s.DB.Exec("UPDATE normalized_rows SET id=? WHERE dataset_id=?", bad, m.DatasetID); err != nil {
				t.Fatal(err)
			}
			if _, err = s.Run(context.Background(), m.DatasetID, 2); err == nil {
				t.Fatal("malformed persisted source ID was published")
			}
			if scalar(t, s, "SELECT count(*) FROM runs WHERE status='published'") != 0 {
				t.Fatal("invalid input became visible")
			}
		})
	}
}

func TestSetBasedBalanceBatchesRollBackAndRetryWithoutLosingOrdinarySales(t *testing.T) {
	s := openTest(t, filepath.Join(t.TempDir(), "balances.sqlite"))
	rows := make([]model.Row, 0, balanceBatchRows+32)
	ids := make([]string, 0, balanceBatchRows+20)
	for i := 0; i < balanceBatchRows+20; i++ {
		r := fixtureRow(fmt.Sprintf("0:%d", i+2), i+2, 6, "09:00")
		r.Customer = fmt.Sprint(i)
		rows = append(rows, r)
		ids = append(ids, r.ID)
	}
	for i := 0; i < 10; i++ {
		r := fixtureRow(fmt.Sprintf("0:%d", balanceBatchRows+22+i), balanceBatchRows+22+i, 1, "09:00")
		r.Excluded = "fixture-excluded"
		rows = append(rows, r)
	}
	c := fixtureRow("1:2", 2, -4, "10:00")
	c.SheetIndex, c.Sheet, c.Customer = 1, "Year 2010-2011", "0"
	rows = append(rows, c)
	dir, m := preparedFixture(t, rows)
	if _, err := s.Import(context.Background(), dir); err != nil {
		t.Fatal(err)
	}
	sort.Strings(ids)
	if _, err := s.DB.Exec(fmt.Sprintf(`CREATE TRIGGER fail_balance_batch BEFORE INSERT ON sale_balances WHEN NEW.sale_id='%s' BEGIN SELECT RAISE(ABORT,'isolated balance failure'); END`, ids[balanceBatchRows+5])); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Run(context.Background(), m.DatasetID, 2); err == nil {
		t.Fatal("second batch injection should fail")
	}
	if scalar(t, s, "SELECT count(*) FROM sale_balances") != balanceBatchRows || scalar(t, s, "SELECT count(*) FROM runs WHERE status='published'") != 0 {
		t.Fatal("balance batch rollback or publication boundary lost")
	}
	if _, err := s.DB.Exec("DROP TRIGGER fail_balance_batch"); err != nil {
		t.Fatal(err)
	}
	run, err := s.Run(context.Background(), m.DatasetID, 2)
	if err != nil {
		t.Fatal(err)
	}
	if run.Reused || run.Matched != 1 || scalar(t, s, "SELECT count(*) FROM sale_balances WHERE run_id=?", run.RunID) != balanceBatchRows+20 || scalar(t, s, "SELECT count(*) FROM sale_balances WHERE run_id=? AND allocated=0 AND remaining=6", run.RunID) != balanceBatchRows+19 || scalar(t, s, "SELECT remaining FROM sale_balances WHERE run_id=? AND sale_id='0:2'", run.RunID) != 2 {
		t.Fatal("keyset batches lost ordinary sales, exclusion scope, or sparse allocation")
	}
}

func TestSetBasedBalancesRejectCorruptionAfterGuardedUpdate(t *testing.T) {
	s := openTest(t, filepath.Join(t.TempDir(), "updated.sqlite"))
	dir, m := preparedFixture(t, []model.Row{fixtureRow("0:2", 2, 6, "09:00"), fixtureRow("0:3", 3, -4, "10:00")})
	if _, err := s.Import(context.Background(), dir); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.Exec(`CREATE TRIGGER corrupt_final_balance AFTER UPDATE ON sale_balances BEGIN UPDATE sale_balances SET allocated=allocated+1,remaining=remaining-1 WHERE run_id=NEW.run_id AND sale_id=NEW.sale_id; END`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Run(context.Background(), m.DatasetID, 2); err == nil {
		t.Fatal("post-update corruption bypassed publication conservation")
	}
	if scalar(t, s, "SELECT count(*) FROM runs WHERE status='published'") != 0 {
		t.Fatal("bad final balance published")
	}
}

func TestGroupedPersistenceConservationRejectsBalancedButWrongAllocation(t *testing.T) {
	s := openTest(t, filepath.Join(t.TempDir(), "conservation.sqlite"))
	dir, m := preparedFixture(t, []model.Row{fixtureRow("0:2", 2, 6, "09:00"), fixtureRow("0:3", 3, -4, "10:00")})
	if _, err := s.Import(context.Background(), dir); err != nil {
		t.Fatal(err)
	}
	// Keep original-allocated=remaining valid while corrupting the relation to
	// actual allocations. Publication must still fail its independent SQL check.
	if _, err := s.DB.Exec(`CREATE TRIGGER corrupt_balance AFTER INSERT ON sale_balances WHEN NEW.sale_id='0:2' BEGIN UPDATE sale_balances SET allocated=allocated+1,remaining=remaining-1 WHERE run_id=NEW.run_id AND sale_id=NEW.sale_id; END`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Run(context.Background(), m.DatasetID, 2); err == nil {
		t.Fatal("allocation inconsistency passed grouped check")
	}
	if scalar(t, s, "SELECT count(*) FROM runs WHERE status='published'") != 0 {
		t.Fatal("corrupt balances became visible")
	}
}
