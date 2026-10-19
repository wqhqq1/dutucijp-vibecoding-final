// Package auth implements local account security. Secrets never appear in errors
// or audit events. There are no trusted-role headers or test-login endpoints.
package auth

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	_ "embed"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"returnsrecon/internal/access"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"
	"golang.org/x/crypto/argon2"
	"golang.org/x/text/unicode/norm"
)

//go:embed schema.sql
var schema string

type Error struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Status  int    `json:"-"`
}

func (e *Error) Error() string     { return e.Code + ": " + e.Message }
func denied() error                { return &Error{"authentication_failed", "认证失败或会话已失效", 401} }
func invalid(message string) error { return &Error{"invalid_input", message, 422} }
func restricted() error {
	return &Error{"configuration_required", "请先完成强密码、因素验证和恢复码保存", 403}
}

type Config struct {
	SecretDir, Origin string
	Now               func() time.Time
}
type Service struct {
	DB        *sql.DB
	WA        *webauthn.WebAuthn
	key       []byte
	now       func() time.Time
	Origin    string
	hashGate  chan struct{}
	touchGate chan struct{}
}
type Identity struct {
	ID   string `json:"id"`
	Role string `json:"role"`
}
type Session struct {
	Identity
	DisplayName        string   `json:"display_name"`
	CountryScope       string   `json:"country_scope"`
	Countries          []string `json:"countries"`
	PermissionsVersion int      `json:"permissions_version"`
	CanManageDataJobs  bool     `json:"can_manage_data_jobs"`
	State              string   `json:"state"`
	Scope              string   `json:"scope"`
	Version            int      `json:"-"`
	HasTOTP            bool     `json:"has_totp"`
	HasPasskey         bool     `json:"has_passkey"`
	CSRF               string   `json:"csrf"`
	Fresh              int64    `json:"-"`
	Seen               int64    `json:"-"`
}
type Grant struct {
	Token         string   `json:"-"`
	Session       Session  `json:"session"`
	RecoveryCodes []string `json:"recovery_codes,omitempty"`
}
type account struct {
	Identity
	DisplayName     string
	Password, State string
	Version         int
	TOTP, Pending   []byte
	LastStep        int64
	Credentials     []webauthn.Credential
}

func (a account) WebAuthnID() []byte   { return []byte(a.ID) }
func (a account) WebAuthnName() string { return a.ID }
func (a account) WebAuthnDisplayName() string {
	if a.DisplayName != "" {
		return a.DisplayName
	}
	return a.ID
}
func (a account) WebAuthnCredentials() []webauthn.Credential { return a.Credentials }

func New(db *sql.DB, cfg Config) (*Service, error) {
	if cfg.Origin == "" {
		cfg.Origin = "http://localhost:8080"
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if _, err := db.Exec(schema); err != nil {
		return nil, err
	}
	if err := PrivateDirectory(cfg.SecretDir); err != nil {
		return nil, err
	}
	keyPath := filepath.Join(cfg.SecretDir, "master.key")
	key, err := os.ReadFile(keyPath)
	if errors.Is(err, os.ErrNotExist) {
		var count int
		if e := db.QueryRow("SELECT count(*) FROM auth_users").Scan(&count); e != nil {
			return nil, e
		}
		if count > 0 {
			return nil, fmt.Errorf("authentication master key missing; restore the existing private key, do not reset accounts")
		}
		key = make([]byte, 32)
		if _, err = rand.Read(key); err != nil {
			return nil, err
		}
		if err = PrivateWrite(keyPath, key); err != nil {
			return nil, err
		}
	} else if err != nil {
		return nil, err
	}
	if len(key) != 32 {
		return nil, fmt.Errorf("invalid authentication master key")
	}
	wa, err := webauthn.New(&webauthn.Config{RPDisplayName: "退货对账与差异处理系统", RPID: "localhost", RPOrigins: []string{cfg.Origin}, AuthenticatorSelection: protocol.AuthenticatorSelection{UserVerification: protocol.VerificationRequired}})
	if err != nil {
		return nil, err
	}
	return &Service{DB: db, WA: wa, key: key, now: cfg.Now, Origin: cfg.Origin, hashGate: make(chan struct{}, 2), touchGate: make(chan struct{}, 1)}, nil
}

func PrivateDirectory(path string) error {
	if path == "" {
		return fmt.Errorf("private runtime directory is required")
	}
	if err := os.MkdirAll(path, 0700); err != nil {
		return err
	}
	if err := privateACL(path, true); err != nil {
		return err
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("private authentication directory must contain only regular private files")
		}
		if err = privateACL(filepath.Join(path, entry.Name()), false); err != nil {
			return err
		}
	}
	return nil
}
func PrivateWrite(path string, data []byte) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	if err = privateACL(path, false); err != nil {
		f.Close()
		return err
	}
	if _, err = f.Write(data); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

