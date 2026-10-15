package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWorkflowVerifyRejectsDefaultOrBusinessDatabaseBeforeOpening(t *testing.T) {
	previous := os.Args
	defer func() { os.Args = previous }()
	root := t.TempDir()
	t.Setenv("LOCALAPPDATA", root)
	os.Args = []string{"recon", "verify-workflow", "--run", "test-run"}
	if err := run(); err == nil || !strings.Contains(err.Error(), "explicit --db") {
		t.Fatalf("missing isolated db: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "dutucijp-vibecoding-final")); !os.IsNotExist(err) {
		t.Fatal("verify-workflow touched default business state")
	}
	verifyRoot := filepath.Join(root, "dutucijp-vibecoding-final", "verification")
	if err := os.MkdirAll(verifyRoot, 0700); err != nil {
		t.Fatal(err)
	}
	protected := filepath.Join(root, "business.sqlite")
	if err := os.WriteFile(protected, []byte("test sentinel, not a SQLite database"), 0600); err != nil {
		t.Fatal(err)
	}
	os.Args = []string{"recon", "verify-workflow", "--db", protected, "--run", "test-run"}
	if err := run(); err == nil || !strings.Contains(err.Error(), "fresh verification/") {
		t.Fatalf("business path accepted: %v", err)
	}
	data, _ := os.ReadFile(protected)
	if string(data) != "test sentinel, not a SQLite database" {
		t.Fatal("business file was opened/modified")
	}
}

func TestWorkflowVerificationMarkerBoundAndClaimedOnce(t *testing.T) {
	root := t.TempDir()
	t.Setenv("LOCALAPPDATA", root)
	dir := filepath.Join(root, "dutucijp-vibecoding-final", "verification", "workflow-test")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	db := filepath.Join(dir, "workflow.sqlite")
	if err := os.WriteFile(db, []byte("isolated test sentinel"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := claimWorkflowVerification(db, "run-a"); err == nil {
		t.Fatal("missing marker accepted")
	}
	marker, _ := json.Marshal(map[string]string{"purpose": "isolated-workflow", "database": "workflow.sqlite", "run_id": "run-a"})
	if err := os.WriteFile(filepath.Join(dir, "workflow-verification.json"), marker, 0600); err != nil {
		t.Fatal(err)
	}
	if err := claimWorkflowVerification(db, "run-b"); err == nil {
		t.Fatal("wrong run accepted")
	}
	if err := claimWorkflowVerification(db, "run-a"); err != nil {
		t.Fatal(err)
	}
	if err := claimWorkflowVerification(db, "run-a"); err == nil {
		t.Fatal("marker used twice")
	}
}

func TestUnsupportedBusinessCommandRejectedBeforeOpening(t *testing.T) {
	previous := os.Args
	defer func() { os.Args = previous }()
	root := t.TempDir()
	t.Setenv("LOCALAPPDATA", root)
	os.Args = []string{"recon", "assign"}
	if err := run(); err == nil || !strings.Contains(err.Error(), "unsupported command") {
		t.Fatalf("%v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "dutucijp-vibecoding-final")); !os.IsNotExist(err) {
		t.Fatal("unsupported command opened store")
	}
}

func TestVerifyRequiresExplicitDatabaseBeforeOpeningStore(t *testing.T) {
	previous := os.Args
	defer func() { os.Args = previous }()
	root := t.TempDir()
	t.Setenv("LOCALAPPDATA", root)
	os.Args = []string{"recon", "verify", "--prepared", "test-only-missing-dir"}
	if err := run(); err == nil || !strings.Contains(err.Error(), "explicit --db") {
		t.Fatalf("missing isolated DB not rejected: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "dutucijp-vibecoding-final")); !os.IsNotExist(err) {
		t.Fatal("verify touched default runtime before validation")
	}
}
