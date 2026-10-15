// perfhttp exercises real authenticated HTTP queries against an official-data
// backup while a real independent-scope job imports and reconciles. It never
// connects to the user's running service, inserts sessions, or outputs secrets.
package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"sync"
	"time"

	"github.com/pquerna/otp/totp"
	sqlite "modernc.org/sqlite"
	"returnsrecon/internal/auth"
	"returnsrecon/internal/store"
	"returnsrecon/internal/web"
)

const origin = "http://localhost:8080"

type requestResult struct {
	Worker       int     `json:"worker"`
	Sequence     int     `json:"sequence"`
	Category     string  `json:"category"`
	StartedAt    string  `json:"started_at"`
	Milliseconds float64 `json:"milliseconds"`
	Status       int     `json:"http_status"`
	Bytes        int     `json:"response_bytes"`
	Error        string  `json:"error_code,omitempty"`
	BeforeState  string  `json:"job_state_before"`
	BeforeStage  string  `json:"job_stage_before"`
	AfterState   string  `json:"job_state_after"`
	AfterStage   string  `json:"job_stage_after"`
	InWindow     bool    `json:"both_job_observations_active"`
}

type report struct {
	TerminalError  string         `json:"terminal_error,omitempty"`
	Complete       bool           `json:"measurement_complete"`
	ActualTime     string         `json:"actual_time"`
	GoVersion      string         `json:"go_version"`
	LogicalCPUs    int            `json:"logical_cpus"`
	BinarySHA256   string         `json:"binary_sha256"`
	SourceDB       string         `json:"source_database"`
	SourceBefore   string         `json:"source_sha256_before"`
	SourceAfter    string         `json:"source_sha256_after"`
	IsolatedDB     string         `json:"isolated_database"`
	BackupSeconds  float64        `json:"read_only_backup_seconds"`
	HTTPURL        string         `json:"actual_http_server_url"`
	CanonicalHost  string         `json:"request_host_and_origin"`
	Authentication string         `json:"authentication_method"`
	QueryScope     string         `json:"query_scope"`
	QueryRun       string         `json:"query_run"`
	JobID          string         `json:"job_id"`
	JobScope       string         `json:"job_scope"`
	JobAtLoadStart web.Job        `json:"job_at_load_start"`
	JobAtLoadEnd   web.Job        `json:"job_at_load_end"`
	JobFinal       web.Job        `json:"job_final"`
	Workers        int            `json:"workers"`
	Requests       int            `json:"requests"`
	PerWorker      int            `json:"requests_per_worker"`
	Failures       int            `json:"failures"`
	InWindow       int            `json:"requests_with_active_job_before_and_after"`
	P95            float64        `json:"p95_milliseconds_nearest_rank"`
	Max            float64        `json:"max_milliseconds"`
	BaselinePassed bool           `json:"baseline_passed"`
	TargetPassed   bool           `json:"target_passed"`
	Categories     map[string]int `json:"categories"`
	RequestsPath   string         `json:"request_output"`
	Boundary       string         `json:"measurement_boundary"`
	Note           string         `json:"notice"`
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func digest(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	h := sha256.New()
	if _, err = io.Copy(h, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func readonlyBackup(ctx context.Context, source, destination string) error {
	db, err := sql.Open("sqlite", filepath.ToSlash(source)+"?mode=ro&_pragma=query_only(1)")
	if err != nil {
		return err
	}
	defer db.Close()
	conn, err := db.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	return conn.Raw(func(raw any) error {
		backuper, ok := raw.(interface {
			NewBackup(string) (*sqlite.Backup, error)
		})
		if !ok {
			return fmt.Errorf("SQLite online backup API unavailable")
		}
		backup, e := backuper.NewBackup(filepath.ToSlash(destination))
		if e != nil {
			return e
		}
		_, stepErr := backup.Step(-1)
		finishErr := backup.Finish()
		if stepErr != nil {
			return stepErr
		}
		return finishErr
	})
}

type client struct {
	http              *http.Client
	base, token, csrf string
}

func (c *client) request(ctx context.Context, method, path string, body any) (int, []byte, error) {
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return 0, nil, err
		}
		reader = bytes.NewReader(encoded)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, reader)
	if err != nil {
		return 0, nil, err
	}
	req.Host = "localhost:8080"
	req.Header.Set("Origin", origin)
	req.Header.Set("X-CSRF-Token", c.csrf)
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: "recon_session", Value: c.token})
	response, err := c.http.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, 16*1024*1024))
	return response.StatusCode, data, err
}

