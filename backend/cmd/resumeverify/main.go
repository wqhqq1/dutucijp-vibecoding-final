// resumeverify checks actual cancellation and recovery in a new verification
// database. It has no business account, session, password or authentication bypass.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"returnsrecon/internal/store"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	prepared := flag.String("prepared", "", "existing verified official prepared input")
	output := flag.String("output-dir", "", "new direct child of the runtime verification directory")
	flag.Parse()
	day := time.Now().In(time.FixedZone("Asia/Shanghai", 8*3600)).Format("01-02")
	if day == "10-22" || day == "11-17" {
		return fmt.Errorf("reserved day: no interruption or recovery verification")
	}
	if *prepared == "" || *output == "" {
		return fmt.Errorf("explicit --prepared and --output-dir required")
	}
	root, err := filepath.Abs(*output)
	if err != nil {
		return err
	}
	parent, err := os.Stat(filepath.Dir(root))
	if err != nil {
		return err
	}
	allowed, err := os.Stat(filepath.Join(os.Getenv("LOCALAPPDATA"), "dutucijp-vibecoding-final", "verification"))
	if err != nil || !os.SameFile(parent, allowed) {
		return fmt.Errorf("new output must be a direct runtime/verification child")
	}
	if _, err = os.Stat(root); !os.IsNotExist(err) {
		return fmt.Errorf("refusing existing verification directory")
	}
	if err = os.Mkdir(root, 0700); err != nil {
		return err
	}
	path := filepath.Join(root, "verification.sqlite")
	s, err := store.Open(path)
	if err != nil {
		return err
	}
	defer s.Close()
	m, err := s.Import(context.Background(), *prepared)
	if err != nil {
		return err
	}
	if m.Scope != "benchmark" || m.Stats.Raw != 10000 || m.SourceHash != "bcbe73b35f5b7babf197fb0cb983a11f5d9ff929078d4aa53d171b1f2df2e980" {
		return fmt.Errorf("requires official 10000-row benchmark; not a performance measurement")
	}
	ctx, cancel := context.WithCancel(context.Background())
	observed := make(chan bool, 1)
	done := make(chan struct{})
	go func() {
		ticker := time.NewTicker(500 * time.Microsecond)
		defer ticker.Stop()
		deadline := time.NewTimer(10 * time.Second)
		defer deadline.Stop()
		for {
			var n int
			if e := s.DB.QueryRow("SELECT count(*) FROM runs WHERE status='staging'").Scan(&n); e == nil && n == 1 {
				cancel()
				observed <- true
				return
			}
			select {
			case <-done:
				observed <- false
				return
			case <-deadline.C:
				observed <- false
				return
			case <-ticker.C:
			}
		}
	}()
	_, interrupted := s.Run(ctx, m.DatasetID, 1)
	close(done)
	seen := <-observed
	cancel()
	var failed, published int
	if err = s.DB.QueryRow("SELECT coalesce(sum(status='failed'),0),coalesce(sum(status='published'),0) FROM runs").Scan(&failed, &published); err != nil {
		return err
	}
	if !seen || !errors.Is(interrupted, context.Canceled) || failed != 1 || published != 0 {
		return fmt.Errorf("actual cancellation prerequisite failed: staging_observed=%t canceled=%t failed=%d published=%d", seen, errors.Is(interrupted, context.Canceled), failed, published)
	}
	if err = s.Close(); err != nil {
		return err
	}
	s, err = store.Open(path)
	if err != nil {
		return err
	}
	defer s.Close()
	result, err := s.Run(context.Background(), m.DatasetID, 1)
	if err != nil {
		return err
	}
	if result.Reused || result.Status != "published" || result.Matched+result.Suspected+result.NoOriginal != m.Stats.AcceptedCancels {
		return fmt.Errorf("reopened recovery was not a fresh conserved published run")
	}
	encoded, _ := json.MarshalIndent(map[string]any{
		"actual_time": time.Now().Format(time.RFC3339Nano), "database": path,
		"scope": m.Scope, "input_rows": m.Stats.Raw, "source_hash": m.SourceHash,
		"staging_observed_before_cancel": seen, "actual_context_canceled": true,
		"failed_runs_before_reopen": failed, "published_before_reopen": published,
		"reopened_run": result, "notice": "Official benchmark in a new isolated DB. Actual cancellation, close/reopen and fresh recomputation. Not a performance result, browser workflow or human action.",
	}, "", "  ")
	if err = os.WriteFile(filepath.Join(root, "summary.json"), append(encoded, '\n'), 0600); err != nil {
		return err
	}
	fmt.Println(string(encoded))
	return nil
}
