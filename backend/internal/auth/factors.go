package auth

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"time"

	"github.com/pquerna/otp"
	"github.com/pquerna/otp/totp"
)

func decode(raw string, out any) error { return json.Unmarshal([]byte(raw), out) }
func encode(value any) string          { data, _ := json.Marshal(value); return string(data) }
func (s *Service) mayConfigure(v Session) bool {
	if v.Scope == "setup" && (v.State == "password" || v.State == "recovering") {
		return true
	}
	return (v.Scope == "business" && v.State == "complete" || v.Scope == "setup" && v.State == "factor") && v.Fresh > 0 && s.now().Unix()-v.Fresh <= 300
}

type Enrollment struct {
	Secret string `json:"secret"`
	URI    string `json:"uri"`
}

func (s *Service) BeginTOTP(ctx context.Context, token string) (out Enrollment, err error) {
	err = s.atomic(ctx, func(c *sql.Conn) error {
		v, e := s.current(ctx, c, token)
		if e != nil {
			return e
		}
		if !s.mayConfigure(v) {
			return restricted()
		}
		a, e := load(ctx, c, v.ID)
		if e != nil {
			return e
		}
		if len(a.TOTP) > 0 {
			return invalid("已存在认证器，请通过受限恢复更换")
		}
		key, e := totp.Generate(totp.GenerateOpts{Issuer: "Returns Reconciliation", AccountName: a.ID, Period: 30, SecretSize: 20, Digits: otp.DigitsSix, Algorithm: otp.AlgorithmSHA1})
		if e != nil {
			return e
		}
		encrypted, e := s.encrypt(key.Secret())
		if e != nil {
			return e
		}
		if _, e = c.ExecContext(ctx, "UPDATE auth_users SET pending_totp=? WHERE id=?", encrypted, a.ID); e != nil {
			return e
		}
		out = Enrollment{key.Secret(), key.URL()}
		return nil
	})
	if err != nil {
		out = Enrollment{}
	}
	return out, err
}
func validStep(secret, code string, now time.Time, last int64) (int64, bool) {
	if len(code) != 6 {
		return 0, false
	}
	step := now.Unix() / 30
	for _, n := range []int64{step, step - 1, step + 1} {
		if n <= last {
			continue
		}
		expected, e := totp.GenerateCodeCustom(secret, time.Unix(n*30, 0), totp.ValidateOpts{Period: 30, Skew: 0, Digits: otp.DigitsSix, Algorithm: otp.AlgorithmSHA1})
		if e == nil && constantEqual(expected, code) {
			return n, true
		}
	}
	return 0, false
}
func constantEqual(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	var diff byte
	for i := range a {
		diff |= a[i] ^ b[i]
	}
	return diff == 0
}

