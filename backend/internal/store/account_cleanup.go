package store

import (
	"context"
	"database/sql"
)

// ReleaseAccountCasesTx participates in the caller's account-deletion transaction.
// Saved allocations and closed cases are immutable here; only unfinished ownership
// is returned to the queue, with a new version to defeat stale workflow batches.
func ReleaseAccountCasesTx(ctx context.Context, conn *sql.Conn, actor, id, at string) (int, error) {
	rows, err := conn.QueryContext(ctx, `SELECT id,run_id,cancel_id,reason,status,coalesce(assignee,''),version,conclusion,assigned_at,assigned_by FROM cases WHERE assignee=? AND status<>'closed' ORDER BY id`, id)
	if err != nil {
		return 0, err
	}
	cases := []WorkflowCase{}
	for rows.Next() {
		var c WorkflowCase
		if err = rows.Scan(&c.ID, &c.RunID, &c.CancelID, &c.Reason, &c.Status, &c.Assignee, &c.Version, &c.Conclusion, &c.AssignedAt, &c.AssignedBy); err != nil {
			rows.Close()
			return 0, err
		}
		cases = append(cases, c)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return 0, err
	}
	for _, before := range cases {
		after := before
		after.Status, after.Assignee, after.AssignedAt, after.AssignedBy, after.Conclusion = "pending", "", "", "", ""
		after.Version++
		result, e := conn.ExecContext(ctx, `UPDATE cases SET status='pending',assignee=NULL,assigned_at='',assigned_by='',conclusion='',version=version+1 WHERE id=? AND assignee=? AND status<>'closed' AND version=?`, before.ID, id, before.Version)
		if e != nil {
			return 0, e
		}
		if e = recordWorkflowAudit(ctx, conn, actor, "account_deleted_unassign", at, before, after, result); e != nil {
			return 0, e
		}
	}
	return len(cases), nil
}
