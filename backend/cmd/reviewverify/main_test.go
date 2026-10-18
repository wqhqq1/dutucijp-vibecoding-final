package main

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"
)

func TestSourceFileURIIsActuallyReadOnlyAndPreservesWAL(t *testing.T) {
	path := filepath.Join(t.TempDir(), "read only # source.sqlite")
	writer, e := sql.Open("sqlite", filepath.ToSlash(path))
	if e != nil {
		t.Fatal(e)
	}
	defer writer.Close()
	writer.SetMaxOpenConns(1)
	if _, e = writer.Exec("PRAGMA journal_mode=WAL; PRAGMA wal_autocheckpoint=0; CREATE TABLE original(value INTEGER); INSERT INTO original VALUES(17)"); e != nil {
		t.Fatal(e)
	}
	beforeMain, e := digest(path)
	if e != nil {
		t.Fatal(e)
	}
	beforeWAL, e := digest(path + "-wal")
	if e != nil {
		t.Fatal(e)
	}
	reader, e := openReadOnlySource(path)
	if e != nil {
		t.Fatal(e)
	}
	var value int
	if e = reader.QueryRow("SELECT value FROM original").Scan(&value); e != nil || value != 17 {
		t.Fatalf("read-only WAL snapshot: value=%d error=%v", value, e)
	}
	if _, e = reader.Exec("INSERT INTO original VALUES(23)"); e == nil {
		t.Fatal("source connection accepted SQL write")
	}
	conn, e := reader.Conn(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	e = conn.Raw(func(raw any) error {
		checker, ok := raw.(interface{ IsReadOnly(string) (bool, error) })
		if !ok {
			t.Fatal("driver read-only inspection unavailable")
		}
		ro, err := checker.IsReadOnly("main")
		if err != nil {
			t.Fatal(err)
		}
		if !ro {
			t.Fatal("query_only masqueraded as an actual read-only file connection")
		}
		return nil
	})
	conn.Close()
	if e != nil {
		t.Fatal(e)
	}
	if e = reader.Close(); e != nil {
		t.Fatal(e)
	}
	afterMain, e := digest(path)
	if e != nil {
		t.Fatal(e)
	}
	afterWAL, e := digest(path + "-wal")
	if e != nil {
		t.Fatal(e)
	}
	if beforeMain != afterMain || beforeWAL != afterWAL {
		t.Fatal("read-only source open/query/close changed main database or WAL bytes")
	}
	if e = writer.QueryRow("SELECT sum(value) FROM original").Scan(&value); e != nil || value != 17 {
		t.Fatal("read-only query changed logical source data")
	}
}

func TestReadOnlySourceCannotCreateMissingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing.sqlite")
	reader, e := openReadOnlySource(path)
	if reader != nil {
		reader.Close()
	}
	if e == nil {
		t.Fatal("mode=ro opened missing source")
	}
	if _, e = os.Stat(path); !os.IsNotExist(e) {
		t.Fatal("read-only source attempt created database")
	}
}