func (s *Service) markFactor(ctx context.Context, c *sql.Conn, a account, method string) (g Grant, err error) {
	if _, err = c.ExecContext(ctx, "UPDATE auth_users SET state='factor',version=version+1,pending_totp=NULL WHERE id=? AND version=?", a.ID, a.Version); err != nil {
		return g, err
	}
	a.State = "factor"
	a.Version++
	for _, table := range []string{"auth_sessions", "auth_recovery", "auth_challenges"} {
		if _, err = c.ExecContext(ctx, "DELETE FROM "+table+" WHERE user_id=?", a.ID); err != nil {
			return g, err
		}
	}
	codes := make([]string, 10)
	for i := range codes {
		b := make([]byte, 16)
		if _, err = rand.Read(b); err != nil {
			return g, err
		}
		codes[i] = base64.RawURLEncoding.EncodeToString(b)
		if _, err = c.ExecContext(ctx, "INSERT INTO auth_recovery VALUES(?,?)", Digest(codes[i]), a.ID); err != nil {
			return g, err
		}
	}
	if err = s.audit(ctx, c, a.ID, "factor_verified", method, "restricted"); err != nil {
		return g, err
	}
	g, err = s.issue(ctx, c, a, "setup", s.now().Unix())
	g.RecoveryCodes = codes
	return g, err
}
func (s *Service) ConfirmTOTP(ctx context.Context, token, code, source string) (g Grant, err error) {
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
		if !s.mayConfigure(v) {
			return restricted()
		}
		a, e := load(ctx, c, v.ID)
		if e != nil {
			return e
		}
		if len(a.Pending) == 0 {
			return denied()
		}
		secret, e := s.decrypt(a.Pending)
		if e != nil {
			return e
		}
		step, ok := validStep(secret, code, s.now(), -1)
		if !ok {
			return denied()
		}
		if _, e = c.ExecContext(ctx, "UPDATE auth_users SET totp_secret=pending_totp,last_step=? WHERE id=?", step, a.ID); e != nil {
			return e
		}
		a.TOTP = a.Pending
		a.LastStep = step
		g, e = s.markFactor(ctx, c, a, "totp")
		return e
	})
	if err == nil {
		s.release(ctx, keys)
	} else {
		s.failed(ctx, v.ID, "totp_enroll")
		g = Grant{}
	}
	return g, err
}
func (s *Service) LoginTOTP(ctx context.Context, token, code, source string) (g Grant, err error) {
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
		if v.Scope != "login_factor" {
			return denied()
		}
		a, e := load(ctx, c, v.ID)
		if e != nil || len(a.TOTP) == 0 {
			return denied()
		}
		secret, e := s.decrypt(a.TOTP)
		if e != nil {
			return e
		}
		step, ok := validStep(secret, code, s.now(), a.LastStep)
		if !ok {
			return denied()
		}
		result, e := c.ExecContext(ctx, "UPDATE auth_users SET last_step=? WHERE id=? AND last_step<? AND version=?", step, a.ID, step, a.Version)
		if e != nil {
			return e
		}
		n, e := result.RowsAffected()
		if e != nil || n != 1 {
			return denied()
		}
		if _, e = c.ExecContext(ctx, "DELETE FROM auth_sessions WHERE hash=?", Digest(token)); e != nil {
			return e
		}
		scope := "setup"
		if a.State == "complete" {
			scope = "business"
		}
		if e = s.audit(ctx, c, a.ID, "login", "totp", "success"); e != nil {
			return e
		}
		g, e = s.issue(ctx, c, a, scope, s.now().Unix())
		return e
	})
	if err == nil {
		s.release(ctx, keys)
	} else {
		s.failed(ctx, v.ID, "totp")
		g = Grant{}
	}
	return g, err
}
func (s *Service) Recover(ctx context.Context, id, password, code, source string) (g Grant, err error) {
	keys, err := s.reserve(ctx, id, source)
	if err != nil {
		return g, err
	}
	select {
	case s.hashGate <- struct{}{}:
	case <-ctx.Done():
		s.release(context.Background(), keys)
		return g, ctx.Err()
	}
	defer func() { <-s.hashGate }()
	a, e := load(ctx, s.DB, id)
	if e != nil || !verifyPassword(a.Password, password) {
		s.failed(ctx, id, "recovery")
		return g, denied()
	}
	err = s.atomic(ctx, func(c *sql.Conn) error {
		current, e := load(ctx, c, id)
		if e != nil || current.Version != a.Version || current.State == "new" {
			return denied()
		}
		res, e := c.ExecContext(ctx, "DELETE FROM auth_recovery WHERE hash=? AND user_id=?", Digest(code), id)
		if e != nil {
			return e
		}
		n, _ := res.RowsAffected()
		if n != 1 {
			return denied()
		}
		for _, table := range []string{"auth_sessions", "auth_credentials", "auth_challenges", "auth_recovery", "auth_bootstrap"} {
			if _, e = c.ExecContext(ctx, "DELETE FROM "+table+" WHERE user_id=?", id); e != nil {
				return e
			}
		}
		if _, e = c.ExecContext(ctx, "UPDATE auth_users SET state='recovering',version=version+1,totp_secret=NULL,pending_totp=NULL,last_step=-1 WHERE id=?", id); e != nil {
			return e
		}
		a = current
		a.State = "recovering"
		a.Version++
		a.TOTP = nil
		a.Credentials = nil
		if e = s.audit(ctx, c, id, "recover", "recovery_code", "restricted"); e != nil {
			return e
		}
		g, e = s.issue(ctx, c, a, "setup", 0)
		return e
	})
	if err == nil {
		s.release(ctx, keys)
	} else {
		s.failed(ctx, id, "recovery")
		g = Grant{}
	}
	return g, err
}
func (s *Service) ChangePassword(ctx context.Context, token, password string) (g Grant, err error) {
	v, err := s.Current(ctx, token, true)
	if err != nil {
		return g, err
	}
	if v.Fresh == 0 || s.now().Unix()-v.Fresh > 300 {
		return g, &Error{"reauthentication_required", "请重新使用已绑定方式登录后再更改", 403}
	}
	p, err := ValidatePassword(v.ID, password)
	if err != nil {
		return g, err
	}
	s.hashGate <- struct{}{}
	encoded, err := hashPassword(p)
	<-s.hashGate
	if err != nil {
		return g, err
	}
	err = s.atomic(ctx, func(c *sql.Conn) error {
		current, e := s.current(ctx, c, token)
		if e != nil {
			return e
		}
		if current.Scope != "business" || current.Fresh == 0 || s.now().Unix()-current.Fresh > 300 {
			return restricted()
		}
		a, e := load(ctx, c, v.ID)
		if e != nil {
			return e
		}
		if _, e = c.ExecContext(ctx, "UPDATE auth_users SET password_hash=? WHERE id=?", encoded, v.ID); e != nil {
			return e
		}
		g, e = s.markFactor(ctx, c, a, "password_change")
		return e
	})
	if err != nil {
		g = Grant{}
	}
	return g, err
}
func (s *Service) RemoveFactor(ctx context.Context, token, method string) (g Grant, err error) {
	err = s.atomic(ctx, func(c *sql.Conn) error {
		v, e := s.current(ctx, c, token)
		if e != nil {
			return e
		}
		if v.Scope != "business" || v.State != "complete" || v.Fresh == 0 || s.now().Unix()-v.Fresh > 300 {
			return restricted()
		}
		a, e := load(ctx, c, v.ID)
		if e != nil {
			return e
		}
		if len(a.TOTP) == 0 || len(a.Credentials) == 0 {
			return invalid("不能移除最后一种认证方式")
		}
		switch method {
		case "totp":
			_, e = c.ExecContext(ctx, "UPDATE auth_users SET totp_secret=NULL,last_step=-1 WHERE id=?", a.ID)
			a.TOTP = nil
		case "passkey":
			_, e = c.ExecContext(ctx, "DELETE FROM auth_credentials WHERE user_id=?", a.ID)
			a.Credentials = nil
		default:
			return invalid("未知认证方式")
		}
		if e != nil {
			return e
		}
		g, e = s.markFactor(ctx, c, a, "remove_"+method)
		return e
	})
	if err != nil {
		g = Grant{}
	}
	return g, err
}
func (s *Service) RenewBootstrap(ctx context.Context, id string) (string, error) {
	var token string
	err := s.atomic(ctx, func(c *sql.Conn) error {
		a, e := load(ctx, c, id)
		if e != nil || a.State != "new" || a.Password != "" {
			return fmt.Errorf("only untouched new accounts can receive a new local bootstrap token")
		}
		token, e = RandomToken()
		if e != nil {
			return e
		}
		if _, e = c.ExecContext(ctx, "DELETE FROM auth_bootstrap WHERE user_id=?", id); e != nil {
			return e
		}
		_, e = c.ExecContext(ctx, "INSERT INTO auth_bootstrap VALUES(?,?,?,0)", Digest(token), id, a.Version)
		return e
	})
	return token, err
}

func (s *Service) ReissueRecovery(ctx context.Context, token string) (g Grant, err error) {
	err = s.atomic(ctx, func(c *sql.Conn) error {
		v, e := s.current(ctx, c, token)
		if e != nil {
			return e
		}
		if (v.Scope != "business" && v.Scope != "setup") || (v.State != "complete" && v.State != "factor") || v.Fresh == 0 || s.now().Unix()-v.Fresh > 300 {
			return restricted()
		}
		a, e := load(ctx, c, v.ID)
		if e != nil {
			return e
		}
		if len(a.TOTP) == 0 && len(a.Credentials) == 0 {
			return restricted()
		}
		g, e = s.markFactor(ctx, c, a, "recovery_codes_reissue")
		return e
	})
	if err != nil {
		g = Grant{}
	}
	return g, err
}
