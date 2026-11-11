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
- [x] Access grants: a user may work on another account's website — chosen areas (website settings, files +
      terminal, DNS, mail, backups), optionally read-only (no changes, no terminal), aliases included — or database
      (credentials, backups, not deletion). Presets: full, web developer, mail only, DNS only. Granted by admins, or
      by resellers to their customers on resources they manage; deleting the resource stays with its owner.
      Enforced centrally in the website/database loaders; the dashboard marks shared items and hides the rest

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
- [x] Sync website status from container health every 15 s (crash loop / unexpected exit → error, recovers on its own)
- [x] Catch-all edge vhost: unknown hosts → 404 instead of an empty 200
- [x] Website and database IDs are never reused (`id_sequences`, seeded from live rows and backup history),
      so a new tenant can never inherit an old site's uid, container name or backup snapshots

## M5 — Domains & websites (`/api/v1/domains`)
- [x] CRUD domains (primary, subdomain, alias) with ownership checks + quota
- [x] Redirect rules CRUD
- [x] SSL toggle + force HTTPS
- [x] Runtime selector + environment variables
- [x] Access/error log streaming over WebSocket
- [x] Application installer (`internal/apps`, v0.19.0, migration v15): WordPress, Joomla and Drupal from the
      website creation dialog or the website's overview. The latest release is downloaded from the project
      (WP-CLI checksum checked; Drupal through its installer API) and installed by a script running as the site
      user in its PHP container, with a new MariaDB database counted in the owner's quota; the admin password is
      never stored. Needs an empty document root and a PHP version the app supports; a failed install removes its
      files and database; installs cut by a restart are marked failed. Website owners only (not grants)

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
- [x] Mail stack: docker-mailserver 16.0.1 (Postfix 3.10, Dovecot 2.4, Rspamd with Redis; no ClamAV/SpamAssassin),
      one server-wide mail host (MX target, PTR, its own Let's Encrypt certificate via the edge, self-signed until then),
      SRS for forwards, spoofing protection, nothing on Docker networks trusted; files rendered from the DB and picked
      up by the container without restarts (only rewritten when they change)
- [x] Roundcube webmail (1.7.6, SQLite in a volume) behind the edge on the mail host and on webmail.<domain> (a CNAME
      added to zones hosted here); reaches Dovecot/Postfix over an internal Docker network; can be turned off
- [x] Mail domains (per website, alias or subdomain), mailboxes (SHA512-CRYPT, 10–128 chars, quota, enable/disable,
      password change), aliases/forwarding (to local or external addresses), usage from Dovecot, 200 each per domain;
      owners manage their own; deleting a mailbox, turning mail off or deleting the website removes the stored mail
- [x] DKIM key per domain (RSA 2048, encrypted in the DB), MX/SPF/DKIM/DMARC added to the zone (an existing MX, SPF or
      DMARC is kept), moved when the mail host changes, removed when mail is turned off; mail host address generated in
      its zone; admin page checks forward DNS, reverse DNS (PTR) and the certificate
- [x] Webmail one-click login: the panel signs a single-use ticket (HMAC, 2 min) for a mailbox the user owns; the
      OpenPlex Roundcube plugin logs in with a Dovecot master user that only works from the webmail's network
      (allow_nets); spoofing protection still applies to mail sent that way
- [x] Login captcha (Cloudflare Turnstile, hCaptcha or Google reCAPTCHA v2) for the panel and the webmail, one set of
      keys, secret encrypted; turning it on needs a token solved with the new keys; 2FA step skips a second captcha;
      `OPENPLEX_CAPTCHA=false` switches the panel's off if a key breaks

## M9 — DNS
- [x] PowerDNS 5.0.8 container (gsqlite3 in a volume, API on 127.0.0.1:7082 with a generated key), only while
      the admin turns it on; port 53 published on the host's own addresses (systemd-resolved keeps 127.0.0.53);
      recreated when image, addresses or key change; not an open resolver, version hidden
- [x] Zones live in the panel DB (so panel backups include them) and are pushed to PowerDNS (create, diff → PATCH,
      remove zones no longer in the panel; resync on change and every 10 min; SOA serial only moves on real
      changes). A zone per website and alias with A/AAAA + www; subdomain websites add their records to the enclosing
      zone and take them away when deleted; apex NS, SOA and in-zone nameserver glue come from the DNS settings;
      changing the server address moves the records that used the old one
- [x] Records: A, AAAA, CNAME, MX, TXT, SRV, NS (sub-delegation), CAA — validated (CNAME exclusivity, host names,
      TXT quoting/splitting, SRV/CAA syntax, TTL 60 s–7 d, 500 per zone), owners edit their own zones, audited;
      DNS tab per website, admin DNS page with nameservers, registrar/glue instructions and a delegation check asked
      of the parent zone's servers (not the caching system resolver); a zone's www record makes the edge serve
      www.<domain> as a 308 redirect to the main name with its own certificate

## M10 — Security & cron
- [x] Firewall rules (nftables) CRUD + apply with safety rollback — implemented: own table `inet openplex` replaced
      atomically (`nft -f`); input chain (policy drop, SSH/ICMP/ND/established always handled) only when on; blocked
      addresses in a prerouting chain at priority raw so they also stop website visitors (Docker-forwarded); applied
      changes roll back after 60 s unless kept; refuses rule sets that close SSH, cut an open SSH session or the
      requester's direct panel access, and blocks of the requester or of an address with an open SSH/panel session;
      timed blocks expire; table re-applied at start and repaired if flushed; first start imports ufw rules (left off
      while ufw is active). Verified live (see log)
- [x] Fail2Ban status, jails, banned IPs, unban — implemented: installer sets up Fail2Ban (sshd + `openplex` jail on
      failed panel logins from the journal; bans drop all traffic in a prerouting nftables chain); Firewall page tab with
      jail counters and unban. Verified live (see log)
- [x] Scheduled tasks per website: 5-field cron / @daily…, server time zone, run as the site user inside its
      container (static sites: throwaway tools container), time limit kills the whole process tree, no overlap,
      4 at a time, missed runs skipped, run now, last 50 runs with output, 20 per site, audited; Cron tab in the UI

- [x] Security audit before the first release (2026-11-10, v0.15.0) — four parallel reviews (auth/RBAC, files &
      containers, config rendering, install & dashboard), every finding checked in the code, fixed:
      - Tenant isolation: one Docker network per account (`openplex_u<id>`, a /24 from `OPENPLEX_TENANT_NETWORKS`,
        default 10.213.0.0/16); the edge and the shared MariaDB/PostgreSQL join each, Redis and shells only their
        owner's; unused account networks are pruned. Before, every site shared `openplex_edge`/`openplex_db`, so a
        site could send FastCGI to another site's PHP-FPM (:9000) and run code as it.
      - The edge no longer mounts tenant files: static sites run `openplex/static` (Caddy as the site uid, read-only
        mount), PHP images bundle Caddy on :8080 with PHP-FPM on 127.0.0.1:9000 (HTTPS/port/scheme/client IP passed
        from the edge). Before, a symlink in a site made the root edge serve other sites' files and every TLS key.
        `.git .env .htaccess .htpasswd .user.ini` are hidden.
      - Adminer needs a panel session (one-time ticket → `opx_adminer` cookie, 8 h) and its driver/server filter
        reads parameters the way PHP does (last value, arrays, spaces, multipart, mixed-case types, semicolons).
      - Host chmod/chown through tenant symlinks: `PrepareSiteDir` and the post-restore chown go through `os.Root`;
        a restore stops the site's containers and shells first.
      - MariaDB grants escape `_`/`%` (`ali_ice_wp` matched `alice_wp`); old wildcard grants are dropped at start.
      - DKIM signs only when the From: header and the envelope share a domain; Rspamd restarts when its files change.
      - Names: public suffixes refused; the panel and mail host names (and, for non-admins, their parents and
        children) are reserved. Customers can't raise their memory/CPU; a reseller's quota counts its customers.
      - Web terminal through a grant needs Files + Website settings. Login: username/password length caps, bounded
        limiter memory, same-origin `next`; CSP (frame-ancestors/base-uri/object-src/form-action) and HSTS on the
        panel; the initial admin password file is deleted once that password is changed; Roundcube clears the SSO
        flag on password logins.
      Known and accepted for now: no proof of domain ownership (as in Plesk/cPanel: a customer can add any
      unclaimed domain, including enabling local mail for it); per-username login lockout can be triggered by
      anyone (captcha + Fail2Ban mitigate); TOTP codes are not remembered within their 30 s window; API keys are not
      scoped and survive a password change; any XSS in Adminer runs on the panel origin.

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
- [x] Mail backups: one snapshot per mail domain from the mail volume (nightly with everything, or "back up now"
      on the Mail tab); Roundcube's database and the webmail keys go with the panel. Restore of the whole domain or
      one mailbox merges: messages missing now come back (owner, mode and received date kept), nothing received
      since is removed, nothing is duplicated (Maildir unique names), then Dovecot resyncs
- [x] Disaster recovery: `openplexd restore-panel --repo PATH|s3:URL` on a fresh install (service stopped, recovery
      key from OPENPLEX_RECOVERY_KEY or stdin) puts back the panel DB, signing secret, engine passwords, certificates,
      webmail keys and env file (this install's files kept aside), points backups at that repository, turns the
      firewall off (rules were for the old host), moves DNS records to this host's public addresses; on start the
      daemon restores every website, database, mail domain and the webmail DB from the same backup run
- [x] Database reconcile re-creates missing databases/users on a fresh engine (idempotent)
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
- [x] Views: Firewall (rules with apply/keep/undo, blocked addresses, Fail2Ban), scheduled tasks
- [x] Views: DNS (admin page + DNS tab per website)
- [x] Views: Mail (admin page + Mail tab per website)
- [x] Interface languages: English, Spanish, Catalan — browser detection, switcher in the header and on
      the login page, per-user preference stored on the account (`users.locale`, `PATCH /auth/me`, set by
      managers too), locale-aware numbers/dates, fixed API error messages translated; the build fails on a
      missing translation (`scripts/check-i18n.mjs`)
- [ ] Turn dynamic API messages (quotas, overlaps, invalid JSON) into fixed, translatable strings
- [ ] Trim the embedded Monaco bundle (~25 MB of the 41 MB binary) to the languages we need

## M15 — Extensions (`internal/extensions`, design in `docs/adr/0001-extensions.md`)
- [x] Manifest `openplex-extension.json` (schema 1, localized texts, permissions, events, settings, pages,
      site tabs) + `.opx` package (tar of package.tar.gz + optional Ed25519 `signature.json`)
- [x] `openplexd ext pack` / `ext keygen`; trusted key `avakiam-2026` marks official packages
- [x] Runtime: one container per extension on its own network, own uid (400000+n), no capabilities,
      read-only rootfs, `/data` + `/tmp`, memory/CPU/pids limits; loaded from the package or pulled
- [x] Per-extension API socket (`/run/openplex/panel.sock`), scope table per permission, `X-OpenPlex-User`
      to act as a user; settings (secrets sealed) and licence key via `GET /api/v1/extension`
- [x] Events (installed, settings.changed, domain.created/deleted, dns.changed) with retries
- [x] Pages and website tabs: session URL `/ext/<id>/s/<token>/…`, proxied with user headers, sandboxed
      iframe + CSP sandbox; access checked like the website areas
- [x] Dashboard: Extensions (installed, marketplace, upload → review → install, settings, licence),
      menu entries and website tabs; audit log entries
- [x] Developer guide `docs/{en,es,ca}/extensions.md` + `examples/extension-hello` (0BSD)
- [x] Edge policies (`edge:write`, v0.17.0): extensions declare trusted proxies and per-host network restrictions
      on their socket; validated, merged across enabled extensions, re-validated when rendering the Caddyfile
- [x] IPv6 on the edge network (v0.17.1), so the edge sees IPv6 visitors' addresses (logs, PHP, Fail2Ban,
      restrictions): created dual-stack (engine pool, then a fixed ULA, then IPv4 only); an IPv4-only network is
      rebuilt once, keeping its IPv4 range, removing the edge and reconnecting the other members
- [ ] IPv6 for the DNS and mail containers' published ports (they don't answer over IPv6 behind ufw)
- [ ] Marketplace service (catalog + paid downloads) — the client side is done (`OPENPLEX_MARKETPLACE_URL`)

