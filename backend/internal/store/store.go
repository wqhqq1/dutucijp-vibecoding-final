package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	_ "embed"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/gofrs/flock"
	_ "modernc.org/sqlite"
	"returnsrecon/internal/model"
	"returnsrecon/internal/reconcile"
)

//go:embed schema.sql
var schema string

const batchRows = 4096
const balanceBatchRows = 16384

type Store struct {
	DB                *sql.DB
	path              string
	transactionCounts transactionCountCache
}
type Stats struct {
	Raw             int `json:"raw"`
	Accepted        int `json:"accepted"`
	Excluded        int `json:"excluded"`
	AcceptedSales   int `json:"accepted_sales"`
	AcceptedCancels int `json:"accepted_cancels"`
	RawCancels      int `json:"raw_cancels"`
	ExcludedCancels int `json:"excluded_cancels"`
}
type Manifest struct {
	DatasetID       string `json:"dataset_id"`
	Scope           string `json:"scope"`
	CleaningVersion string `json:"cleaning_version"`
	SourceHash      string `json:"source_hash"`
	RecordsHash     string `json:"records_sha256"`
	Status          string `json:"status"`
	Stats           Stats  `json:"stats"`
}
type Summary struct {
	RunID       string  `json:"run_id"`
	DatasetID   string  `json:"dataset_id"`
	RuleVersion string  `json:"rule_version"`
	Status      string  `json:"status"`
	Reused      bool    `json:"reused"`
	Stats       Stats   `json:"input"`
	Matched     int     `json:"matched"`
	Suspected   int     `json:"suspected"`
	NoOriginal  int     `json:"no_original"`
	Cases       int     `json:"cases"`
	Workers     int     `json:"workers"`
	Seconds     float64 `json:"reconcile_and_persist_seconds"`
	Coverage    string  `json:"coverage"`
	Caveat      string  `json:"caveat"`
}

func ID(text string) string { sum := sha256.Sum256([]byte(text)); return hex.EncodeToString(sum[:]) }
func hashFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	_, err = io.Copy(h, f)
	return hex.EncodeToString(h.Sum(nil)), err
}

func Open(path string) (*Store, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	if err = os.MkdirAll(filepath.Dir(absolute), 0700); err != nil {
		return nil, err
	}
	// A bounded 64 MiB page cache and at most 2 GiB read-only file mapping per
	// connection avoid repeated Windows reads for million-row inputs. SQLite may
	// cap the mapping below the request. WAL/FULL durability and the four-connection
	// pool remain unchanged; mapped address space is not reserved resident memory.
	db, err := sql.Open("sqlite", filepath.ToSlash(absolute)+"?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)&_pragma=cache_size(-65536)&_pragma=mmap_size(2147483648)&_pragma=temp_store(2)")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(4)
	db.SetMaxIdleConns(4)
	if _, err = db.Exec(schema); err != nil {
		db.Close()
		return nil, err
	}
	s := &Store{DB: db, path: absolute}
	if err = s.migrateWorkflow(context.Background()); err != nil {
		db.Close()
		return nil, err
	}
	if err = s.migrateRemediation(context.Background()); err != nil {
		db.Close()
		return nil, err
	}
	if err = s.EnsureQueryIndexes(context.Background()); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}
func (s *Store) Close() error { return s.DB.Close() }

func (s *Store) heavyLock(ctx context.Context) (func(), error) {
	lock := flock.New(s.path + ".heavy.lock")
	ok, err := lock.TryLockContext(ctx, 25*time.Millisecond)
	if err != nil || !ok {
		lock.Close()
		if err == nil {
			err = fmt.Errorf("heavy task lock unavailable")
		}
		return nil, err
	}
	return func() { lock.Unlock(); lock.Close() }, nil
}

func ReadManifest(dir string) (Manifest, []byte, error) {
	var m Manifest
	raw, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err != nil {
		return m, nil, err
	}
	if err = json.Unmarshal(raw, &m); err != nil {
		return m, nil, err
	}
	if m.Status != "prepared" || m.DatasetID != ID(m.SourceHash+"|"+m.Scope+"|"+m.CleaningVersion) || len(m.SourceHash) != 64 || m.CleaningVersion == "" {
		return m, nil, fmt.Errorf("invalid prepared manifest identity")
	}
	if m.Stats.Raw != m.Stats.Accepted+m.Stats.Excluded || m.Stats.RawCancels != m.Stats.AcceptedCancels+m.Stats.ExcludedCancels {
		return m, nil, fmt.Errorf("manifest conservation failed")
	}
	hash, err := hashFile(filepath.Join(dir, "records.csv"))
	if err != nil {
		return m, nil, err
	}
	if hash != m.RecordsHash {
		return m, nil, fmt.Errorf("prepared records SHA256 mismatch")
	}
	return m, raw, nil
}

