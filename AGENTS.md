# AGENTS.md — OpenPlex multi-agent operating manual

OpenPlex (`openplex.dev`) is an open-source, container-native web hosting control panel
(a Plesk alternative). It ships as **one static Go binary** (`openplexd`) that embeds the
Next.js dashboard, keeps its state in embedded SQLite, and drives everything else
(edge proxy, site runtimes, databases, mail, DNS) through the Docker Engine API.

This file is engine-agnostic. `CLAUDE.md` and `GEMINI.md` hold the exact commands and
conventions; `PROGRESS.md` is the single source of truth for what is done.

---

## 1. Roles

Every change is owned by exactly one role. When a task spans roles, the Architect splits it.

| Role | Owns | Typical output |
|---|---|---|
| **Architect** | `AGENTS.md`, `PROGRESS.md`, package boundaries, DB schema, API contracts, cross-cutting decisions | ADR-style notes in `docs/adr/`, migration plans, interface definitions |
| **Core-Go-Daemon** | `cmd/openplexd`, `internal/{api,auth,config,db,files,monitor,terminal,backup}` | Handlers, repositories, migrations, middleware, tests |
| **Docker-Orchestrator** | `internal/{docker,ssl,dns,mail}`, container images/templates, edge (Caddy) config | Provisioning code, image pins, Caddyfile templates, reconciliation loops |
| **NextJS-UI** | `web/` | App Router pages, shadcn/ui components, TanStack Query hooks, xterm.js/Monaco integration |
| **SecOps-QA** | `scripts/`, `deploy/`, security review, test plans | `install.sh`, systemd units, firewall rules, threat reviews, integration tests on the real server |

## 2. Architecture rules (non-negotiable)

1. **Single binary.** No runtime dependency on an external DB, Node, or interpreter on the host.
   The only host requirement is Docker Engine + systemd.
2. **CGO-free.** `CGO_ENABLED=0` must always build. SQLite = `modernc.org/sqlite`.
3. **SQLite in WAL mode**, `foreign_keys=ON`, `busy_timeout=5000`. Schema changes only via
   numbered migrations in `internal/db/migrations.go` — never edit an applied migration.
4. **The panel DB is the source of truth.** Docker/Caddy state is *derived* and must be
   reproducible by a reconcile pass (`docker.Orchestrator.Reconcile`). Never store
   authoritative state only in container labels.
5. **Everything we create in Docker carries labels** `openplex.managed=true` and
   `openplex.kind=<edge|site|db|mail|dns>` (+ `openplex.domain_id` where relevant).
   OpenPlex must never touch unlabelled containers.
6. **Tenant isolation.** One container per site; all site containers join only the
   `openplex_edge` network; databases join a per-owner internal network. Memory/CPU limits are
   always set. Containers run with `no-new-privileges` and dropped capabilities where the image allows.
7. **RBAC is enforced in the API layer**, never in the UI. Every handler that takes an ID
   resolves ownership through `api.authorize*` helpers before acting.
8. **Secrets** (passwords, API keys, refresh tokens) are stored hashed (bcrypt for passwords,
   SHA-256 for high-entropy tokens). Only TOTP secrets and generated DB/mail passwords that must
   be recoverable are stored, and only in the 0600 data directory.
9. **File access is jailed.** All file-manager operations go through `internal/files.Jail`,
   which resolves symlinks and rejects any path escaping the site root.
10. **No placeholders.** No `// TODO`, no mock handlers, no fake data in shipped code. If a
    feature is not finished it is not routed and stays `[ ]` in `PROGRESS.md`.
11. **Errors**: wrap with `fmt.Errorf("context: %w", err)`; API responds with
    `{"error": "<code>", "message": "<human text>"}` and never leaks internal errors for 5xx.

## 3. Effort routing rules

Use reasoning effort proportional to blast radius, to keep token spend efficient.

**HIGH effort** — think carefully, write tests, request review from SecOps-QA:
- System architecture, package boundaries, DB schema & migrations
- Concurrency (goroutines, reconcile loops, WebSocket fan-out, locking)
- Security: auth, JWT/refresh rotation, 2FA, RBAC checks, path jailing, shell/terminal,
  firewall, container privileges, installer
- Debugging production/live-server failures

**MEDIUM effort** — follow existing patterns, keep it tight:
- Standard CRUD endpoints and repository methods that mirror an existing one
- UI components, pages, forms, table views
- Boilerplate, docs, config templates, image version bumps

When in doubt: if a bug would let one tenant affect another or the host, it is HIGH.

## 4. Workflow

1. Pick the first unchecked item in `PROGRESS.md` for your role.
2. Implement it fully (code + migration + route + UI where applicable).
3. `make check` (vet + test + build) must pass.
4. Tick the box in `PROGRESS.md` and make **one atomic commit** per milestone:
   `feat(<pkg>): <what>` / `fix(<pkg>): …` / `docs: …`.
5. Host-level features (systemd, ufw/nftables, Fail2Ban, installer) are verified on the
   real Linux test server over SSH before being ticked.
