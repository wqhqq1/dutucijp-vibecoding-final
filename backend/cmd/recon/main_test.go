package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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