func (s *Service) ValidateWrite(ctx context.Context, c *sql.Conn, token string, earlier Session) error {
	current, err := s.current(ctx, c, token)
	if err != nil {
		return err
	}
	if current.Scope != "business" || current.State != "complete" || current.ID != earlier.ID || current.Role != earlier.Role || current.Version != earlier.Version {
		return denied()
	}
	return nil
}
func RandomToken() (string, error) {
	b := make([]byte, 32)
	_, err := rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b), err
}
func Digest(s string) string { h := sha256.Sum256([]byte(s)); return hex.EncodeToString(h[:]) }
func (s *Service) csrf(token string) string {
	h := hmac.New(sha256.New, s.key)
	h.Write([]byte("csrf:" + token))
	return base64.RawURLEncoding.EncodeToString(h.Sum(nil))
}
func (s *Service) CheckCSRF(token, provided string) bool {
	return hmac.Equal([]byte(s.csrf(token)), []byte(provided))
}
func (s *Service) atomic(ctx context.Context, fn func(*sql.Conn) error) error {
	c, e := s.DB.Conn(ctx)
	if e != nil {
		return e
	}
	defer c.Close()
	if _, e = c.ExecContext(ctx, "BEGIN IMMEDIATE"); e != nil {
		return e
	}
	defer c.ExecContext(context.Background(), "ROLLBACK")
	if e = fn(c); e != nil {
		return e
	}
	_, e = c.ExecContext(ctx, "COMMIT")
	return e
}

