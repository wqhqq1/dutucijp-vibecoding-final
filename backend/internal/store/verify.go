package store

import (
	"context"
	"encoding/json"
	"fmt"
	"returnsrecon/internal/model"
)

type SheetCounts struct {
	Sheet    string `json:"sheet"`
	Raw      int    `json:"raw"`
	Accepted int    `json:"accepted"`
	Excluded int    `json:"excluded"`
	FirstRow int    `json:"first_source_row"`
	LastRow  int    `json:"last_source_row"`
}

type DatasetVerification struct {
	DatasetID        string          `json:"dataset_id"`
	Scope            string          `json:"scope"`
	SourceHash       string          `json:"source_hash"`
	CleaningVersion  string          `json:"cleaning_version"`
	SQLiteVersion    string          `json:"go_sqlite_version"`
	Source           json.RawMessage `json:"source"`
	Counts           Stats           `json:"counts"`
	ExclusionReasons map[string]int  `json:"exclusion_reasons"`
	Sheets           []SheetCounts   `json:"sheets"`
	Conserved        bool            `json:"conserved"`
	Example          *model.Row      `json:"excluded_source_example,omitempty"`
}

// VerifyDataset is for the local isolated CLI. It never starts an HTTP service.
func (s *Store) VerifyDataset(ctx context.Context, dataset, rowID string) (DatasetVerification, error) {
	v := DatasetVerification{DatasetID: dataset, ExclusionReasons: map[string]int{}}
	if err := s.DB.QueryRowContext(ctx, "SELECT sqlite_version()").Scan(&v.SQLiteVersion); err != nil {
		return v, err
	}
	var raw string
	if err := s.DB.QueryRowContext(ctx, `SELECT scope,source_hash,cleaning_version,manifest_json FROM datasets WHERE id=? AND status='published'`, dataset).Scan(&v.Scope, &v.SourceHash, &v.CleaningVersion, &raw); err != nil {
		return v, err
	}
	var manifest struct {
		Stats  Stats           `json:"stats"`
		Source json.RawMessage `json:"source"`
	}
	if err := json.Unmarshal([]byte(raw), &manifest); err != nil {
		return v, err
	}
	v.Source = manifest.Source
	query, err := s.DB.QueryContext(ctx, `SELECT s.sheet,count(*),sum(n.excluded=''),sum(n.excluded<>''),min(s.source_row),max(s.source_row) FROM normalized_rows n JOIN source_rows s ON s.id=n.source_id WHERE n.dataset_id=? GROUP BY s.sheet_index,s.sheet ORDER BY s.sheet_index`, dataset)
	if err != nil {
		return v, err
	}
	for query.Next() {
		var counts SheetCounts
		if err = query.Scan(&counts.Sheet, &counts.Raw, &counts.Accepted, &counts.Excluded, &counts.FirstRow, &counts.LastRow); err != nil {
			query.Close()
			return v, err
		}
		if counts.Raw != counts.Accepted+counts.Excluded {
			query.Close()
			return v, fmt.Errorf("sheet conservation failed")
		}
		v.Sheets = append(v.Sheets, counts)
		v.Counts.Raw += counts.Raw
		v.Counts.Accepted += counts.Accepted
		v.Counts.Excluded += counts.Excluded
	}
	err = query.Err()
	query.Close()
	if err != nil {
		return v, err
	}
	err = s.DB.QueryRowContext(ctx, `SELECT coalesce(sum(excluded='' AND kind='sale'),0),coalesce(sum(excluded='' AND kind='cancel'),0),coalesce(sum(upper(substr(invoice,1,1))='C'),0),coalesce(sum(excluded<>'' AND upper(substr(invoice,1,1))='C'),0) FROM normalized_rows WHERE dataset_id=?`, dataset).Scan(&v.Counts.AcceptedSales, &v.Counts.AcceptedCancels, &v.Counts.RawCancels, &v.Counts.ExcludedCancels)
	if err != nil {
		return v, err
	}
	if v.Counts != manifest.Stats || v.Counts.Raw != v.Counts.Accepted+v.Counts.Excluded || v.Counts.Accepted != v.Counts.AcceptedSales+v.Counts.AcceptedCancels || v.Counts.RawCancels != v.Counts.AcceptedCancels+v.Counts.ExcludedCancels {
		return v, fmt.Errorf("SQLite counts disagree with manifest or conservation: %+v", v.Counts)
	}
	query, err = s.DB.QueryContext(ctx, `SELECT excluded,count(*) FROM normalized_rows WHERE dataset_id=? AND excluded<>'' GROUP BY excluded ORDER BY excluded`, dataset)
	if err != nil {
		return v, err
	}
	for query.Next() {
		var reason string
		var count int
		if err = query.Scan(&reason, &count); err != nil {
			query.Close()
			return v, err
		}
		v.ExclusionReasons[reason] = count
	}
	err = query.Err()
	query.Close()
	if err != nil {
		return v, err
	}
	if rowID == "" && v.Counts.Excluded > 0 {
		if err = s.DB.QueryRowContext(ctx, `SELECT n.id FROM normalized_rows n JOIN source_rows s ON s.id=n.source_id WHERE n.dataset_id=? AND n.excluded<>'' ORDER BY s.sheet_index,s.source_row LIMIT 1`, dataset).Scan(&rowID); err != nil {
			return v, err
		}
	}
	if rowID != "" {
		r, e := s.source(ctx, dataset, rowID)
		if e != nil {
			return v, e
		}
		if r.Excluded == "" {
			return v, fmt.Errorf("requested source row is not excluded")
		}
		v.Example = &r
	}
	v.Conserved = true
	return v, nil
}
