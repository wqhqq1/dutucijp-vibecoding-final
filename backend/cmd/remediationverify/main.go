// remediationverify runs a fixed same-value correction and subsequent rechecks,
// on a new online backup of the official two-year dataset. It never opens the
// source for writes. HTTP authentication is completed in a new synthetic DB;
// only its new simulated identity records are appended to the isolated copy for
// transaction-bound POST checks. This is not formal NFR or human acceptance.
package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/pquerna/otp/totp"
	sqlite "modernc.org/sqlite"
	"returnsrecon/internal/auth"
	"returnsrecon/internal/store"
	"returnsrecon/internal/web"
)

const officialSource = "bcbe73b35f5b7babf197fb0cb983a11f5d9ff929078d4aa53d171b1f2df2e980"

type report struct {
	ActualTime      string             `json:"actual_time"`
	Boundary        string             `json:"boundary"`
	Source          string             `json:"source_database"`
	Database        string             `json:"isolated_database"`
	SourceBefore    string             `json:"source_sha256_before"`
	SourceAfter     string             `json:"source_sha256_after"`
	SourceUnchanged bool               `json:"source_unchanged"`
	WorkbookSHA     string             `json:"official_workbook_sha256"`
	Run             string             `json:"run_id"`
	Dataset         string             `json:"dataset_id"`
	Durations       map[string]float64 `json:"phase_seconds"`
	Before          map[string]int64   `json:"before_counts"`
	After           map[string]int64   `json:"after_counts"`
	Checks          map[string]bool    `json:"checks"`
	Recheck         map[string]any     `json:"isolated_recheck"`
	Probes          map[string]any     `json:"store_query_probes"`
	HTTP            queryReport        `json:"concurrent_http_diagnostic"`
	Passed          bool               `json:"passed"`
	Failure         string             `json:"failure,omitempty"`
	Resumed         bool               `json:"resumed_existing_isolated_projection"`
}
type latency struct {
	Attempted int            `json:"attempted"`
	Succeeded int            `json:"succeeded"`
	Failed    int            `json:"failed"`
	P95       float64        `json:"p95_milliseconds"`
	Maximum   float64        `json:"maximum_milliseconds"`
	Errors    map[string]int `json:"errors,omitempty"`
}
type queryReport struct {
	Boundary string `json:"boundary"`
	Workers  int    `json:"workers"`
	latency
	Classes map[string]latency `json:"classes"`
}