type querier interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func load(ctx context.Context, q querier, id string) (a account, err error) {
	err = q.QueryRowContext(ctx, "SELECT u.id,u.role,u.password_hash,u.state,u.version,u.totp_secret,u.pending_totp,u.last_step,coalesce(nullif(p.display_name,''),u.id) FROM auth_users u LEFT JOIN auth_account_profiles p ON p.user_id=u.id WHERE u.id=? AND u.state<>'deleted'", id).Scan(&a.ID, &a.Role, &a.Password, &a.State, &a.Version, &a.TOTP, &a.Pending, &a.LastStep, &a.DisplayName)
	if err != nil {
		return a, err
	}
	rows, err := q.QueryContext(ctx, "SELECT credential_json FROM auth_credentials WHERE user_id=? ORDER BY id", id)
	if err != nil {
		return a, err
	}
	defer rows.Close()
	for rows.Next() {
		var raw string
		if err = rows.Scan(&raw); err != nil {
			return a, err
		}
		var credential webauthn.Credential
		if err = decode(raw, &credential); err != nil {
			return a, err
		}
		a.Credentials = append(a.Credentials, credential)
	}
	return a, rows.Err()
}
func (s *Service) audit(ctx context.Context, c *sql.Conn, actor, action, method, result string) error {
	_, err := c.ExecContext(ctx, "INSERT INTO auth_audit(actor,action,method,result,occurred_at) VALUES(?,?,?,?,?)", actor, action, method, result, s.now().Format(time.RFC3339Nano))
	return err
}
func (s *Service) issue(ctx context.Context, c *sql.Conn, a account, scope string, fresh int64) (g Grant, err error) {
	token, err := RandomToken()
	if err != nil {
		return g, err
	}
	now := s.now().Unix()
	lifetime := int64(600)
	if scope == "business" {
		if a.State != "complete" {
			return g, restricted()
		}
		lifetime = 8 * 3600
	}
	if _, err = c.ExecContext(ctx, "INSERT INTO auth_sessions(hash,user_id,scope,version,created,seen,expires,fresh) VALUES(?,?,?,?,?,?,?,?)", Digest(token), a.ID, scope, a.Version, now, now, now+lifetime, fresh); err != nil {
		return g, err
	}
	v := Session{Identity: a.Identity, State: a.State, Scope: scope, Version: a.Version, HasTOTP: len(a.TOTP) > 0, HasPasskey: len(a.Credentials) > 0, CSRF: s.csrf(token), Fresh: fresh}
	if err = s.profileSession(ctx, c, &v); err != nil {
		return g, err
	}
	return Grant{Token: token, Session: v}, nil
}
func (s *Service) current(ctx context.Context, q querier, token string) (Session, error) {
	var v Session
	var seen, expires int64
	err := q.QueryRowContext(ctx, `SELECT u.id,u.role,u.state,t.scope,u.version,t.fresh,t.seen,t.expires,
 coalesce(length(u.totp_secret),0)>0,EXISTS(SELECT 1 FROM auth_credentials k WHERE k.user_id=u.id)
 FROM auth_sessions t JOIN auth_users u ON u.id=t.user_id WHERE t.hash=? AND t.version=u.version AND u.state<>'deleted'`, Digest(token)).Scan(&v.ID, &v.Role, &v.State, &v.Scope, &v.Version, &v.Fresh, &seen, &expires, &v.HasTOTP, &v.HasPasskey)
	now := s.now().Unix()
	if err != nil || now >= expires || now-seen >= 1800 {
		return v, denied()
	}
	v.CSRF = s.csrf(token)
	v.Seen = seen
	if err = s.profileSession(ctx, q, &v); err != nil {
		return Session{}, err
	}
	return v, nil
}
func (s *Service) profileSession(ctx context.Context, q access.Querier, v *Session) error {
	p, e := access.Load(ctx, q, v.ID)
	if e != nil {
		return e
	}
	v.DisplayName = p.DisplayName
	v.CountryScope = p.CountryScope
	v.Countries = p.Countries
	v.PermissionsVersion = p.PermissionsVersion
	v.CanManageDataJobs = p.CanManageJobs()
	return nil
}
func (v Session) Profile() access.Profile {
	return access.Profile{ID: v.ID, Role: v.Role, DisplayName: v.DisplayName, CountryScope: v.CountryScope, Countries: v.Countries, PermissionsVersion: v.PermissionsVersion}
}
func (s *Service) Current(ctx context.Context, token string, business bool) (Session, error) {
	v, e := s.current(ctx, s.DB, token)
	if e != nil {
		return v, e
	}
	if business && (v.Scope != "business" || v.State != "complete") {
		return v, restricted()
	}
	if s.now().Unix()-v.Seen >= 60 {
		e = s.touch(ctx, token, v)
	}
	return v, e
}

