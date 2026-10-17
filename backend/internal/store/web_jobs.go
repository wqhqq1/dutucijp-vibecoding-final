package store

import (
	"context"
	"database/sql"
)

// EnqueueWebJob rechecks the admitted HTTP session inside the same write
// transaction that persists the job, just like workflow and manual writes.
func (s *Store) EnqueueWebJob(ctx context.Context, id, scope string) error {
	return s.immediate(ctx, func(conn *sql.Conn) error {
		_, err := conn.ExecContext(ctx, "INSERT INTO web_jobs(id,scope,status,stage) VALUES(?,?,'queued','queued')", id, scope)
		return err
	})
}
