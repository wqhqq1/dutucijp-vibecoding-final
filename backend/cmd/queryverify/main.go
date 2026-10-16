// queryverify measures production list queries on a new online backup of an
// official published all-scope run. Its identities are internal test actors;
// this is a store diagnostic, not HTTP authentication or an NFR-04 result.
package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	sqlite "modernc.org/sqlite"
	"returnsrecon/internal/store"
)

type measurement struct {
	Query        string  `json:"query"`
	Iteration    int     `json:"iteration"`
	Milliseconds float64 `json:"milliseconds"`
	Total        int     `json:"total"`
	Returned     int     `json:"returned"`
}

func digest(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err = io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	source := flag.String("source", "", "existing official verification database, opened read-only")
	destination := flag.String("db", "", "new isolated destination database")
	legacy := flag.Bool("legacy", false, "also measure original full-join transaction count and page for comparison")
	flag.Parse()
	if *source == "" || *destination == "" {
		return fmt.Errorf("explicit --source and new --db required")
	}
	day := time.Now().In(time.FixedZone("Asia/Shanghai", 8*3600)).Format("01-02")
	if day == "10-22" || day == "11-17" {
		return fmt.Errorf("reserved day: no verification work")
	}
	for _, suffix := range []string{"", "-wal", "-shm"} {
		if _, err := os.Stat(*destination + suffix); !os.IsNotExist(err) {
			return fmt.Errorf("destination or companion already exists")
		}
	}
	before, err := digest(*source)
	if err != nil {
		return err
	}
	ctx := context.Background()
	original, err := sql.Open("sqlite", filepath.ToSlash(*source)+"?mode=ro&_pragma=query_only(1)")
	if err != nil {
		return err
	}
	defer original.Close()
	var runID, datasetID string
	if err = original.QueryRowContext(ctx, `SELECT r.id,r.dataset_id FROM runs r JOIN datasets d ON d.id=r.dataset_id
 WHERE r.status='published' AND d.status='published' AND d.scope='all' AND d.cleaning_version='cr-1.0.1'
 AND r.rule_version='conservative-1.0.0' AND d.source_hash='bcbe73b35f5b7babf197fb0cb983a11f5d9ff929078d4aa53d171b1f2df2e980'
 ORDER BY r.id LIMIT 1`).Scan(&runID, &datasetID); err != nil {
		return err
	}
	if err = os.MkdirAll(filepath.Dir(*destination), 0700); err != nil {
		return err
	}
	conn, err := original.Conn(ctx)
	if err != nil {
		return err
	}
	err = conn.Raw(func(raw any) error {
		backuper, ok := raw.(interface {
			NewBackup(string) (*sqlite.Backup, error)
		})
		if !ok {
			return fmt.Errorf("SQLite online backup unavailable")
		}
		backup, e := backuper.NewBackup(filepath.ToSlash(*destination))
		if e != nil {
			return e
		}
		_, e = backup.Step(-1)
		finish := backup.Finish()
		if e != nil {
			return e
		}
		return finish
	})
	conn.Close()
	if err != nil {
		return err
	}
	started := time.Now()
	s, err := store.Open(*destination)
	if err != nil {
		return err
	}
	defer s.Close()
	if err = s.EnsureQueryIndexes(ctx); err != nil {
		return err
	}
	indexMillis := float64(time.Since(started).Microseconds()) / 1000
	actor := store.WorkflowActor{ID: "isolated-query-test-finance", Role: "finance"}
	filter := store.QueryFilter{RunID: runID, PageSize: 50}
	results := []measurement{}
	for iteration := 1; iteration <= 3; iteration++ {
		for _, kind := range []string{"transactions", "cases", "allocations", "warehouse-unassigned"} {
			started = time.Now()
			m := measurement{Query: kind, Iteration: iteration}
			switch kind {
			case "transactions":
				page, e := s.ListTransactions(ctx, actor, filter)
				err = e
				m.Total, m.Returned = page.Total, len(page.Items)
			case "cases":
				page, e := s.ListCases(ctx, actor, filter)
				err = e
				m.Total, m.Returned = page.Total, len(page.Items)
			case "allocations":
				page, e := s.ListAllocations(ctx, actor, filter)
				err = e
				m.Total, m.Returned = page.Total, len(page.Items)
			case "warehouse-unassigned":
				page, e := s.ListTransactions(ctx, store.WorkflowActor{ID: "isolated-query-unassigned", Role: "warehouse"}, filter)
				err = e
				m.Total, m.Returned = page.Total, len(page.Items)
			}
			if err != nil {
				return err
			}
			m.Milliseconds = float64(time.Since(started).Microseconds()) / 1000
			results = append(results, m)
		}
	}
	if *legacy {
		from := ` FROM runs r JOIN datasets d ON d.id=r.dataset_id JOIN normalized_rows n ON n.dataset_id=d.id JOIN source_rows sr ON sr.id=n.source_id LEFT JOIN decisions dc ON dc.run_id=r.id AND dc.cancel_id=n.id LEFT JOIN cases c ON c.run_id=r.id AND c.cancel_id=n.id WHERE r.id=? AND r.status='published' AND d.status='published'`
		started = time.Now()
		m := measurement{Query: "legacy-transactions-count-and-page", Iteration: 1}
		if err = s.DB.QueryRowContext(ctx, "SELECT count(*)"+from, runID).Scan(&m.Total); err != nil {
			return err
		}
		rows, e := s.DB.QueryContext(ctx, "SELECT n.id"+from+" ORDER BY sr.sheet_index,sr.source_row,n.id LIMIT 50", runID)
		if e != nil {
			return e
		}
		for rows.Next() {
			var id string
			if e = rows.Scan(&id); e != nil {
				rows.Close()
				return e
			}
			m.Returned++
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		m.Milliseconds = float64(time.Since(started).Microseconds()) / 1000
		results = append(results, m)
	}
	after, err := digest(*source)
	if err != nil {
		return err
	}
	if before != after {
		return fmt.Errorf("source database changed during verification")
	}
	return json.NewEncoder(os.Stdout).Encode(map[string]any{
		"actual_time":     time.Now().In(time.FixedZone("Asia/Shanghai", 8*3600)).Format(time.RFC3339),
		"source_database": *source, "isolated_database": *destination,
		"source_sha256_before": before, "source_sha256_after": after, "run_id": runID, "dataset_id": datasetID,
		"open_and_index_migration_milliseconds": indexMillis, "measurements": results,
		"boundary": "Store API diagnostic; first list call after startup, then repeated queries; OS file cache not purged; no concurrent job and no HTTP authentication. Legacy uses the same migrated backup and retrieves only IDs, so its cost is a conservative comparison. Not an NFR-04 acceptance measurement.",
	})
}
