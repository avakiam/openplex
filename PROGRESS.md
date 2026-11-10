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
- [x] Views: Firewall (rules with apply/keep/undo, blocked addresses, Fail2Ban), scheduled tasks
- [x] Views: DNS (admin page + DNS tab per website)
- [x] Views: Mail (admin page + Mail tab per website)
- [x] Interface languages: English, Spanish, Catalan — browser detection, switcher in the header and on
      the login page, per-user preference stored on the account (`users.locale`, `PATCH /auth/me`, set by
      managers too), locale-aware numbers/dates, fixed API error messages translated; the build fails on a
      missing translation (`scripts/check-i18n.mjs`)
- [ ] Turn dynamic API messages (quotas, overlaps, invalid JSON) into fixed, translatable strings
- [ ] Trim the embedded Monaco bundle (~25 MB of the 41 MB binary) to the languages we need

## M14 — Installer & packaging
- [x] `scripts/install.sh`: unattended Ubuntu/Debian install (Docker, user, dirs, binary, systemd, firewall)
- [x] `deploy/openplexd.service` (hardened systemd unit)
- [ ] Release build (`linux/amd64`, `linux/arm64`) + checksums
- [x] Verified end-to-end on the Linux test server and on a clean public VPS

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