func (s *Store) Import(ctx context.Context, dir string) (m Manifest, err error) {
	unlock, err := s.heavyLock(ctx)
	if err != nil {
		return m, err
	}
	defer unlock()
	phaseStarted := time.Now()
	m, raw, err := ReadManifest(dir)
	observePhase(ctx, "import.manifest_before", phaseStarted)
	if err != nil {
		return m, err
	}
	var status string
	err = s.DB.QueryRowContext(ctx, "SELECT status FROM datasets WHERE id=?", m.DatasetID).Scan(&status)
	if err == nil && status == "published" {
		return m, nil
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return m, err
	}
	_, err = s.DB.ExecContext(ctx, `INSERT INTO datasets VALUES(?,?,?,?,?,?, 'staging') ON CONFLICT(id) DO UPDATE SET status='staging'`, m.DatasetID, m.Scope, m.SourceHash, m.CleaningVersion, m.RecordsHash, string(raw))
	if err != nil {
		return m, err
	}
	defer func() {
		if err != nil {
			s.DB.Exec("UPDATE datasets SET status='failed' WHERE id=? AND status='staging'", m.DatasetID)
		}
	}()
	if _, err = s.DB.ExecContext(ctx, "DELETE FROM normalized_rows WHERE dataset_id=?", m.DatasetID); err != nil {
		return m, err
	}
	f, err := os.Open(filepath.Join(dir, "records.csv"))
	if err != nil {
		return m, err
	}
	defer f.Close()
	reader := csv.NewReader(f)
	header, err := reader.Read()
	if err != nil {
		return m, err
	}
	expected := "id,sheet_index,sheet,source_row,raw_json,invoice,stock,description,quantity,time,price,customer,country,kind,excluded,flags,duplicate_of"
	if strings.Join(header, ",") != expected {
		return m, fmt.Errorf("unsupported prepared CSV columns")
	}
	phaseStarted = time.Now()
	batch := make([][]string, 0, batchRows)
	for {
		record, e := reader.Read()
		if e == io.EOF {
			break
		}
		if e != nil {
			return m, e
		}
		batch = append(batch, record)
		if len(batch) == batchRows {
			if err = s.importBatch(ctx, m, batch); err != nil {
				return m, err
			}
			batch = batch[:0]
		}
	}
	if err = s.importBatch(ctx, m, batch); err != nil {
		return m, err
	}
	observePhase(ctx, "import.rows", phaseStarted)
	phaseStarted = time.Now()
	var got Stats
	err = s.DB.QueryRowContext(ctx, `SELECT count(*),coalesce(sum(excluded=''),0),coalesce(sum(excluded<>''),0),coalesce(sum(excluded='' AND kind='sale'),0),coalesce(sum(excluded='' AND kind='cancel'),0) FROM normalized_rows WHERE dataset_id=?`, m.DatasetID).Scan(&got.Raw, &got.Accepted, &got.Excluded, &got.AcceptedSales, &got.AcceptedCancels)
	if err != nil {
		return m, err
	}
	if got.Raw != m.Stats.Raw || got.Accepted != m.Stats.Accepted || got.Excluded != m.Stats.Excluded || got.AcceptedSales != m.Stats.AcceptedSales || got.AcceptedCancels != m.Stats.AcceptedCancels {
		return m, fmt.Errorf("persisted dataset conservation failed: %+v", got)
	}
	observePhase(ctx, "import.conservation", phaseStarted)
	phaseStarted = time.Now()
	// Verify bytes again after streaming, so a changed intermediate is never published.
	if _, _, err = ReadManifest(dir); err != nil {
		return m, err
	}
	observePhase(ctx, "import.manifest_after", phaseStarted)
	phaseStarted = time.Now()
	// Empty databases build secondary indexes once, after their input batches.
	// Existing published data keeps its indexes throughout subsequent imports.
	if err = s.ensureQueryIndexes(ctx, true); err != nil {
		return m, err
	}
	observePhase(ctx, "import.indexes", phaseStarted)
	_, err = s.DB.ExecContext(ctx, "UPDATE datasets SET status='published' WHERE id=? AND status='staging'", m.DatasetID)
	return m, err
}

