CREATE TABLE IF NOT EXISTS auth_users (
 id TEXT PRIMARY KEY, role TEXT NOT NULL CHECK(role IN ('finance','warehouse','admin')),
 password_hash TEXT NOT NULL DEFAULT '', state TEXT NOT NULL DEFAULT 'new',
 version INTEGER NOT NULL DEFAULT 1, totp_secret BLOB, pending_totp BLOB,
 last_step INTEGER NOT NULL DEFAULT -1
);
CREATE TABLE IF NOT EXISTS auth_sessions (
 hash TEXT PRIMARY KEY, user_id TEXT NOT NULL REFERENCES auth_users(id), scope TEXT NOT NULL,
 version INTEGER NOT NULL, created INTEGER NOT NULL, seen INTEGER NOT NULL,
 expires INTEGER NOT NULL, fresh INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE IF NOT EXISTS auth_account_profiles (
 user_id TEXT PRIMARY KEY REFERENCES auth_users(id), display_name TEXT NOT NULL,
 country_scope TEXT NOT NULL CHECK(country_scope IN ('all','selected')),
 permissions_version INTEGER NOT NULL DEFAULT 1
);
CREATE TABLE IF NOT EXISTS auth_user_countries (
 user_id TEXT NOT NULL REFERENCES auth_users(id),country TEXT NOT NULL CHECK(country<>''),
 PRIMARY KEY(user_id,country)
);
CREATE TABLE IF NOT EXISTS warehouse_requests (
 id TEXT PRIMARY KEY, requester TEXT NOT NULL REFERENCES auth_users(id),
 account_id TEXT NOT NULL,display_name TEXT NOT NULL,country_scope TEXT NOT NULL,
 countries_json TEXT NOT NULL,reason TEXT NOT NULL DEFAULT '',
 status TEXT NOT NULL CHECK(status IN ('pending','approved','rejected')),
 created_at TEXT NOT NULL,reviewed_by TEXT NOT NULL DEFAULT '',reviewed_at TEXT NOT NULL DEFAULT '',
 rejection_reason TEXT NOT NULL DEFAULT ''
);
CREATE UNIQUE INDEX IF NOT EXISTS warehouse_pending_account ON warehouse_requests(account_id) WHERE status='pending';
CREATE TABLE IF NOT EXISTS account_audit (
 id INTEGER PRIMARY KEY,actor TEXT NOT NULL,action TEXT NOT NULL,target TEXT NOT NULL,
 request_id TEXT NOT NULL DEFAULT '',before_json TEXT NOT NULL,after_json TEXT NOT NULL,occurred_at TEXT NOT NULL
);
CREATE TRIGGER IF NOT EXISTS account_audit_no_update BEFORE UPDATE ON account_audit
BEGIN SELECT RAISE(ABORT,'account audit is append-only'); END;
CREATE TRIGGER IF NOT EXISTS account_audit_no_delete BEFORE DELETE ON account_audit
BEGIN SELECT RAISE(ABORT,'account audit is append-only'); END;
CREATE INDEX IF NOT EXISTS auth_session_user ON auth_sessions(user_id);
CREATE TABLE IF NOT EXISTS auth_bootstrap (
 hash TEXT PRIMARY KEY, user_id TEXT NOT NULL REFERENCES auth_users(id), version INTEGER NOT NULL, expires INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS auth_credentials (
 id TEXT PRIMARY KEY, user_id TEXT NOT NULL REFERENCES auth_users(id), credential_json TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS auth_challenges (
 id TEXT PRIMARY KEY, user_id TEXT NOT NULL, session_hash TEXT NOT NULL,
 purpose TEXT NOT NULL, version INTEGER NOT NULL, data_json TEXT NOT NULL, expires INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS auth_passkey_domains (
 id INTEGER PRIMARY KEY CHECK(id=1),origins_json TEXT NOT NULL,version INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS auth_client_identity (
 id INTEGER PRIMARY KEY CHECK(id=1),backend_id TEXT NOT NULL,
 rp_id TEXT NOT NULL,logical_origin TEXT NOT NULL
);
INSERT OR IGNORE INTO auth_passkey_domains(id,origins_json,version) VALUES(1,'[]',1);
CREATE TABLE IF NOT EXISTS auth_recovery (
 hash TEXT PRIMARY KEY, user_id TEXT NOT NULL REFERENCES auth_users(id)
);
CREATE TABLE IF NOT EXISTS auth_limits (key TEXT PRIMARY KEY, attempts INTEGER NOT NULL, started INTEGER NOT NULL);
CREATE TABLE IF NOT EXISTS auth_audit (
 id INTEGER PRIMARY KEY, actor TEXT NOT NULL, action TEXT NOT NULL,
 method TEXT NOT NULL, result TEXT NOT NULL, occurred_at TEXT NOT NULL
);
CREATE TRIGGER IF NOT EXISTS auth_audit_no_update BEFORE UPDATE ON auth_audit
BEGIN SELECT RAISE(ABORT,'audit events are append-only'); END;
CREATE TRIGGER IF NOT EXISTS auth_audit_no_delete BEFORE DELETE ON auth_audit
BEGIN SELECT RAISE(ABORT,'audit events are append-only'); END;
