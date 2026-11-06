# PROGRESS.md — OpenPlex MVP checklist

Tick `[x]` only when the item is implemented, routed, builds, and (for host-level items)
has been verified on the Linux test server. One commit per milestone.

## M0 — Project foundation
- [x] Git repository, `.gitignore`, `.editorconfig`
- [x] `AGENTS.md`, `CLAUDE.md`, `GEMINI.md`, `PROGRESS.md`
- [x] `Makefile` (check / build / backend / web / run) + Docker toolchain fallback
- [x] `go.mod` (CGO-free deps pinned)

## M1 — Daemon core (`cmd/openplexd`, `internal/config`)
- [x] Flags + `OPENPLEX_*` env config, data-dir creation (0700)
- [x] Persistent signing secret (`secret.key`, 0600)
- [x] First-boot admin bootstrap (`initial-admin-password`, 0600)
- [x] Structured logging (slog), graceful shutdown

## M2 — State database (`internal/db`)
- [x] SQLite open: WAL, foreign keys, busy timeout, in-memory test helper
- [x] Versioned migration runner
- [x] Schema v1: users, refresh_tokens, api_keys, ssh_keys, domains, redirects, audit_log
- [x] Repositories: users, refresh tokens, API keys, SSH keys, domains, redirects, audit log
- [x] Repository tests

## M3 — Authentication & RBAC (`internal/auth`, `internal/api`)
- [x] bcrypt password hashing + policy
- [x] JWT access tokens (HS256, 15 min)
- [x] Refresh tokens (opaque, hashed, rotation, reuse detection → revoke family)
- [x] TOTP 2FA: setup / enable / disable / login challenge
- [x] API keys (`opx_` prefix, hashed, last-used tracking)
- [x] SSH public-key manager (parse + fingerprint validation)
- [x] RBAC: admin → all; reseller → own customers; customer → self
- [x] Endpoints: `/auth/login|refresh|logout|me`, `/auth/2fa/*`, `/users`, `/api-keys`, `/ssh-keys`
- [x] Login rate limiting
- [x] Audit log writes for security-relevant actions + `/audit` endpoint (admin)

## M4 — Docker orchestrator (`internal/docker`, `internal/ssl`)
- [x] Engine client + ping, managed labels
- [x] `openplex_edge` network
- [x] Caddy edge container (80/443/443udp, admin API on 127.0.0.1:2019)
- [x] Caddyfile rendering: vhosts, aliases, redirects, force-HTTPS, PHP FastCGI, reverse proxy, static
- [x] Live config reload via Caddy admin API
- [x] Site runtimes: static, PHP 7.4/8.1/8.2/8.3/8.4-FPM, Node.js, Python, Go
- [x] Per-site CPU/memory limits, env vars, restart policy, hardening
- [x] Deploy / start / stop / restart / remove site container
- [x] Reconcile loop (DB → Docker + Caddy) at boot and on demand
- [ ] Sync domain status from container health (a crash-looping container still shows `running`)
- [ ] Catch-all edge vhost: unknown hosts → 404 instead of an empty 200
- [ ] `AUTOINCREMENT` domain IDs so a deleted site's uid/container name is never reused

## M5 — Domains & websites (`/api/v1/domains`)
- [x] CRUD domains (primary, subdomain, alias) with ownership checks + quota
- [x] Redirect rules CRUD
- [x] SSL toggle + force HTTPS
- [x] Runtime selector + environment variables
- [x] Access/error log streaming over WebSocket

## M6 — File manager & terminal (`internal/files`, `internal/terminal`)
- [x] Jailed path resolution (symlink-safe)
- [x] List / read / write / mkdir / rename / delete / chmod
- [x] Multipart upload + download
- [x] Extract `.zip` / `.tar.gz` (zip-slip safe) + compress
- [x] WebSocket web terminal (xterm.js): docker exec TTY in the site's own container as the site uid,
      throwaway `openplex/shell` tools container for static sites, resize, root mode (admins only),
      30 min idle / 4 h max, whole session killed on disconnect, audited open/close

