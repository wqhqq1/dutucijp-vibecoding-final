// accountverify measures FR-19 country queries on a fresh online backup only.
// Its synthetic accounts use the real same-database authentication and handler.
// This is a query diagnostic, not NFR-04's concurrent background-job window.
package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
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

type identity struct {
	ID       string `json:"id"`
	Password string `json:"password"`
	TOTP     string `json:"totp_secret"`
	Cookie   string `json:"session_cookie"`
	CSRF     string `json:"csrf"`
	Country  string `json:"country,omitempty"`
}
type measurement struct {
	Country      string  `json:"country"`
	Path         string  `json:"path"`
	Expected     int     `json:"expected"`
	Actual       int     `json:"actual"`
	Milliseconds float64 `json:"milliseconds"`
}
type report struct {
	ActualTime      string        `json:"actual_time"`
	Boundary        string        `json:"boundary"`
	Source          string        `json:"source"`
	Database        string        `json:"database"`
	SourceBefore    string        `json:"source_sha256_before"`
	SourceAfter     string        `json:"source_sha256_after"`
	SourceUnchanged bool          `json:"source_unchanged"`
	RunID           string        `json:"run_id"`
	Measurements    []measurement `json:"measurements"`
	Workers         int           `json:"workers"`
	Attempted       int           `json:"attempted"`
	Succeeded       int           `json:"succeeded"`
	Failed          int           `json:"failed"`
	P95             float64       `json:"p95_milliseconds"`
	Maximum         float64       `json:"maximum_milliseconds"`
	TotalSeconds    float64       `json:"total_seconds"`
	Passed          bool          `json:"passed"`
	Failure         string        `json:"failure,omitempty"`
	URL             string        `json:"serve_url,omitempty"`
	Credentials     string        `json:"private_credentials_file"`
}
type client struct {
	base, origin string
	http         *http.Client
}

