-- +goose Up

CREATE TABLE config (
    key        TEXT NOT NULL PRIMARY KEY,
    value      TEXT NOT NULL,
    updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now'))
);

CREATE TABLE households (
    id                  TEXT NOT NULL PRIMARY KEY,
    name_enc            TEXT NOT NULL,
    created_at          TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now')),
    updated_at          TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now')),
    encryption_salt     TEXT NOT NULL,
    onboarding_progress TEXT NOT NULL DEFAULT '{}',
    settings            TEXT NOT NULL DEFAULT '{}'
);

CREATE TABLE users (
    id                 TEXT NOT NULL PRIMARY KEY,
    household_id       TEXT NOT NULL REFERENCES households(id),
    email_enc          TEXT NOT NULL,
    email_hash         TEXT NOT NULL UNIQUE,
    password_hash      TEXT,
    display_name_enc   TEXT NOT NULL,
    role               TEXT NOT NULL DEFAULT 'member',
    auth_provider      TEXT NOT NULL DEFAULT 'local',
    timezone           TEXT NOT NULL DEFAULT 'America/New_York',
    notification_prefs TEXT NOT NULL DEFAULT '{}',
    totp_secret_enc    TEXT,
    last_login_at      TEXT,
    created_at         TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now')),
    updated_at         TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now')),
    deleted_at         TEXT
);

CREATE INDEX idx_users_household ON users(household_id) WHERE deleted_at IS NULL;
CREATE INDEX idx_users_email_hash ON users(email_hash);

CREATE TABLE sessions (
    id             TEXT NOT NULL PRIMARY KEY,
    user_id        TEXT NOT NULL REFERENCES users(id),
    household_id   TEXT NOT NULL REFERENCES households(id),
    ip_address     TEXT,
    user_agent     TEXT,
    created_at     TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now')),
    expires_at     TEXT NOT NULL,
    last_active_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now'))
);

CREATE INDEX idx_sessions_user ON sessions(user_id);
CREATE INDEX idx_sessions_expires ON sessions(expires_at);

CREATE TABLE login_attempts (
    id           TEXT NOT NULL PRIMARY KEY,
    email_hash   TEXT NOT NULL,
    ip_address   TEXT NOT NULL,
    succeeded    INTEGER NOT NULL DEFAULT 0,
    attempted_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now'))
);

CREATE INDEX idx_login_attempts_email ON login_attempts(email_hash, attempted_at);
CREATE INDEX idx_login_attempts_ip ON login_attempts(ip_address, attempted_at);

CREATE TABLE invites (
    id           TEXT NOT NULL PRIMARY KEY,
    household_id TEXT NOT NULL REFERENCES households(id),
    invited_by   TEXT NOT NULL REFERENCES users(id),
    email_enc    TEXT,
    email_hash   TEXT,
    token_hash   TEXT NOT NULL UNIQUE,
    role         TEXT NOT NULL DEFAULT 'member',
    expires_at   TEXT NOT NULL,
    accepted_at  TEXT,
    created_at   TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now'))
);

CREATE INDEX idx_invites_token ON invites(token_hash);
CREATE INDEX idx_invites_household ON invites(household_id);

CREATE TABLE audit_log (
    id           TEXT NOT NULL PRIMARY KEY,
    household_id TEXT NOT NULL REFERENCES households(id),
    user_id      TEXT REFERENCES users(id),
    action       TEXT NOT NULL,
    entity_type  TEXT NOT NULL,
    entity_id    TEXT,
    metadata     TEXT,
    ip_address   TEXT,
    created_at   TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now'))
);

CREATE INDEX idx_audit_household ON audit_log(household_id, created_at);
CREATE INDEX idx_audit_user ON audit_log(user_id, created_at);
CREATE INDEX idx_audit_entity ON audit_log(entity_type, entity_id);

-- +goose Down

DROP TABLE IF EXISTS audit_log;
DROP TABLE IF EXISTS invites;
DROP TABLE IF EXISTS login_attempts;
DROP TABLE IF EXISTS sessions;
DROP TABLE IF EXISTS users;
DROP TABLE IF EXISTS households;
DROP TABLE IF EXISTS config;
