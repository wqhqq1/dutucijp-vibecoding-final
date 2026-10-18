package store

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strconv"
	"testing"

	"returnsrecon/internal/model"
)

// reviewRaw is a typed, eight-cell source fixture. Unlike the legacy fixtures,
// its original fields agree with the normalized values used by the test.
func reviewRaw(t *testing.T, row model.Row) model.Row {
	t.Helper()
	cells := [8][2]string{
		{"s", row.Invoice}, {"s", row.Stock}, {"s", row.Description},
		{"n", strconv.FormatInt(row.Quantity, 10)}, {"s", row.Time},
		{"n", row.Price}, {"s", row.Customer}, {"s", row.Country},
	}
	value, err := json.Marshal(cells)
	if err != nil {
		t.Fatal(err)
	}
	row.RawJSON = string(value)
	return row
}

func reviewFixture(t *testing.T, rows []model.Row) (*Store, Summary, Manifest) {
	t.Helper()
	s := openTest(t, filepath.Join(t.TempDir(), "review-isolated.sqlite"))
	dir, manifest := preparedFixture(t, rows)
	if _, err := s.Import(context.Background(), dir); err != nil {
		t.Fatal(err)
	}
	run, err := s.Run(context.Background(), manifest.DatasetID, 1)
	if err != nil {
		t.Fatal(err)
	}
	return s, run, manifest
}

func reviewProcessing(t *testing.T, s *Store, runID, cancelID string) WorkflowCase {
	t.Helper()
	c := assignWorkflow(t, s, ID(runID+"|"+cancelID))
	c, err := s.TransitionCase(context.Background(), testOwner, c.ID, "in_progress", c.Version, "")
	if err != nil {
		t.Fatal(err)
	}
	return c
}