func (c client) request(ctx context.Context, who *identity, method, path string, body any) (*http.Response, error) {
	var data []byte
	var err error
	if body != nil {
		data, err = json.Marshal(body)
		if err != nil {
			return nil, err
		}
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	u, _ := url.Parse(c.origin)
	req.Host = u.Host
	req.Header.Set("Origin", c.origin)
	req.Header.Set("Content-Type", "application/json")
	if who != nil && who.Cookie != "" {
		req.AddCookie(&http.Cookie{Name: "recon_session", Value: who.Cookie})
		req.Header.Set("X-CSRF-Token", who.CSRF)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != 200 {
		resp.Body.Close()
		return nil, fmt.Errorf("HTTP %s %s status=%d (response withheld)", method, path, resp.StatusCode)
	}
	return resp, nil
}
func (c client) json(ctx context.Context, who *identity, method, path string, body, out any) error {
	resp, err := c.request(ctx, who, method, path, body)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 8*1024*1024))
	if err != nil {
		return err
	}
	if who != nil {
		for _, cookie := range resp.Cookies() {
			if cookie.Name == "recon_session" {
				who.Cookie = cookie.Value
				var grant auth.Grant
				if json.Unmarshal(raw, &grant) == nil {
					who.CSRF = grant.Session.CSRF
				}
			}
		}
	}
	if out != nil && json.Unmarshal(raw, out) != nil {
		return fmt.Errorf("invalid HTTP JSON (response withheld)")
	}
	return nil
}
func configure(ctx context.Context, c client, who *identity) error {
	for _, step := range []struct {
		path string
		body any
	}{{"/api/auth/setup/start", map[string]any{"id": who.ID}}, {"/api/auth/setup/password", map[string]any{"password": who.Password}}} {
		if err := c.json(ctx, who, "POST", step.path, step.body, nil); err != nil {
			return err
		}
	}
	var enrollment auth.Enrollment
	if err := c.json(ctx, who, "POST", "/api/auth/totp/enroll", map[string]any{}, &enrollment); err != nil {
		return err
	}
	who.TOTP = enrollment.Secret
	code, err := totp.GenerateCode(who.TOTP, time.Now())
	if err != nil {
		return fmt.Errorf("synthetic TOTP generation failed")
	}
	if err = c.json(ctx, who, "POST", "/api/auth/totp/confirm", map[string]any{"code": code}, nil); err != nil {
		return err
	}
	return c.json(ctx, who, "POST", "/api/auth/setup/complete", map[string]any{"saved": true}, nil)
}
func digest(path string) (string, error) {
	f, e := os.Open(path)
	if e != nil {
		return "", e
	}
	defer f.Close()
	h := sha256.New()
	_, e = io.Copy(h, f)
	return hex.EncodeToString(h.Sum(nil)), e
}
func backup(ctx context.Context, source *sql.DB, path string) error {
	conn, e := source.Conn(ctx)
	if e != nil {
		return e
	}
	defer conn.Close()
	return conn.Raw(func(raw any) error {
		check, ok := raw.(interface{ IsReadOnly(string) (bool, error) })
		if !ok {
			return fmt.Errorf("source read-only verification unavailable")
		}
		ro, e := check.IsReadOnly("main")
		if e != nil {
			return e
		}
		if !ro {
			return fmt.Errorf("source connection is not read-only")
		}
		provider, ok := raw.(interface {
			NewBackup(string) (*sqlite.Backup, error)
		})
		if !ok {
			return fmt.Errorf("SQLite online backup unavailable")
		}
		b, e := provider.NewBackup(filepath.ToSlash(path))
		if e != nil {
			return e
		}
		_, e = b.Step(-1)
		finish := b.Finish()
		if e != nil {
			return e
		}
		return finish
	})
}
func destination(source, path, output string) (string, string, string, error) {
	src, e := filepath.Abs(source)
	if e != nil {
		return "", "", "", e
	}
	src, e = filepath.EvalSymlinks(src)
	if e != nil {
		return "", "", "", e
	}
	dst, e := filepath.Abs(path)
	if e != nil {
		return "", "", "", e
	}
	out, e := filepath.Abs(output)
	if e != nil {
		return "", "", "", e
	}
	root, e := filepath.EvalSymlinks(filepath.Join(os.Getenv("LOCALAPPDATA"), "dutucijp-vibecoding-final", "verification"))
	if e != nil {
		return "", "", "", e
	}
	parent, e := filepath.EvalSymlinks(filepath.Dir(dst))
	if e != nil {
		return "", "", "", fmt.Errorf("destination parent must exist")
	}
	rel, e := filepath.Rel(root, parent)
	if e != nil {
		return "", "", "", e
	}
	if strings.Contains(rel, string(filepath.Separator)) || !strings.HasPrefix(strings.ToLower(rel), "account-") || len(rel) <= 8 {
		return "", "", "", fmt.Errorf("destination must be directly in verification/account-* directory")
	}
	dst = filepath.Join(parent, filepath.Base(dst))
	if strings.EqualFold(src, dst) {
		return "", "", "", fmt.Errorf("source and destination must differ")
	}
	if !strings.EqualFold(out, parent) {
		return "", "", "", fmt.Errorf("output must equal the new database's parent")
	}
	for _, suffix := range []string{"", "-wal", "-shm"} {
		if _, e = os.Stat(dst + suffix); !os.IsNotExist(e) {
			return "", "", "", fmt.Errorf("destination or companion already exists")
		}
	}
	return src, dst, out, nil
}
func clearCopiedAuthentication(ctx context.Context, path string) error {
	db, e := sql.Open("sqlite", filepath.ToSlash(path))
	if e != nil {
		return e
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	if _, e = db.ExecContext(ctx, "PRAGMA secure_delete=ON"); e != nil {
		return e
	}
	rows, e := db.QueryContext(ctx, "SELECT name FROM sqlite_master WHERE type='table' AND (substr(name,1,5)='auth_' OR name IN ('account_audit','warehouse_requests'))")
	if e != nil {
		return e
	}
	names := []string{}
	for rows.Next() {
		var name string
		if e = rows.Scan(&name); e != nil {
			rows.Close()
			return e
		}
		names = append(names, name)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return e
	}
	// This connection has foreign_keys disabled before any DDL. Only copied
	// authentication tables are dropped; their append-only triggers go with them.
	for _, name := range names {
		if _, e = db.ExecContext(ctx, `DROP TABLE "`+strings.ReplaceAll(name, `"`, `""`)+`"`); e != nil {
			return e
		}
	}
	_, e = db.ExecContext(ctx, "PRAGMA wal_checkpoint(TRUNCATE)")
	return e
}
func scalar(ctx context.Context, db *sql.DB, q string, args ...any) (int, error) {
	var n int
	e := db.QueryRowContext(ctx, q, args...).Scan(&n)
	return n, e
}

func verifyCountry(ctx context.Context, s *store.Store, c client, who *identity, run, dataset string, r *report) error {
	country := who.Country
	total, e := scalar(ctx, s.DB, "SELECT count(*) FROM normalized_rows WHERE dataset_id=? AND country=?", dataset, country)
	if e != nil {
		return e
	}
	caseTotal, e := scalar(ctx, s.DB, "SELECT count(*) FROM cases c JOIN normalized_rows n ON n.dataset_id=? AND n.id=c.cancel_id WHERE c.run_id=? AND n.country=?", dataset, run, country)
	if e != nil {
		return e
	}
	allocationTotal, e := scalar(ctx, s.DB, "SELECT count(*) FROM allocations a JOIN normalized_rows n ON n.dataset_id=? AND n.id=a.cancel_id WHERE a.run_id=? AND n.country=?", dataset, run, country)
	if e != nil {
		return e
	}
	seen := map[string]bool{}
	for _, page := range []int{1, 2, (total + 49) / 50} {
		path := fmt.Sprintf("/api/transactions?run_id=%s&page_size=50&page=%d", run, page)
		start := time.Now()
		var out store.TransactionPage
		if e = c.json(ctx, who, "GET", path, nil, &out); e != nil {
			return e
		}
		r.Measurements = append(r.Measurements, measurement{country, path, total, out.Total, float64(time.Since(start).Microseconds()) / 1000})
		if out.Total != total || len(out.Items) == 0 {
			return fmt.Errorf("country transaction total/page mismatch")
		}
		for _, item := range out.Items {
			if item.Country != country || seen[item.ID] {
				return fmt.Errorf("country transaction leakage or duplicate pages")
			}
			seen[item.ID] = true
		}
	}
	for _, endpoint := range []string{"cases", "allocations"} {
		path := "/api/" + endpoint + "?run_id=" + run + "&page_size=1"
		var out struct {
			Total int `json:"total"`
		}
		start := time.Now()
		if e = c.json(ctx, who, "GET", path, nil, &out); e != nil {
			return e
		}
		want := caseTotal
		if endpoint == "allocations" {
			want = allocationTotal
		}
		r.Measurements = append(r.Measurements, measurement{country, path, want, out.Total, float64(time.Since(start).Microseconds()) / 1000})
		if out.Total != want {
			return fmt.Errorf("country %s total mismatch", endpoint)
		}
	}
	var overview struct {
		Cases       map[string]int `json:"cases"`
		Unallocated int            `json:"currently_unallocated"`
		Initial     any            `json:"initial_reconciliation"`
	}
	if e = c.json(ctx, who, "GET", "/api/overview?run_id="+run, nil, &overview); e != nil {
		return e
	}
	if overview.Cases["total"] != caseTotal || overview.Initial != nil {
		return fmt.Errorf("country overview mismatch or global summary leakage")
	}
	unallocated, e := scalar(ctx, s.DB, "SELECT count(*) FROM decisions d JOIN cases c ON c.run_id=d.run_id AND c.cancel_id=d.cancel_id JOIN normalized_rows n ON n.dataset_id=? AND n.id=c.cancel_id LEFT JOIN allocations a ON a.run_id=d.run_id AND a.cancel_id=d.cancel_id WHERE d.run_id=? AND n.country=? AND a.cancel_id IS NULL", dataset, run, country)
	if e != nil {
		return e
	}
	if overview.Unallocated != unallocated {
		return fmt.Errorf("country unallocated overview mismatch")
	}
	want, e := scalar(ctx, s.DB, "SELECT count(*) FROM normalized_rows WHERE dataset_id=? AND country=? AND kind='cancel'", dataset, country)
	if e != nil {
		return e
	}
	path := "/api/exports?run_id=" + run + "&kind=cancel&page_size=1"
	start := time.Now()
	resp, e := c.request(ctx, who, "GET", path, nil)
	if e != nil {
		return e
	}
	defer resp.Body.Close()
	reader := csv.NewReader(resp.Body)
	header, e := reader.Read()
	if e != nil || len(header) != 21 {
		return fmt.Errorf("country CSV header invalid")
	}
	actual := 0
	for {
		row, e := reader.Read()
		if e == io.EOF {
			break
		}
		if e != nil {
			return fmt.Errorf("country CSV parse failed")
		}
		if len(row) != 21 || row[16] != country || row[17] != "cancel" {
			return fmt.Errorf("country CSV leaked or ignored filter")
		}
		actual++
	}
	r.Measurements = append(r.Measurements, measurement{country, path, want, actual, float64(time.Since(start).Microseconds()) / 1000})
	if actual != want {
		return fmt.Errorf("country CSV count mismatch")
	}
	return nil
}

func concurrency(ctx context.Context, c client, users []*identity, run string, expected []int, r *report) error {
	var wg sync.WaitGroup
	var mu sync.Mutex
	times := []float64{}
	r.Workers = 10
	for worker := 0; worker < 10; worker++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			idx := worker % len(users)
			who := users[idx]
			for i := 0; i < 30; i++ {
				path := fmt.Sprintf("/api/transactions?run_id=%s&page_size=50&page=%d", run, i%2+1)
				start := time.Now()
				var out store.TransactionPage
				err := c.json(ctx, who, "GET", path, nil, &out)
				valid := err == nil && out.Total == expected[idx] && len(out.Items) > 0 && len(out.Items) <= 50
				for _, row := range out.Items {
					if row.Country != who.Country {
						valid = false
					}
				}
				mu.Lock()
				r.Attempted++
				if valid {
					r.Succeeded++
				} else {
					r.Failed++
				}
				times = append(times, float64(time.Since(start).Microseconds())/1000)
				mu.Unlock()
			}
		}(worker)
	}
	wg.Wait()
	sort.Float64s(times)
	r.P95 = times[(len(times)*95+99)/100-1]
	r.Maximum = times[len(times)-1]
	if r.Failed != 0 || r.Attempted != 300 {
		return fmt.Errorf("concurrent country queries failed")
	}
	return nil
}

