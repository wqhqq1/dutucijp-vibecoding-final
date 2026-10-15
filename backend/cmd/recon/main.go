package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"returnsrecon/internal/store"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run() error {
	if len(os.Args) < 2 {
		return fmt.Errorf("usage: recon run --prepared DIR [--db PATH] | verify --prepared DIR --db ISOLATED_PATH [--row ID] | verify-workflow --db ISOLATED_COPY --run ID | evidence --run ID --cancel ROW | examples --run ID")
	}
	command := os.Args[1]
	if command == "serve" || command == "init-auth" {
		return serveCommand(command, os.Args[2:])
	}
	switch command {
	case "run", "verify", "verify-workflow", "evidence", "examples":
	default:
		return fmt.Errorf("unsupported command: %s", command)
	}
	flags := flag.NewFlagSet(command, flag.ContinueOnError)
	dbPath := flags.String("db", filepath.Join(os.Getenv("LOCALAPPDATA"), "dutucijp-vibecoding-final", "state", "business.sqlite"), "SQLite file outside source tree")
	prepared := flags.String("prepared", "", "published preparation directory")
	runID := flags.String("run", "", "published run ID")
	cancel := flags.String("cancel", "", "source row ID")
	rowID := flags.String("row", "", "excluded source row ID for isolated verification")
	workers := flags.Int("workers", 0, "bounded compute workers (1..8)")
	if err := flags.Parse(os.Args[2:]); err != nil {
		return err
	}
	if command == "verify" || command == "verify-workflow" {
		explicitDB := false
		flags.Visit(func(f *flag.Flag) {
			if f.Name == "db" {
				explicitDB = true
			}
		})
		if !explicitDB || *dbPath == "" {
			return fmt.Errorf("%s requires an explicit --db for isolated verification", command)
		}
		if command == "verify" && *prepared == "" {
			return fmt.Errorf("verify requires --prepared and an explicit --db for isolated verification")
		}
		if command == "verify-workflow" {
			if *runID == "" {
				return fmt.Errorf("verify-workflow requires --run")
			}
			if err := claimWorkflowVerification(*dbPath, *runID); err != nil {
				return err
			}
		}
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	s, err := store.Open(*dbPath)
	if err != nil {
		return err
	}
	defer s.Close()
	var out any
	switch command {
	case "verify-workflow":
		out, err = s.VerifyWorkflow(ctx, *runID)
	case "verify":
		m, importErr := s.Import(ctx, *prepared)
		if importErr != nil {
			return importErr
		}
		out, err = s.VerifyDataset(ctx, m.DatasetID, *rowID)
	case "run":
		if *prepared == "" {
			return fmt.Errorf("--prepared is required; first run tools/prepare.py")
		}
		m, err := s.Import(ctx, *prepared)
		if err != nil {
			return err
		}
		out, err = s.Run(ctx, m.DatasetID, *workers)
		if err != nil {
			return err
		}
	case "evidence":
		out, err = s.Evidence(ctx, *runID, *cancel)
	case "examples":
		out, err = s.Examples(ctx, *runID)
	default:
		return fmt.Errorf("unsupported command: %s", command)
	}
	if err != nil && out == nil {
		return err
	}
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	encoder.SetEscapeHTML(false)
	if encodeErr := encoder.Encode(out); encodeErr != nil {
		return encodeErr
	}
	return err
}
