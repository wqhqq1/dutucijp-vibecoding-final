package store

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"

	"returnsrecon/internal/model"
)

func TestBulkImportRollsBackEarlierSQLChunks(t *testing.T) {
	ctx := context.Background()
	s := openTest(t, filepath.Join(t.TempDir(), "bulk.sqlite"))
	rows := make([]model.Row, 130)
	for i := range rows {
		rn := i + 2
		rows[i] = fixtureRow(fmt.Sprintf("0:%d", rn), rn, 10, "08:00")
	}
	dir, manifest := preparedFixture(t, rows)
	// The failure is in the second SQL chunk, after the first chunk wrote both
	// tables. The whole <=1000-row import transaction must still roll back.
	if _, err := s.DB.Exec(`CREATE TRIGGER fail_second_chunk BEFORE INSERT ON normalized_rows WHEN NEW.id='0:67' BEGIN SELECT RAISE(ABORT,'isolated bulk rollback test'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Import(ctx, dir); err == nil {
		t.Fatal("expected the injected second-chunk failure")
	}
	if scalar(t, s, "SELECT count(*) FROM source_rows") != 0 || scalar(t, s, "SELECT count(*) FROM normalized_rows") != 0 || scalar(t, s, "SELECT count(*) FROM datasets WHERE status='published'") != 0 {
		t.Fatal("failed batch left partial source/normalized data or a publication")
	}
	if _, err := s.DB.Exec("DROP TRIGGER fail_second_chunk"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Import(ctx, dir); err != nil {
		t.Fatal(err)
	}
	if scalar(t, s, "SELECT count(*) FROM normalized_rows WHERE dataset_id=?", manifest.DatasetID) != len(rows) {
		t.Fatal("retry did not import all rows")
	}
	// Probe the real connection's required parameter budget, not a Python
	// SQLite build or an assumed compile-time maximum.
	arguments := make([]any, 960)
	arguments[959] = "parameter-960"
	var result string
	if err := s.DB.QueryRowContext(ctx, "SELECT ?960", arguments...).Scan(&result); err != nil || result != "parameter-960" {
		t.Fatalf("required 960-parameter budget unavailable: %q %v", result, err)
	}
}