func execute(ctx context.Context, source, dst, out string, port int, r *report) (func(), error) {
	var e error
	source, dst, out, e = destination(source, dst, out)
	if e != nil {
		return nil, e
	}
	r.Source, r.Database = source, dst
	r.SourceBefore, e = digest(source)
	if e != nil {
		return nil, e
	}
	src, e := store.OpenReadOnlySource(source)
	if e != nil {
		return nil, e
	}
	defer src.Close()
	var dataset, scope, sourceHash string
	e = src.QueryRowContext(ctx, "SELECT r.id,r.dataset_id,d.scope,d.source_hash FROM runs r JOIN datasets d ON d.id=r.dataset_id WHERE r.status='published' AND d.status='published' AND d.scope='all' ORDER BY r.completed_at DESC LIMIT 1").Scan(&r.RunID, &dataset, &scope, &sourceHash)
	if e != nil {
		return nil, fmt.Errorf("source has no published all run")
	}
	if sourceHash != officialSource {
		return nil, fmt.Errorf("source official workbook hash mismatch")
	}
	fmt.Fprintln(os.Stderr, "phase=read_only_online_backup")
	if e = backup(ctx, src, dst); e != nil {
		return nil, e
	}
	if e = clearCopiedAuthentication(ctx, dst); e != nil {
		return nil, e
	}
	s, e := store.Open(dst)
	if e != nil {
		return nil, e
	}
	cleanup := func() { s.Close() }
	private := filepath.Join(out, "synthetic-secrets")
	svc, e := auth.New(s.DB, auth.Config{SecretDir: private, Origin: fmt.Sprintf("http://localhost:%d", port)})
	if e != nil {
		cleanup()
		return nil, e
	}
	admin := &identity{ID: "isolated-account-admin"}
	uk := &identity{ID: "isolated-account-uk", Country: "United Kingdom"}
	fr := &identity{ID: "isolated-account-france", Country: "France"}
	if _, e = svc.Initialize(ctx, []auth.Identity{{ID: admin.ID, Role: "admin"}, {ID: uk.ID, Role: "finance"}, {ID: fr.ID, Role: "finance"}}); e != nil {
		cleanup()
		return nil, e
	}
	for _, who := range []*identity{admin, uk, fr} {
		token, e := auth.RandomToken()
		if e != nil {
			cleanup()
			return nil, e
		}
		who.Password = "Isolated Amber River 73! " + token[:20]
	}
	listener, e := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if e != nil {
		cleanup()
		return nil, e
	}
	actualPort := listener.Addr().(*net.TCPAddr).Port
	if port == 0 {
		svc.Origin = fmt.Sprintf("http://localhost:%d", actualPort)
	}
	origin := fmt.Sprintf("http://localhost:%d", actualPort)
	handler := (&web.Server{Store: s, Auth: svc, Origin: origin}).Handler()
	server := &http.Server{Handler: handler, ReadHeaderTimeout: 10 * time.Second}
	go server.Serve(listener)
	cleanup = func() { server.Close(); s.Close() }
	c := client{base: fmt.Sprintf("http://127.0.0.1:%d", actualPort), origin: origin, http: &http.Client{Timeout: 2 * time.Minute}}
	if e = configure(ctx, c, admin); e != nil {
		cleanup()
		return nil, e
	}
	for _, who := range []*identity{uk, fr} {
		if e = c.json(ctx, admin, "PATCH", "/api/admin/accounts/"+who.ID, map[string]any{"display_name": "隔离验证 " + who.Country, "country_scope": "selected", "countries": []string{who.Country}}, nil); e != nil {
			cleanup()
			return nil, e
		}
		if e = configure(ctx, c, who); e != nil {
			cleanup()
			return nil, e
		}
	}
	r.Credentials = filepath.Join(private, "credentials.json")
	credentials, _ := json.MarshalIndent(map[string]any{"boundary": "Synthetic isolated verification only; no original account credentials reused", "origin": origin, "accounts": []*identity{admin, uk, fr}}, "", "  ")
	if e = auth.PrivateWrite(r.Credentials, credentials); e != nil {
		cleanup()
		return nil, e
	}
	fmt.Fprintln(os.Stderr, "phase=country_reference_checks")
	for _, who := range []*identity{uk, fr} {
		if e = verifyCountry(ctx, s, c, who, r.RunID, dataset, r); e != nil {
			cleanup()
			return nil, e
		}
	}
	expected := []int{}
	for _, who := range []*identity{uk, fr} {
		n, e := scalar(ctx, s.DB, "SELECT count(*) FROM normalized_rows WHERE dataset_id=? AND country=?", dataset, who.Country)
		if e != nil {
			cleanup()
			return nil, e
		}
		expected = append(expected, n)
	}
	fmt.Fprintln(os.Stderr, "phase=ten_workers_300_country_http_queries")
	if e = concurrency(ctx, c, []*identity{uk, fr}, r.RunID, expected, r); e != nil {
		cleanup()
		return nil, e
	}
	r.SourceAfter, e = digest(source)
	if e != nil {
		cleanup()
		return nil, e
	}
	r.SourceUnchanged = r.SourceBefore == r.SourceAfter
	if !r.SourceUnchanged {
		cleanup()
		return nil, fmt.Errorf("source database changed")
	}
	if port != 0 {
		r.URL = origin
	} else {
		cleanup()
		cleanup = nil
	}
	return cleanup, nil
}
func serveExisting(path, output string, port int) error {
	if port < 1 || port > 65535 {
		return fmt.Errorf("existing isolated server requires a valid explicit serve port")
	}
	dst, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	parent, err := filepath.EvalSymlinks(filepath.Dir(dst))
	if err != nil {
		return err
	}
	dst = filepath.Join(parent, filepath.Base(dst))
	out, err := filepath.Abs(output)
	if err != nil {
		return err
	}
	root, err := filepath.EvalSymlinks(filepath.Join(os.Getenv("LOCALAPPDATA"), "dutucijp-vibecoding-final", "verification"))
	if err != nil {
		return err
	}
	rel, err := filepath.Rel(root, parent)
	if err != nil {
		return err
	}
	if strings.Contains(rel, string(filepath.Separator)) || !strings.HasPrefix(strings.ToLower(rel), "account-") || !strings.EqualFold(out, parent) {
		return fmt.Errorf("existing server must remain in its verification/account-* output directory")
	}
	info, err := os.Lstat(dst)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("isolated database must be a regular file")
	}
	data, err := os.ReadFile(filepath.Join(parent, "report.json"))
	if err != nil {
		return err
	}
	var previous report
	if json.Unmarshal(data, &previous) != nil || !previous.Passed || !previous.SourceUnchanged || !strings.EqualFold(previous.Database, dst) || strings.EqualFold(previous.Source, dst) {
		return fmt.Errorf("existing database lacks its successful isolated verification marker")
	}
	s, err := store.Open(dst)
	if err != nil {
		return err
	}
	defer s.Close()
	origin := fmt.Sprintf("http://localhost:%d", port)
	svc, err := auth.New(s.DB, auth.Config{SecretDir: filepath.Join(parent, "synthetic-secrets"), Origin: origin})
	if err != nil {
		return err
	}
	// Preserve the same verified business copy, synthetic credentials and key.
	listener, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		return err
	}
	defer listener.Close()
	server := &http.Server{Handler: (&web.Server{Store: s, Auth: svc, Origin: origin}).Handler(), ReadHeaderTimeout: 10 * time.Second}
	fmt.Printf("isolated_existing_server=%s\n", origin)
	return server.Serve(listener)
}

