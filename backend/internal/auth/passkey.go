package auth

import (
	"context"
	"database/sql"
	"encoding/base64"
	"net/http"
	"time"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"
)

type PasskeyBegin struct {
	Token       string `json:"-"`
	ChallengeID string `json:"challenge_id"`
	Options     any    `json:"options"`
	CSRF        string `json:"csrf"`
}

func (s *Service) BeginPasskey(ctx context.Context, token, userID string, register bool) (out PasskeyBegin, err error) {
	if !register && userID == "" {
		return s.beginPasskeySelection(ctx, token)
	}
	err = s.atomic(ctx, func(c *sql.Conn) error {
		var a account
		var v Session
		var e error
		purpose := "login"
		if register {
			purpose = "register"
			v, e = s.current(ctx, c, token)
			if e != nil {
				return e
			}
			if userID != "" && userID != v.ID {
				return denied()
			}
			if !s.mayConfigure(v) {
				return restricted()
			}
			a, e = load(ctx, c, v.ID)
		} else {
			a, e = load(ctx, c, userID)
			if e != nil || len(a.Credentials) == 0 {
				return denied()
			}
			g, e := s.issue(ctx, c, a, "login_passkey", 0)
			if e != nil {
				return e
			}
			token = g.Token
		}
		if e != nil {
			return e
		}
		wa, rp, e := s.passkeyWA(ctx, c)
		if e != nil {
			return e
		}
		a, e = filterCredentials(ctx, c, a, rp)
		if e != nil {
			return e
		}
		if !register && len(a.Credentials) == 0 {
			return denied()
		}
		var data *webauthn.SessionData
		if register {
			out.Options, data, e = wa.BeginRegistration(a, webauthn.WithAuthenticatorSelection(protocol.AuthenticatorSelection{UserVerification: protocol.VerificationRequired}), webauthn.WithResidentKeyRequirement(protocol.ResidentKeyRequirementRequired))
		} else {
			out.Options, data, e = wa.BeginLogin(a, webauthn.WithUserVerification(protocol.VerificationRequired))
		}
		if e != nil {
			return e
		}
		data.Expires = s.now().Add(2 * time.Minute)
		data.UserVerification = protocol.VerificationRequired
		id, e := RandomToken()
		if e != nil {
			return e
		}
		if _, e = c.ExecContext(ctx, "DELETE FROM auth_challenges WHERE session_hash=? AND purpose=?", Digest(token), purpose); e != nil {
			return e
		}
		if _, e = c.ExecContext(ctx, "INSERT INTO auth_challenges VALUES(?,?,?,?,?,?,?)", id, a.ID, Digest(token), purpose, a.Version, encode(passkeySession{*data, s.passkeyOrigin(ctx), s.nativeProof(ctx)}), data.Expires.Unix()); e != nil {
			return e
		}
		out.Token = token
		out.ChallengeID = id
		out.CSRF = s.csrf(token)
		return nil
	})
	if err != nil {
		out = PasskeyBegin{}
	}
	return out, err
}

func (s *Service) FinishPasskey(ctx context.Context, token, challengeID string, request *http.Request, register bool, source string) (g Grant, err error) {
	if !register {
		var selection bool
		err = s.DB.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM auth_challenges WHERE id=? AND session_hash=? AND purpose='passkey_selection' AND user_id='' AND version=0)`, challengeID, Digest(token)).Scan(&selection)
		if err != nil {
			return g, err
		}
		if selection {
			return s.finishPasskeySelection(ctx, token, challengeID, request, source)
		}
	}
	v, err := s.Current(ctx, token, false)
	if err != nil {
		return g, err
	}
	keys, err := s.reserve(ctx, v.ID, source)
	if err != nil {
		return g, err
	}
	err = s.atomic(ctx, func(c *sql.Conn) error {
		v, e := s.current(ctx, c, token)
		if e != nil {
			return e
		}
		purpose := "login"
		if register {
			purpose = "register"
			if !s.mayConfigure(v) {
				return restricted()
			}
		} else if v.Scope != "login_passkey" {
			return denied()
		}
		var raw string
		e = c.QueryRowContext(ctx, `SELECT data_json FROM auth_challenges WHERE id=? AND user_id=? AND session_hash=? AND purpose=? AND version=? AND expires>?`, challengeID, v.ID, Digest(token), purpose, v.Version, s.now().Unix()).Scan(&raw)
		if e != nil {
			return denied()
		}
		var data passkeySession
		if e = decode(raw, &data); e != nil {
			return e
		}
		if data.UserVerification != protocol.VerificationRequired || !s.validChallengeOrigin(ctx, data.Origin) || !s.validNativeProof(ctx, data.Native) {
			return denied()
		}
		a, e := load(ctx, c, v.ID)
		if e != nil {
			return e
		}
		wa, rp, e := s.passkeyWA(ctx, c)
		if e != nil {
			return e
		}
		a, e = filterCredentials(ctx, c, a, rp)
		if e != nil {
			return e
		}
		var credential *webauthn.Credential
		if register {
			credential, e = wa.FinishRegistration(a, data.SessionData, request)
		} else {
			credential, e = wa.FinishLogin(a, data.SessionData, request)
		}
		if e != nil {
			return denied()
		}
		if !credential.Flags.UserPresent || !credential.Flags.UserVerified || credential.Authenticator.CloneWarning {
			return denied()
		}
		result, e := c.ExecContext(ctx, "DELETE FROM auth_challenges WHERE id=? AND session_hash=? AND version=?", challengeID, Digest(token), a.Version)
		if e != nil {
			return e
		}
		n, _ := result.RowsAffected()
		if n != 1 {
			return denied()
		}
		id := base64.RawURLEncoding.EncodeToString(credential.ID)
		if register {
			if _, e = c.ExecContext(ctx, "INSERT INTO auth_credentials(id,user_id,credential_json) VALUES(?,?,?)", id, a.ID, encodeCredential(credential, rp)); e != nil {
				return invalid("凭据已登记，不能重复或跨账户使用")
			}
			a.Credentials = append(a.Credentials, *credential)
			g, e = s.markFactor(ctx, c, a, "passkey")
			return e
		}
		result, e = c.ExecContext(ctx, "UPDATE auth_credentials SET credential_json=? WHERE id=? AND user_id=?", encodeCredential(credential, rp), id, a.ID)
		if e != nil {
			return e
		}
		n, _ = result.RowsAffected()
		if n != 1 {
			return denied()
		}
		if _, e = c.ExecContext(ctx, "DELETE FROM auth_sessions WHERE hash=?", Digest(token)); e != nil {
			return e
		}
		scope := "setup"
		if a.State == "complete" {
			scope = "business"
		}
		if e = s.audit(ctx, c, a.ID, "login", "passkey", "success"); e != nil {
			return e
		}
		g, e = s.issue(ctx, c, a, scope, s.now().Unix())
		return e
	})
	if err == nil {
		s.release(ctx, keys)
	} else {
		s.failed(ctx, v.ID, "passkey")
		g = Grant{}
	}
	return g, err
}
