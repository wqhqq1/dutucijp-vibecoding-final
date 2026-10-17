// transactionverify measures production transaction queries on a new backup of an
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
	"strings"
	"time"

	sqlite "modernc.org/sqlite"
	"returnsrecon/internal/store"
)

type measurement struct {
	Query        string            `json:"query"`
	Iteration    int               `json:"iteration"`
	Milliseconds float64           `json:"milliseconds"`
	Total        int               `json:"total"`
	Returned     int               `json:"returned"`
	IDsSHA256    string            `json:"ids_sha256"`
	Filter       store.QueryFilter `json:"filter"`
}

// An intentionally simple joined query is an independent correctness oracle.
// Its time is excluded from production query measurements.
func verifyPage(ctx context.Context, s *store.Store, f store.QueryFilter, page store.TransactionPage) error {
	where := " WHERE r.id=? AND r.status='published' AND d.status='published'"
	args := []any{f.RunID}
	for _, pair := range []struct{ column, value string }{{"n.customer", f.Customer}, {"n.invoice", f.Invoice}, {"n.stock", f.Stock}, {"n.country", f.Country}, {"n.kind", f.Kind}, {"n.source_time>=", f.DateFrom}, {"n.source_time<", f.DateTo}} {
		if pair.value == "" {
			continue
		}
		column, value := pair.column, pair.value
		if strings.HasPrefix(column, "n.source_time") {
			if len(value) == 10 {
				value += "T00:00:00.000000"
			}
			where += " AND " + column + "?"
		} else {
			where += " AND " + column + "=?"
		}
		args = append(args, value)
	}
	from := ` FROM runs r JOIN datasets d ON d.id=r.dataset_id JOIN normalized_rows n ON n.dataset_id=d.id JOIN source_rows sr ON sr.id=n.source_id`
	var total int
	if err := s.DB.QueryRowContext(ctx, "SELECT count(*)"+from+where, args...).Scan(&total); err != nil {
		return err
	}
	order := "sr.sheet_index,sr.source_row,n.id"
	if f.Sort != "" && f.Sort != "source" {
		order = map[string]string{"date": "n.source_time", "invoice": "n.invoice", "stock": "n.stock", "customer": "n.customer", "country": "n.country"}[f.Sort] + ",n.id"
	}
	if f.Direction == "desc" {
		order = strings.ReplaceAll(order, ",", " DESC,") + " DESC"
	}
	args = append(args, page.PageSize, (page.Page-1)*page.PageSize)
	rows, err := s.DB.QueryContext(ctx, "SELECT n.id"+from+where+" ORDER BY "+order+" LIMIT ? OFFSET ?", args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	i := 0
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			return err
		}
		if i >= len(page.Items) || page.Items[i].ID != id {
			return fmt.Errorf("reference page differs at %d", i)
		}
		i++
	}
	if err = rows.Err(); err != nil {
		return err
	}
	if total != page.Total || i != len(page.Items) {
		return fmt.Errorf("reference count differs: %d/%d rows=%d/%d", total, page.Total, i, len(page.Items))
	}
	return nil
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