## M14 — Installer & packaging
- [x] `scripts/install.sh`: unattended Ubuntu/Debian install (Docker, user, dirs, binary, systemd, firewall)
- [x] `deploy/openplexd.service` (hardened systemd unit)
- [x] Licence and public docs: core AGPL-3.0-only (`LICENSE`) + OpenPlex Extension Exception draft
      (`LICENSE-EXCEPTION.md`, to be reviewed by a lawyer), `TRADEMARKS.md`, `SECURITY.md`, `CONTRIBUTING.md`
      (no code contributions yet; CLA when opened), README and `docs/` (installation, hosting providers,
      licensing) in en/es/ca
- [ ] Image mode for providers (prepare a template without initialising; first boot generates secrets),
      one-time sign-in link, signed releases + update channel, published API reference
- [x] Release build (`linux/amd64`, `linux/arm64`) + checksums: `.github/workflows/release.yml` builds a draft on a
      `v*` tag; `scripts/sign-release.sh` signs `SHA256SUMS` offline (key `avakiam-release-2026`) and publishes
      (see `RELEASING.md`); CI runs `make check` + the dashboard build on every push
- [x] One-click updates (`internal/update`, v0.18.0): stable release check twice a day, signature + checksum
      verification, install through a transient systemd unit (`openplexd update apply`) with binary and database
      backup, confirmation by the new version and automatic rollback; **Updates** page, audit entries