// Activity renewal must not queue authenticated readers behind an import writer.
// Authorization above is always read from SQLite, never from this concurrency gate.
// A busy writer leaves the persisted expiry unchanged; a later request retries.
func (s *Service) touch(ctx context.Context, token string, v Session) error {
	select {
	case s.touchGate <- struct{}{}:
		defer func() { <-s.touchGate }()
	default:
		return nil
	}
	conn, err := s.DB.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	var timeout int
	if err = conn.QueryRowContext(ctx, "PRAGMA busy_timeout").Scan(&timeout); err != nil {
		return err
	}
	if _, err = conn.ExecContext(ctx, "PRAGMA busy_timeout=0"); err != nil {
		return err
	}
	defer conn.ExecContext(context.Background(), fmt.Sprintf("PRAGMA busy_timeout=%d", timeout))
	if _, err = conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		var code interface{ Code() int }
		if errors.As(err, &code) && (code.Code()&255 == 5 || code.Code()&255 == 6) {
			return nil // SQLITE_BUSY / SQLITE_LOCKED: do not extend the session.
		}
		return err
	}
	defer conn.ExecContext(context.Background(), "ROLLBACK")
	now := s.now().Unix()
	if _, err = conn.ExecContext(ctx, `UPDATE auth_sessions SET seen=? WHERE hash=? AND version=? AND seen<=? AND seen>? AND expires>?`, now, Digest(token), v.Version, now-60, now-1800, now); err != nil {
		return err
	}
	_, err = conn.ExecContext(ctx, "COMMIT")
	return err
}
func (s *Service) Initialize(ctx context.Context, identities []Identity) (map[string]string, error) {
	out := map[string]string{}
	err := s.atomic(ctx, func(c *sql.Conn) error {
		for _, id := range identities {
			if id.ID == "" || (id.Role != "finance" && id.Role != "warehouse" && id.Role != "admin") {
				return invalid("非法本地账户")
			}
			r, e := c.ExecContext(ctx, "INSERT OR IGNORE INTO auth_users(id,role) VALUES(?,?)", id.ID, id.Role)
			if e != nil {
				return e
			}
			n, _ := r.RowsAffected()
			if n == 0 {
				continue
			}
			token, e := RandomToken()
			if e != nil {
				return e
			}
			if _, e = c.ExecContext(ctx, "INSERT INTO auth_bootstrap VALUES(?,?,1,0)", Digest(token), id.ID); e != nil {
				return e
			}
			out[id.ID] = token
			if e = s.audit(ctx, c, id.ID, "initialize", "local", "restricted"); e != nil {
				return e
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}
func (s *Service) Bootstrap(ctx context.Context, token string) (g Grant, err error) {
	err = s.atomic(ctx, func(c *sql.Conn) error {
		var id string
		// Existing private links remain compatible, including links generated by
		// older versions with a 15-minute deadline. Account state/version and
		// one-time consumption still prevent reuse or password resets.
		err := c.QueryRowContext(ctx, `SELECT b.user_id FROM auth_bootstrap b JOIN auth_users u ON u.id=b.user_id WHERE b.hash=? AND b.version=u.version AND u.state='new' AND u.password_hash=''`, Digest(token)).Scan(&id)
		if err != nil {
			return denied()
		}
		a, err := load(ctx, c, id)
		if err != nil {
			return err
		}
		_, err = c.ExecContext(ctx, "DELETE FROM auth_bootstrap WHERE hash=?", Digest(token))
		if err != nil {
			return err
		}
		g, err = s.issue(ctx, c, a, "setup", 0)
		return err
	})
	return g, err
}

const PasswordPolicyVersion = "password-1.0.0"

func ValidatePassword(id, password string) (string, error) {
	p := norm.NFC.String(password)
	length := utf8.RuneCountInString(p)
	if length < 16 || length > 128 {
		return "", invalid("密码须为16至128个Unicode字符")
	}
	low := strings.ToLower(p)
	compact := strings.NewReplacer(" ", "", "-", "", "_", "", "!", "", "@", "", "1", "").Replace(low)
	weak := []string{"password", "qwerty", "letmein", "administrator", "1234567890123456", "1234567890", "iloveyou", "correcthorsebatterystaple"}
	for _, w := range weak {
		if low == w || compact == w || strings.Trim(low, "0123456789!@#$%^&* ") == w {
			return "", invalid("密码在本地常见弱密码名单中")
		}
	}
	allSame := true
	var first rune
	for i, r := range p {
		if i == 0 {
			first = r
		} else if r != first {
			allSame = false
		}
	}
	if allSame {
		return "", invalid("密码过于容易猜测")
	}
	if len(id) >= 3 && strings.Contains(low, strings.ToLower(norm.NFC.String(id))) {
		return "", invalid("密码不能包含可猜测的账户名")
	}
	return p, nil
}
func hashPassword(p string) (string, error) {
	salt := make([]byte, 16)
	if _, e := rand.Read(salt); e != nil {
		return "", e
	}
	key := argon2.IDKey([]byte(p), salt, 3, 64*1024, 1, 32)
	return "argon2id$v=19$m=65536,t=3,p=1$" + base64.RawStdEncoding.EncodeToString(salt) + "$" + base64.RawStdEncoding.EncodeToString(key), nil
}
func verifyPassword(encoded, p string) bool {
	v := strings.Split(encoded, "$")
	if len(v) != 5 || v[0] != "argon2id" || v[1] != "v=19" || v[2] != "m=65536,t=3,p=1" {
		return false
	}
	salt, e := base64.RawStdEncoding.DecodeString(v[3])
	if e != nil || len(salt) != 16 {
		return false
	}
	expected, e := base64.RawStdEncoding.DecodeString(v[4])
	if e != nil || len(expected) != 32 {
		return false
	}
	got := argon2.IDKey([]byte(norm.NFC.String(p)), salt, 3, 64*1024, 1, 32)
	return subtle.ConstantTimeCompare(got, expected) == 1
}
func (s *Service) SetPassword(ctx context.Context, token, password string) (g Grant, err error) {
	session, err := s.Current(ctx, token, false)
	if err != nil {
		return g, err
	}
	if session.Scope != "setup" || session.State != "new" {
		return g, restricted()
	}
	p, err := ValidatePassword(session.ID, password)
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
		v, e := s.current(ctx, c, token)
		if e != nil {
			return e
		}
		if v.Scope != "setup" || v.State != "new" {
			return restricted()
		}
		a, e := load(ctx, c, v.ID)
		if e != nil {
			return e
		}
		if _, e = c.ExecContext(ctx, "UPDATE auth_users SET password_hash=?,state='password',version=version+1 WHERE id=? AND version=?", encoded, a.ID, a.Version); e != nil {
			return e
		}
		a.Password = encoded
		a.State = "password"
		a.Version++
		if _, e = c.ExecContext(ctx, "DELETE FROM auth_bootstrap WHERE user_id=?", a.ID); e != nil {
			return e
		}
		if _, e = c.ExecContext(ctx, "DELETE FROM auth_sessions WHERE user_id=?", a.ID); e != nil {
			return e
		}
		if e = s.audit(ctx, c, a.ID, "set_password", "password", "restricted"); e != nil {
			return e
		}
		g, e = s.issue(ctx, c, a, "setup", 0)
		return e
	})
	return g, err
}

// Reserve rate-limit slots atomically before expensive password work. Successful
// authentication releases its slots; failures retain them for five minutes.
func (s *Service) reserve(ctx context.Context, id, source string) ([]string, error) {
	keys := []string{"account:" + Digest(id), "source:" + Digest(source)}
	now := s.now().Unix()
	err := s.atomic(ctx, func(c *sql.Conn) error {
		for _, key := range keys {
			var attempts int
			var start int64
			e := c.QueryRowContext(ctx, "SELECT attempts,started FROM auth_limits WHERE key=?", key).Scan(&attempts, &start)
			if e != nil && !errors.Is(e, sql.ErrNoRows) {
				return e
			}
			if now-start >= 300 {
				attempts = 0
				start = now
			}
			if attempts >= 5 {
				return &Error{"rate_limited", "认证失败次数过多，请稍后再试", 429}
			}
			if _, e = c.ExecContext(ctx, "INSERT INTO auth_limits VALUES(?,?,?) ON CONFLICT(key) DO UPDATE SET attempts=excluded.attempts,started=excluded.started", key, attempts+1, start); e != nil {
				return e
			}
		}
		return nil
	})
	return keys, err
}
func (s *Service) release(ctx context.Context, keys []string) {
	for _, key := range keys {
		s.DB.ExecContext(ctx, "UPDATE auth_limits SET attempts=max(attempts-1,0) WHERE key=?", key)
	}
}
func (s *Service) failed(ctx context.Context, id, method string) {
	_ = s.atomic(ctx, func(c *sql.Conn) error { return s.audit(ctx, c, id, "authentication", method, "failed") })
}
func (s *Service) LoginPassword(ctx context.Context, id, password, source string) (g Grant, err error) {
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
	if e != nil || a.Password == "" || len(password) > 2048 || !verifyPassword(a.Password, password) {
		s.failed(ctx, id, "password")
		return g, denied()
	}
	err = s.atomic(ctx, func(c *sql.Conn) error {
		current, e := load(ctx, c, id)
		if e != nil || current.Version != a.Version {
			return denied()
		}
		scope := "login_factor"
		if current.State == "password" {
			scope = "setup"
		}
		g, e = s.issue(ctx, c, current, scope, 0)
		return e
	})
	if err == nil {
		s.release(ctx, keys)
	}
	return g, err
}
func (s *Service) Logout(ctx context.Context, token string) error {
	return s.atomic(ctx, func(c *sql.Conn) error {
		if _, e := c.ExecContext(ctx, "DELETE FROM auth_challenges WHERE session_hash=?", Digest(token)); e != nil {
			return e
		}
		_, e := c.ExecContext(ctx, "DELETE FROM auth_sessions WHERE hash=?", Digest(token))
		return e
	})
}
func (s *Service) Complete(ctx context.Context, token string, saved bool) (g Grant, err error) {
	if !saved {
		return g, invalid("请保存恢复码后明确确认")
	}
	err = s.atomic(ctx, func(c *sql.Conn) error {
		v, e := s.current(ctx, c, token)
		if e != nil {
			return e
		}
		if v.Scope != "setup" || v.State != "factor" || v.Fresh == 0 {
			return restricted()
		}
		a, e := load(ctx, c, v.ID)
		if e != nil {
			return e
		}
		if len(a.TOTP) == 0 && len(a.Credentials) == 0 {
			return restricted()
		}
		var n int
		if e = c.QueryRowContext(ctx, "SELECT count(*) FROM auth_recovery WHERE user_id=?", a.ID).Scan(&n); e != nil {
			return e
		}
		if n != 10 {
			return restricted()
		}
		if _, e = c.ExecContext(ctx, "UPDATE auth_users SET state='complete',version=version+1 WHERE id=?", a.ID); e != nil {
			return e
		}
		a.State = "complete"
		a.Version++
		if _, e = c.ExecContext(ctx, "DELETE FROM auth_sessions WHERE user_id=?", a.ID); e != nil {
			return e
		}
		if e = s.audit(ctx, c, a.ID, "configuration_complete", "factor", "success"); e != nil {
			return e
		}
		g, e = s.issue(ctx, c, a, "business", s.now().Unix())
		return e
	})
	return g, err
}
func (s *Service) encrypt(plain string) ([]byte, error) {
	block, e := aes.NewCipher(s.key)
	if e != nil {
		return nil, e
	}
	g, e := cipher.NewGCM(block)
	if e != nil {
		return nil, e
	}
	nonce := make([]byte, g.NonceSize())
	if _, e = rand.Read(nonce); e != nil {
		return nil, e
	}
	return g.Seal(nonce, nonce, []byte(plain), []byte("totp-v1")), nil
}
func (s *Service) decrypt(data []byte) (string, error) {
	block, e := aes.NewCipher(s.key)
	if e != nil {
		return "", e
	}
	g, e := cipher.NewGCM(block)
	if e != nil {
		return "", e
	}
	if len(data) < g.NonceSize() {
		return "", fmt.Errorf("invalid encrypted factor")
	}
	plain, e := g.Open(nil, data[:g.NonceSize()], data[g.NonceSize():], []byte("totp-v1"))
	return string(plain), e
}
func (s *Service) Users(ctx context.Context) ([]access.Profile, error) {
	rows, e := s.DB.QueryContext(ctx, "SELECT id FROM auth_users WHERE state<>'deleted' ORDER BY id")
	if e != nil {
		return nil, e
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if e = rows.Scan(&id); e != nil {
			rows.Close()
			return nil, e
		}
		ids = append(ids, id)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return nil, e
	}
	out := []access.Profile{}
	for _, id := range ids {
		p, e := access.Load(ctx, s.DB, id)
		if errors.Is(e, sql.ErrNoRows) {
			// A concurrent deletion may have retired an ID from the first query.
			continue
		}
		if e != nil {
			return nil, e
		}
		out = append(out, p)
	}
	return out, nil
}
func parseInt(v string) int64 { i, _ := strconv.ParseInt(v, 10, 64); return i }
