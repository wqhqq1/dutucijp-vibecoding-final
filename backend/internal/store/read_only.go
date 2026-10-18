package store

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"path/filepath"
	"strings"
)

// OpenReadOnlySource opens an existing diagnostic input without migrations or
// checkpoint writes. SQLite only honors mode=ro for a file: URI; query_only by
// itself does not make an ordinary file-path connection physically read-only.
func OpenReadOnlySource(path string) (*sql.DB, error) {
	absolute, e := filepath.Abs(path)
	if e != nil {
		return nil, e
	}
	uriPath := filepath.ToSlash(absolute)
	if !strings.HasPrefix(uriPath, "/") {
		uriPath = "/" + uriPath
	}
	uri := (&url.URL{Scheme: "file", Path: uriPath, RawQuery: "mode=ro&_pragma=query_only(1)"}).String()
	db, e := sql.Open("sqlite", uri)
	if e != nil {
		return nil, e
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	conn, e := db.Conn(context.Background())
	if e != nil {
		db.Close()
		return nil, e
	}
	e = conn.Raw(func(raw any) error {
		checker, ok := raw.(interface{ IsReadOnly(string) (bool, error) })
		if !ok {
			return fmt.Errorf("SQLite read-only verification unavailable")
		}
		ro, e := checker.IsReadOnly("main")
		if e != nil {
			return e
		}
		if !ro {
			return fmt.Errorf("source must be file-level read-only")
		}
		return nil
	})
	conn.Close()
	if e != nil {
		db.Close()
		return nil, e
	}
	return db, nil
}