- [x] Verified end-to-end on the Linux test server and on a clean public VPS

---

## M16 — Parity with Plesk/cPanel (done in this order)
- [x] SFTP access to website files (`internal/sftpd`, v0.20.0, migration v16): built into openplexd on :2222
      (`OPENPLEX_SFTP_LISTEN`), panel username + one of the user's SSH keys or an SFTP-only password (must differ
      from the panel one); the root lists the websites whose files the user may manage (owners, managers, grants
      with Files; read-only grants stay read-only), every operation through an os.Root on the website, new files
      owned by the site uid, no setuid bits, no links, no moves across websites, no shell or exec. Failed sign-ins
      are logged for the Fail2Ban openplex jail and refused after 10 per address in 10 minutes; logins audited.
      Port opened by the panel firewall (service port) and by the installer in ufw; "SFTP access" card on the
      API & SSH keys page with host, port, user and host-key fingerprint, and a warning with the `ufw allow` command
      when ufw is active and does not let the port in (v0.20.1; the panel cannot edit ufw, /etc is read-only to it)
- [x] PHP settings per website (v0.20.0, migration v17): memory_limit (up to the website's memory, clamped if
      that is lowered later), upload_max_filesize, post_max_size (≥ upload), max_execution_time, max_input_time,
      max_input_vars, session.gc_maxlifetime, display_errors, allow_url_fopen, date.timezone, disable_functions;
      allowlisted and validated (no other ini keys, no injection), only changes from the defaults stored. Written by
      the panel to `<data>/php/<id>/zz-site.ini` (root-owned) and mounted read-only with `PHP_INI_SCAN_DIR`, so
      PHP-FPM, cron and the terminal all use it; saving recreates the container. Websites without settings keep
      the same container spec (no restart on upgrade). Card in the website's Settings tab; user guide
      `docs/*/websites.md` (sites, applications, PHP settings, SFTP)
