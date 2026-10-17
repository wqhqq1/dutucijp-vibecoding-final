package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPortablePortValidationPrecedesDatabaseCreation(t *testing.T) {
	for _, port := range []string{"0", "1023", "65536"} {
		t.Run(port, func(t *testing.T) {
			db := filepath.Join(t.TempDir(), "尚未创建", "business.sqlite")
			if err := serveCommand("serve", []string{"--db", db, "--port", port}); err == nil || !strings.Contains(err.Error(), "port must be") {
				t.Fatalf("invalid port accepted: %v", err)
			}
			if _, err := os.Stat(filepath.Dir(db)); !os.IsNotExist(err) {
				t.Fatal("invalid port touched runtime state")
			}
		})
	}
}
