package auth

import (
	"context"
	"database/sql"
	"returnsrecon/internal/access"
)

// SetupAccounts exposes only locally provisioned, untouched identities. It does
// not create accounts or grant access to accounts with an existing password.
func (s *Service) SetupAccounts(ctx context.Context) ([]access.Profile, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT id,role FROM auth_users WHERE state='new' AND password_hash='' ORDER BY CASE role WHEN 'finance' THEN 0 WHEN 'warehouse' THEN 1 ELSE 2 END,id`)
	if err != nil {
		return nil, err
	}
	ids := []string{}
	for rows.Next() {
		var identity Identity
		if err = rows.Scan(&identity.ID, &identity.Role); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, identity.ID)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	out := []access.Profile{}
	for _, id := range ids {
		p, e := access.Load(ctx, s.DB, id)
		if e != nil {
			return nil, e
		}
		out = append(out, p)
	}
	return out, nil
}

// StartSetup is the local first-use claim, protected by the HTTP origin/host
// boundary. Recheck under the same write lock as SetPassword; the public list
// is never authority to reset an account or assign a role.
func (s *Service) StartSetup(ctx context.Context, id string) (g Grant, err error) {
	err = s.atomic(ctx, func(c *sql.Conn) error {
		a, e := load(ctx, c, id)
		if e != nil || a.State != "new" || a.Password != "" {
			return denied()
		}
		if _, e = c.ExecContext(ctx, "DELETE FROM auth_sessions WHERE user_id=?", a.ID); e != nil {
			return e
		}
		g, e = s.issue(ctx, c, a, "setup", 0)
		return e
	})
	return g, err
}