- [x] E-mail alerts (`internal/alerts`, v0.21.0, migration v18): failed backup, website down (>= 2 min in error),
      data disk >= 90 % (clears < 85 %), certificate of a served name with < 1/6 of its lifetime left (Caddy renews at
      1/3), new release, update rolled back/failed. Checked every minute; ongoing problems sent once, reminded every
      24 h and reported when over; state in the `alerts` table so restarts don't resend; 90 days of history.
      Up to 10 recipients, e-mails in en/es/ca, sent through the server's mail service (sendmail in the mail
      container) or an SMTP server (STARTTLS/TLS/none, password sealed); header-injection-proof messages, test
      e-mail, Alerts page for administrators
- [ ] Manage installed applications: update core and plugins/extensions (WP-CLI, Joomla/Drupal CLIs),
      clone to a staging subdomain and push back, security hardening checks
- [ ] Git deploy: clone a repository into a website (deploy key), pull on a webhook or by hand, optional
      build command

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
- 2026-11-10 — Clean public VPS (OVH, Ubuntu 26.04.1, 2 vCPU, 3.7 GiB): `install.sh --binary …
  --panel-domain <vps hostname>` installed Docker 29.9.3 + OpenPlex in 26 s. Caddy obtained real
  Let's Encrypt certificates on its own for the panel and two sslip.io sites; HTTP → 308 HTTPS.
  PHP 8.4 image built in 5 min on 2 vCPU; PHP 8.4.27 site talked to MariaDB 11.8 over PDO with the
  password injected as an env var. First automatic backup ran. From the internet only 22/80/443
  answer (2019, 3306, 5432, 6379, 7080, 7081 closed; ufw active); bare-IP HTTPS gets no certificate.
