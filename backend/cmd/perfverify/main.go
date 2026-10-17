// perfverify measures the production import and reconciliation functions against
// an explicit, new isolated SQLite database. It never opens the default state.
package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"runtime/pprof"
	"time"

	"returnsrecon/internal/store"
)

type report struct {
	Mode            string              `json:"mode"`
	Database        string              `json:"database"`
	Prepared        string              `json:"prepared"`
	InputTemplate   string              `json:"input_template,omitempty"`
	InputCopySHA256 string              `json:"input_copy_sha256,omitempty"`
	InputCopySecs   float64             `json:"input_copy_seconds"`
	OpenSeconds     float64             `json:"open_seconds"`
	ImportSeconds   float64             `json:"import_seconds"`
	RunSeconds      float64             `json:"run_wall_seconds"`
	CloseSeconds    float64             `json:"close_seconds"`
	TotalSeconds    float64             `json:"total_go_seconds"`
	MeasuredSeconds float64             `json:"measured_seconds"`
	RunsBefore      int                 `json:"runs_before"`
	RunsAfter       int                 `json:"published_runs_after"`
	CasesAfter      int                 `json:"cases_after"`
	DecisionsAfter  int                 `json:"decisions_after"`
	SQLiteVersion   string              `json:"sqlite_version"`
	SQLiteCacheSize int                 `json:"sqlite_cache_size"`
	SQLiteMmapSize  int64               `json:"sqlite_mmap_size"`
	SQLiteSync      int                 `json:"sqlite_synchronous"`
	QueryIndexes    int                 `json:"query_indexes_present"`
	ParameterProbe  int                 `json:"bound_parameter_probe"`
	GoVersion       string              `json:"go_version"`
	Manifest        store.Manifest      `json:"manifest"`
	Summary         store.Summary       `json:"summary"`
	Phases          []store.PhaseTiming `json:"phases,omitempty"`
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	flags := flag.NewFlagSet("perfverify", flag.ContinueOnError)
	prepared := flags.String("prepared", "", "published prepared directory")
	database := flags.String("db", "", "required new isolated database path")
	mode := flags.String("mode", "full", "full or recompute; recompute excludes initial input import")
	inputTemplate := flags.String("input-template", "", "optional closed input-only SQLite template for recompute, copied into a new isolated database")
	workers := flags.Int("workers", 0, "production bounded compute workers")
	cpuProfile := flags.String("cpu-profile", "", "optional diagnostic CPU profile (never an acceptance baseline)")
	if err := flags.Parse(os.Args[1:]); err != nil {
		return err
	}
	zone := time.FixedZone("Asia/Shanghai", 8*60*60)
	day := time.Now().In(zone).Format("01-02")
	if day == "10-22" || day == "11-17" {
		return fmt.Errorf("reserved day in Asia/Shanghai: no verification work")
	}
	if *prepared == "" || *database == "" || (*mode != "full" && *mode != "recompute") {
		return fmt.Errorf("explicit --prepared, --db and full/recompute --mode required")
	}
	if *inputTemplate != "" && *mode != "recompute" {
		return fmt.Errorf("--input-template is only valid for isolated recomputation")
	}
	abs, err := filepath.Abs(*database)
	if err != nil {
		return err
	}
	for _, suffix := range []string{"", "-wal", "-shm"} {
		if _, err = os.Stat(abs + suffix); !os.IsNotExist(err) {
			return fmt.Errorf("refusing existing database or companion: %s", abs+suffix)
		}
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	out := report{Mode: *mode, Database: abs, Prepared: *prepared, GoVersion: runtime.Version()}
	ctx = store.WithPhaseObserver(ctx, func(phase store.PhaseTiming) { out.Phases = append(out.Phases, phase) })
	if *inputTemplate != "" {
		copyStarted := time.Now()
		for _, suffix := range []string{"-wal", "-shm"} {
			if _, err = os.Stat(*inputTemplate + suffix); !os.IsNotExist(err) {
				return fmt.Errorf("input template must be closed with no WAL/SHM companion")
			}
		}
		if err = os.MkdirAll(filepath.Dir(abs), 0700); err != nil {
			return err
		}
		source, openErr := os.Open(*inputTemplate)
		if openErr != nil {
			return openErr
		}
		destination, createErr := os.OpenFile(abs, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if createErr != nil {
			source.Close()
			return createErr
		}
		digest := sha256.New()
		_, copyErr := io.Copy(io.MultiWriter(destination, digest), source)
		closeErr := destination.Close()
		source.Close()
		if copyErr != nil {
			return copyErr
		}
		if closeErr != nil {
			return closeErr
		}
		out.InputTemplate = *inputTemplate
		out.InputCopySHA256 = hex.EncodeToString(digest.Sum(nil))
		out.InputCopySecs = time.Since(copyStarted).Seconds()
	}
	totalStart := time.Now()
	started := time.Now()
	s, err := store.Open(abs)
	if err != nil {
		return err
	}
	defer s.Close()
	if err = s.EnsureQueryIndexes(ctx); err != nil {
		return err
	}
	out.OpenSeconds = time.Since(started).Seconds()
	started = time.Now()
	out.Manifest, err = s.Import(ctx, *prepared)
	if err != nil {
		return err
	}
	out.ImportSeconds = time.Since(started).Seconds()
	if err = s.DB.QueryRowContext(ctx, "SELECT count(*) FROM runs").Scan(&out.RunsBefore); err != nil {
		return err
	}
	if out.RunsBefore != 0 {
		return fmt.Errorf("isolated input must have zero prior runs")
	}
	if *inputTemplate != "" {
		for _, table := range []string{"decisions", "allocations", "cases", "sale_balances", "candidate_evidence", "audit_events"} {
			var count int
			if err = s.DB.QueryRowContext(ctx, "SELECT count(*) FROM "+table).Scan(&count); err != nil {
				return err
			}
			if count != 0 {
				return fmt.Errorf("input template contains prior computed or human output in %s", table)
			}
		}
	}
	if *cpuProfile != "" {
		file, createErr := os.OpenFile(*cpuProfile, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if createErr != nil {
			return createErr
		}
		defer file.Close()
		if err = pprof.StartCPUProfile(file); err != nil {
			return err
		}
		defer pprof.StopCPUProfile()
	}
	started = time.Now()
	out.Summary, err = s.Run(ctx, out.Manifest.DatasetID, *workers)
	out.RunSeconds = time.Since(started).Seconds()
	if err != nil {
		return err
	}
	if out.Summary.Reused || out.Summary.Status != "published" {
		return fmt.Errorf("reconciliation was reused or unpublished")
	}
	started = time.Now()
	if err = s.Close(); err != nil {
		return err
	}
	out.CloseSeconds = time.Since(started).Seconds()
	out.TotalSeconds = time.Since(totalStart).Seconds()
	out.MeasuredSeconds = out.TotalSeconds
	if *mode == "recompute" {
		out.MeasuredSeconds = out.RunSeconds + out.CloseSeconds
	}
	// Reopen after timing to independently check durable publication and counts.
	s, err = store.Open(abs)
	if err != nil {
		return err
	}
	defer s.Close()
	checks := []struct {
		query string
		dst   any
	}{
		{"SELECT sqlite_version()", &out.SQLiteVersion},
		{"PRAGMA cache_size", &out.SQLiteCacheSize},
		{"PRAGMA mmap_size", &out.SQLiteMmapSize},
		{"PRAGMA synchronous", &out.SQLiteSync},
		{"SELECT count(*) FROM sqlite_master WHERE type='index' AND name IN ('cases_owner','row_invoice','row_source_time','row_stock')", &out.QueryIndexes},
		{"SELECT count(*) FROM runs WHERE status='published'", &out.RunsAfter},
		{"SELECT count(*) FROM cases", &out.CasesAfter},
		{"SELECT count(*) FROM decisions", &out.DecisionsAfter},
	}
	for _, c := range checks {
		if err = s.DB.QueryRowContext(ctx, c.query).Scan(c.dst); err != nil {
			return err
		}
	}
	if out.RunsAfter != 1 || out.CasesAfter != out.Summary.Cases || out.DecisionsAfter != out.Manifest.Stats.AcceptedCancels || out.QueryIndexes != 4 {
		return fmt.Errorf("durable result conservation failed")
	}
	arguments := make([]any, 960)
	arguments[959] = 960
	if err = s.DB.QueryRowContext(ctx, "SELECT ?960", arguments...).Scan(&out.ParameterProbe); err != nil || out.ParameterProbe != 960 {
		return fmt.Errorf("required 960-parameter budget unavailable: %w", err)
	}
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	return encoder.Encode(out)
}