## M7 — Databases
- [x] Shared MariaDB 11.8 / PostgreSQL 17 servers + per-instance Redis 7.4 on internal `openplex_db`
- [x] Database + user create / drop, password rotation, size reporting, per-user database quota
- [x] Passwords encrypted at rest (AES-256-GCM, HKDF from secret.key); credentials + connection URL view
- [x] Adminer 5.4.1 behind `/tools/adminer/` with one-click login; SSRF guard (only `mariadb`/`postgres`, MySQL/PG drivers)
- [x] Databases UI page (create, connect details, rotate, Adminer, delete)
- [x] PHP runtime images `openplex/php:<ver>-<recipe-hash>` built on demand from the official
      php-fpm-alpine images: bcmath exif gd(freetype/jpeg/webp) intl(+full ICU) mysqli opcache pdo_mysql
      pdo_pgsql pgsql redis zip, production php.ini + OpenPlex overrides; recipe changes rebuild and
      redeploy automatically, stale images are removed
- [ ] Remote access option (publish a DB port per-IP allow-list) and per-database size limits

## M8 — Mail
- [ ] Mail stack containers (Postfix, Dovecot, Rspamd, OpenDKIM) + Roundcube
- [ ] Mail domains, mailboxes, aliases, forwarders, quotas, password rotation
- [ ] DKIM key generation, SPF/DKIM/DMARC record generation
- [ ] Webmail one-click login

## M9 — DNS
- [ ] PowerDNS container (gsqlite3 backend) + API key
- [ ] Zones CRUD synced from panel state; default records on domain create
- [ ] Records: A, AAAA, CNAME, MX, TXT, SRV, NS, CAA

## M10 — Security & cron
- [ ] Firewall rules (nftables) CRUD + apply with safety rollback
- [ ] Fail2Ban / CrowdSec status, jails, banned IPs, unban
- [ ] Cron jobs per site (run inside site container) + scheduler + run history

## M11 — Backup & restore
- [x] Backup engine: restic 0.18.1 in throwaway containers — incremental, deduplicated, encrypted;
      one snapshot per site (files), database (mariadb-dump --single-transaction / pg_dump -Fc / Redis SAVE)
      and the panel (SQLite VACUUM INTO, secret.key, engine root passwords, TLS certificates, env file)
- [x] Targets: local directory (default or mounted disk), S3-compatible (secret stored encrypted)
- [x] Daily schedule (catches up if missed) + retention (keep daily/weekly/monthly per item, prune)
- [x] One-click restore of a site (exact, `--delete`, ownership fixed, redeploy) or a database
      (MariaDB reset + import, pg_restore --clean as the owner role, Redis data swap); snapshot must
      carry the item's own tag (no cross-tenant restores)
- [x] Recovery key view (audited), activity log, owners can back up/restore their own sites and DBs
- [ ] Mail backups (with the mail module)
- [ ] Disaster recovery: `openplexd restore-panel` CLI to rebuild a new server from the repository
- [ ] Off-site copy check: warn when the repository is on the same disk as the data

## M12 — Telemetry
- [x] Host CPU / RAM / disk / network sampling
- [x] Per-container CPU / memory / network
- [x] WebSocket live stream + short ring-buffer history

## M13 — Web dashboard (`web/`)
- [x] Next.js scaffold (App Router, TS, Tailwind, shadcn/ui, TanStack Query, static export)
- [x] `go:embed` integration + SPA fallback (`internal/ui`)
- [x] Auth: login, 2FA challenge, token refresh (in-memory access token + httpOnly refresh cookie)
- [x] Layout: sidebar, role-aware navigation, dark mode
- [x] Views: dashboard (live host telemetry + containers), domains list/create, domain detail
      (overview/actions, settings, env vars, redirects, aliases & subdomains, live logs),
      file manager with Monaco (self-hosted, no CDN), users, API/SSH keys, account (password, 2FA + QR), audit log