- 2026-11-10 — Languages on the real server (v0.6.0, migration v4 applied on both servers): a Spanish
  browser got the login page and panel in Spanish automatically (`2,6%`, `2,2 GB`, `4 d 1 h`); switching
  to Català from the header translated the whole panel at once and was saved on the account;
  "Automatic" cleared it again. 436 strings × 2 languages, 113 server messages.
- 2026-11-10 — Core fixes + scheduled tasks on the real server (v0.7.0): PHP site task ran as uid 100002
  with PHP 8.3 in /srv/sites/<site>; a 10 s limit stopped `sleep 300 & sleep 301` and left no process
  behind; a second run while busy → 409; static-site task ran in a temporary container (git 2.49) that was
  removed; an every-minute task fired at :00 on two consecutive minutes; a failing Laravel task showed
  "Failed" with its real output in the UI. A Node app that exits at start went to "error" ("keeps crashing")
  within a minute; unknown Host → 404, known sites unaffected; after deleting site 4 the next site got id 5.
- 2026-11-10 — Firewall + Fail2Ban on the LAN server (v0.8.0, migration v7): the installer kept ufw (active), installed
  Fail2Ban 1.1.0 with the `sshd` and `openplex` jails; first start imported ufw's 7 allow rules with the OpenPlex
  firewall off. Turning it on without port 3000 from the panel (Spanish UI): nftables 1.1.6 accepted the table, a test
  client in a network namespace saw 3000 closed while 22, 8734 and the websites on 80 stayed reachable, and a new SSH
  session got in; left unconfirmed, the previous rules came back on their own ~60 s later and 3000 answered again.
  `fail2ban-regex` matched a failed panel login in the journal (127.0.0.1 is ignored, as configured).
  Then: "Keep" held past 60 s and across a panel restart; blocking 10.99.0.2 for 1 h from the panel cut SSH, other
  ports, the website on 80 and ping, and unblocking restored them; blocking the admin PC (open SSH tunnel) was refused
  with the translated message; 7 SSH attempts with unknown users got 10.99.0.2 banned by Fail2Ban (drop in prerouting,
  website unreachable too) and "Unban" in the panel lifted it. Server left as found (firewall off, ufw on). Timed
  block expiry is covered by unit tests only.