// inventory emits counts and one-way fingerprints only; no credential material
// or business rows are printed. Used to compare an existing Demo across update.
func inventory(source string) (map[string]any, error) {
	db, e := store.OpenReadOnlySource(source)
	if e != nil {
		return nil, e
	}
	defer db.Close()
	ctx := context.Background()
	counts := map[string]int{}
	fingerprints := map[string]string{}
	for _, table := range []string{"source_rows", "normalized_rows", "datasets", "runs", "cases", "allocations", "sale_balances", "audit_events", "auth_users", "auth_credentials", "auth_recovery"} {
		var count int
		if e = db.QueryRowContext(ctx, "SELECT count(*) FROM "+table).Scan(&count); e != nil {
			return nil, e
		}
		counts[table] = count
		if table == "source_rows" || table == "normalized_rows" || table == "sale_balances" {
			continue
		}
		rows, e := db.QueryContext(ctx, "SELECT * FROM "+table+" ORDER BY 1")
		if e != nil {
			return nil, e
		}
		cols, e := rows.Columns()
		if e != nil {
			rows.Close()
			return nil, e
		}
		h := sha256.New()
		encoder := json.NewEncoder(h)
		for rows.Next() {
			values := make([]any, len(cols))
			pointers := make([]any, len(cols))
			for i := range values {
				pointers[i] = &values[i]
			}
			if e = rows.Scan(pointers...); e != nil {
				rows.Close()
				return nil, e
			}
			if e = encoder.Encode(values); e != nil {
				rows.Close()
				return nil, e
			}
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return nil, e
		}
		fingerprints[table] = hex.EncodeToString(h.Sum(nil))
	}
	return map[string]any{"counts": counts, "fingerprints": fingerprints}, nil
}

