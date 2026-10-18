// reviewverify runs one fixed, explicitly simulated review scenario on a fresh
// online backup. It exposes no role/action override and never opens the source
// for writes. HTTP GET diagnostics use a fresh synthetic authentication DB and
// production handlers; they do not constitute the formal NFR-04 measurement.
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
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/pquerna/otp/totp"
	"github.com/shopspring/decimal"
	sqlite "modernc.org/sqlite"
	"returnsrecon/internal/auth"
	"returnsrecon/internal/store"
	"returnsrecon/internal/web"
)

const officialSource = "bcbe73b35f5b7babf197fb0cb983a11f5d9ff929078d4aa53d171b1f2df2e980"

type report struct {
	ActualTime          string             `json:"actual_time"`
	Boundary            string             `json:"boundary"`
	Source              string             `json:"source_database"`
	Database            string             `json:"isolated_database"`
	SourceBefore        string             `json:"source_sha256_before"`
	SourceAfter         string             `json:"source_sha256_after"`
	SourceUnchanged     bool               `json:"source_unchanged"`
	OfficialWorkbookSHA string             `json:"official_workbook_sha256"`
	Durations           map[string]float64 `json:"phase_seconds"`
	Coverage            map[string]int     `json:"recommendation_coverage"`
	InitialCounts       map[string]int64   `json:"before_counts"`
	FinalCounts         map[string]int64   `json:"after_counts"`
	Batch               map[string]any     `json:"batch"`
	Queries             queryReport        `json:"concurrent_http_queries"`
	Checks              map[string]bool    `json:"independent_checks"`
	Passed              bool               `json:"passed"`
	Failure             string             `json:"failure,omitempty"`
}

type queryReport struct {
	Boundary     string  `json:"boundary"`
	Workers      int     `json:"workers"`
	Attempted    int     `json:"attempted"`
	Succeeded    int     `json:"succeeded"`
	Failed       int     `json:"failed"`
	DuringCommit int     `json:"started_during_commit"`
	P95          float64 `json:"p95_milliseconds"`
	Maximum      float64 `json:"maximum_milliseconds"`
	LatestStart  float64 `json:"last_request_start_seconds_from_commit_start"`
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
		body, _ := json.Marshal(map[string]any{"phase": name, "actual_time": time.Now().Format(time.RFC3339), "phase_seconds": r.Durations})
		_ = os.WriteFile(filepath.Join(filepath.Dir(r.Database), "progress.json"), body, 0600)
	}
}

func openReadOnlySource(path string) (*sql.DB, error) { return store.OpenReadOnlySource(path) }

func validateDestination(source, destination string) (string, string, error) {
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
	root := filepath.Join(os.Getenv("LOCALAPPDATA"), "dutucijp-vibecoding-final", "verification")
	root, e = filepath.EvalSymlinks(root)
	if e != nil {
		return "", "", e
	}
	rel, e := filepath.Rel(root, parent)
	if e != nil {
		return "", "", e
	}
	parts := strings.Split(rel, string(filepath.Separator))
	if len(parts) != 1 || !strings.HasPrefix(strings.ToLower(parts[0]), "review-") || len(parts[0]) <= len("review-") {
		return "", "", fmt.Errorf("destination must be directly inside verification/review-* directory")
	}
	if strings.EqualFold(src, dst) {
		return "", "", fmt.Errorf("source and destination must differ")
	}
	for _, suffix := range []string{"", "-wal", "-shm"} {
		if _, e := os.Stat(dst + suffix); !os.IsNotExist(e) {
			return "", "", fmt.Errorf("destination or SQLite companion already exists")
		}
	}
	return src, dst, nil
}

