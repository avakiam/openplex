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
	// v4: per-user interface language ('' = follow the browser).
	`ALTER TABLE users ADD COLUMN locale TEXT NOT NULL DEFAULT '';`,
	// v5: website and database IDs are never reused. A site's uid, container
	// name and backup tags derive from its ID, so a recycled ID could hand a new
	// tenant an old tenant's snapshots. Seeded from the highest ID ever seen
	// (live rows and backup history).
	`
CREATE TABLE id_sequences (
	name  TEXT PRIMARY KEY,
	value INTEGER NOT NULL
);
INSERT INTO id_sequences (name, value) VALUES
	('domains', MAX(
		(SELECT COALESCE(MAX(id), 0) FROM domains),
		(SELECT COALESCE(MAX(CAST(substr(scope, 6) AS INTEGER)), 0) FROM backup_runs WHERE scope LIKE 'site:%'))),
	('databases', MAX(
		(SELECT COALESCE(MAX(id), 0) FROM databases),
		(SELECT COALESCE(MAX(CAST(substr(scope, 4) AS INTEGER)), 0) FROM backup_runs WHERE scope LIKE 'db:%')));
`,
	// v6: scheduled tasks per website and their run history.
	`
CREATE TABLE cron_jobs (
	id           INTEGER PRIMARY KEY AUTOINCREMENT,
	domain_id    INTEGER NOT NULL REFERENCES domains(id) ON DELETE CASCADE,
	name         TEXT NOT NULL DEFAULT '',
	schedule     TEXT NOT NULL,
	command      TEXT NOT NULL,
	enabled      INTEGER NOT NULL DEFAULT 1,
	timeout_sec  INTEGER NOT NULL DEFAULT 3600,
	next_run_at  TIMESTAMP,
	last_run_at  TIMESTAMP,
	last_status  TEXT NOT NULL DEFAULT '',
	created_at   TIMESTAMP NOT NULL,
	updated_at   TIMESTAMP NOT NULL
);
CREATE INDEX cron_jobs_domain ON cron_jobs(domain_id);
CREATE INDEX cron_jobs_due ON cron_jobs(enabled, next_run_at);
CREATE TABLE cron_runs (
	id          INTEGER PRIMARY KEY AUTOINCREMENT,
	job_id      INTEGER NOT NULL REFERENCES cron_jobs(id) ON DELETE CASCADE,
	trigger     TEXT NOT NULL DEFAULT 'schedule',
	status      TEXT NOT NULL DEFAULT 'running',
	exit_code   INTEGER,
	output      TEXT NOT NULL DEFAULT '',
	started_at  TIMESTAMP NOT NULL,
	finished_at TIMESTAMP
);
CREATE INDEX cron_runs_job ON cron_runs(job_id, id);
`,
	// v7: host firewall (confirmed configuration) and blocked addresses.
	`
CREATE TABLE firewall_settings (
	id         INTEGER PRIMARY KEY CHECK (id = 1),
	enabled    INTEGER NOT NULL DEFAULT 0,
	seeded     INTEGER NOT NULL DEFAULT 0,
	updated_at TIMESTAMP NOT NULL
);
CREATE TABLE firewall_rules (
	id         INTEGER PRIMARY KEY AUTOINCREMENT,
	protocol   TEXT NOT NULL,
	ports      TEXT NOT NULL DEFAULT '',
	source     TEXT NOT NULL DEFAULT '',
	comment    TEXT NOT NULL DEFAULT '',
	position   INTEGER NOT NULL,
	created_at TIMESTAMP NOT NULL
);
CREATE TABLE firewall_blocks (
	id         INTEGER PRIMARY KEY AUTOINCREMENT,
	address    TEXT NOT NULL UNIQUE,
	comment    TEXT NOT NULL DEFAULT '',
	expires_at TIMESTAMP,
	created_at TIMESTAMP NOT NULL
);
`,
	// v8: authoritative DNS (zones synced to PowerDNS).
	`
CREATE TABLE dns_settings (
	id          INTEGER PRIMARY KEY CHECK (id = 1),
	enabled     INTEGER NOT NULL DEFAULT 0,
	ns1         TEXT NOT NULL DEFAULT '',
	ns2         TEXT NOT NULL DEFAULT '',
	ipv4        TEXT NOT NULL DEFAULT '',
	ipv6        TEXT NOT NULL DEFAULT '',
	hostmaster  TEXT NOT NULL DEFAULT '',
	updated_at  TIMESTAMP NOT NULL
);
CREATE TABLE dns_zones (
	id         INTEGER PRIMARY KEY AUTOINCREMENT,
	domain_id  INTEGER NOT NULL UNIQUE REFERENCES domains(id) ON DELETE CASCADE,
	name       TEXT NOT NULL UNIQUE COLLATE NOCASE,
	created_at TIMESTAMP NOT NULL,
	updated_at TIMESTAMP NOT NULL
);
CREATE TABLE dns_records (
	id         INTEGER PRIMARY KEY AUTOINCREMENT,
	zone_id    INTEGER NOT NULL REFERENCES dns_zones(id) ON DELETE CASCADE,
	-- set when the record belongs to a subdomain website: removed with it
	domain_id  INTEGER REFERENCES domains(id) ON DELETE CASCADE,
	name       TEXT NOT NULL,
	type       TEXT NOT NULL,
	content    TEXT NOT NULL,
	priority   INTEGER NOT NULL DEFAULT 0,
	ttl        INTEGER NOT NULL DEFAULT 3600,
	created_at TIMESTAMP NOT NULL,
	updated_at TIMESTAMP NOT NULL
);
CREATE INDEX dns_records_zone ON dns_records(zone_id, name, type);
`,
	// v9: mail (docker-mailserver), records added by modules are tagged.
	`
ALTER TABLE dns_records ADD COLUMN source TEXT NOT NULL DEFAULT '';
CREATE TABLE mail_settings (
	id         INTEGER PRIMARY KEY CHECK (id = 1),
	enabled    INTEGER NOT NULL DEFAULT 0,
	hostname   TEXT NOT NULL DEFAULT '',
	updated_at TIMESTAMP NOT NULL
);
CREATE TABLE mail_domains (
	id            INTEGER PRIMARY KEY AUTOINCREMENT,
	domain_id     INTEGER NOT NULL UNIQUE REFERENCES domains(id) ON DELETE CASCADE,
	dkim_selector TEXT NOT NULL,
	dkim_key_enc  TEXT NOT NULL,
	created_at    TIMESTAMP NOT NULL
);
CREATE TABLE mailboxes (
	id             INTEGER PRIMARY KEY AUTOINCREMENT,
	mail_domain_id INTEGER NOT NULL REFERENCES mail_domains(id) ON DELETE CASCADE,
	local_part     TEXT NOT NULL,
	password_hash  TEXT NOT NULL,
	quota_mb       INTEGER NOT NULL DEFAULT 1024,
	enabled        INTEGER NOT NULL DEFAULT 1,
	created_at     TIMESTAMP NOT NULL,
	updated_at     TIMESTAMP NOT NULL,
	UNIQUE (mail_domain_id, local_part)
);
CREATE TABLE mail_aliases (
	id             INTEGER PRIMARY KEY AUTOINCREMENT,
	mail_domain_id INTEGER NOT NULL REFERENCES mail_domains(id) ON DELETE CASCADE,
	local_part     TEXT NOT NULL,
	destinations   TEXT NOT NULL,
	created_at     TIMESTAMP NOT NULL,
	updated_at     TIMESTAMP NOT NULL,
	UNIQUE (mail_domain_id, local_part)
);
`,
	// v10: webmail (Roundcube) and login captchas.
	`
ALTER TABLE mail_settings ADD COLUMN webmail INTEGER NOT NULL DEFAULT 1;
CREATE TABLE captcha_settings (
	id         INTEGER PRIMARY KEY CHECK (id = 1),
	provider   TEXT NOT NULL DEFAULT '',
	site_key   TEXT NOT NULL DEFAULT '',
	secret_enc TEXT NOT NULL DEFAULT '',
	panel      INTEGER NOT NULL DEFAULT 0,
	webmail    INTEGER NOT NULL DEFAULT 0,
	updated_at TIMESTAMP NOT NULL
);
`,
	// v11: access grants — a user may work on someone else's website (some
	// areas, optionally read-only) or database.
	`
CREATE TABLE access_grants (
	id          INTEGER PRIMARY KEY AUTOINCREMENT,
	user_id     INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
	kind        TEXT NOT NULL CHECK (kind IN ('site', 'database')),
	resource_id INTEGER NOT NULL,
	areas       TEXT NOT NULL DEFAULT '',
	read_only   INTEGER NOT NULL DEFAULT 0,
	created_by  INTEGER REFERENCES users(id) ON DELETE SET NULL,
	created_at  TIMESTAMP NOT NULL,
	UNIQUE (user_id, kind, resource_id)
);
CREATE INDEX access_grants_resource ON access_grants(kind, resource_id);
CREATE TRIGGER access_grants_site_gone AFTER DELETE ON domains BEGIN
	DELETE FROM access_grants WHERE kind = 'site' AND resource_id = OLD.id;
END;
CREATE TRIGGER access_grants_database_gone AFTER DELETE ON databases BEGIN
	DELETE FROM access_grants WHERE kind = 'database' AND resource_id = OLD.id;
END;
`,
	// v12: extensions (see docs/adr/0001-extensions.md).
	`
CREATE TABLE extensions (
	id             TEXT PRIMARY KEY,
	uid            INTEGER NOT NULL UNIQUE,
	version        TEXT NOT NULL,
	manifest       TEXT NOT NULL,
	source         TEXT NOT NULL CHECK (source IN ('upload', 'marketplace')),
	signer         TEXT NOT NULL DEFAULT '',
	enabled        INTEGER NOT NULL DEFAULT 1,
	settings_enc   TEXT NOT NULL DEFAULT '',
	license_enc    TEXT NOT NULL DEFAULT '',
	token          TEXT NOT NULL,
	status         TEXT NOT NULL DEFAULT 'pending',
	status_message TEXT NOT NULL DEFAULT '',
	installed_at   TIMESTAMP NOT NULL,
	updated_at     TIMESTAMP NOT NULL
);
INSERT INTO id_sequences (name, value) VALUES ('extensions', 0);
`,
	// v13: what an extension with edge:write asks of the edge (JSON EdgePolicy).
	`ALTER TABLE extensions ADD COLUMN edge_policy TEXT NOT NULL DEFAULT '';`,
	// v14: panel updates.
	`
CREATE TABLE update_settings (
	id            INTEGER PRIMARY KEY CHECK (id = 1),
	check_enabled INTEGER NOT NULL DEFAULT 1,
	updated_at    TIMESTAMP NOT NULL
);
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