func (s *Store) importBatch(ctx context.Context, m Manifest, batch [][]string) error {
	if len(batch) == 0 {
		return nil
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// Multi-row statements amortize the driver/SQLite VM boundary. Each has
	// at most 64*15=960 parameters, below even SQLite's legacy 999 limit.
	// All chunks of the bounded batch still share one transaction.
	const statementRows = 64
	sourceStatements := make(map[int]*sql.Stmt)
	normalizedStatements := make(map[int]*sql.Stmt)
	for offset := 0; offset < len(batch); offset += statementRows {
		chunk := batch[offset:min(offset+statementRows, len(batch))]
		sourceValues := make([]any, 0, len(chunk)*6)
		normalizedValues := make([]any, 0, len(chunk)*15)
		for _, v := range chunk {
			si, e := strconv.Atoi(v[1])
			if e != nil {
				return e
			}
			rn, e := strconv.Atoi(v[3])
			if e != nil {
				return e
			}
			q := int64(0)
			if v[8] != "" {
				q, e = strconv.ParseInt(v[8], 10, 64)
				if e != nil {
					return e
				}
			}
			if v[0] != fmt.Sprintf("%d:%d", si, rn) || rn < 2 || si < 0 || si > 1 || !json.Valid([]byte(v[4])) {
				return fmt.Errorf("invalid source location or raw fields")
			}
			sourceID := m.SourceHash + ":" + v[0]
			sourceValues = append(sourceValues, sourceID, m.SourceHash, v[2], si, rn, v[4])
			normalizedValues = append(normalizedValues, m.DatasetID, v[0], sourceID, v[5], v[6], v[7], q, v[9], v[10], v[11], v[12], v[13], v[14], v[15], v[16])
		}
		if sourceStatements[len(chunk)] == nil {
			sourceSQL := "INSERT OR IGNORE INTO source_rows VALUES" + strings.TrimSuffix(strings.Repeat("(?,?,?,?,?,?),", len(chunk)), ",")
			sourceStatements[len(chunk)], err = tx.PrepareContext(ctx, sourceSQL)
			if err != nil {
				return err
			}
			defer sourceStatements[len(chunk)].Close()
			normalizedSQL := "INSERT INTO normalized_rows(dataset_id,id,source_id,invoice,stock,description,quantity,source_time,price,customer,country,kind,excluded,flags,duplicate_of) VALUES" + strings.TrimSuffix(strings.Repeat("(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?),", len(chunk)), ",")
			normalizedStatements[len(chunk)], err = tx.PrepareContext(ctx, normalizedSQL)
			if err != nil {
				return err
			}
			defer normalizedStatements[len(chunk)].Close()
		}
		if _, err = sourceStatements[len(chunk)].ExecContext(ctx, sourceValues...); err != nil {
			return err
		}
		if _, err = normalizedStatements[len(chunk)].ExecContext(ctx, normalizedValues...); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) LoadRows(ctx context.Context, dataset string) ([]model.Row, error) {
	return s.loadRows(ctx, dataset, 0)
}

func (s *Store) loadRows(ctx context.Context, dataset string, capacity int) ([]model.Row, error) {
	query, err := s.DB.QueryContext(ctx, `SELECT n.id,s.sheet_index,s.sheet,s.source_row,n.invoice,n.stock,n.quantity,n.source_time,n.price,n.customer,n.country,n.kind,n.flags FROM normalized_rows n JOIN source_rows s ON s.id=n.source_id JOIN datasets d ON d.id=n.dataset_id WHERE n.dataset_id=? AND n.excluded='' AND d.status='published'`, dataset)
	if err != nil {
		return nil, err
	}
	defer query.Close()
	rows := make([]model.Row, 0, capacity)
	for query.Next() {
		var r model.Row
		if err = query.Scan(&r.ID, &r.SheetIndex, &r.Sheet, &r.SourceRow, &r.Invoice, &r.Stock, &r.Quantity, &r.Time, &r.Price, &r.Customer, &r.Country, &r.Kind, &r.Flags); err != nil {
			return nil, err
		}
		rows = append(rows, r)
	}
	return rows, query.Err()
}

// Input publication verifies canonical sheet_index:source_row IDs. Computation
// only needs that ordering and the exact matching fields; provenance is still
// read from source_rows by public row/evidence APIs, never reconstructed there.
func (s *Store) loadComputeRows(ctx context.Context, dataset string, capacity int) ([]model.Row, error) {
	query, err := s.DB.QueryContext(ctx, `SELECT n.id,n.stock,n.quantity,n.source_time,n.price,n.customer,n.country,n.kind,n.flags FROM normalized_rows n WHERE n.dataset_id=(SELECT id FROM datasets WHERE id=? AND status='published') AND n.excluded=''`, dataset)
	if err != nil {
		return nil, err
	}
	defer query.Close()
	rows := make([]model.Row, 0, capacity)
	for query.Next() {
		// Scan directly into the final preallocated row. A temporary Row whose
		// field addresses escape through Scan would allocate and copy per input.
		rows = append(rows, model.Row{})
		r := &rows[len(rows)-1]
		if err = query.Scan(&r.ID, &r.Stock, &r.Quantity, &r.Time, &r.Price, &r.Customer, &r.Country, &r.Kind, &r.Flags); err != nil {
			return nil, err
		}
		if len(r.ID) < 3 || r.ID[0] < '0' || r.ID[0] > '1' || r.ID[1] != ':' || r.ID[2] < '1' || r.ID[2] > '9' {
			return nil, fmt.Errorf("invalid persisted source ID")
		}
		r.SheetIndex = int(r.ID[0] - '0')
		r.SourceRow, err = strconv.Atoi(r.ID[2:])
		if err != nil || r.SourceRow < 2 {
			return nil, fmt.Errorf("invalid persisted source order")
		}
	}
	return rows, query.Err()
}

func (s *Store) Run(ctx context.Context, dataset string, workers int) (summary Summary, err error) {
	unlock, err := s.heavyLock(ctx)
	if err != nil {
		return summary, err
	}
	defer unlock()
	runID := ID(dataset + "|" + model.RuleVersion)
	var state, summaryJSON string
	err = s.DB.QueryRowContext(ctx, "SELECT status,coalesce(summary_json,'') FROM runs WHERE id=?", runID).Scan(&state, &summaryJSON)
	if err == nil && state == "published" {
		err = json.Unmarshal([]byte(summaryJSON), &summary)
		summary.Reused = true
		return summary, err
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return summary, err
	}
	var manifestJSON string
	if err = s.DB.QueryRowContext(ctx, "SELECT manifest_json FROM datasets WHERE id=? AND status='published'", dataset).Scan(&manifestJSON); err != nil {
		return summary, fmt.Errorf("dataset is not published: %w", err)
	}
	var m Manifest
	if err = json.Unmarshal([]byte(manifestJSON), &m); err != nil {
		return summary, err
	}
	// Only unpublished staging/failed runs can be replaced. Published human history is untouched.
	if _, err = s.DB.ExecContext(ctx, "DELETE FROM runs WHERE id=? AND status<>'published'", runID); err != nil {
		return summary, err
	}
	if _, err = s.DB.ExecContext(ctx, "INSERT INTO runs(id,dataset_id,rule_version,status,started_at) VALUES(?,?,?,'staging',?)", runID, dataset, model.RuleVersion, time.Now().Format(time.RFC3339Nano)); err != nil {
		return summary, err
	}
	defer func() {
		if err != nil {
			s.DB.Exec("UPDATE runs SET status='failed',error=? WHERE id=? AND status='staging'", err.Error(), runID)
		}
	}()
	started := time.Now()
	phaseStarted := time.Now()
	if err = s.ensureBalanceStorage(ctx); err != nil {
		return summary, err
	}
	observePhase(ctx, "run.balance_layout", phaseStarted)
	phaseStarted = time.Now()
	rows, err := s.loadComputeRows(ctx, dataset, m.Stats.Accepted)
	observePhase(ctx, "run.load", phaseStarted)
	if err != nil {
		return summary, err
	}
	if len(rows) != m.Stats.Accepted {
		return summary, fmt.Errorf("published input row count differs from its manifest")
	}
	phaseStarted = time.Now()
	result, err := reconcile.Compute(ctx, rows, workers)
	observePhase(ctx, "run.compute", phaseStarted)
	if err != nil {
		return summary, err
	}
	if len(result.Decisions) != m.Stats.AcceptedCancels || len(result.Balances) != m.Stats.AcceptedSales {
		return summary, fmt.Errorf("result counts differ from published dataset manifest")
	}
	phaseStarted = time.Now()
	err = s.persist(ctx, runID, result)
	observePhase(ctx, "run.persist", phaseStarted)
	if err != nil {
		return summary, err
	}
	summary = Summary{RunID: runID, DatasetID: dataset, RuleVersion: model.RuleVersion, Status: "published", Stats: m.Stats, Matched: result.Matched, Suspected: result.Suspected, NoOriginal: result.NoOriginal, Cases: result.Suspected + result.NoOriginal, Workers: result.Workers, Coverage: "not applicable", Caveat: "规则推断；无真实原订单关联号。无原单仅指当前范围及规则下无合格候选。"}
	if m.Stats.AcceptedCancels > 0 {
		summary.Coverage = fmt.Sprintf("%d/%d (%.6f%%)", result.Matched, m.Stats.AcceptedCancels, float64(result.Matched)*100/float64(m.Stats.AcceptedCancels))
	}
	summary.Seconds = time.Since(started).Seconds()
	encoded, _ := json.Marshal(summary)
	_, err = s.DB.ExecContext(ctx, "UPDATE runs SET status='published',completed_at=?,summary_json=? WHERE id=? AND status='staging'", time.Now().Format(time.RFC3339Nano), string(encoded), runID)
	return summary, err
}

// Batches are bounded. Publication follows SQL conservation checks.
func (s *Store) batch(ctx context.Context, statement string, values [][]any) error {
	valueStart := strings.LastIndex(statement, "VALUES")
	if valueStart < 0 {
		return fmt.Errorf("batch insert must contain VALUES")
	}
	prefix, rowSQL := statement[:valueStart+6], statement[valueStart+6:]
	for len(values) > 0 {
		n := min(batchRows, len(values))
		tx, err := s.DB.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		statements := make(map[int]*sql.Stmt)
		for offset := 0; offset < n; offset += 64 {
			chunk := values[offset:min(offset+64, n)]
			stmt := statements[len(chunk)]
			if stmt == nil {
				stmt, err = tx.PrepareContext(ctx, prefix+strings.TrimSuffix(strings.Repeat(rowSQL+",", len(chunk)), ","))
				if err != nil {
					break
				}
				statements[len(chunk)] = stmt
			}
			args := make([]any, 0, len(chunk)*len(chunk[0]))
			for _, v := range chunk {
				args = append(args, v...)
			}
			if _, err = stmt.ExecContext(ctx, args...); err != nil {
				break
			}
		}
		for _, stmt := range statements {
			stmt.Close()
		}
		if err != nil {
			tx.Rollback()
			return err
		}
		if err = tx.Commit(); err != nil {
			return err
		}
		values = values[n:]
	}
	return nil
}

// Each batch walks a disjoint primary-key range, never a growing OFFSET. SQL
// copies the immutable originals once; only actually allocated sales need Go
// parameters. Both initial rows and guarded adjustments share the bounded FULL
// transaction, and allocations are inserted only after all balances exist.
func (s *Store) persistBalances(ctx context.Context, runID string, balances []model.Balance) error {
	var dataset string
	if err := s.DB.QueryRowContext(ctx, "SELECT dataset_id FROM runs WHERE id=? AND status='staging'", runID).Scan(&dataset); err != nil {
		return err
	}
	lower := ""
	for offset := 0; offset < len(balances); offset += balanceBatchRows {
		chunk := balances[offset:min(offset+balanceBatchRows, len(balances))]
		upper := chunk[len(chunk)-1].SaleID
		if upper <= lower {
			return fmt.Errorf("computed balances are not ordered")
		}
		err := func() error {
			tx, err := s.DB.BeginTx(ctx, nil)
			if err != nil {
				return err
			}
			defer tx.Rollback()
			inserted, err := tx.ExecContext(ctx, `INSERT INTO sale_balances(run_id,sale_id,original,allocated,remaining)
 SELECT ?,n.id,n.quantity,0,n.quantity FROM normalized_rows n INDEXED BY sqlite_autoindex_normalized_rows_1
 JOIN datasets d ON d.id=n.dataset_id AND d.status='published'
 WHERE n.dataset_id=? AND n.id>? AND n.id<=? AND n.excluded='' AND n.kind='sale' AND n.quantity>0 ORDER BY n.id`, runID, dataset, lower, upper)
			if err != nil {
				return err
			}
			count, err := inserted.RowsAffected()
			if err != nil {
				return err
			}
			if count != int64(len(chunk)) {
				return fmt.Errorf("persisted balance batch count differs")
			}
			var update *sql.Stmt
			defer func() {
				if update != nil {
					update.Close()
				}
			}()
			for _, b := range chunk {
				if b.Allocated == 0 {
					continue
				}
				if update == nil {
					update, err = tx.PrepareContext(ctx, `UPDATE sale_balances SET allocated=?,remaining=? WHERE run_id=? AND sale_id=? AND original=? AND allocated=0 AND remaining=original`)
					if err != nil {
						return err
					}
				}
				changed, err := update.ExecContext(ctx, b.Allocated, b.Remaining, runID, b.SaleID, b.Original)
				if err != nil {
					return err
				}
				count, err = changed.RowsAffected()
				if err != nil {
					return err
				}
				if count != 1 {
					return fmt.Errorf("persisted balance initial state differs")
				}
			}
			return tx.Commit()
		}()
		if err != nil {
			return err
		}
		lower = upper
	}
	return nil
}

func (s *Store) persist(ctx context.Context, runID string, r model.Result) error {
	phaseStarted := time.Now()
	if err := s.persistBalances(ctx, runID, r.Balances); err != nil {
		return err
	}
	observePhase(ctx, "persist.balances", phaseStarted)
	phaseStarted = time.Now()
	decisions := [][]any{}
	allocations := [][]any{}
	cases := [][]any{}
	evidence := [][]any{}
	for _, d := range r.Decisions {
		decisions = append(decisions, []any{runID, d.CancelID, d.Quantity, d.Classification, d.Reason, d.CandidateLowerBound})
		if d.SaleID != "" {
			allocations = append(allocations, []any{runID, d.CancelID, d.SaleID, d.Quantity, d.Amount, "automatic", nil})
		} else {
			cases = append(cases, []any{ID(runID + "|" + d.CancelID), runID, d.CancelID, d.Reason})
		}
		for i, e := range d.Evidence {
			evidence = append(evidence, []any{runID, d.CancelID, i, e.SaleID, e.Remaining, e.Eligible, e.Reason, r.RuleVersion})
		}
	}
	for _, b := range []struct {
		sql    string
		values [][]any
	}{{"INSERT INTO decisions VALUES(?,?,?,?,?,?)", decisions}, {"INSERT INTO allocations VALUES(?,?,?,?,?,?,?)", allocations}, {"INSERT INTO cases(id,run_id,cancel_id,reason) VALUES(?,?,?,?)", cases}, {"INSERT INTO candidate_evidence VALUES(?,?,?,?,?,?,?,?)", evidence}} {
		if err := s.batch(ctx, b.sql, b.values); err != nil {
			return err
		}
	}
	observePhase(ctx, "persist.decisions_and_evidence", phaseStarted)
	phaseStarted = time.Now()
	var total, matched, casesN, balancesN, nonzeroBalances, bad int
	if err := s.DB.QueryRowContext(ctx, "SELECT count(*),coalesce(sum(classification='matched'),0) FROM decisions WHERE run_id=?", runID).Scan(&total, &matched); err != nil {
		return err
	}
	if err := s.DB.QueryRowContext(ctx, "SELECT count(*) FROM cases WHERE run_id=?", runID).Scan(&casesN); err != nil {
		return err
	}
	// Every persisted balance is still checked. Only nonzero balances can have
	// allocation groups: checking every group against its balance and comparing
	// the two set sizes proves both directions without joining a million zeros.
	if err := s.DB.QueryRowContext(ctx, `SELECT count(*),coalesce(sum(allocated<>0),0),
 coalesce(sum(original<=0 OR allocated<0 OR remaining<0 OR original-allocated<>remaining),0)
 FROM sale_balances WHERE run_id=?`, runID).Scan(&balancesN, &nonzeroBalances, &bad); err != nil {
		return err
	}
	var allocatedSales, badAllocations int
	if err := s.DB.QueryRowContext(ctx, `SELECT count(*),coalesce(sum(b.sale_id IS NULL OR b.allocated<>a.quantity),0)
 FROM (SELECT sale_id,sum(quantity) AS quantity FROM allocations WHERE run_id=? GROUP BY sale_id) a
 LEFT JOIN sale_balances b ON b.run_id=? AND b.sale_id=a.sale_id`, runID, runID).Scan(&allocatedSales, &badAllocations); err != nil {
		return err
	}
	var allocationN int
	if err := s.DB.QueryRowContext(ctx, "SELECT count(*) FROM allocations WHERE run_id=?", runID).Scan(&allocationN); err != nil {
		return err
	}
	if total != len(r.Decisions) || matched != r.Matched || allocationN != matched || casesN != total-matched || balancesN != len(r.Balances) || bad != 0 || badAllocations != 0 || allocatedSales != nonzeroBalances {
		return fmt.Errorf("persisted run conservation failed")
	}
	observePhase(ctx, "persist.conservation", phaseStarted)
	return nil
}
