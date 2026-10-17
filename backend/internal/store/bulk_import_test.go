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
	// tables. The whole bounded import transaction must still roll back.
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

func TestImportPartialBatchesStayUnpublishedAndRetryBuildsIndexes(t *testing.T) {
	ctx := context.Background()
	s := openTest(t, filepath.Join(t.TempDir(), "retry.sqlite"))
	if scalar(t, s, "SELECT count(*) FROM sqlite_master WHERE name='row_identity'") != 0 {
		t.Fatal("empty database did not defer its row indexes")
	}
	rows := make([]model.Row, batchRows+100)
	for i := range rows {
		rows[i] = fixtureRow(fmt.Sprintf("0:%d", i+2), i+2, 10, "08:00")
	}
	dir, m := preparedFixture(t, rows)
	statement := fmt.Sprintf(`CREATE TRIGGER fail_next_batch BEFORE INSERT ON normalized_rows WHEN NEW.id='0:%d' BEGIN SELECT RAISE(ABORT,'isolated retry test'); END`, batchRows+5)
	if _, err := s.DB.Exec(statement); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Import(ctx, dir); err == nil {
		t.Fatal("expected second-batch failure")
	}
	if scalar(t, s, "SELECT count(*) FROM normalized_rows WHERE dataset_id=?", m.DatasetID) != batchRows || scalar(t, s, "SELECT count(*) FROM datasets WHERE status='published'") != 0 {
		t.Fatal("failed import lost atomic batch or publication boundary")
	}
	if _, err := s.DB.Exec("DROP TRIGGER fail_next_batch"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Import(ctx, dir); err != nil {
		t.Fatal(err)
	}
	if scalar(t, s, "SELECT count(*) FROM normalized_rows WHERE dataset_id=?", m.DatasetID) != len(rows) || scalar(t, s, "SELECT count(*) FROM source_rows") != len(rows) {
		t.Fatal("retry duplicated or lost source records")
	}
	if scalar(t, s, "SELECT count(*) FROM sqlite_master WHERE name IN ('row_identity','row_query_source','row_source_time','cases_source_order','allocations_source_order')") != 5 {
		t.Fatal("input published before required indexes were built")
	}
}
