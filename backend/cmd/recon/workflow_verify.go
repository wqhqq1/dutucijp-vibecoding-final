package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func claimWorkflowVerification(dbPath, runID string) error {
	day := time.Now().In(time.FixedZone("Asia/Shanghai", 8*60*60)).Format("01-02")
	if day == "10-22" || day == "11-17" {
		return fmt.Errorf("reserved day: no workflow verification or rebuilding")
	}
	root := filepath.Join(os.Getenv("LOCALAPPDATA"), "dutucijp-vibecoding-final", "verification")
	root, err := filepath.EvalSymlinks(root)
	if err != nil {
		return fmt.Errorf("workflow verification root unavailable: %w", err)
	}
	canonical, err := filepath.EvalSymlinks(dbPath)
	if err != nil {
		return fmt.Errorf("workflow verification requires an existing isolated copy: %w", err)
	}
	canonical, err = filepath.Abs(canonical)
	if err != nil {
		return err
	}
	rootInfo, err := os.Stat(root)
	if err != nil {
		return err
	}
	parentInfo, err := os.Stat(filepath.Dir(filepath.Dir(canonical)))
	if err != nil {
		return err
	}
	// Directory identity also handles Windows app-container path virtualization.
	if !os.SameFile(rootInfo, parentInfo) || !strings.HasPrefix(filepath.Base(filepath.Dir(canonical)), "workflow-") || filepath.Base(canonical) != "workflow.sqlite" {
		return fmt.Errorf("verify-workflow only accepts a fresh verification/workflow-*/workflow.sqlite copy")
	}
	marker := filepath.Join(filepath.Dir(canonical), "workflow-verification.json")
	data, err := os.ReadFile(marker)
	if err != nil {
		return fmt.Errorf("missing one-use isolated workflow marker; run verify-workflow.ps1: %w", err)
	}
	var m struct {
		Purpose  string `json:"purpose"`
		Database string `json:"database"`
		RunID    string `json:"run_id"`
	}
	if err = json.Unmarshal(data, &m); err != nil || m.Purpose != "isolated-workflow" || m.Database != "workflow.sqlite" || m.RunID != runID {
		return fmt.Errorf("isolated workflow marker does not match database/run")
	}
	// Exclusive claim before any Store.Open migration or workflow write. The
	// marker is an accident-prevention guard, not a substitute for FR-14 auth.
	claim, err := os.OpenFile(marker+".claimed", os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return fmt.Errorf("workflow copy already claimed; create a fresh copy: %w", err)
	}
	return claim.Close()
}
