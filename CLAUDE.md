# CLAUDE.md — OpenPlex agent instructions

Read `AGENTS.md` (roles, architecture rules, effort routing) and `PROGRESS.md` (what is done)
before changing anything. Keep this file and `GEMINI.md` identical except for the title line.

## Build & test

Linux / macOS with Go ≥ 1.26 and Node ≥ 22:

```bash
make check          # go vet + go test + CGO_ENABLED=0 build
make build          # builds web/ (static export) then bin/openplexd with the UI embedded
make backend        # Go only (UI falls back to a "dashboard not built" page)
make web            # Next.js static export → copied into internal/ui/dist
make run            # runs ./bin/openplexd with data dir ./.data and listen :7080
```

No local Go toolchain (e.g. the Windows dev box)? Use the toolchain image — identical results:

```bash
docker run --rm -v "$PWD":/src -v openplex-gomod:/go/pkg/mod -w /src golang:1.26-bookworm make check
```

Dashboard development (hot reload; proxies `/api` to a running openplexd, WebSockets go direct):

```bash
cd web && OPENPLEX_API=http://127.0.0.1:7080 NEXT_PUBLIC_WS_HOST=127.0.0.1:7080 npm run dev   # http://localhost:3100
```

Run the daemon against the local Docker engine (smoke test):

```bash
docker run --rm -it -p 7080:7080 -v /var/run/docker.sock:/var/run/docker.sock \
  -v "$PWD":/src -v openplex-gomod:/go/pkg/mod -w /src golang:1.26-bookworm \
  go run ./cmd/openplexd --data-dir /tmp/opx --listen :7080
```

Production install (Ubuntu 22.04+/Debian 12+, root): `scripts/install.sh` (unattended; see
`--help`). Service: `systemctl status openplexd`, logs: `journalctl -u openplexd -f`.

## Directory layout

```
cmd/openplexd/        main: flags, bootstrap, graceful shutdown
internal/config/      runtime config (flags + OPENPLEX_* env)
internal/db/          SQLite open (WAL), migrations, repositories (one file per aggregate)
internal/auth/        password hashing, JWT access tokens, refresh tokens, TOTP, API keys
internal/api/         chi router, middleware (auth, RBAC, logging), JSON handlers, WebSockets
internal/docker/      Docker SDK orchestrator: networks, edge, site runtimes, reconcile, stats
internal/ssl/         Caddy edge config rendering + admin-API reload (ACME is Caddy's job)
internal/files/       jailed filesystem ops for the web file manager
internal/firewall/    host firewall (own nftables table inet openplex, apply/keep/rollback) + Fail2Ban client
internal/monitor/     host + container telemetry sampling
internal/dns/          authoritative DNS: zones/records in the panel DB, pushed to a PowerDNS container via its API
internal/mail/         mail domains, mailboxes, aliases, DKIM → docker-mailserver files + DNS records
internal/backup/ terminal/   (see PROGRESS.md for status)
internal/ui/          go:embed of the built dashboard (dist/)
web/                  Next.js App Router dashboard (static export)
scripts/install.sh    unattended installer      deploy/   systemd unit, sample config
```

Runtime data dir (default `/var/lib/openplex`): `openplex.db`, `secret.key`,
`initial-admin-password`, `sites/<domain>/{public,logs}`, `caddy/{Caddyfile,data,config}`.

## Go conventions

- Go 1.26, module `openplex.dev/openplex`. `gofmt`/`go vet` clean. No CGO.
- Router: `go-chi/chi/v5`; WebSockets: `github.com/coder/websocket`; JWT: `golang-jwt/jwt/v5`;
  TOTP: `pquerna/otp`; Docker: `github.com/docker/docker/client` (moby Engine SDK);
  telemetry: `shirou/gopsutil/v4`.
- `context.Context` is the first param of anything doing I/O. No package-level mutable globals.
- Repositories take/return plain structs from `internal/db`; handlers never write SQL.
- Errors: `fmt.Errorf("doing x: %w", err)`; sentinel `db.ErrNotFound` → HTTP 404.
- JSON: snake_case fields; times as RFC 3339 UTC; IDs are int64.
- Logging: `log/slog` (JSON in production). Never log secrets or tokens.
- Tests: table-driven, `_test.go` beside the code, use `db.OpenMemory(t)` for DB tests.

## TypeScript conventions (web/)

- Next.js App Router, `output: "export"`, strict TS, Tailwind + shadcn/ui, Lucide icons.
- Data fetching only via TanStack Query hooks in `web/lib/api/*`; one hook file per resource.
- API client attaches the access token and transparently refreshes on 401.
- No business rules in the UI; the API is authoritative for RBAC and validation.
- i18n (en / es / ca): every visible string goes through `t("English text", { vars })` from
  `web/lib/i18n.tsx` (`useT()` in components, `tk()` to mark module-level constants). Keys are the
  English source; translations live in `web/locales/{es,ca}/<area>.json` (plural forms as objects,
  English plurals in `web/locales/en`). `npm run build` runs `scripts/check-i18n.mjs` and fails on a
  missing translation. API error messages stay English in Go; fixed ones are translated in
  `web/locales/*/server.json`. Code, comments and commits are always English.

## Context-saving rules for agents

- Don't re-read files you just wrote; don't dump whole files — read the range you need.
- Look up status in `PROGRESS.md` rather than re-exploring the tree.
- Follow an existing sibling file (e.g. `db/domains.go`, `api/domains.go`) instead of inventing patterns.
- One milestone → tick `PROGRESS.md` → one commit. Never leave work uncommitted at the end of a session.
- Never add `TODO` comments or stub handlers; unfinished features are simply not routed.
