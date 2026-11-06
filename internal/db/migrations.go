package db

import (
	"context"
	"database/sql"
	"fmt"
)

// migrations are applied in order; never edit an entry once released.
var migrations = []string{
	// 1: core schema
	`
CREATE TABLE users (
	id            INTEGER PRIMARY KEY,
	username      TEXT NOT NULL UNIQUE COLLATE NOCASE,
	email         TEXT NOT NULL DEFAULT '',
	password_hash TEXT NOT NULL,
	role          TEXT NOT NULL CHECK (role IN ('admin','reseller','customer')),
	parent_id     INTEGER REFERENCES users(id) ON DELETE RESTRICT,
	totp_secret   TEXT NOT NULL DEFAULT '',
	totp_enabled  INTEGER NOT NULL DEFAULT 0,
	disabled      INTEGER NOT NULL DEFAULT 0,
	max_domains   INTEGER NOT NULL DEFAULT 0,
	created_at    TIMESTAMP NOT NULL,
	updated_at    TIMESTAMP NOT NULL
);
CREATE INDEX users_parent ON users(parent_id);

CREATE TABLE refresh_tokens (
	id          INTEGER PRIMARY KEY,
	user_id     INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
	family      TEXT NOT NULL,
	token_hash  TEXT NOT NULL UNIQUE,
	expires_at  TIMESTAMP NOT NULL,
	revoked     INTEGER NOT NULL DEFAULT 0,
	user_agent  TEXT NOT NULL DEFAULT '',
	ip          TEXT NOT NULL DEFAULT '',
	created_at  TIMESTAMP NOT NULL
);
CREATE INDEX refresh_tokens_user ON refresh_tokens(user_id);
CREATE INDEX refresh_tokens_family ON refresh_tokens(family);

CREATE TABLE api_keys (
	id           INTEGER PRIMARY KEY,
	user_id      INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
	name         TEXT NOT NULL,
	prefix       TEXT NOT NULL,
	key_hash     TEXT NOT NULL UNIQUE,
	last_used_at TIMESTAMP,
	expires_at   TIMESTAMP,
	created_at   TIMESTAMP NOT NULL
);
CREATE INDEX api_keys_user ON api_keys(user_id);

CREATE TABLE ssh_keys (
	id          INTEGER PRIMARY KEY,
	user_id     INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
	name        TEXT NOT NULL,
	public_key  TEXT NOT NULL,
	fingerprint TEXT NOT NULL,
	created_at  TIMESTAMP NOT NULL,
	UNIQUE (user_id, fingerprint)
);

CREATE TABLE domains (
	id              INTEGER PRIMARY KEY,
	owner_id        INTEGER NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
	name            TEXT NOT NULL UNIQUE COLLATE NOCASE,
	kind            TEXT NOT NULL CHECK (kind IN ('primary','subdomain','alias')),
	parent_id       INTEGER REFERENCES domains(id) ON DELETE CASCADE,
	runtime         TEXT NOT NULL DEFAULT 'static',
	runtime_version TEXT NOT NULL DEFAULT '',
	document_root   TEXT NOT NULL DEFAULT 'public',
	start_command   TEXT NOT NULL DEFAULT '',
	app_port        INTEGER NOT NULL DEFAULT 0,
	ssl_enabled     INTEGER NOT NULL DEFAULT 1,
	force_https     INTEGER NOT NULL DEFAULT 1,
	memory_mb       INTEGER NOT NULL DEFAULT 512,
	cpus            REAL NOT NULL DEFAULT 1.0,
	env_json        TEXT NOT NULL DEFAULT '{}',
	status          TEXT NOT NULL DEFAULT 'pending',
	status_message  TEXT NOT NULL DEFAULT '',
	container_id    TEXT NOT NULL DEFAULT '',
	created_at      TIMESTAMP NOT NULL,
	updated_at      TIMESTAMP NOT NULL
);
CREATE INDEX domains_owner ON domains(owner_id);
CREATE INDEX domains_parent ON domains(parent_id);

CREATE TABLE redirects (
	id          INTEGER PRIMARY KEY,
	domain_id   INTEGER NOT NULL REFERENCES domains(id) ON DELETE CASCADE,
	source_path TEXT NOT NULL,
	target_url  TEXT NOT NULL,
	status_code INTEGER NOT NULL DEFAULT 301 CHECK (status_code IN (301,302,307,308)),
	created_at  TIMESTAMP NOT NULL,
	UNIQUE (domain_id, source_path)
);

CREATE TABLE audit_log (
	id         INTEGER PRIMARY KEY,
	user_id    INTEGER REFERENCES users(id) ON DELETE SET NULL,
	action     TEXT NOT NULL,
	target     TEXT NOT NULL DEFAULT '',
	detail     TEXT NOT NULL DEFAULT '',
	ip         TEXT NOT NULL DEFAULT '',
	created_at TIMESTAMP NOT NULL
);
CREATE INDEX audit_log_created ON audit_log(created_at);
`,
	// 2: databases module
	`
ALTER TABLE users ADD COLUMN max_databases INTEGER NOT NULL DEFAULT 0;

CREATE TABLE databases (
	id             INTEGER PRIMARY KEY AUTOINCREMENT,
	owner_id       INTEGER NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
	engine         TEXT NOT NULL CHECK (engine IN ('mariadb','postgres','redis')),
	name           TEXT NOT NULL,
	username       TEXT NOT NULL,
	password_enc   TEXT NOT NULL,
	status         TEXT NOT NULL DEFAULT 'pending',
	status_message TEXT NOT NULL DEFAULT '',
	created_at     TIMESTAMP NOT NULL,
	updated_at     TIMESTAMP NOT NULL,
	UNIQUE (engine, name)
);
CREATE INDEX databases_owner ON databases(owner_id);
`,
	// 3: backups
	`
CREATE TABLE backup_settings (
	id                INTEGER PRIMARY KEY CHECK (id = 1),
	target            TEXT NOT NULL DEFAULT 'local' CHECK (target IN ('local','s3')),
	local_path        TEXT NOT NULL DEFAULT '',
	s3_endpoint       TEXT NOT NULL DEFAULT '',
	s3_bucket         TEXT NOT NULL DEFAULT '',
	s3_prefix         TEXT NOT NULL DEFAULT 'openplex',
	s3_region         TEXT NOT NULL DEFAULT '',
	s3_access_key     TEXT NOT NULL DEFAULT '',
	s3_secret_enc     TEXT NOT NULL DEFAULT '',
	repo_password_enc TEXT NOT NULL DEFAULT '',
	schedule_enabled  INTEGER NOT NULL DEFAULT 1,
	schedule_time     TEXT NOT NULL DEFAULT '03:00',
	keep_daily        INTEGER NOT NULL DEFAULT 7,
	keep_weekly       INTEGER NOT NULL DEFAULT 4,
	keep_monthly      INTEGER NOT NULL DEFAULT 6,
	last_scheduled_at TIMESTAMP,
	updated_at        TIMESTAMP NOT NULL
);

CREATE TABLE backup_runs (
	id          INTEGER PRIMARY KEY AUTOINCREMENT,
	kind        TEXT NOT NULL CHECK (kind IN ('backup','restore')),
	trigger     TEXT NOT NULL DEFAULT 'manual',
	scope       TEXT NOT NULL,
	status      TEXT NOT NULL DEFAULT 'running',
	message     TEXT NOT NULL DEFAULT '',
	bytes_added INTEGER NOT NULL DEFAULT 0,
	user_id     INTEGER REFERENCES users(id) ON DELETE SET NULL,
	started_at  TIMESTAMP NOT NULL,
	finished_at TIMESTAMP
);
CREATE INDEX backup_runs_started ON backup_runs(started_at);
`,
}

func (d *DB) migrate(ctx context.Context) error {
	if _, err := d.sql.ExecContext(ctx,
		`CREATE TABLE IF NOT EXISTS schema_migrations (version INTEGER PRIMARY KEY, applied_at TIMESTAMP NOT NULL)`); err != nil {
		return fmt.Errorf("create schema_migrations: %w", err)
	}
	var current int
	if err := d.sql.QueryRowContext(ctx, `SELECT COALESCE(MAX(version),0) FROM schema_migrations`).Scan(&current); err != nil {
		return fmt.Errorf("read schema version: %w", err)
	}
	for i := current; i < len(migrations); i++ {
		version := i + 1
		err := d.tx(ctx, func(tx *sql.Tx) error {
			if _, err := tx.ExecContext(ctx, migrations[i]); err != nil {
				return fmt.Errorf("apply migration %d: %w", version, err)
			}
			_, err := tx.ExecContext(ctx, `INSERT INTO schema_migrations(version, applied_at) VALUES (?, ?)`, version, now())
			return err
		})
		if err != nil {
			return err
		}
	}
	return nil
}