// baselineTransactions retains the pre-specialist SQL shape for repeatable
// before/after measurements. It is diagnostic-only and never serves HTTP.
func baselineTransactions(ctx context.Context, s *store.Store, f store.QueryFilter) (store.TransactionPage, error) {
	p := store.TransactionPage{Items: []store.TransactionRecord{}, Page: f.Page, PageSize: f.PageSize}
	if p.Page == 0 {
		p.Page = 1
	}
	if p.PageSize == 0 {
		p.PageSize = 50
	}
	if p.PageSize > 100 {
		p.PageSize = 100
	}
	where := " WHERE r.id=? AND r.status='published' AND d.status='published'"
	args := []any{f.RunID}
	for _, pair := range []struct{ column, value string }{{"n.customer", f.Customer}, {"n.invoice", f.Invoice}, {"n.stock", f.Stock}, {"n.kind", f.Kind}, {"n.source_time>=", f.DateFrom}, {"n.source_time<", f.DateTo}} {
		if pair.value == "" {
			continue
		}
		value := pair.value
		if strings.HasPrefix(pair.column, "n.source_time") {
			if len(value) == 10 {
				value += "T00:00:00.000000"
			}
			where += " AND " + pair.column + "?"
		} else {
			where += " AND " + pair.column + "=?"
		}
		args = append(args, value)
	}
	const runFrom = ` FROM runs r JOIN datasets d ON d.id=r.dataset_id`
	from := runFrom + ` JOIN normalized_rows n ON n.dataset_id=d.id`
	unfiltered := f.Customer == "" && f.Stock == "" && f.Invoice == "" && f.Kind == "" && f.DateFrom == "" && f.DateTo == ""
	count := "SELECT count(*)" + from + where
	if unfiltered {
		count = `SELECT json_extract(d.manifest_json,'$.stats.raw')` + runFrom + where
	}
	if err := s.DB.QueryRowContext(ctx, count, args...).Scan(&p.Total); err != nil {
		return p, err
	}
	if unfiltered {
		from = runFrom + ` CROSS JOIN source_rows sr ON sr.source_hash=d.source_hash CROSS JOIN normalized_rows n ON n.dataset_id=d.id AND n.source_id=sr.id`
	} else {
		from += ` JOIN source_rows sr ON sr.id=n.source_id`
	}
	args = append(args, p.PageSize, (p.Page-1)*p.PageSize)
	const columns = `n.id,sr.sheet_index,sr.sheet,sr.source_row,n.invoice,n.stock,n.description,n.quantity,n.source_time,n.price,n.customer,n.country,n.kind,n.excluded,n.flags,n.duplicate_of,d.id,d.scope,d.source_hash,r.rule_version,d.cleaning_version`
	rows, err := s.DB.QueryContext(ctx, "SELECT "+columns+from+where+" ORDER BY sr.sheet_index,sr.source_row,n.id LIMIT ? OFFSET ?", args...)
	if err != nil {
		return p, err
	}
	defer rows.Close()
	for rows.Next() {
		var r store.TransactionRecord
		if err = rows.Scan(&r.ID, &r.SheetIndex, &r.Sheet, &r.SourceRow, &r.Invoice, &r.Stock, &r.Description, &r.Quantity, &r.Time, &r.Price, &r.Customer, &r.Country, &r.Kind, &r.Excluded, &r.Flags, &r.DuplicateOf, &r.DatasetID, &r.Scope, &r.SourceHash, &r.RuleVersion, &r.CleaningVersion); err != nil {
			return p, err
		}
		p.Items = append(p.Items, r)
	}
	return p, rows.Err()
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
	iterations := flag.Int("iterations", 3, "iterations of the transaction scenario matrix")
	verify := flag.Bool("verify", true, "compare every scenario's first result with an independent joined query outside the timer")
	only := flag.String("only", "", "optional comma-separated scenario names for a targeted rerun")
	baseline := flag.Bool("baseline", false, "retain pre-optimization SQL/index schema; source must not contain row_query_source/row_country/row_customer indexes")
	flag.Parse()
	if *source == "" || *destination == "" || *iterations < 1 || *iterations > 100 {
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
	var s *store.Store
	if *baseline {
		var db *sql.DB
		db, err = sql.Open("sqlite", filepath.ToSlash(*destination)+"?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)&_pragma=cache_size(-65536)&_pragma=mmap_size(536870912)&_pragma=temp_store(2)")
		if err == nil {
			s = &store.Store{DB: db}
			var newer int
			err = db.QueryRowContext(ctx, "SELECT count(*) FROM sqlite_master WHERE type='index' AND name IN ('row_query_source','row_country','row_customer')").Scan(&newer)
			if err == nil && newer != 0 {
				err = fmt.Errorf("baseline requires original index schema")
			}
		}
	} else {
		s, err = store.Open(*destination)
	}
	if err != nil {
		return err
	}
	defer s.Close()
	indexMillis := float64(time.Since(started).Microseconds()) / 1000
	actor := store.WorkflowActor{ID: "isolated-query-test-finance", Role: "finance"}
	filter := store.QueryFilter{RunID: runID, PageSize: 50}
	results := []measurement{}
	scenarios := []struct {
		name   string
		filter store.QueryFilter
	}{
		{"transactions", filter},
		{"customer", store.QueryFilter{RunID: runID, Customer: "17850"}},
		{"invoice", store.QueryFilter{RunID: runID, Invoice: "489434"}},
		{"stock", store.QueryFilter{RunID: runID, Stock: "85123A"}},
		{"date-day", store.QueryFilter{RunID: runID, DateFrom: "2010-12-01", DateTo: "2010-12-02"}},
		{"date-year", store.QueryFilter{RunID: runID, DateFrom: "2010-01-01", DateTo: "2011-01-01"}},
		{"kind-sale", store.QueryFilter{RunID: runID, Kind: "sale"}},
		{"kind-cancel", store.QueryFilter{RunID: runID, Kind: "cancel"}},
		{"combined", store.QueryFilter{RunID: runID, Customer: "17850", Stock: "85123A", DateFrom: "2009-12-01", DateTo: "2011-12-10"}},
		{"page-2", store.QueryFilter{RunID: runID, Page: 2}},
		{"page-10000", store.QueryFilter{RunID: runID, Page: 10000}},
		{"page-10000-size100", store.QueryFilter{RunID: runID, Page: 10000, PageSize: 100}},
		{"country-uk", store.QueryFilter{RunID: runID, Country: "United Kingdom"}},
		{"country-france", store.QueryFilter{RunID: runID, Country: "France"}},
		{"sort-source-desc", store.QueryFilter{RunID: runID, Sort: "source", Direction: "desc"}},
		{"sort-date", store.QueryFilter{RunID: runID, Sort: "date"}},
		{"sort-date-desc", store.QueryFilter{RunID: runID, Sort: "date", Direction: "desc"}},
		{"sort-customer", store.QueryFilter{RunID: runID, Sort: "customer"}},
		{"sort-invoice", store.QueryFilter{RunID: runID, Sort: "invoice"}},
		{"sort-stock", store.QueryFilter{RunID: runID, Sort: "stock"}},
		{"sort-country", store.QueryFilter{RunID: runID, Sort: "country"}},
		{"sort-date-deep", store.QueryFilter{RunID: runID, Sort: "date", Page: 10000, PageSize: 100}},
		{"date-year-deep", store.QueryFilter{RunID: runID, DateFrom: "2010-01-01", DateTo: "2011-01-01", Page: 1000, PageSize: 100}},
	}
	for iteration := 1; iteration <= *iterations; iteration++ {
		for _, scenario := range scenarios {
			if *baseline && (scenario.filter.Sort != "" || scenario.filter.Country != "") {
				continue
			}
			if *only != "" && !strings.Contains(","+*only+",", ","+scenario.name+",") {
				continue
			}
			kind := scenario.name
			started = time.Now()
			m := measurement{Query: kind, Iteration: iteration, Filter: scenario.filter}
			var measuredPage store.TransactionPage
			var page store.TransactionPage
			var e error
			if *baseline {
				page, e = baselineTransactions(ctx, s, scenario.filter)
			} else {
				page, e = s.ListTransactions(ctx, actor, scenario.filter)
			}
			err = e
			m.Total, m.Returned = page.Total, len(page.Items)
			measuredPage = page
			if err != nil {
				return err
			}
			m.Milliseconds = float64(time.Since(started).Microseconds()) / 1000
			ids := []string{}
			for _, row := range measuredPage.Items {
				ids = append(ids, row.ID)
			}
			sum := sha256.Sum256([]byte(strings.Join(ids, "\n")))
			m.IDsSHA256 = hex.EncodeToString(sum[:])
			results = append(results, m)
			fmt.Fprintf(os.Stderr, "%s iteration=%d %.3fms total=%d returned=%d\n", kind, iteration, m.Milliseconds, m.Total, m.Returned)
			if *verify && iteration == 1 {
				if err = verifyPage(ctx, s, scenario.filter, measuredPage); err != nil {
					return fmt.Errorf("%s: %w", kind, err)
				}
			}
		}
	}
	if len(results) == 0 {
		return fmt.Errorf("no matching scenarios")
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
		"independent_reference_verified": *verify,
		"baseline_sql_and_indexes":       *baseline,
		"boundary":                       "Production Store API diagnostic; first list call after startup, then repeated matrix; OS file cache not purged. First-iteration reference count and exact ordered IDs checked outside measurement; reference warms caches before subsequent queries. No concurrent job or HTTP authentication. Not page-visible timing or an NFR-04 result; three samples do not establish stable P95.",
	})
}
