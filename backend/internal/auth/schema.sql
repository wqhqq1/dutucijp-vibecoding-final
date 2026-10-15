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
