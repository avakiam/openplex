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
- [ ] WebSocket container terminal (docker exec TTY, resize)

## M7 — Databases
- [ ] MariaDB / PostgreSQL / Redis service containers per owner (internal network)
- [ ] Database + user create / drop, password rotation, size reporting
- [ ] phpMyAdmin / Adminer routing + one-click signed login

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
- [ ] Backup engine: files (incremental tar), DB dumps, mail, panel DB snapshot
- [ ] Targets: local directory, S3-compatible
- [ ] Schedules + retention
- [ ] One-click restore (site / DB / full panel)

## M12 — Telemetry
- [x] Host CPU / RAM / disk / network sampling
- [x] Per-container CPU / memory / network
- [x] WebSocket live stream + short ring-buffer history

## M13 — Web dashboard (`web/`)
- [ ] Next.js scaffold (App Router, TS, Tailwind, shadcn/ui, TanStack Query, static export)
- [x] `go:embed` integration + SPA fallback (`internal/ui`)
- [ ] Auth: login, 2FA challenge, token refresh
- [ ] Layout: sidebar, role-aware navigation, dark mode
- [ ] Views: dashboard (telemetry), users, domains, domain detail, file manager (Monaco),
      terminal (xterm.js), databases, mail, DNS, security, cron, backups, API/SSH keys, settings

## M14 — Installer & packaging
- [ ] `scripts/install.sh`: unattended Ubuntu/Debian install (Docker, user, dirs, binary, systemd, firewall)
- [ ] `deploy/openplexd.service` (hardened systemd unit)
- [ ] Release build (`linux/amd64`, `linux/arm64`) + checksums
- [ ] Verified end-to-end on the Linux test server
