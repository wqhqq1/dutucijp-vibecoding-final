package main

import (
	"context"
	"path/filepath"
	"reflect"
	"testing"

	"returnsrecon/internal/store"
)

// The retained baseline must reproduce complete records, numeric source order,
// date normalization and page/count semantics, rather than timing an ID-only
// shortcut that would understate the original endpoint's cost.
func TestRetainedBaselineMatchesProductionResults(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "queries.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	for _, statement := range []string{
		`INSERT INTO datasets VALUES('d','all','hash','cr-1.0.1','records','{"stats":{"raw":3}}','published')`,
		`INSERT INTO runs(id,dataset_id,rule_version,status,started_at) VALUES('r','d','rule','published','now')`,
		`INSERT INTO source_rows VALUES('s2','hash','Year 1',0,2,'{}'),('s10','hash','Year 1',0,10,'{}'),('s12','hash','Year 2',1,2,'{}')`,
		`INSERT INTO normalized_rows VALUES('d','0:2','s2','i','A','name',2,'2010-01-01T08:00:00.000000','1','42','UK','sale','','',''),('d','0:10','s10','j','B','other',3,'2010-01-01T09:00:00.000000','1','43','France','sale','','',''),('d','1:2','s12','k','A','name',-1,'2010-01-02T08:00:00.000000','1','42','UK','cancel','','','')`,
	} {
		if _, err = s.DB.ExecContext(ctx, statement); err != nil {
			t.Fatal(err)
		}
	}
	if err = s.EnsureQueryIndexes(ctx); err != nil {
		t.Fatal(err)
	}
	for _, f := range []store.QueryFilter{{}, {Page: 2, PageSize: 1}, {Customer: "42", Stock: "A"}, {Invoice: "j"}, {Kind: "sale"}, {DateFrom: "2010-01-01", DateTo: "2010-01-02"}, {Customer: "43", DateFrom: "2009-01-01", DateTo: "2011-01-01"}} {
		f.RunID = "r"
		old, err := baselineTransactions(ctx, s, f)
		if err != nil {
			t.Fatal(err)
		}
		current, err := s.ListTransactions(ctx, store.WorkflowActor{ID: "isolated", Role: "finance"}, f)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(old, current) {
			t.Fatalf("baseline differs: filter=%+v old=%+v current=%+v", f, old, current)
		}
		if err = verifyPage(ctx, s, f, current); err != nil {
			t.Fatal(err)
		}
	}
}