- 2026-11-10 — DNS on the public VPS (v0.9.0, migration v8) with a real domain: the installer upgrade also brought
  nftables + Fail2Ban (firewall left off, ufw active). PowerDNS 5.0.8 answered on 167.114.71.160 and the public IPv6
  (systemd-resolved untouched); from outside: SOA/NS/A/AAAA/www/glue correct, `aa` set, UDP and TCP, other domains
  refused, version hidden; a re-sync without changes kept the SOA serial, adding a TXT published it in seconds
  (serial +1). At Hostinger: child nameservers ns1/ns2.mailcactus.com → 167.114.71.160, then the domain's
  nameservers changed to them; Verisign served the new delegation + glue and 1.1.1.1, 8.8.8.8 and 9.9.9.9 resolved
  mailcactus.com through the VPS. Caddy then got Let's Encrypt certificates for mailcactus.com and
  www.mailcactus.com (www → 308 to the apex keeping path and query). The panel showed the zone as delegated once the
  check asked the .com servers instead of OVH's caching resolver. DNS tab tested in the UI on the LAN server
  (MX added; a bare "mail" target refused with the translated message).
- 2026-11-10 — Mail on the public VPS (v0.10.0, migration v9) with mailcactus.com: outbound port 25 open at OVH.
  Turning mail on with host mail.mailcactus.com started docker-mailserver healthy in ~5 s; Caddy issued the mail host's
  Let's Encrypt certificate and the panel copied it in without a restart: ports 993, 465, 587 and 25 (STARTTLS) all
  present it. Enabling mail published MX 10 mail.mailcactus.com, `v=spf1 mx ~all`, the DKIM key and DMARC p=none.
  Tests from outside the container: inbound mail delivered (a bare test message from an unauthenticated host was
  filed in Junk by Rspamd), a sender with a null MX refused, relaying to gmail.com refused, no AUTH offered without
  TLS, a wrong password refused, sending as another address of the domain refused, authenticated submission on 587
  delivered with `DKIM-Signature: d=mailcactus.com`; quota usage read from Dovecot. Mail tab tested in the UI on the LAN
  server (turn on, generated password, create, turn off removing only the mail records; the user's own MX was kept).
  Reverse DNS of the IPv4 and IPv6 set to mail.mailcactus.com at OVH (1.1.1.1 and 8.8.8.8 answer it at once; the
  host's own OVH resolver keeps the old PTR for its 24 h TTL, so the panel's PTR check turns green a day later).
  mail-tester.com: 10/10 — SPF pass, DKIM pass (2048-bit, s=opx), DMARC pass, rDNS and HELO match, no blocklists.
- 2026-11-10 — Webmail and login captcha on the VPS (v0.11.0, migration v10). Upgrading recreated the mail server on
  the new internal network and started Roundcube 1.7.6; saving the mail settings added webmail.mailcactus.com
  (CNAME) and Caddy issued certificates for both webmail hosts. The master login is refused from the Internet on 993
  and accepted from the mail network, where Postfix still refuses sending as another address. One-click link from
  the panel → straight into the inbox; replaying the same link is refused ("expired"). Captcha with Cloudflare's
  Turnstile test keys: saving with an always-failing secret or without a test token refused; panel login without a
  token refused, with one accepted; the webmail login says "please confirm that you are not a robot" without it and
  checks the password with it; the one-click link skips it. Widgets render (in Spanish) on both login pages and in
  the settings tab. Test keys removed afterwards.
- 2026-11-10 — Mail backups on the VPS (v0.12.0). "Back up now" on mailcactus.com's mail made a `mail:3` snapshot.
  Emptied the Junk folder and received a new message, then restored prueba@: the deleted message came back, the new
  one stayed (INBOX 3, Junk 1); restoring again put back 0 messages (no duplicates). The nightly "all" backup now
  holds 6 items: sites, database, `mail:3`, `webmail` (Roundcube DB) and the panel.
- 2026-11-10 — Disaster recovery rehearsal (v0.13.0): the VPS repository and recovery key copied to an isolated
  Docker-in-Docker "new server" on the LAN machine. `restore-panel` found the panel backup and restored it; on start
  the daemon pulled the images, reconciled and restored 6 items with no errors. The three websites answer with
  byte-identical pages, prueba@mailcactus.com has the same messages (INBOX 3, Junk 1), the webmail login works and
  the database exists with its user. Found and fixed on the way: restore took a lock on the read-only repository
  (now --no-lock), a fresh MariaDB had no user for the restored database (reconcile now provisions), restored
  webmail/mail files lost their mode (now kept). Rehearsal environment and copies deleted afterwards.
- 2026-11-10 — Access grants on the LAN server (v0.14.0, migration v11). Created a test customer, shared node.lan.test
  (files + mail) and, from the Users page dialog, php.lan.test (web developer preset, read-only). As that customer:
  both sites listed as "shared with you"; node.lan.test shows only Overview, Files, Mail and Terminal with no
  actions; php.lan.test shows its areas without terminal or actions; the API answered 403 for DNS, redirects,
  settings and deletion and 403 for /audit. Test account deleted afterwards (its grants went with it).
- 2026-11-10 — Security fixes (v0.15.0) on the LAN server and the VPS. Upgrade recreated the edge without tenant
  files, rebuilt PHP 8.3/8.4 with Caddy and built `openplex/static`; all sites answered 200. With a throwaway
  customer + PHP site on the LAN: other accounts' containers don't resolve and are unreachable by IP; MariaDB,
  PostgreSQL and the edge are reachable; symlinks to `/data/caddy`, another site and `secret.key` answer 404; PHP
  sees HTTPS=on/443 and the visitor IP behind the edge; raising limits 403; `co.uk` 409; unused network pruned.
  Adminer: 403 without a session or reusing a ticket, panel ticket → logged in, `driver=server&driver=sqlite` 403;
  grant stored as `admin\_sectest`. VPS: panel sends CSP + HSTS, Adminer 403 anonymously, webmail and captcha
  load; a matching From: is DKIM-signed, a foreign From: is not.
- 2026-11-11 — Extensions (v0.16.0, migration v12) on the LAN server with `examples/extension-hello`. Unsigned
  upload → review shows no signer → install: running as uid 400001, read-only rootfs, CapDrop ALL, 32 MB, socket
  0600 owned by that uid. Admin page rendered with the domain list and a CSP sandbox header; a forged session URL
  403; the website tab listed static.lan.test's DNS. Settings change, domain create/delete → events delivered;
  extension network reaches the internet but not tenant sites or MariaDB. Disable removes the container, enable
  restarts it; upgrade with the signed package shows `avakiam-2026` and keeps settings; uninstall removes the
  container, network and data; audit entries recorded.
- 2026-11-11 — First official extension, Cloudflare (`avakiam.cloudflare` 1.0.0, private repo), on the LAN server
  with v0.16.1: signed package (amd64 + arm64 images, no source) installed and upgraded; runs as uid 400002,
  64 MB; admin page and website tab render in en/es/ca, form POST → 303 → flash through the page proxy; outbound
  HTTPS to the Cloudflare API works (a bad token is explained in the user's language). Found and fixed in core:
  reinstalling a package rebuilt under the same image tag kept the old container (the image ID is now part of the
  spec), and `ext pack -o` wrote its temporary file in the working directory.
- 2026-11-11 — Edge policies (v0.17.0, migration v13) and Cloudflare extension 1.1.0 on the VPS. Real visitor IP:
  the edge got Cloudflare's 22 networks as trusted proxies (downloaded by the extension); a forged
  CF-Connecting-IP from elsewhere is ignored. A restriction set through the extension socket gave 403 to a direct
  IPv4 visit from outside, kept other sites at 200 and was removed cleanly; a host that isn't a website is refused;
  every change is audited. "Only through Cloudflare" on mailcactus.com (zone pending) restricts nothing and shows
  why. Found: IPv6 visits reach the edge through Docker's proxy with the gateway's address (the edge network has
  no IPv6), so restrictions never block the gateway — IPv6 visits can't be filtered yet.
- 2026-11-11 — IPv6 edge network (v0.17.1) on the VPS (Docker 29.9). The upgrade rebuilt `openplex_edge` with
  `fd39:ff54:8444::/64` and its old 172.18.0.0/16, recreated the edge and reconnected the webmail in ~4 s; every
  site, the www redirects and the webmail answered on IPv4 and IPv6 after it (the edge still reaches Roundcube over
  IPv4, which its proxy whitelist trusts). Visits to the public IPv6 now log the real address (Mythic Beasts'
  IPv6 checker as `2a00:1098:0:86:1000:1a:0:1`) instead of 172.18.0.1. A restriction set through the extension
  socket gave 403 to direct IPv4 and IPv6 visits and to the external IPv6 checker; only the host's own requests
  to localhost, which docker-proxy forwards from the gateways (172.18.0.1, fd39:ff54:8444::1), pass — so the
  gateway exemption stays. Found: DNS (53) and SMTP (25) don't answer over IPv6 from outside — their networks have
  no IPv6, so docker-proxy receives them through the host input chain, where ufw doesn't allow those ports.
- 2026-11-11 — Panel updates (v0.18.0, migration v14) on the LAN server against a local fake GitHub serving releases
  signed with the real release key: 0.18.0 → 0.18.1 from the API in 6 s (backup of binary and database, new
  version confirmed, outcome audited); a broken 0.18.2 (reports its version, refuses to start) was rolled back to
  0.18.1 after 3 minutes with its database, and the panel showed why. The installer's OpenSSL check accepts the
  real signature and rejects a modified SHA256SUMS.
- 2026-11-11 — Application installer on the LAN server (0.18.90): WordPress 7.1.4 (Spanish language pack, admin
  login and pretty permalinks checked), Joomla 6.2.0 and Drupal 11.4.12 (Catalan) installed through the API, each
  site answering through the edge; a second install and a non-empty document root were refused; WordPress first
  failed (no full es_ES package of 7.1.4 yet) and left an empty document root and no database behind, so it now
  installs the language pack after the core. Also created from the dashboard as a customer.
- 2026-11-11 — SFTP on the LAN server (0.19.90) from Windows OpenSSH `sftp` with a key added in the panel: root
  lists the 3 websites, upload/mkdir/rename/download/delete in php.lan.test (files owned by the site uid 100002),
  chmod 4755 stored as 0755; `../../../etc/passwd`, writing at the root, a new top-level folder and a symlink
  refused; the host-key fingerprint matched the panel; login audited.
- 2026-11-11 — PHP settings on the LAN server (0.19.91): the upgrade left the 3 site containers untouched; through
  the API 4 bad inputs refused (memory above the site's 512 MB, post below upload, auto_prepend_file, a newline
  injection); saved values reached PHP-FPM (probe page through the edge) and the CLI as the site user (300M,
  Europe/Madrid); the site user cannot write the settings file; reset removed the file and restored 256M/UTC.
  Dashboard checked: SFTP card, SFTP password created from it and used by a client (wrong password logged in the
  Fail2Ban format), PHP settings card and its error message.
- 2026-11-11 — v0.20.0 published and installed on the LAN and the VPS with the panel updater. The VPS uses ufw, which
  kept 2222 closed (an update from the panel does not run the installer): opened with `ufw allow 2222/tcp`, both
  servers then answered `SSH-2.0-OpenPlex` from outside; the SFTP card now warns about this case.
- 2026-11-11 — E-mail alerts (0.20.91): on the VPS a test e-mail reached prueba@mailcactus.com both through the local
  mail service and over SMTP with STARTTLS (the first SMTP try was refused for HELO "localhost": the client now
  introduces itself with the server name; local messages are not DKIM-signed, the page no longer says they are);
  on the LAN (no mail service) the test failed with a clear message, a website kept in error raised "Web caída"
  after 2 minutes with the send error recorded, and it was resolved when the site came back; settings
  validation refused an empty recipient list, a CRLF in an address and a bad SMTP host. Alerts page checked.