func digest(path string) (string, error) {
	f, e := os.Open(path)
	if e != nil {
		return "", e
	}
	defer f.Close()
	h := sha256.New()
	if _, e = io.Copy(h, f); e != nil {
		return "", e
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
func phase(r *report, name string) {
	fmt.Fprintln(os.Stderr, "phase="+name)
	if r.Database != "" {
		b, _ := json.Marshal(map[string]any{"phase": name, "actual_time": time.Now().Format(time.RFC3339), "phase_seconds": r.Durations, "source_sha256_before": r.SourceBefore})
		_ = os.WriteFile(filepath.Join(filepath.Dir(r.Database), "progress.json"), b, 0600)
	}
}
func validateDestination(source, destination string, resume bool) (string, string, error) {
	src, e := filepath.Abs(source)
	if e != nil {
		return "", "", e
	}
	src, e = filepath.EvalSymlinks(src)
	if e != nil {
		return "", "", e
	}
	dst, e := filepath.Abs(destination)
	if e != nil {
		return "", "", e
	}
	parent, e := filepath.EvalSymlinks(filepath.Dir(dst))
	if e != nil {
		return "", "", fmt.Errorf("destination parent must already exist")
	}
	dst = filepath.Join(parent, filepath.Base(dst))
	root, e := filepath.EvalSymlinks(filepath.Join(os.Getenv("LOCALAPPDATA"), "dutucijp-vibecoding-final", "verification"))
	if e != nil {
		return "", "", e
	}
	rel, e := filepath.Rel(root, parent)
	if e != nil {
		return "", "", e
	}
	parts := strings.Split(rel, string(filepath.Separator))
	if len(parts) != 1 || !strings.HasPrefix(strings.ToLower(parts[0]), "fr20-") || len(parts[0]) <= len("fr20-") {
		return "", "", fmt.Errorf("destination must be directly inside verification/fr20-* directory")
	}
	if strings.EqualFold(src, dst) {
		return "", "", fmt.Errorf("source and destination must differ")
	}
	if resume {
		resolved, e := filepath.EvalSymlinks(dst)
		if e != nil {
			return "", "", e
		}
		if !strings.EqualFold(resolved, dst) {
			return "", "", fmt.Errorf("resumed database must not be a symlink")
		}
		info, e := os.Stat(dst)
		if e != nil || !info.Mode().IsRegular() {
			return "", "", fmt.Errorf("resumed isolated database must be an existing regular file")
		}
		return src, dst, nil
	}
	for _, suffix := range []string{"", "-wal", "-shm"} {
		if _, e := os.Stat(dst + suffix); !os.IsNotExist(e) {
			return "", "", fmt.Errorf("destination or SQLite companion already exists")
		}
	}
	return src, dst, nil
}
func backup(ctx context.Context, db *sql.DB, destination string) error {
	c, e := db.Conn(ctx)
	if e != nil {
		return e
	}
	defer c.Close()
	return c.Raw(func(raw any) error {
		checker, ok := raw.(interface{ IsReadOnly(string) (bool, error) })
		if !ok {
			return fmt.Errorf("SQLite file read-only verification unavailable")
		}
		ro, e := checker.IsReadOnly("main")
		if e != nil {
			return e
		}
		if !ro {
			return fmt.Errorf("refusing writable source")
		}
		b, ok := raw.(interface {
			NewBackup(string) (*sqlite.Backup, error)
		})
		if !ok {
			return fmt.Errorf("SQLite online backup unavailable")
		}
		copy, e := b.NewBackup(filepath.ToSlash(destination))
		if e != nil {
			return e
		}
		_, e = copy.Step(-1)
		finish := copy.Finish()
		if e != nil {
			return e
		}
		return finish
	})
}
func scalar(ctx context.Context, db *sql.DB, q string, args ...any) (int64, error) {
	var n int64
	e := db.QueryRowContext(ctx, q, args...).Scan(&n)
	return n, e
}
func counts(ctx context.Context, s *store.Store, run, dataset string) (map[string]int64, error) {
	out := map[string]int64{}
	for k, q := range map[string]string{
		"normalized_rows": "SELECT count(*) FROM normalized_rows WHERE dataset_id=?", "source_sheets": "SELECT count(DISTINCT sr.sheet_index) FROM normalized_rows n JOIN source_rows sr ON sr.id=n.source_id WHERE n.dataset_id=?",
		"accepted_sales": "SELECT count(*) FROM normalized_rows WHERE dataset_id=? AND kind='sale' AND excluded=''", "accepted_cancels": "SELECT count(*) FROM normalized_rows WHERE dataset_id=? AND kind='cancel' AND excluded=''",
		"balances": "SELECT count(*) FROM sale_balances WHERE run_id=?", "allocations": "SELECT count(*) FROM allocations WHERE run_id=?", "allocated_quantity": "SELECT coalesce(sum(quantity),0) FROM allocations WHERE run_id=?", "remaining_quantity": "SELECT coalesce(sum(remaining),0) FROM sale_balances WHERE run_id=?",
		"cases": "SELECT count(*) FROM cases WHERE run_id=?", "closed_cases": "SELECT count(*) FROM cases WHERE run_id=? AND status='closed'", "business_rows": "SELECT count(*) FROM business_rows WHERE run_id=?", "remediation_events": "SELECT count(*) FROM remediation_events WHERE run_id=?",
	} {
		arg := run
		if k == "normalized_rows" || k == "source_sheets" || k == "accepted_sales" || k == "accepted_cancels" {
			arg = dataset
		}
		n, e := scalar(ctx, s.DB, q, arg)
		if e != nil {
			return out, e
		}
		out[k] = n
	}
	n, e := scalar(ctx, s.DB, s.BusinessSQL(ctx, run, "SELECT count(*) FROM normalized_rows WHERE dataset_id=?"), dataset)
	if e != nil {
		return out, e
	}
	out["effective_rows"] = n
	n, e = scalar(ctx, s.DB, "SELECT coalesce((SELECT sparse FROM remediation_runs WHERE run_id=?),0)", run)
	if e != nil {
		return out, e
	}
	out["sparse_mode"] = n
	return out, nil
}
func queryDigest(ctx context.Context, db *sql.DB, query string, run string) (string, error) {
	rows, e := db.QueryContext(ctx, query, run)
	if e != nil {
		return "", e
	}
	defer rows.Close()
	cols, e := rows.Columns()
	if e != nil {
		return "", e
	}
	h := sha256.New()
	enc := json.NewEncoder(h)
	for rows.Next() {
		values := make([]any, len(cols))
		ptr := make([]any, len(cols))
		for i := range values {
			ptr[i] = &values[i]
		}
		if e = rows.Scan(ptr...); e != nil {
			return "", e
		}
		if e = enc.Encode(values); e != nil {
			return "", e
		}
	}
	if e = rows.Err(); e != nil {
		return "", e
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
func invariants(ctx context.Context, s *store.Store, run string) error {
	for name, q := range map[string]string{
		"balance_equation":        "SELECT count(*) FROM sale_balances WHERE run_id=? AND (original-allocated<>remaining OR allocated<0 OR remaining<0)",
		"allocation_balance_sums": "SELECT count(*) FROM sale_balances b LEFT JOIN (SELECT sale_id,sum(quantity) used FROM allocations WHERE run_id=? GROUP BY sale_id) a ON a.sale_id=b.sale_id WHERE b.run_id=? AND b.allocated<>coalesce(a.used,0)",
		"duplicate_capacity":      "SELECT count(*) FROM duplicate_sale_groups g WHERE g.run_id=? AND (SELECT coalesce(sum(b.allocated),0) FROM duplicate_sale_members m JOIN sale_balances b ON b.run_id=m.run_id AND b.sale_id=m.sale_id WHERE m.run_id=g.run_id AND m.group_id=g.id)>g.capacity",
	} {
		args := []any{run}
		if name == "allocation_balance_sums" {
			args = append(args, run)
		}
		n, e := scalar(ctx, s.DB, q, args...)
		if e != nil {
			return e
		}
		if n != 0 {
			return fmt.Errorf("%s violations=%d", name, n)
		}
	}
	return nil
}

// Only records created by the complete synthetic authentication flow are copied.
// Existing copied accounts, sessions and credentials are never overwritten.
func mirrorSyntheticAccount(ctx context.Context, target, source *sql.DB, id string) error {
	tx, e := target.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	for _, table := range []string{"auth_users", "auth_account_profiles", "auth_user_countries", "auth_credentials", "auth_recovery", "auth_sessions"} {
		var ddl string
		if e = source.QueryRowContext(ctx, "SELECT sql FROM sqlite_master WHERE type='table' AND name=?", table).Scan(&ddl); e != nil {
			return e
		}
		ddl = strings.Replace(ddl, "CREATE TABLE ", "CREATE TABLE IF NOT EXISTS ", 1)
		ddl = strings.ReplaceAll(ddl, "IF NOT EXISTS IF NOT EXISTS", "IF NOT EXISTS")
		if _, e = tx.ExecContext(ctx, ddl); e != nil {
			return e
		}
		key := "user_id"
		if table == "auth_users" {
			key = "id"
		}
		rows, e := source.QueryContext(ctx, "SELECT * FROM "+table+" WHERE "+key+"=?", id)
		if e != nil {
			return e
		}
		columns, e := rows.Columns()
		if e != nil {
			rows.Close()
			return e
		}
		slots := strings.TrimSuffix(strings.Repeat("?,", len(columns)), ",")
		insert := "INSERT INTO " + table + "(" + strings.Join(columns, ",") + ") VALUES(" + slots + ")"
		for rows.Next() {
			values := make([]any, len(columns))
			pointers := make([]any, len(columns))
			for i := range values {
				pointers[i] = &values[i]
			}
			if e = rows.Scan(pointers...); e != nil {
				rows.Close()
				return e
			}
			if _, e = tx.ExecContext(ctx, insert, values...); e != nil {
				rows.Close()
				return e
			}
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return e
		}
	}
	return tx.Commit()
}

func syntheticHTTP(ctx context.Context, s *store.Store, root string, mirror bool, id string) (*httptest.Server, string, func(), error) {
	db, e := sql.Open("sqlite", filepath.ToSlash(filepath.Join(root, "auth-only.sqlite"))+"?_pragma=foreign_keys(1)&_pragma=busy_timeout(10000)")
	if e != nil {
		return nil, "", func() {}, e
	}
	cleanup := func() { db.Close() }
	svc, e := auth.New(db, auth.Config{SecretDir: filepath.Join(root, "synthetic-secrets"), Origin: "http://localhost:8080"})
	if e != nil {
		cleanup()
		return nil, "", func() {}, e
	}
	if _, e = svc.Initialize(ctx, []auth.Identity{{ID: id, Role: "finance"}}); e != nil {
		cleanup()
		return nil, "", func() {}, e
	}
	g, e := svc.StartSetup(ctx, id)
	if e != nil {
		cleanup()
		return nil, "", func() {}, e
	}
	g, e = svc.SetPassword(ctx, g.Token, "Isolated FR20 winter forest amber 83!")
	if e != nil {
		cleanup()
		return nil, "", func() {}, e
	}
	enroll, e := svc.BeginTOTP(ctx, g.Token)
	if e != nil {
		cleanup()
		return nil, "", func() {}, e
	}
	code, e := totp.GenerateCode(enroll.Secret, time.Now())
	if e != nil {
		cleanup()
		return nil, "", func() {}, e
	}
	g, e = svc.ConfirmTOTP(ctx, g.Token, code, "isolated-fr20-local")
	if e != nil {
		cleanup()
		return nil, "", func() {}, e
	}
	g, e = svc.Complete(ctx, g.Token, true)
	if e != nil {
		cleanup()
		return nil, "", func() {}, e
	}
	if g.Session.Scope != "business" {
		cleanup()
		return nil, "", func() {}, fmt.Errorf("synthetic authentication did not obtain business scope")
	}
	if mirror {
		if e = mirrorSyntheticAccount(ctx, s.DB, db, id); e != nil {
			cleanup()
			return nil, "", func() {}, e
		}
	}
	server := httptest.NewServer((&web.Server{Store: s, Auth: svc, Origin: "http://localhost:8080"}).Handler())
	return server, g.Token, func() { server.Close(); cleanup() }, nil
}

func apiRemediate(ctx context.Context, server *httptest.Server, token, id string, req store.RemediationRequest) (store.RemediationResult, float64, error) {
	var out store.RemediationResult
	get, e := http.NewRequestWithContext(ctx, "GET", server.URL+"/api/auth/me", nil)
	if e != nil {
		return out, 0, e
	}
	get.Host = "localhost:8080"
	get.AddCookie(&http.Cookie{Name: "recon_session", Value: token})
	response, e := server.Client().Do(get)
	if e != nil {
		return out, 0, e
	}
	var session auth.Session
	e = json.NewDecoder(response.Body).Decode(&session)
	response.Body.Close()
	if e != nil || response.StatusCode != 200 {
		return out, 0, fmt.Errorf("synthetic auth session lookup failed")
	}
	body, e := json.Marshal(req)
	if e != nil {
		return out, 0, e
	}
	post, e := http.NewRequestWithContext(ctx, "POST", server.URL+"/api/cases/"+id+"/remediations", bytes.NewReader(body))
	if e != nil {
		return out, 0, e
	}
	post.Host = "localhost:8080"
	post.Header.Set("Origin", "http://localhost:8080")
	post.Header.Set("Content-Type", "application/json")
	post.Header.Set("X-CSRF-Token", session.CSRF)
	post.AddCookie(&http.Cookie{Name: "recon_session", Value: token})
	start := time.Now()
	response, e = server.Client().Do(post)
	if e != nil {
		return out, time.Since(start).Seconds(), e
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		var failure struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		}
		_ = json.NewDecoder(io.LimitReader(response.Body, 65536)).Decode(&failure)
		return out, time.Since(start).Seconds(), fmt.Errorf("production remediation HTTP status %d: %s (%s)", response.StatusCode, failure.Code, failure.Message)
	}
	e = json.NewDecoder(io.LimitReader(response.Body, 4*1024*1024)).Decode(&out)
	elapsed := time.Since(start).Seconds()
	if e != nil || response.StatusCode != 200 {
		return out, elapsed, fmt.Errorf("production remediation HTTP status %d", response.StatusCode)
	}
	return out, elapsed, nil
}

func additionalAPIScenarios(ctx context.Context, s *store.Store, server *httptest.Server, token string, actor store.WorkflowActor, r *report) error {
	before, e := counts(ctx, s, r.Run, r.Dataset)
	if e != nil {
		return e
	}
	scenarios := []map[string]any{}
	type openProblem struct{ id, stock, reason, status, assignee, key string }
	problems := []openProblem{}
	groups := map[string]int{}
	read, e := s.DB.QueryContext(ctx, s.BusinessSQL(ctx, r.Run, `SELECT c.id,n.stock,c.reason,c.status,coalesce(c.assignee,''),n.customer,n.price,n.country FROM cases c JOIN runs r ON r.id=c.run_id JOIN normalized_rows n ON n.dataset_id=r.dataset_id AND n.id=c.cancel_id WHERE c.run_id=? AND c.is_current=1 AND c.status<>'closed' AND NOT EXISTS(SELECT 1 FROM allocations a WHERE a.run_id=c.run_id AND a.cancel_id=c.cancel_id) ORDER BY c.id`), r.Run)
	if e != nil {
		return e
	}
	for read.Next() {
		var p openProblem
		var customer, price, country string
		if e = read.Scan(&p.id, &p.stock, &p.reason, &p.status, &p.assignee, &customer, &price, &country); e != nil {
			read.Close()
			return e
		}
		key, _ := json.Marshal([]string{customer, p.stock, price, country})
		p.key = string(key)
		groups[p.key]++
		problems = append(problems, p)
	}
	e = read.Err()
	read.Close()
	if e != nil {
		return e
	}
	maximum := 0
	for _, size := range groups {
		if size > maximum {
			maximum = size
		}
	}
	r.Probes["maximum_related_open_case_count"] = maximum
	for _, reason := range []string{"ambiguous_candidates", "insufficient_remaining_quantity", "no_identity_match"} {
		phase(r, "additional_api_"+reason)
		opCtx, stop := context.WithTimeout(ctx, 60*time.Second)
		var selected openProblem
		for _, p := range problems {
			if p.reason == reason && p.status == "pending" && p.assignee == "" && (selected.id == "" || groups[p.key] > groups[selected.key]) {
				selected = p
			}
		}
		id, stock := selected.id, selected.stock
		if id == "" {
			stop()
			return fmt.Errorf("existing API scenario %s unavailable", reason)
		}
		c, e := s.AssignCase(opCtx, actor, actor, id)
		if e == nil {
			c, e = s.TransitionCase(opCtx, actor, id, "in_progress", c.Version, "")
		}
		if e != nil {
			stop()
			return e
		}
		req := store.RemediationRequest{Version: c.Version, RequestID: "isolated-fr20-reason-api-" + reason, Action: "correct_return", Changes: map[string]string{"stock": stock}, Evidence: "隔离原值核查：stock赋回完全相同的原值；仅验证此原有差异原因的完整规则/余量路径，不代表新增官方资料、本人核实或真实原单关系。"}
		out, elapsed, err := apiRemediate(opCtx, server, token, id, req)
		stop()
		r.Durations["additional_api_"+reason+"_including_commit"] = elapsed
		if err != nil {
			return fmt.Errorf("additional API %s: %w", reason, err)
		}
		scenarios = append(scenarios, map[string]any{"original_reason": reason, "related_open_cases": groups[selected.key], "action": "correct_return_equal_original_stock", "after_status": out.Case.Status, "after_reason": out.Case.Reason, "matched": out.Matched, "new_problems": out.Reclassified, "excluded": out.Excluded, "seconds_including_commit": elapsed, "boundary": "Existing official unresolved row from the largest identity group for this reason; no invented fields or supporting documents. Any new match is only isolated rule inference after the main unchanged-digest check."})
	}
	if e = invariants(ctx, s, r.Run); e != nil {
		return e
	}
	after, e := counts(ctx, s, r.Run, r.Dataset)
	if e != nil {
		return e
	}
	if after["effective_rows"] != before["effective_rows"] || after["normalized_rows"] != before["normalized_rows"] || after["balances"] != before["balances"] {
		return fmt.Errorf("additional API scenarios lost effective source rows or balances")
	}
	r.Probes["additional_existing_problem_api_scenarios"] = scenarios
	r.Probes["additional_scenarios_separate_aggregates"] = map[string]any{"before": before, "after": after, "boundary": "These follow the primary unchanged allocation/balance digest validation and are not mixed into that claim."}
	r.Checks["additional_api_balance_and_capacity_invariants"] = true
	return nil
}

type workload struct {
	name   string
	filter store.QueryFilter
}

func workloads(run, stock, country string) []workload {
	return []workload{
		{"source_first", store.QueryFilter{RunID: run, Page: 1, PageSize: 50}},
		{"source_deep_page", store.QueryFilter{RunID: run, Page: 10000, PageSize: 50}},
		{"source_desc", store.QueryFilter{RunID: run, Sort: "source", Direction: "desc", PageSize: 50}},
		{"date_filter_sort", store.QueryFilter{RunID: run, DateFrom: "2010-12-01", DateTo: "2010-12-02", Sort: "date", PageSize: 50}},
		{"stock_filter_date_sort", store.QueryFilter{RunID: run, Stock: stock, Sort: "date", Direction: "desc", PageSize: 50}},
		{"country_filter_country_sort", store.QueryFilter{RunID: run, Country: country, Sort: "country", PageSize: 50}},
	}
}
func queryPath(f store.QueryFilter) string {
	v := make(map[string]string)
	v["run_id"] = f.RunID
	v["page_size"] = "50"
	if f.Page > 0 {
		v["page"] = fmt.Sprint(f.Page)
	}
	v["sort"] = f.Sort
	v["direction"] = f.Direction
	v["stock"] = f.Stock
	v["country"] = f.Country
	v["date_from"] = f.DateFrom
	v["date_to"] = f.DateTo
	parts := []string{}
	for k, x := range v {
		if x != "" {
			parts = append(parts, k+"="+url.QueryEscape(x))
		}
	}
	return "/api/transactions?" + strings.Join(parts, "&")
}
func summarize(times []float64, succeeded int, errors map[string]int) latency {
	out := latency{Attempted: len(times), Succeeded: succeeded, Failed: len(times) - succeeded, Errors: errors}
	sort.Float64s(times)
	if len(times) > 0 {
		out.P95 = times[(len(times)*95+99)/100-1]
		out.Maximum = times[len(times)-1]
	}
	return out
}
func concurrentHTTP(ctx context.Context, server *httptest.Server, token string, work []workload) queryReport {
	out := queryReport{Boundary: "10 workers / 300 actual authenticated production-handler HTTP GETs after initial FR20 projection. Fresh synthetic auth DB and TOTP; copied business authentication untouched. Query diagnostic only, not formal NFR04, browser-visible timing, human review, or real-order accuracy.", Workers: 10, Classes: map[string]latency{}}
	times := []float64{}
	classTimes := map[string][]float64{}
	success := map[string]int{}
	errs := map[string]int{}
	classErrs := map[string]map[string]int{}
	var mu sync.Mutex
	var wg sync.WaitGroup
	for worker := 0; worker < 10; worker++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			for i := 0; i < 30; i++ {
				w := work[(worker+i)%len(work)]
				started := time.Now()
				reqCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
				req, e := http.NewRequestWithContext(reqCtx, "GET", server.URL+queryPath(w.filter), nil)
				status := 0
				var page store.TransactionPage
				if e == nil {
					req.Host = "localhost:8080"
					req.AddCookie(&http.Cookie{Name: "recon_session", Value: token})
					resp, sendErr := server.Client().Do(req)
					e = sendErr
					if e == nil {
						status = resp.StatusCode
						e = json.NewDecoder(io.LimitReader(resp.Body, 4*1024*1024)).Decode(&page)
						resp.Body.Close()
					}
				}
				cancel()
				ms := float64(time.Since(started).Microseconds()) / 1000
				mu.Lock()
				times = append(times, ms)
				classTimes[w.name] = append(classTimes[w.name], ms)
				if e == nil && status == 200 && page.Total >= len(page.Items) && len(page.Items) <= 50 {
					success[w.name]++
				} else {
					key := fmt.Sprintf("http_status_%d", status)
					if e != nil {
						key = "transport_or_decode_failure"
					}
					errs[key]++
					if classErrs[w.name] == nil {
						classErrs[w.name] = map[string]int{}
					}
					classErrs[w.name][key]++
				}
				mu.Unlock()
			}
		}(worker)
	}
	wg.Wait()
	totalSuccess := 0
	for _, w := range work {
		totalSuccess += success[w.name]
		out.Classes[w.name] = summarize(classTimes[w.name], success[w.name], classErrs[w.name])
	}
	out.latency = summarize(times, totalSuccess, errs)
	return out
}

func execute(r *report, source, destination string, resume, skipLoad bool) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	var e error
	r.Source, r.Database, e = validateDestination(source, destination, resume)
	if e != nil {
		return e
	}
	phase(r, "source_hash_and_read_only_validation")
	r.SourceBefore, e = digest(r.Source)
	if e != nil {
		return e
	}
	defer func() { r.SourceAfter, _ = digest(r.Source); r.SourceUnchanged = r.SourceBefore == r.SourceAfter }()
	original, e := store.OpenReadOnlySource(r.Source)
	if e != nil {
		return e
	}
	defer original.Close()
	var originalSummary string
	e = original.QueryRowContext(ctx, `SELECT r.id,r.dataset_id,r.summary_json FROM runs r JOIN datasets d ON d.id=r.dataset_id WHERE r.status='published' AND d.status='published' AND d.scope='all' AND d.cleaning_version='cr-1.0.1' AND r.rule_version='conservative-1.0.0' AND d.source_hash=? ORDER BY r.id LIMIT 1`, officialSource).Scan(&r.Run, &r.Dataset, &originalSummary)
	if e != nil {
		return fmt.Errorf("official two-year all source validation: %w", e)
	}
	start := time.Now()
	if !resume {
		phase(r, "online_backup")
		if e = backup(ctx, original, r.Database); e != nil {
			return e
		}
		r.Durations["online_backup"] = time.Since(start).Seconds()
	}
	r.Checks["source_driver_file_read_only"] = true
	start = time.Now()
	phase(r, "open_and_migrate")
	s, e := store.Open(r.Database)
	if e != nil {
		return e
	}
	defer s.Close()
	r.Durations["open_and_migrate"] = time.Since(start).Seconds()
	phase(r, "initial_aggregates_and_digests")
	r.Before, e = counts(ctx, s, r.Run, r.Dataset)
	if e != nil {
		return e
	}
	if r.Before["normalized_rows"] != 1067371 || r.Before["source_sheets"] != 2 || r.Before["effective_rows"] != 1067371 || (!resume && r.Before["business_rows"] != 0) {
		return fmt.Errorf("requires full official dataset and expected isolated projection state: %+v", r.Before)
	}
	digestQueries := map[string]string{"initial_decisions": "SELECT cancel_id,quantity,classification,reason,candidate_lower_bound FROM decisions WHERE run_id=? ORDER BY cancel_id", "allocations": "SELECT cancel_id,sale_id,quantity,amount,method,actor FROM allocations WHERE run_id=? ORDER BY cancel_id", "balances": "SELECT sale_id,original,allocated,remaining FROM sale_balances WHERE run_id=? ORDER BY sale_id"}
	beforeDigests := map[string]string{}
	for k, q := range digestQueries {
		beforeDigests[k], e = queryDigest(ctx, original, q, r.Run)
		if e != nil {
			return e
		}
	}
	var id, stock, country string
	a := store.WorkflowActor{ID: "isolated-fr20-http-simulated-finance", Role: "finance"}
	var c store.WorkflowCase
	var req store.RemediationRequest
	if resume {
		var raw string
		e = s.DB.QueryRowContext(ctx, `SELECT e.case_id,e.request_json,n.stock,n.country,e.actor FROM remediation_events e JOIN cases c ON c.id=e.case_id JOIN normalized_rows n ON n.dataset_id=? AND n.id=c.cancel_id WHERE e.run_id=? AND e.actor IN ('isolated-fr20-http-simulated-finance','isolated-fr20-simulated-finance') AND e.request_id IN ('isolated-fr20-original-recheck-v1','isolated-fr20-equal-stock-first-v1') AND c.assignee=e.actor ORDER BY e.occurred_at LIMIT 1`, r.Dataset, r.Run).Scan(&id, &raw, &stock, &country, &a.ID)
		if e != nil {
			return fmt.Errorf("resume requires prior fixed isolated recheck event: %w", e)
		}
		if e = json.Unmarshal([]byte(raw), &req); e != nil {
			return e
		}
		c, e = s.WorkflowCase(ctx, a, id)
		if e != nil {
			return e
		}
	} else {
		e = s.DB.QueryRowContext(ctx, `SELECT c.id,n.stock,n.country FROM cases c JOIN normalized_rows n ON n.dataset_id=? AND n.id=c.cancel_id WHERE c.run_id=? AND c.status='pending' AND c.is_current=1 AND coalesce(c.assignee,'')='' AND c.reason='missing_identity' AND n.customer='' AND n.stock<>'' AND n.country<>'' AND NOT EXISTS(SELECT 1 FROM allocations a WHERE a.run_id=c.run_id AND a.cancel_id=c.cancel_id) ORDER BY c.id LIMIT 1`, r.Dataset, r.Run).Scan(&id, &stock, &country)
		if e != nil {
			return fmt.Errorf("select existing unresolved original problem: %w", e)
		}
		c, e = s.AssignCase(ctx, a, a, id)
		if e != nil {
			return e
		}
		c, e = s.TransitionCase(ctx, a, id, "in_progress", c.Version, "")
		if e != nil {
			return e
		}
		req = store.RemediationRequest{Version: c.Version, RequestID: "isolated-fr20-equal-stock-first-v1", Action: "correct_return", Changes: map[string]string{"stock": stock}, Evidence: "FR20隔离等值核查：仅将原始stock赋回完全同值，验证真实首次修正及事务提交路径；未补造业务字段或官方凭证，缺客户问题继续保留，不代表本人核实或真实原单关系。"}
	}
	start = time.Now()
	phaseName := "first_equal_value_correction_with_initialization_and_commit"
	if resume {
		phaseName = "resume_existing_isolated_recheck"
	}
	phase(r, phaseName)
	opCtx, stopOp := context.WithTimeout(ctx, 60*time.Second)
	result, e := s.RemediateCase(opCtx, a, id, req)
	stopOp()
	if resume {
		r.Durations["resume_idempotent_recheck"] = time.Since(start).Seconds()
	} else {
		r.Durations["first_store_equal_correction_with_initialization_and_commit"] = time.Since(start).Seconds()
	}
	if e != nil {
		return e
	}
	r.Recheck = map[string]any{"action": req.Action, "simulated_actor": a.ID, "before_reason": c.Reason, "after_reason": result.Case.Reason, "after_status": result.Case.Status, "matched": result.Matched, "new_problems": result.Reclassified, "excluded": result.Excluded, "revision": result.Revision, "evidence_boundary": "Only the original stock value was assigned to itself. No business fields or supporting documents were invented; original missing identity remains unresolved."}
	if result.Matched != 0 || result.Excluded != 0 || result.Case.Status != "in_progress" || result.Case.Reason != "missing_identity" {
		return fmt.Errorf("original missing identity unexpectedly resolved")
	}
	start = time.Now()
	phase(r, "fresh_synthetic_http_authentication")
	authRoot, e := os.MkdirTemp(filepath.Dir(r.Database), "http-auth-")
	if e != nil {
		return e
	}
	httpIdentity := a.ID
	if resume {
		httpIdentity = "isolated-fr20-http-resume-" + store.ID(authRoot)[:12]
	}
	server, token, cleanup, e := syntheticHTTP(ctx, s, authRoot, true, httpIdentity)
	if e != nil {
		return e
	}
	defer cleanup()
	r.Durations["synthetic_http_auth_setup"] = time.Since(start).Seconds()
	var expectedRevision int
	if e = s.DB.QueryRowContext(ctx, "SELECT revision FROM remediation_runs WHERE run_id=?", r.Run).Scan(&expectedRevision); e != nil {
		return e
	}
	if !resume {
		current := result.Case
		for _, action := range []string{"correct_return", "recheck"} {
			apiRequest := store.RemediationRequest{Version: current.Version, RequestID: "isolated-fr20-subsequent-api-" + action, Action: action, Evidence: "隔离等值核查的后续API请求：无新增官方业务证据，仅验证原值修正或按原字段再次重核。"}
			if action == "correct_return" {
				apiRequest.Changes = map[string]string{"stock": stock}
			}
			phase(r, "subsequent_authenticated_api_"+action)
			apiCtx, stop := context.WithTimeout(ctx, 60*time.Second)
			response, elapsed, err := apiRemediate(apiCtx, server, token, id, apiRequest)
			stop()
			r.Durations["subsequent_api_"+action+"_including_commit"] = elapsed
			if err != nil {
				return err
			}
			if response.Revision <= expectedRevision {
				return fmt.Errorf("subsequent API was not a new non-idempotent operation")
			}
			current = response.Case
			expectedRevision = response.Revision
		}
		r.Probes["authenticated_api_boundary"] = "Both POSTs use the production handler, CSRF, normal strong-password/TOTP-created synthetic identity, and transaction-bound authentication recheck. These are subsequent operations after first Store initialization. Only this synthetic identity's records were appended to the isolated copy; original authentication rows were not overwritten."
	}
	work := workloads(r.Run, stock, country)
	phase(r, "post_projection_store_probes")
	for _, w := range work {
		phase(r, "store_probe_"+w.name)
		start = time.Now()
		probeCtx, stop := context.WithTimeout(ctx, 60*time.Second)
		page, err := s.ListTransactions(probeCtx, a, w.filter)
		stop()
		r.Durations["query_"+w.name] = time.Since(start).Seconds()
		r.Probes[w.name] = map[string]any{"total": page.Total, "items": len(page.Items), "success": err == nil}
		if err != nil {
			return fmt.Errorf("query probe %s: %w", w.name, err)
		}
	}
	start = time.Now()
	phase(r, "store_case_list")
	caseCtx, stopCase := context.WithTimeout(ctx, 60*time.Second)
	problems, e := s.ListCases(caseCtx, a, store.QueryFilter{RunID: r.Run, Status: "in_progress", PageSize: 50})
	stopCase()
	r.Durations["case_list"] = time.Since(start).Seconds()
	if e != nil {
		return e
	}
	r.Probes["case_list"] = map[string]int{"total": problems.Total, "items": len(problems.Items)}
	start = time.Now()
	phase(r, "store_overview")
	overviewCtx, stopOverview := context.WithTimeout(ctx, 60*time.Second)
	overview, err := s.BusinessOverview(overviewCtx, a, r.Run)
	stopOverview()
	r.Durations["overview"] = time.Since(start).Seconds()
	if err != nil {
		return err
	}
	r.Probes["overview"] = map[string]any{"success": true, "keys": len(overview)}
	start = time.Now()
	phase(r, "store_case_evidence")
	evidenceCtx, stopEvidence := context.WithTimeout(ctx, 60*time.Second)
	evidence, e := s.CaseEvidence(evidenceCtx, a, id, store.PageRequest{})
	stopEvidence()
	r.Durations["case_evidence"] = time.Since(start).Seconds()
	if e != nil {
		return e
	}
	r.Probes["case_evidence"] = map[string]any{"original_preserved": evidence.OriginalCancel != nil, "effective_present": evidence.EffectiveCancel != nil, "events": len(evidence.Remediations), "related_cases": len(evidence.RelatedCases), "candidate_count": evidence.Candidates.Total}
	start = time.Now()
	phase(r, "store_candidates")
	candidateCtx, stopCandidates := context.WithTimeout(ctx, 60*time.Second)
	candidates, e := s.ListCandidates(candidateCtx, a, id, store.PageRequest{})
	stopCandidates()
	r.Durations["candidates"] = time.Since(start).Seconds()
	if e != nil {
		return e
	}
	if candidates.Total != 0 {
		return fmt.Errorf("missing customer unexpectedly has eligible candidates")
	}
	start = time.Now()
	phase(r, "store_csv")
	csvCtx, stopCSV := context.WithTimeout(ctx, 60*time.Second)
	e = s.ExportCSV(csvCtx, a, store.QueryFilter{RunID: r.Run, Stock: stock}, io.Discard)
	stopCSV()
	r.Durations["csv_filtered_stream"] = time.Since(start).Seconds()
	if e != nil {
		return e
	}
	start = time.Now()
	phase(r, "generate_review_from_full_projection")
	reviewCtx, stopReview := context.WithTimeout(ctx, 60*time.Second)
	set, e := s.GenerateReviewSet(reviewCtx, a, r.Run)
	stopReview()
	r.Durations["generate_review_after_projection"] = time.Since(start).Seconds()
	if e != nil {
		return e
	}
	r.Probes["review"] = map[string]any{"items": len(set.Items), "revision": set.RemediationRevision, "policy_version": set.PolicyVersion}
	if set.RemediationRevision != expectedRevision {
		return fmt.Errorf("review did not retain remediation revision")
	}
	if !skipLoad {
		start = time.Now()
		phase(r, "ten_workers_three_hundred_http_queries")
		httpCtx, stopHTTP := context.WithTimeout(ctx, 60*time.Second)
		r.HTTP = concurrentHTTP(httpCtx, server, token, work)
		stopHTTP()
		r.Durations["concurrent_http_queries"] = time.Since(start).Seconds()
	} else {
		r.HTTP.Boundary = "Explicit --skip-http-load: 300-request load was deferred; no HTTP load pass or latency is claimed."
	}
	start = time.Now()
	phase(r, "final_digests_and_invariants")
	r.After, e = counts(ctx, s, r.Run, r.Dataset)
	if e != nil {
		return e
	}
	for k, q := range digestQueries {
		after, e := queryDigest(ctx, s.DB, q, r.Run)
		if e != nil {
			return e
		}
		r.Checks[k+"_unchanged"] = after == beforeDigests[k]
		if !r.Checks[k+"_unchanged"] {
			return fmt.Errorf("%s changed during fixed recheck", k)
		}
	}
	var finalSummary string
	if e = s.DB.QueryRowContext(ctx, "SELECT summary_json FROM runs WHERE id=?", r.Run).Scan(&finalSummary); e != nil {
		return e
	}
	r.Checks["initial_summary_unchanged"] = originalSummary == finalSummary
	r.Checks["full_effective_row_count"] = r.After["effective_rows"] == r.Before["normalized_rows"]
	if e = invariants(ctx, s, r.Run); e != nil {
		return e
	}
	r.Checks["balance_and_capacity_invariants"] = true
	for _, k := range []string{"normalized_rows", "source_sheets", "accepted_sales", "accepted_cancels", "balances", "allocations", "allocated_quantity", "remaining_quantity", "cases", "closed_cases"} {
		r.Checks[k+"_unchanged"] = r.After[k] == r.Before[k]
		if !r.Checks[k+"_unchanged"] {
			return fmt.Errorf("aggregate %s changed", k)
		}
	}
	retry, e := s.RemediateCase(ctx, a, id, req)
	if e != nil {
		return e
	}
	r.Checks["retry_same_revision"] = retry.Revision == result.Revision
	r.Durations["final_validation"] = time.Since(start).Seconds()
	if skipLoad {
		r.Checks["http_load_explicitly_deferred"] = true
	} else {
		r.Checks["concurrent_http_zero_failures"] = r.HTTP.Failed == 0 && r.HTTP.Attempted == 300
	}
	for k, v := range r.Checks {
		if !v {
			return fmt.Errorf("check failed: %s", k)
		}
	}
	phase(r, "additional_existing_problem_api_diagnostics_after_main_conservation_checks")
	if e = additionalAPIScenarios(ctx, s, server, token, store.WorkflowActor{ID: httpIdentity, Role: "finance"}, r); e != nil {
		return e
	}
	return nil
}