func backup(ctx context.Context, source *sql.DB, destination string) error {
	c, e := source.Conn(ctx)
	if e != nil {
		return e
	}
	defer c.Close()
	return c.Raw(func(raw any) error {
		checker, ok := raw.(interface{ IsReadOnly(string) (bool, error) })
		if !ok {
			return fmt.Errorf("SQLite read-only connection verification unavailable")
		}
		readOnly, e := checker.IsReadOnly("main")
		if e != nil {
			return e
		}
		if !readOnly {
			return fmt.Errorf("refusing source connection that is not SQLite read-only")
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

func counts(ctx context.Context, db *sql.DB, run string) (map[string]int64, error) {
	out := map[string]int64{}
	for key, q := range map[string]string{
		"sale_balances":       "SELECT count(*) FROM sale_balances WHERE run_id=?",
		"allocations":         "SELECT count(*) FROM allocations WHERE run_id=?",
		"allocation_quantity": "SELECT coalesce(sum(quantity),0) FROM allocations WHERE run_id=?",
		"cases":               "SELECT count(*) FROM cases WHERE run_id=?",
		"closed_cases":        "SELECT count(*) FROM cases WHERE run_id=? AND status='closed'",
		"audit_events":        "SELECT count(*) FROM audit_events a JOIN cases c ON c.id=a.case_id WHERE c.run_id=?",
	} {
		n, e := scalar(ctx, db, q, run)
		if e != nil {
			return out, e
		}
		out[key] = n
	}
	return out, nil
}

func decisionsDigest(ctx context.Context, db *sql.DB, run string) (string, error) {
	rows, e := db.QueryContext(ctx, "SELECT cancel_id,quantity,classification,reason,candidate_lower_bound FROM decisions WHERE run_id=? ORDER BY cancel_id", run)
	if e != nil {
		return "", e
	}
	defer rows.Close()
	h := sha256.New()
	enc := json.NewEncoder(h)
	for rows.Next() {
		var id, class, reason string
		var qty int64
		var candidates int
		if e = rows.Scan(&id, &qty, &class, &reason, &candidates); e != nil {
			return "", e
		}
		if e = enc.Encode([]any{id, qty, class, reason, candidates}); e != nil {
			return "", e
		}
	}
	if e = rows.Err(); e != nil {
		return "", e
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func verifyInvariants(ctx context.Context, db *sql.DB, run, dataset string) (map[string]bool, error) {
	checks := map[string]bool{}
	queries := map[string]string{
		"source_balance_original_and_equation": `SELECT count(*) FROM sale_balances b LEFT JOIN normalized_rows n ON n.dataset_id=? AND n.id=b.sale_id WHERE b.run_id=? AND (n.id IS NULL OR n.kind<>'sale' OR n.quantity<>b.original OR b.original-b.allocated<>b.remaining OR b.allocated<0 OR b.remaining<0)`,
		"all_accepted_sales_have_balance":      `SELECT count(*) FROM normalized_rows n LEFT JOIN sale_balances b ON b.run_id=? AND b.sale_id=n.id WHERE n.dataset_id=? AND n.kind='sale' AND n.excluded='' AND b.sale_id IS NULL`,
		"balances_match_allocation_sums":       `SELECT count(*) FROM sale_balances b LEFT JOIN (SELECT sale_id,sum(quantity) AS used FROM allocations WHERE run_id=? GROUP BY sale_id) a ON a.sale_id=b.sale_id WHERE b.run_id=? AND b.allocated<>coalesce(a.used,0)`,
		"allocations_have_balances":            `SELECT count(*) FROM allocations a LEFT JOIN sale_balances b ON b.run_id=a.run_id AND b.sale_id=a.sale_id WHERE a.run_id=? AND b.sale_id IS NULL`,
		"duplicate_group_capacity":             `SELECT count(*) FROM duplicate_sale_groups g WHERE g.run_id=? AND (SELECT coalesce(sum(b.allocated),0) FROM duplicate_sale_members m JOIN sale_balances b ON b.run_id=m.run_id AND b.sale_id=m.sale_id WHERE m.run_id=g.run_id AND m.group_id=g.id)>g.capacity`,
	}
	for name, q := range queries {
		var args []any
		switch name {
		case "source_balance_original_and_equation":
			args = []any{dataset, run}
		case "all_accepted_sales_have_balance":
			args = []any{run, dataset}
		case "balances_match_allocation_sums":
			args = []any{run, run}
		default:
			args = []any{run}
		}
		n, e := scalar(ctx, db, q, args...)
		if e != nil {
			return checks, e
		}
		checks[name] = n == 0
		if n != 0 {
			return checks, fmt.Errorf("independent invariant %s violations=%d", name, n)
		}
	}
	rows, e := db.QueryContext(ctx, `SELECT a.quantity,a.amount,c.kind,c.quantity,c.customer,c.stock,c.country,c.source_time,c.price,c.excluded,s.kind,s.quantity,s.customer,s.stock,s.country,s.source_time,s.price,s.excluded,d.quantity FROM allocations a JOIN runs r ON r.id=a.run_id LEFT JOIN normalized_rows c ON c.dataset_id=r.dataset_id AND c.id=a.cancel_id LEFT JOIN normalized_rows s ON s.dataset_id=r.dataset_id AND s.id=a.sale_id LEFT JOIN decisions d ON d.run_id=a.run_id AND d.cancel_id=a.cancel_id WHERE a.run_id=?`, run)
	if e != nil {
		return checks, e
	}
	defer rows.Close()
	for rows.Next() {
		var q, cq, sq, dq int64
		var amount, ck, cc, cs, cn, ct, cp, ce, sk, sc, ss, sn, st, sp, se string
		if e = rows.Scan(&q, &amount, &ck, &cq, &cc, &cs, &cn, &ct, &cp, &ce, &sk, &sq, &sc, &ss, &sn, &st, &sp, &se, &dq); e != nil {
			return checks, e
		}
		cprice, pe := decimal.NewFromString(cp)
		sprice, spe := decimal.NewFromString(sp)
		paid, ae := decimal.NewFromString(amount)
		cancelTime, cte := time.Parse("2006-01-02T15:04:05.000000", ct)
		saleTime, ste := time.Parse("2006-01-02T15:04:05.000000", st)
		if ck != "cancel" || sk != "sale" || cq >= 0 || cq == (-1<<63) || sq <= 0 || q <= 0 || q != -cq || dq != q || q > sq || cc == "" || cs == "" || cn == "" || cc != sc || cs != ss || cn != sn || ce != "" || se != "" || pe != nil || spe != nil || ae != nil || !cprice.IsPositive() || !cprice.Equal(sprice) || !paid.Equal(cprice.Mul(decimal.NewFromInt(q))) || cte != nil || ste != nil || !saleTime.Before(cancelTime) {
			return checks, fmt.Errorf("allocation exact identity/time/quantity/price/amount invariant failed")
		}
	}
	if e = rows.Err(); e != nil {
		return checks, e
	}
	checks["every_allocation_exact_identity_time_quantity_price_amount"] = true
	return checks, nil
}

func syntheticHTTP(ctx context.Context, s *store.Store, root string) (*httptest.Server, string, func(), error) {
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
	const identity = "isolated-review-tc55-http-finance"
	if _, e = svc.Initialize(ctx, []auth.Identity{{ID: identity, Role: "finance"}}); e != nil {
		cleanup()
		return nil, "", func() {}, e
	}
	g, e := svc.StartSetup(ctx, identity)
	if e != nil {
		cleanup()
		return nil, "", func() {}, e
	}
	g, e = svc.SetPassword(ctx, g.Token, "Isolated TC55 amber forest river 83!")
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
	g, e = svc.ConfirmTOTP(ctx, g.Token, code, "isolated-tc55-local")
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
		return nil, "", func() {}, fmt.Errorf("synthetic authenticated setup did not obtain business scope")
	}
	server := httptest.NewServer((&web.Server{Store: s, Auth: svc, Origin: "http://localhost:8080"}).Handler())
	return server, g.Token, func() { server.Close(); cleanup() }, nil
}

func concurrentQueries(ctx context.Context, s *store.Store, server *httptest.Server, token, run, batch string, active *atomic.Bool, commitStart time.Time, ready chan<- struct{}, result chan<- queryReport) {
	var mu sync.Mutex
	var wg sync.WaitGroup
	times := []float64{}
	out := queryReport{Boundary: "Four workers, 300 actual HTTP GETs through production Host/session/finance scope and transaction queries. Separate fresh synthetic authentication DB; copied business auth history untouched. No same-database write-session validation; transaction revocation is covered separately by HTTP integration tests. Requests paced across completed batch groups to cover the commit window. Local TC55 diagnostic, not formal NFR04 or NFR02/03 or browser-visible timing.", Workers: 4}
	initialReady, _ := scalar(ctx, s.DB, "SELECT count(*) FROM review_batch_groups WHERE batch_id=? AND status='ready'", batch)
	for worker := 0; worker < 4; worker++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			for iteration := 0; iteration < 75; iteration++ {
				if iteration > 0 && initialReady > 0 {
					for active.Load() {
						n, e := scalar(ctx, s.DB, "SELECT count(*) FROM review_batch_groups WHERE batch_id=? AND status='ready'", batch)
						if e != nil || n <= initialReady*int64(74-iteration)/74 {
							break
						}
						time.Sleep(20 * time.Millisecond)
					}
				}
				path := "/api/transactions?run_id=" + run + "&page_size=50"
				switch (worker + iteration) % 4 {
				case 1:
					path += "&page=10000"
				case 2:
					path += "&sort=source&direction=desc"
				case 3:
					path += "&date_from=2010-12-01&date_to=2010-12-02"
				}
				started := time.Now()
				during := active.Load()
				request, e := http.NewRequestWithContext(ctx, "GET", server.URL+path, nil)
				var page store.TransactionPage
				status := 0
				if e == nil {
					request.Host = "localhost:8080"
					request.AddCookie(&http.Cookie{Name: "recon_session", Value: token})
					var response *http.Response
					response, e = server.Client().Do(request)
					if e == nil {
						status = response.StatusCode
						e = json.NewDecoder(io.LimitReader(response.Body, 4*1024*1024)).Decode(&page)
						response.Body.Close()
					}
				}
				elapsed := float64(time.Since(started).Microseconds()) / 1000
				mu.Lock()
				out.Attempted++
				if during {
					out.DuringCommit++
				}
				if e != nil || status != 200 || len(page.Items) > 50 || page.Total < 0 {
					out.Failed++
				} else {
					out.Succeeded++
				}
				times = append(times, elapsed)
				if offset := started.Sub(commitStart).Seconds(); offset > out.LatestStart {
					out.LatestStart = offset
				}
				mu.Unlock()
			}
		}(worker)
	}
	close(ready)
	wg.Wait()
	sort.Float64s(times)
	if len(times) > 0 {
		out.P95 = times[(len(times)*95+99)/100-1]
		out.Maximum = times[len(times)-1]
	}
	result <- out
}

func execute(r *report, source, destination string) error {
	ctx := context.Background()
	var e error
	r.Source, r.Database, e = validateDestination(source, destination)
	if e != nil {
		return e
	}
	phase(r, "source_hash")
	r.SourceBefore, e = digest(r.Source)
	if e != nil {
		return e
	}
	defer func() { r.SourceAfter, _ = digest(r.Source); r.SourceUnchanged = r.SourceBefore == r.SourceAfter }()
	original, e := openReadOnlySource(r.Source)
	if e != nil {
		return e
	}
	defer original.Close()
	var run, dataset, summary string
	e = original.QueryRowContext(ctx, `SELECT r.id,r.dataset_id,r.summary_json FROM runs r JOIN datasets d ON d.id=r.dataset_id WHERE r.status='published' AND d.status='published' AND d.scope='all' AND d.cleaning_version='cr-1.0.1' AND r.rule_version='conservative-1.0.0' AND d.source_hash=? ORDER BY r.id LIMIT 1`, officialSource).Scan(&run, &dataset, &summary)
	if e != nil {
		return fmt.Errorf("official all-scope source validation: %w", e)
	}
	start := time.Now()
	phase(r, "online_backup")
	if e = backup(ctx, original, r.Database); e != nil {
		return e
	}
	r.Checks["source_driver_reports_file_read_only"] = true
	r.Durations["online_backup"] = time.Since(start).Seconds()
	start = time.Now()
	phase(r, "open_and_migrate")
	s, e := store.Open(r.Database)
	if e != nil {
		return e
	}
	defer s.Close()
	r.Durations["open_and_migrate"] = time.Since(start).Seconds()
	a := store.WorkflowActor{ID: "isolated-review-tc55-simulated-finance", Role: "finance"}
	if n, e := scalar(ctx, s.DB, "SELECT count(*) FROM cases WHERE run_id=? AND assignee=?", run, a.ID); e != nil || n != 0 {
		return fmt.Errorf("fixed simulated actor must have no preexisting ownership")
	}
	phase(r, "initial_counts_and_decision_digest")
	r.InitialCounts, e = counts(ctx, s.DB, run)
	if e != nil {
		return e
	}
	decisionsBefore, e := decisionsDigest(ctx, s.DB, run)
	if e != nil {
		return e
	}
	start = time.Now()
	phase(r, "generate_review")
	set, e := s.GenerateReviewSet(ctx, a, run)
	if e != nil {
		return e
	}
	r.Durations["generate_review"] = time.Since(start).Seconds()
	r.Coverage["items"] = len(set.Items)
	dup, baskets := map[string]bool{}, map[string]bool{}
	for _, it := range set.Items {
		seen := map[string]bool{}
		if it.ChoiceID != "" {
			r.Coverage["default_chosen"]++
		}
		if it.ChoiceID == "" && len(it.Options) > 0 {
			r.Coverage["unchosen_with_options"]++
		}
		for _, o := range it.Options {
			r.Coverage["options_"+o.Kind]++
			if !seen[o.Kind] {
				r.Coverage["items_with_"+o.Kind]++
				seen[o.Kind] = true
			}
			if o.Duplicate != nil {
				dup[o.Duplicate.ID] = true
			}
			if o.BasketID != "" {
				baskets[o.BasketID] = true
			}
		}
	}
	r.Coverage["distinct_duplicate_proposals"] = len(dup)
	r.Coverage["distinct_basket_proposals"] = len(baskets)
	afterGenerate, e := counts(ctx, s.DB, run)
	if e != nil {
		return e
	}
	if !sameCounts(r.InitialCounts, afterGenerate) {
		return fmt.Errorf("recommendation generation changed business state")
	}
	r.Checks["recommendations_do_not_reserve_or_write_business"] = true
	start = time.Now()
	phase(r, "preview")
	b, e := s.PreviewBatch(ctx, a, store.BatchRequest{SetID: set.ID, Action: "approve", All: true, RecommendedOnly: true, Conclusion: "TC55隔离自动化验证：固定模拟身份依据推荐完成复核，仅用于验证，不代表真实原单关系或本人操作。"}, store.WorkflowActor{})
	if e != nil {
		return e
	}
	r.Durations["preview"] = time.Since(start).Seconds()
	afterPreview, e := counts(ctx, s.DB, run)
	if e != nil {
		return e
	}
	if !sameCounts(r.InitialCounts, afterPreview) {
		return fmt.Errorf("preview changed business state")
	}
	r.Checks["preview_does_not_reserve_or_write_business"] = true
	start = time.Now()
	phase(r, "synthetic_http_auth_setup")
	httpServer, httpToken, httpCleanup, e := syntheticHTTP(ctx, s, filepath.Dir(r.Database))
	if e != nil {
		return fmt.Errorf("synthetic HTTP diagnostic setup: %w", e)
	}
	defer httpCleanup()
	r.Durations["synthetic_http_auth_setup"] = time.Since(start).Seconds()
	var active atomic.Bool
	active.Store(true)
	ready := make(chan struct{})
	queryResult := make(chan queryReport, 1)
	start = time.Now()
	go concurrentQueries(ctx, s, httpServer, httpToken, run, b.ID, &active, start, ready, queryResult)
	<-ready
	phase(r, "commit_with_concurrent_authenticated_http_queries")
	committed, e := s.CommitBatch(ctx, a, b.ID)
	active.Store(false)
	r.Durations["commit"] = time.Since(start).Seconds()
	r.Queries = <-queryResult
	if e != nil {
		return e
	}
	groupCounts, codeCounts := map[string]int{}, map[string]int{}
	for _, g := range committed.Groups {
		groupCounts[g.Status]++
		if g.Code != "" {
			codeCounts[g.Code] += len(g.Items)
		}
	}
	r.Batch = map[string]any{"status": committed.Status, "total": committed.Total, "succeeded": committed.Succeeded, "skipped": committed.Skipped, "failed": committed.Failed, "groups": groupCounts, "item_codes": codeCounts}
	r.FinalCounts, e = counts(ctx, s.DB, run)
	if e != nil {
		return e
	}
	start = time.Now()
	phase(r, "independent_invariants")
	checks, e := verifyInvariants(ctx, s.DB, run, dataset)
	for k, v := range checks {
		r.Checks[k] = v
	}
	if e != nil {
		return e
	}
	var summaryAfter string
	if e = s.DB.QueryRowContext(ctx, "SELECT summary_json FROM runs WHERE id=?", run).Scan(&summaryAfter); e != nil {
		return e
	}
	r.Checks["initial_summary_unchanged"] = summaryAfter == summary
	if summaryAfter != summary {
		return fmt.Errorf("initial summary changed")
	}
	decisionsAfter, e := decisionsDigest(ctx, s.DB, run)
	if e != nil {
		return e
	}
	r.Checks["initial_decisions_unchanged"] = decisionsBefore == decisionsAfter
	if decisionsBefore != decisionsAfter {
		return fmt.Errorf("initial decisions changed")
	}
	deltaAlloc := r.FinalCounts["allocations"] - r.InitialCounts["allocations"]
	deltaAudit := r.FinalCounts["audit_events"] - r.InitialCounts["audit_events"]
	deltaClosed := r.FinalCounts["closed_cases"] - r.InitialCounts["closed_cases"]
	r.Checks["successful_approvals_have_allocation_close_and_four_audits"] = deltaAlloc == int64(committed.Succeeded) && deltaClosed == deltaAlloc && deltaAudit == deltaAlloc*4
	if !r.Checks["successful_approvals_have_allocation_close_and_four_audits"] {
		return fmt.Errorf("successful approval workflow aggregate mismatch")
	}
	r.Durations["independent_validation"] = time.Since(start).Seconds()
	start = time.Now()
	phase(r, "idempotent_replay")
	replayed, e := s.CommitBatch(ctx, a, b.ID)
	if e != nil {
		return e
	}
	again, e := counts(ctx, s.DB, run)
	if e != nil {
		return e
	}
	r.Checks["commit_replay_idempotent"] = sameCounts(again, r.FinalCounts) && replayed.Succeeded == committed.Succeeded && replayed.Skipped == committed.Skipped && replayed.Failed == committed.Failed && replayed.Status == committed.Status
	if !r.Checks["commit_replay_idempotent"] {
		return fmt.Errorf("commit replay changed outcomes or business state")
	}
	r.Durations["idempotent_replay"] = time.Since(start).Seconds()
	r.Checks["concurrent_authenticated_http_queries_zero_failures"] = r.Queries.Failed == 0 && r.Queries.Attempted == 300 && r.Queries.DuringCommit > 0
	if !r.Checks["concurrent_authenticated_http_queries_zero_failures"] {
		return fmt.Errorf("concurrent authenticated HTTP query diagnostic failed")
	}
	return nil
}

func sameCounts(a, b map[string]int64) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

func main() {
	source := flag.String("source", "", "official all database, opened read-only")
	destination := flag.String("db", "", "new database in an existing verification/review-* directory")
	flag.Parse()
	r := report{ActualTime: time.Now().In(time.FixedZone("Asia/Shanghai", 8*3600)).Format(time.RFC3339), Boundary: "TC55 fixed simulated finance on fresh online backup; official two-year all scope. Separate synthetic auth DB enables real authenticated HTTP GET diagnostics; source/copy business auth history untouched. No same-database write-session validation, formal NFR04/NFR02/NFR03 claim, real-order accuracy claim, or human acceptance. Only aggregate outcomes and file hashes are emitted.", OfficialWorkbookSHA: officialSource, Durations: map[string]float64{}, Coverage: map[string]int{}, Checks: map[string]bool{}}
	start := time.Now()
	var e error
	day := time.Now().In(time.FixedZone("Asia/Shanghai", 8*3600)).Format("01-02")
	if *source == "" || *destination == "" || flag.NArg() != 0 {
		e = fmt.Errorf("explicit --source and new --db required; no additional arguments accepted")
	} else if day == "10-22" || day == "11-17" {
		e = fmt.Errorf("reserved day: no verification")
	} else {
		e = execute(&r, *source, *destination)
	}
	r.Durations["total"] = time.Since(start).Seconds()
	r.Passed = e == nil && r.SourceUnchanged
	if e != nil {
		r.Failure = e.Error()
	} else if !r.SourceUnchanged {
		r.Failure = "source database hash changed or unavailable"
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	_ = enc.Encode(r)
	if !r.Passed {
		os.Exit(1)
	}
}
