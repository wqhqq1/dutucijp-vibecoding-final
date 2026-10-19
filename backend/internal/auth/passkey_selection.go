package auth

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"errors"
	"net/http"
	"time"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"
)

type passkeySelection struct {
	Origin   string               `json:"app_origin,omitempty"`
	Session  webauthn.SessionData `json:"session"`
	Versions map[string]int       `json:"versions"`
}

// A selection cookie binds a challenge only. It is deliberately absent from
// auth_sessions, so it cannot grant account configuration or business access.
func (s *Service) beginPasskeySelection(ctx context.Context, previous string) (out PasskeyBegin, err error) {
	err = s.atomic(ctx, func(c *sql.Conn) error {
		wa, _, e := s.passkeyWA(ctx, c)
		if e != nil {
			return e
		}
		options, data, e := wa.BeginDiscoverableLogin(webauthn.WithUserVerification(protocol.VerificationRequired))
		if e != nil {
			return e
		}
		data.Expires = s.now().Add(2 * time.Minute)
		selection := passkeySelection{Origin: s.passkeyOrigin(ctx), Session: *data, Versions: map[string]int{}}
		rows, e := c.QueryContext(ctx, `SELECT id,version FROM auth_users u WHERE u.state<>'deleted' AND EXISTS(SELECT 1 FROM auth_credentials k WHERE k.user_id=u.id)`)
		if e != nil {
			return e
		}
		for rows.Next() {
			var id string
			var version int
			if e = rows.Scan(&id, &version); e != nil {
				rows.Close()
				return e
			}
			selection.Versions[id] = version
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return e
		}
		token, e := RandomToken()
		if e != nil {
			return e
		}
		id, e := RandomToken()
		if e != nil {
			return e
		}
		if _, e = c.ExecContext(ctx, `DELETE FROM auth_challenges WHERE expires<=? OR (session_hash=? AND purpose='passkey_selection')`, s.now().Unix(), Digest(previous)); e != nil {
			return e
		}
		if _, e = c.ExecContext(ctx, `INSERT INTO auth_challenges VALUES(?,'',?,'passkey_selection',0,?,?)`, id, Digest(token), encode(selection), data.Expires.Unix()); e != nil {
			return e
		}
		out = PasskeyBegin{Token: token, ChallengeID: id, Options: options, CSRF: s.csrf(token)}
		return nil
	})
	if err != nil {
		out = PasskeyBegin{}
	}
	return
}

func (s *Service) finishPasskeySelection(ctx context.Context, token, challengeID string, request *http.Request, source string) (g Grant, err error) {
	parsed, parseErr := protocol.ParseCredentialRequestResponse(request)
	var owner string
	if parseErr == nil {
		err = s.DB.QueryRowContext(ctx, `SELECT user_id FROM auth_credentials WHERE id=?`, base64.RawURLEncoding.EncodeToString(parsed.RawID)).Scan(&owner)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return g, err
		}
	}
	limitID := owner
	if limitID == "" {
		limitID = "passkey-selection:" + Digest(token)
	}
	keys, err := s.reserve(ctx, limitID, source)
	if err != nil {
		return g, err
	}
	defer func() {
		if err == nil {
			s.release(ctx, keys)
		} else {
			s.failed(ctx, owner, "passkey")
			g = Grant{}
		}
	}()
	if parseErr != nil || owner == "" {
		return g, denied()
	}
	err = s.atomic(ctx, func(c *sql.Conn) error {
		var raw string
		if e := c.QueryRowContext(ctx, `SELECT data_json FROM auth_challenges WHERE id=? AND session_hash=? AND purpose='passkey_selection' AND user_id='' AND version=0 AND expires>?`, challengeID, Digest(token), s.now().Unix()).Scan(&raw); e != nil {
			return denied()
		}
		var selection passkeySelection
		if e := decode(raw, &selection); e != nil {
			return e
		}
		if !s.validChallengeOrigin(ctx, selection.Origin) || selection.Session.UserVerification != protocol.VerificationRequired || len(selection.Session.UserID) != 0 || len(selection.Session.AllowedCredentialIDs) != 0 {
			return denied()
		}
		wa, rp, e := s.passkeyWA(ctx, c)
		if e != nil {
			return e
		}
		var selected account
		user, credential, e := wa.ValidatePasskeyLogin(func(rawID, handle []byte) (webauthn.User, error) {
			var currentOwner string
			if e := c.QueryRowContext(ctx, `SELECT user_id FROM auth_credentials WHERE id=?`, base64.RawURLEncoding.EncodeToString(rawID)).Scan(&currentOwner); e != nil || currentOwner != owner || !bytes.Equal(handle, []byte(currentOwner)) {
				return nil, denied()
			}
			a, e := load(ctx, c, currentOwner)
			if e != nil || selection.Versions[currentOwner] != a.Version {
				return nil, denied()
			}
			a, e = filterCredentials(ctx, c, a, rp)
			if e != nil {
				return nil, e
			}
			selected = a
			return a, nil
		}, selection.Session, parsed)
		if e != nil || user == nil || credential == nil || !credential.Flags.UserPresent || !credential.Flags.UserVerified || credential.Authenticator.CloneWarning {
			return denied()
		}
		result, e := c.ExecContext(ctx, `DELETE FROM auth_challenges WHERE id=? AND session_hash=? AND purpose='passkey_selection'`, challengeID, Digest(token))
		if e != nil {
			return e
		}
		if n, e := result.RowsAffected(); e != nil || n != 1 {
			return denied()
		}
		result, e = c.ExecContext(ctx, `UPDATE auth_credentials SET credential_json=? WHERE id=? AND user_id=?`, encodeCredential(credential, rp), base64.RawURLEncoding.EncodeToString(credential.ID), selected.ID)
		if e != nil {
			return e
		}
		if n, e := result.RowsAffected(); e != nil || n != 1 {
			return denied()
		}
		if e = s.audit(ctx, c, selected.ID, "login", "passkey", "success"); e != nil {
			return e
		}
		scope := "setup"
		if selected.State == "complete" {
			scope = "business"
		}
		g, e = s.issue(ctx, c, selected, scope, s.now().Unix())
		return e
	})
	return
}