func main() {
	source := flag.String("source", "", "official all database, opened physically read-only")
	destination := flag.String("db", "", "new database in existing verification/fr20-* directory")
	resume := flag.Bool("resume-isolated", false, "resume only an existing verification/fr20-* projection with the prior fixed simulated recheck")
	skipLoad := flag.Bool("skip-http-load", false, "defer 300-request load while checking projected query plans; no load pass is claimed")
	flag.Parse()
	r := report{ActualTime: time.Now().Format(time.RFC3339), Boundary: "FR20 fixed simulated recheck on fresh full official two-year online backup. No invented official business evidence, no changed source or existing business/auth database. Separate fresh synthetic authentication supports actual production-handler HTTP diagnostics. Not formal NFR or human/device acceptance. Only aggregate counts, timings, file hashes, and metadata are emitted.", WorkbookSHA: officialSource, Durations: map[string]float64{}, Checks: map[string]bool{}, Probes: map[string]any{}}
	r.Resumed = *resume
	start := time.Now()
	var e error
	day := time.Now().In(time.FixedZone("Asia/Shanghai", 8*3600)).Format("01-02")
	if *source == "" || *destination == "" || flag.NArg() != 0 {
		e = fmt.Errorf("explicit source and new db required; no additional args accepted")
	} else if day == "10-22" || day == "11-17" {
		e = fmt.Errorf("reserved day: no verification")
	} else {
		e = execute(&r, *source, *destination, *resume, *skipLoad)
	}
	r.Durations["total"] = time.Since(start).Seconds()
	r.Passed = e == nil && r.SourceUnchanged
	if e != nil {
		r.Failure = e.Error()
	} else if !r.SourceUnchanged {
		r.Failure = "source hash changed or unavailable"
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	_ = enc.Encode(r)
	if !r.Passed {
		os.Exit(1)
	}
}
