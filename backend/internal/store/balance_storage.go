package store

import (
	"context"
	"database/sql"
	"strings"
)

// Empty legacy input databases can adopt the clustered composite primary key.
// This is deliberately called by Run, never Open: its DDL and FULL commit are
// part of recomputation. Published history, custom DDL, indexes and triggers
// leave the legacy layout intact. No existing balance is copied or discarded.
func (s *Store) ensureBalanceStorage(ctx context.Context) error {
	const prefix = "CREATE TABLE IF NOT EXISTS sale_balances ("
	_, rest, _ := strings.Cut(schema, prefix)
	body, _, _ := strings.Cut(rest, ";")
	definition := prefix + body
	normalize := func(value string) string {
		return strings.ReplaceAll(strings.Join(strings.Fields(strings.TrimSuffix(value, ";")), " "), "CREATE TABLE IF NOT EXISTS", "CREATE TABLE")
	}
	legacy := normalize(strings.TrimSuffix(definition, " WITHOUT ROWID"))
	var current string
	if err := s.DB.QueryRowContext(ctx, "SELECT sql FROM sqlite_schema WHERE type='table' AND name='sale_balances'").Scan(&current); err != nil {
		return err
	}
	if normalize(current) != legacy {
		return nil
	}
	return s.immediate(ctx, func(conn *sql.Conn) error {
		// Recheck under the write lock; another process could have completed a
		// run or installed custom schema since the initial read.
		if err := conn.QueryRowContext(ctx, "SELECT sql FROM sqlite_schema WHERE type='table' AND name='sale_balances'").Scan(&current); err != nil {
			return err
		}
		if normalize(current) != legacy {
			return nil
		}
		var preserve bool
		if err := conn.QueryRowContext(ctx, `SELECT
		 EXISTS(SELECT 1 FROM sale_balances LIMIT 1)
		 OR EXISTS(SELECT 1 FROM allocations LIMIT 1)
		 OR EXISTS(SELECT 1 FROM runs WHERE status='published' LIMIT 1)
		 OR EXISTS(SELECT 1 FROM sqlite_schema WHERE tbl_name='sale_balances' AND type IN ('trigger','index') AND sql IS NOT NULL)`).
			Scan(&preserve); err != nil {
			return err
		}
		if preserve {
			return nil
		}
		if _, err := conn.ExecContext(ctx, "DROP TABLE sale_balances"); err != nil {
			return err
		}
		_, err := conn.ExecContext(ctx, definition)
		return err
	})
}
