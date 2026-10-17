package store

import (
	"context"
	"database/sql/driver"
	"fmt"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"modernc.org/sqlite"
	"returnsrecon/internal/model"
)

func TestBalanceBatchCancellationRollsBackAndRetries(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// Test-only SQLite callback cancels while the second bounded statement is
	// executing, not merely before Run or after all balances have committed.
	function := fmt.Sprintf("test_cancel_balance_%d", time.Now().UnixNano())
	if err := sqlite.RegisterScalarFunction(function, 0, func(*sqlite.FunctionContext, []driver.Value) (driver.Value, error) {
		cancel()
		return nil, nil
	}); err != nil {
		t.Fatal(err)
	}
	s := openTest(t, filepath.Join(t.TempDir(), "cancel.sqlite"))
	rows := make([]model.Row, balanceBatchRows+20)
	ids := make([]string, len(rows))
	for i := range rows {
		rows[i] = fixtureRow(fmt.Sprintf("0:%d", i+2), i+2, 6, "09:00")
		ids[i] = rows[i].ID
	}
	dir, m := preparedFixture(t, rows)
	if _, err := s.Import(context.Background(), dir); err != nil {
		t.Fatal(err)
	}
	sort.Strings(ids)
	if _, err := s.DB.Exec(fmt.Sprintf(`CREATE TRIGGER cancel_balance_batch BEFORE INSERT ON sale_balances WHEN NEW.sale_id='%s' BEGIN SELECT %s(); END`, ids[balanceBatchRows+5], function)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Run(ctx, m.DatasetID, 2); err == nil || ctx.Err() == nil {
		t.Fatal("mid-statement cancellation did not stop the run")
	}
	if scalar(t, s, "SELECT count(*) FROM sale_balances") != balanceBatchRows || scalar(t, s, "SELECT count(*) FROM runs WHERE status='published'") != 0 {
		t.Fatal("cancelled transaction retained partial rows or published")
	}
	if _, err := s.DB.Exec("DROP TRIGGER cancel_balance_batch"); err != nil {
		t.Fatal(err)
	}
	run, err := s.Run(context.Background(), m.DatasetID, 2)
	if err != nil || run.Reused || scalar(t, s, "SELECT count(*) FROM sale_balances WHERE run_id=?", run.RunID) != len(rows) || scalar(t, s, "SELECT count(*) FROM pragma_foreign_key_check") != 0 {
		t.Fatalf("retry after cancellation did not recover all balances: %+v %v", run, err)
	}
}