func realTestLogin(ctx context.Context, security *auth.Service) (auth.Grant, error) {
	idBytes := make([]byte, 8)
	if _, err := rand.Read(idBytes); err != nil {
		return auth.Grant{}, err
	}
	id := "perf-test-finance-" + hex.EncodeToString(idBytes)
	bootstrap, err := security.Initialize(ctx, []auth.Identity{{ID: id, Role: "finance"}})
	if err != nil {
		return auth.Grant{}, fmt.Errorf("isolated test identity initialization failed")
	}
	grant, err := security.Bootstrap(ctx, bootstrap[id])
	if err != nil {
		return auth.Grant{}, fmt.Errorf("real bootstrap validation failed")
	}
	passwordBytes := make([]byte, 16)
	if _, err = rand.Read(passwordBytes); err != nil {
		return auth.Grant{}, err
	}
	password := base64.RawURLEncoding.EncodeToString(passwordBytes)
	grant, err = security.SetPassword(ctx, grant.Token, password)
	if err != nil {
		return auth.Grant{}, fmt.Errorf("real strong password setup failed")
	}
	enrollment, err := security.BeginTOTP(ctx, grant.Token)
	if err != nil {
		return auth.Grant{}, fmt.Errorf("real TOTP enrollment failed")
	}
	// Enroll with the previous still-valid 30s step. The current real-time step
	// remains unused for the subsequent password+TOTP login. No clock is changed.
	code, err := totp.GenerateCode(enrollment.Secret, time.Now().Add(-30*time.Second))
	if err != nil {
		return auth.Grant{}, fmt.Errorf("isolated TOTP generation failed")
	}
	grant, err = security.ConfirmTOTP(ctx, grant.Token, code, "isolated-perf-enrollment")
	if err != nil {
		return auth.Grant{}, fmt.Errorf("real TOTP proof verification failed")
	}
	if len(grant.RecoveryCodes) != 10 {
		return auth.Grant{}, fmt.Errorf("recovery acknowledgement prerequisite missing")
	}
	// Test recovery codes remain only in memory, are acknowledged for this test,
	// and are discarded. This is not a human saving codes or configuring a device.
	grant, err = security.Complete(ctx, grant.Token, true)
	if err != nil {
		return auth.Grant{}, fmt.Errorf("real completion gate failed")
	}
	if err = security.Logout(ctx, grant.Token); err != nil {
		return auth.Grant{}, fmt.Errorf("test logout failed")
	}
	grant, err = security.LoginPassword(ctx, id, password, "isolated-perf-login")
	if err != nil {
		return auth.Grant{}, fmt.Errorf("real password login failed")
	}
	code, err = totp.GenerateCode(enrollment.Secret, time.Now())
	if err != nil {
		return auth.Grant{}, fmt.Errorf("isolated login TOTP generation failed")
	}
	grant, err = security.LoginTOTP(ctx, grant.Token, code, "isolated-perf-login")
	if err != nil {
		return auth.Grant{}, fmt.Errorf("real TOTP login validation failed")
	}
	if grant.Session.Scope != "business" || grant.Session.State != "complete" || grant.Session.Role != "finance" {
		return auth.Grant{}, fmt.Errorf("real factor flow did not issue a finance business session")
	}
	return grant, nil
}

