package store

import (
	"context"
	"path/filepath"
	"returnsrecon/internal/model"
	"testing"
)

func TestTC01TC08_VerifyDatasetSourceExclusionAndCorruption(t *testing.T) {
	ctx := context.Background()
	s := openTest(t, filepath.Join(t.TempDir(), "isolated.sqlite"))
	rows := []model.Row{fixtureRow("0:2", 2, 10, "09:00"), fixtureRow("0:3", 3, 1, "10:00")}
	rows[1].Price = "0"
	rows[1].Excluded = "nonpositive_price"
	dir, m := preparedFixture(t, rows)
	if _, err := s.Import(ctx, dir); err != nil {
		t.Fatal(err)
	}
	v, err := s.VerifyDataset(ctx, m.DatasetID, "")
	if err != nil {
		t.Fatal(err)
	}
	if !v.Conserved || v.Counts.Raw != 2 || v.Counts.Accepted != 1 || v.Counts.Excluded != 1 || v.ExclusionReasons["nonpositive_price"] != 1 || len(v.Sheets) != 1 || v.Example == nil || v.Example.SourceRow != 3 || v.Example.RawJSON != rows[1].RawJSON {
		t.Fatalf("lost source/counts: %+v", v)
	}
	if _, err = s.VerifyDataset(ctx, m.DatasetID, "0:2"); err == nil {
		t.Fatal("accepted row reported as exclusion")
	}
	if _, err = s.DB.Exec("DELETE FROM normalized_rows WHERE dataset_id=? AND id='0:3'", m.DatasetID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.VerifyDataset(ctx, m.DatasetID, ""); err == nil {
		t.Fatal("damaged published dataset passed conservation")
	}
}

func TestTC01_VerifyRejectsUnpublishedDataset(t *testing.T) {
	ctx := context.Background()
	s := openTest(t, filepath.Join(t.TempDir(), "isolated.sqlite"))
	dir, m := preparedFixture(t, []model.Row{fixtureRow("0:2", 2, 1, "09:00")})
	if _, err := s.Import(ctx, dir); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.Exec("UPDATE datasets SET status='failed' WHERE id=?", m.DatasetID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.VerifyDataset(ctx, m.DatasetID, ""); err == nil {
		t.Fatal("failed dataset reported as verified")
	}
}