- [x] Views: terminal (xterm.js), databases, backups
- [ ] Views for upcoming modules: mail, DNS, security, cron
- [ ] Trim the embedded Monaco bundle (~25 MB of the 41 MB binary) to the languages we need

## M14 — Installer & packaging
- [x] `scripts/install.sh`: unattended Ubuntu/Debian install (Docker, user, dirs, binary, systemd, firewall)
- [x] `deploy/openplexd.service` (hardened systemd unit)
- [ ] Release build (`linux/amd64`, `linux/arm64`) + checksums
- [ ] Verified end-to-end on the Linux test server

---

## Verification log
- 2026-11-06 — Local smoke test on Docker Desktop 29.9 (openplexd in a container with the
  engine socket): edge auto-provisioned (caddy:2.10-alpine), static site served, 302 redirect,
  PHP 8.4-FPM site served via FastCGI as isolated uid 100002 with injected env, stop → 503
  maintenance page, start → live again, Caddy hot reloads, per-site access log written,
  per-container stats and host telemetry returned. `go test ./...` green (db, auth, ssl, files, api).
- 2026-11-06 — Real server `programacio-danzai` (Ubuntu 26.04.1 LTS, kernel 7.0, 4 vCPU, 7 GiB):
  `install.sh --binary` installed Docker 29.9.3 from download.docker.com, the hardened systemd
  unit and ufw (SSH/80/443/443udp + pre-existing 3000/3389/8734). Static, PHP 8.3 and Node 22
  sites served over the LAN, 301 redirect, isolated uids 100002/100003, memory limits applied;
  `systemctl restart docker` → daemon and all sites recovered automatically; re-running the
  installer upgrades in place.
  **Bug found & fixed:** systemd `UMask=0027` made site files 0640 → edge 403. The unit now uses
  0022 and the code sets 0755/0644 explicitly (regression test forces umask 077).
- Pending: Let's Encrypt issuance with a public DNS name (the test server is LAN-only).
- 2026-11-06 — Databases on the real server: 4 databases for 2 customers provisioned in 22 s
  (first pull included); own-DB read/write OK on MariaDB/PostgreSQL/Redis; cross-tenant access
  denied (MariaDB 1044, Postgres CONNECT revoked incl. maintenance DBs); Redis without password
  NOAUTH; DB network has no internet; site containers reach `mariadb:3306`/`postgres:5432`;
  quota → 403; rotation invalidates the old password; Adminer one-click login lands on the DB;
  deletion removes DBs, users/roles and Redis containers.
- 2026-11-06 — PHP images on the real server: recipe builds on 7.4 (Alpine 3.16), 8.3 and 8.4
  (~3–5 min first build, 130–195 MB); all 11 extensions load. A PHP 8.3 site connected to MariaDB
  (PDO + mysqli), PostgreSQL (PDO) and Redis, GD/intl/OPcache work, es_ES formatting correct
  (`1.234,50 €`). Rebuild after a recipe change swapped the site container with no downtime.
- 2026-11-06 — Backups on the real server: full backup (3 sites, 3 DBs, panel) in ~10 s, second run
  added ~150 KB (incremental). After deliberately corrupting everything, restores brought back the
  exact state: MariaDB rows + dropped extra table, Postgres rows, Redis keys (extra key gone), site
  files (injected malware.php deleted, owner uid kept). Restoring a DB snapshot into a site was
  refused. The daily scheduler caught up on first start and applied retention. Restore from the UI OK.
- 2026-11-06 — Web terminal on the real server: PHP 8.3 site shell runs as uid 100002 inside its
  container (php CLI, DB hosts reachable); static site gets a throwaway `openplex/shell` container
  (bash, git, DB clients, internet) removed on disconnect. Closing the tab killed the shell and a
  background `sleep` it had started (Docker does not hang up TTY execs on its own — fixed).
  Open/close audited with duration.