func run() (runErr error) {
	flags := flag.NewFlagSet("perfhttp", flag.ContinueOnError)
	source := flags.String("source-db", "", "closed published official all database to back up read-only")
	output := flags.String("output-dir", "", "required new isolated directory outside source")
	project := flags.String("project", "", "actual project source for the real preparation worker")
	if err := flags.Parse(os.Args[1:]); err != nil {
		return err
	}
	day := time.Now().In(time.FixedZone("Asia/Shanghai", 8*3600)).Format("01-02")
	if day == "10-22" || day == "11-17" {
		return fmt.Errorf("reserved day: no verification work")
	}
	if *source == "" || *output == "" || *project == "" {
		return fmt.Errorf("explicit --source-db, --output-dir, --project required")
	}
	root, err := filepath.Abs(*output)
	if err != nil {
		return err
	}
	projectRoot, err := filepath.Abs(*project)
	if err != nil {
		return err
	}
	if rel, e := filepath.Rel(projectRoot, root); e == nil && (rel == "." || rel != ".." && !bytes.HasPrefix([]byte(rel), []byte(".."+string(os.PathSeparator)))) {
		return fmt.Errorf("output must be outside the source project")
	}
	if _, err = os.Stat(root); !os.IsNotExist(err) {
		return fmt.Errorf("requires a new isolated output directory")
	}
	if err = os.MkdirAll(root, 0700); err != nil {
		return err
	}
	if err = auth.PrivateDirectory(root); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	out := report{ActualTime: time.Now().Format(time.RFC3339Nano), GoVersion: runtime.Version(), LogicalCPUs: runtime.NumCPU(), SourceDB: *source, IsolatedDB: filepath.Join(root, "isolated.sqlite"), CanonicalHost: origin, Authentication: "Isolated random finance identity; real Bootstrap -> strong password -> TOTP proof -> Complete -> Logout -> password+TOTP login. No sessions inserted. No real human/device claim.", QueryScope: "all", JobScope: "year1", Workers: 10, PerWorker: 30, Categories: map[string]int{}, RequestsPath: filepath.Join(root, "requests.jsonl"), Boundary: "10 simultaneous goroutines, 30 mixed authenticated HTTP GETs each. Timer spans request construction/transport/server/authentication/query/full body read. Per-request production job state sampled directly before and after, outside query timer. Load begins only at real importing/reconciling stage. No password hashing per query.", Note: "Official transactions in an isolated read-only backup. Test secrets remain only in private runtime or memory and are never logged. Source database must remain unchanged; current all is queried while a real previously absent year1 dataset is prepared/imported/reconciled."}
	// Preserve an explicitly incomplete report on timeout/observation failures.
	// A failed verifier must never discard already measured request evidence.
	defer func() {
		if runErr != nil {
			out.TerminalError = runErr.Error()
		}
		if out.SourceBefore != "" && out.SourceAfter == "" {
			out.SourceAfter, _ = digest(*source)
		}
		if encoded, e := json.MarshalIndent(out, "", "  "); e == nil {
			_ = os.WriteFile(filepath.Join(root, "summary.json"), append(encoded, '\n'), 0600)
		}
	}()
	executable, _ := os.Executable()
	out.BinarySHA256, err = digest(executable)
	if err != nil {
		return err
	}
	out.SourceBefore, err = digest(*source)
	if err != nil {
		return err
	}
	started := time.Now()
	if err = readonlyBackup(ctx, *source, out.IsolatedDB); err != nil {
		return err
	}
	out.BackupSeconds = time.Since(started).Seconds()
	s, err := store.Open(out.IsolatedDB)
	if err != nil {
		return err
	}
	defer s.Close()
	if err = s.EnsureQueryIndexes(ctx); err != nil {
		return err
	}
	err = s.DB.QueryRowContext(ctx, `SELECT r.id FROM runs r JOIN datasets d ON d.id=r.dataset_id WHERE r.status='published' AND d.status='published' AND d.scope='all' AND d.source_hash='bcbe73b35f5b7babf197fb0cb983a11f5d9ff929078d4aa53d171b1f2df2e980' AND d.cleaning_version='cr-1.0.1' AND r.rule_version='conservative-1.0.0'`).Scan(&out.QueryRun)
	if err != nil {
		return fmt.Errorf("source needs the actual published official all run: %w", err)
	}
	var previousYear1 int
	if err = s.DB.QueryRowContext(ctx, "SELECT count(*) FROM datasets WHERE scope='year1'").Scan(&previousYear1); err != nil {
		return err
	}
	if previousYear1 != 0 {
		return fmt.Errorf("year1 must be absent so the live long task is not reused")
	}
	var caseID string
	if err = s.DB.QueryRowContext(ctx, "SELECT id FROM cases WHERE run_id=? ORDER BY cancel_id LIMIT 1", out.QueryRun).Scan(&caseID); err != nil {
		return err
	}
	security, err := auth.New(s.DB, auth.Config{SecretDir: filepath.Join(root, "private-auth"), Origin: origin})
	if err != nil {
		return err
	}
	grant, err := realTestLogin(ctx, security)
	if err != nil {
		return err
	}
	runtimeRoot := filepath.Join(os.Getenv("LOCALAPPDATA"), "dutucijp-vibecoding-final")
	jobs, err := web.NewJobs(s, projectRoot, runtimeRoot)
	if err != nil {
		return err
	}
	defer jobs.Close()
	server := httptest.NewServer((&web.Server{Store: s, Auth: security, Origin: origin, Jobs: jobs}).Handler())
	defer server.Close()
	out.HTTPURL = server.URL
	transport := &http.Transport{MaxIdleConns: 20, MaxIdleConnsPerHost: 20, MaxConnsPerHost: 20, DisableCompression: true}
	defer transport.CloseIdleConnections()
	c := &client{http: &http.Client{Transport: transport, Timeout: 30 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, base: server.URL, token: grant.Token, csrf: grant.Session.CSRF}
	status, data, err := c.request(ctx, "GET", "/api/auth/me", nil)
	if err != nil || status != 200 {
		return fmt.Errorf("real HTTP business session verification failed")
	}
	var session auth.Session
	if err = json.Unmarshal(data, &session); err != nil || session.Scope != "business" {
		return fmt.Errorf("HTTP session was not business scoped")
	}
	status, data, err = c.request(ctx, "POST", "/api/jobs", map[string]string{"scope": "year1"})
	if err != nil || status != 200 {
		return fmt.Errorf("real authenticated year1 job creation failed (HTTP %d)", status)
	}
	var job web.Job
	if err = json.Unmarshal(data, &job); err != nil {
		return err
	}
	out.JobID = job.ID
	fmt.Fprintln(os.Stderr, "NFR04: authenticated isolated HTTP server started; waiting for real year1 import")
	for {
		job, err = jobs.Get(ctx, out.JobID)
		if err != nil {
			return err
		}
		if job.Status == "running" && (job.Stage == "importing" || job.Stage == "reconciling") {
			break
		}
		if job.Status == "failed" || job.Status == "succeeded" {
			return fmt.Errorf("no active Go heavy-task measurement window (job %s/%s)", job.Status, job.Stage)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(200 * time.Millisecond):
		}
	}
	out.JobAtLoadStart = job
	fmt.Fprintln(os.Stderr, "NFR04: real import active; starting 10 goroutines x 30 mixed GETs")
	query := url.QueryEscape(out.QueryRun)
	workloads := []struct{ category, path string }{
		{"cases", "/api/cases?run_id=" + query + "&page_size=50"},
		{"transactions", "/api/transactions?run_id=" + query + "&page_size=50"},
		{"overview", "/api/overview?run_id=" + query},
		{"allocations", "/api/allocations?run_id=" + query + "&page_size=50"},
		{"case_evidence", "/api/cases/" + caseID + "/evidence?page_size=50"},
		{"case_candidates", "/api/cases/" + caseID + "/candidates?page_size=50"},
	}
	results := make(chan requestResult, 300)
	startGate := make(chan struct{})
	var wg sync.WaitGroup
	for worker := 0; worker < 10; worker++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			<-startGate
			for sequence := 0; sequence < 30; sequence++ {
				work := workloads[(worker+sequence)%len(workloads)]
				result := requestResult{Worker: worker, Sequence: sequence, Category: work.category}
				before, beforeErr := jobs.Get(ctx, out.JobID)
				result.BeforeState, result.BeforeStage = before.Status, before.Stage
				result.StartedAt = time.Now().Format(time.RFC3339Nano)
				started := time.Now()
				status, data, requestErr := c.request(ctx, "GET", work.path, nil)
				result.Milliseconds = time.Since(started).Seconds() * 1000
				result.Status, result.Bytes = status, len(data)
				after, afterErr := jobs.Get(ctx, out.JobID)
				result.AfterState, result.AfterStage = after.Status, after.Stage
				result.InWindow = beforeErr == nil && afterErr == nil && (before.Status == "running" || before.Status == "queued") && (after.Status == "running" || after.Status == "queued")
				if requestErr != nil {
					result.Error = "request_error"
				} else if status != 200 {
					result.Error = "non_200"
				} else if !json.Valid(data) {
					result.Error = "invalid_json"
				}
				if beforeErr != nil || afterErr != nil {
					result.Error = "job_observation_error"
				}
				results <- result
			}
		}(worker)
	}
	close(startGate)
	wg.Wait()
	close(results)
	requestsFile, err := os.OpenFile(out.RequestsPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	encoder := json.NewEncoder(requestsFile)
	latencies := make([]float64, 0, 300)
	for result := range results {
		if err = encoder.Encode(result); err != nil {
			requestsFile.Close()
			return err
		}
		out.Requests++
		out.Categories[result.Category]++
		latencies = append(latencies, result.Milliseconds)
		if result.InWindow {
			out.InWindow++
		}
		if result.Error != "" {
			out.Failures++
		}
	}
	if err = requestsFile.Close(); err != nil {
		return err
	}
	if len(latencies) != 300 {
		return fmt.Errorf("expected exactly 300 actual requests")
	}
	sort.Float64s(latencies)
	out.P95 = latencies[int(math.Ceil(.95*float64(len(latencies))))-1]
	out.Max = latencies[len(latencies)-1]
	out.JobAtLoadEnd, err = jobs.Get(ctx, out.JobID)
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "NFR04: %d requests, %d failures, %d active-window requests, P95 %.3fms; waiting for actual job completion\n", out.Requests, out.Failures, out.InWindow, out.P95)
	for {
		out.JobFinal, err = jobs.Get(ctx, out.JobID)
		if err != nil {
			return err
		}
		if out.JobFinal.Status == "failed" || out.JobFinal.Status == "succeeded" {
			break
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(250 * time.Millisecond):
		}
	}
	out.SourceAfter, err = digest(*source)
	if err != nil {
		return err
	}
	out.BaselinePassed = out.Requests >= 300 && out.Failures == 0 && out.InWindow >= 300 && out.P95 <= 500 && out.SourceBefore == out.SourceAfter && out.JobFinal.Status == "succeeded"
	out.TargetPassed = out.BaselinePassed && out.P95 <= 200
	out.Complete = true
	encoded, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return err
	}
	if err = os.WriteFile(filepath.Join(root, "summary.json"), append(encoded, '\n'), 0600); err != nil {
		return err
	}
	fmt.Println(string(encoded))
	if !out.BaselinePassed {
		return fmt.Errorf("actual NFR04 did not meet its baseline; raw request evidence preserved")
	}
	return nil
}
