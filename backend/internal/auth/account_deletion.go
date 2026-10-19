package auth

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"returnsrecon/internal/access"
	"returnsrecon/internal/store"
)

type AccountDeletion struct {
	Account              access.Profile `json:"account"`
	OpenCases            int            `json:"open_cases"`
	ClosedCases          int            `json:"closed_cases"`
	PendingRequests      int            `json:"pending_requests"`
	PasskeyCount         int            `json:"passkey_count"`
	DeletedAt            string         `json:"deleted_at,omitempty"`
	RevokedCredentialIDs []string       `json:"revoked_credential_ids,omitempty"`
}

func deletionImpact(ctx context.Context, q access.Querier, id string) (out AccountDeletion, err error) {
	out.Account, err = access.Load(ctx, q, id)
	if errors.Is(err, sql.ErrNoRows) {
		return out, &Error{"not_found", "账户不存在或已删除", 404}
	}
	if err != nil {
		return out, err
	}
	if out.Account.Role == "admin" {
		return out, &Error{"admin_protected", "管理员账户不能删除", 403}
	}
	err = q.QueryRowContext(ctx, `SELECT coalesce(sum(status<>'closed'),0),coalesce(sum(status='closed'),0) FROM cases WHERE assignee=?`, id).Scan(&out.OpenCases, &out.ClosedCases)
	if err == nil {
		err = q.QueryRowContext(ctx, `SELECT count(*) FROM warehouse_requests WHERE requester=? AND status='pending'`, id).Scan(&out.PendingRequests)
	}
	if err == nil {
		err = q.QueryRowContext(ctx, `SELECT count(*) FROM auth_credentials WHERE user_id=?`, id).Scan(&out.PasskeyCount)
	}
	return
}

func (s *Service) AccountDeletionPreview(ctx context.Context, token, id string) (AccountDeletion, error) {
	v, e := s.Current(ctx, token, true)
	if e != nil {
		return AccountDeletion{}, e
	}
	if v.Role != "admin" {
		return AccountDeletion{}, forbidden()
	}
	return deletionImpact(ctx, s.DB, id)
}

func (s *Service) DeleteAccount(ctx context.Context, token, id string) (out AccountDeletion, err error) {
	var revoked []string
	err = s.atomic(ctx, func(c *sql.Conn) error {
		actor, e := s.manager(ctx, c, token, "admin")
		if e != nil {
			return e
		}
		out, e = deletionImpact(ctx, c, id)
		if e != nil {
			return e
		}
		before := out
		_, rp, e := s.passkeyWA(ctx, c)
		if e != nil {
			return e
		}
		credentials, e := c.QueryContext(ctx, `SELECT id FROM auth_credentials WHERE user_id=? AND coalesce(json_extract(credential_json,'$.app_rp_id'),'localhost')=? ORDER BY id`, id, rp)
		if e != nil {
			return e
		}
		for credentials.Next() {
			var key string
			if e = credentials.Scan(&key); e != nil {
				credentials.Close()
				return e
			}
			revoked = append(revoked, key)
		}
		e = credentials.Err()
		credentials.Close()
		if e != nil {
			return e
		}
		out.DeletedAt = s.now().Format(time.RFC3339Nano)
		if _, e = store.ReleaseAccountCasesTx(ctx, c, actor.ID, id, out.DeletedAt); e != nil {
			return e
		}
		// Keep the identity as a historical tombstone and reserve its login name.
		if _, e = c.ExecContext(ctx, `UPDATE auth_users SET state='deleted',version=version+1,password_hash='',totp_secret=NULL,pending_totp=NULL,last_step=-1 WHERE id=?`, id); e != nil {
			return e
		}
		if _, e = c.ExecContext(ctx, `UPDATE auth_account_profiles SET permissions_version=permissions_version+1 WHERE user_id=?`, id); e != nil {
			return e
		}
		if _, e = c.ExecContext(ctx, `DELETE FROM auth_challenges WHERE user_id=? OR session_hash IN (SELECT hash FROM auth_sessions WHERE user_id=?)`, id, id); e != nil {
			return e
		}
		for _, table := range []string{"auth_sessions", "auth_credentials", "auth_recovery", "auth_bootstrap"} {
			if _, e = c.ExecContext(ctx, "DELETE FROM "+table+" WHERE user_id=?", id); e != nil {
				return e
			}
		}
		rows, e := c.QueryContext(ctx, `SELECT `+requestColumns+` FROM warehouse_requests WHERE requester=? AND status='pending' ORDER BY id`, id, rp)
		if e != nil {
			return e
		}
		pending := []WarehouseRequest{}
		for rows.Next() {
			r, e := scanRequest(rows)
			if e != nil {
				rows.Close()
				return e
			}
			pending = append(pending, r)
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return e
		}
		for _, r := range pending {
			after := r
			after.Status, after.ReviewedBy, after.ReviewedAt, after.RejectionReason = "rejected", actor.ID, out.DeletedAt, "申请人账户已删除，申请自动驳回。"
			if _, e = c.ExecContext(ctx, `UPDATE warehouse_requests SET status='rejected',reviewed_by=?,reviewed_at=?,rejection_reason=? WHERE id=? AND status='pending'`, actor.ID, out.DeletedAt, after.RejectionReason, r.ID); e != nil {
				return e
			}
			if e = s.managementAudit(ctx, c, actor.ID, "rejected", r.AccountID, r.ID, r, after); e != nil {
				return e
			}
		}
		return s.managementAudit(ctx, c, actor.ID, "delete", id, "", before, out)
	})
	if err != nil {
		out = AccountDeletion{}
	} else {
		// Returned only after COMMIT, never included in management audit or preview.
		out.RevokedCredentialIDs = revoked
	}
	return
}