func main() {
	snapshotSource := flag.String("snapshot-source", "", "read-only aggregate Demo inventory, without exposing rows or secrets")
	source := flag.String("source", "", "official all database opened read-only")
	dst := flag.String("db", "", "new database in verification/account-*")
	output := flag.String("output", "", "same new verification directory")
	port := flag.Int("serve-port", 0, "optional localhost UI server after verification")
	existing := flag.Bool("serve-existing", false, "serve an already verified isolated copy without another backup or reset")
	flag.Parse()
	if *snapshotSource != "" {
		v, e := inventory(*snapshotSource)
		if e != nil {
			fmt.Fprintln(os.Stderr, "read-only Demo inventory failed")
			os.Exit(1)
		}
		json.NewEncoder(os.Stdout).Encode(v)
		return
	}
	r := report{ActualTime: time.Now().Format(time.RFC3339), Boundary: "FR-19 official two-year all data, fresh online backup, synthetic real same-database accounts. Country reference SQL, HTTP pagination/CSV/overview/allocation counts, ten workers and 300 country-scoped GETs. No background job: not formal NFR04, NFR02/03 or human acceptance.", Measurements: []measurement{}}
	start := time.Now()
	var cleanup func()
	var err error
	day := time.Now().In(time.FixedZone("Asia/Shanghai", 8*3600)).Format("01-02")
	if *existing {
		if *source != "" || *dst == "" || *output == "" || flag.NArg() != 0 {
			fmt.Fprintln(os.Stderr, "existing serving requires db, output and port only")
			os.Exit(1)
		}
		if err = serveExisting(*dst, *output, *port); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	if *source == "" || *dst == "" || *output == "" || flag.NArg() != 0 || *port < 0 || *port > 65535 {
		err = fmt.Errorf("explicit source, fresh db and output required with a valid serve port")
	} else if day == "10-22" || day == "11-17" {
		err = fmt.Errorf("reserved day: no verification")
	} else {
		cleanup, err = execute(context.Background(), *source, *dst, *output, *port, &r)
	}
	r.TotalSeconds = time.Since(start).Seconds()
	r.Passed = err == nil && r.SourceUnchanged
	if err != nil {
		r.Failure = err.Error()
	}
	data, _ := json.MarshalIndent(r, "", "  ")
	if r.Database != "" {
		_ = os.WriteFile(filepath.Join(filepath.Dir(r.Database), "report.json"), data, 0600)
	}
	fmt.Println(string(data))
	if !r.Passed {
		os.Exit(1)
	}
	if cleanup != nil {
		defer cleanup()
		select {}
	}
}
