# ADR 0001 — Extension system

Status: accepted (2026-11-11). Implemented in v0.16.0.

## Context

OpenPlex is open core: the panel is AGPL-3.0, and Avakiam (and anyone else)
publishes extensions, free or paid, closed or open. The
[Extension Exception](../../LICENSE-EXCEPTION.md) lets an extension use any
licence as long as it talks to OpenPlex only through the documented
**Extension Interface**. That interface must therefore be a clear process
boundary, and it must be safe: an administrator installs extensions from the
marketplace or uploads their own, and a bad extension must not be able to
take over the server or other tenants.

## Decision

### An extension is a container plus a manifest

A package (`.opx`) holds a manifest (`openplex-extension.json`) and the
extension's container image, either as a registry reference or as
`docker save` archives per architecture. OpenPlex runs one container per
installed extension (`opx-ext-<id>`):

- its own bridge network (internet access, nothing else of OpenPlex on it);
- a dedicated uid, all capabilities dropped, `no-new-privileges`, read-only
  root filesystem, `/tmp` tmpfs, memory/CPU/pids limits from the manifest
  (capped), persistent `/data`;
- no Docker socket, no access to tenant files.

### Talking to OpenPlex: the panel socket

OpenPlex mounts a unix socket at `/run/openplex/panel.sock` in the
container. It serves the normal HTTP API (`/api/v1/...`). Requests on it are
authenticated **by the socket itself** as that extension — no token to leak —
and are limited to the **permissions** the manifest declares and the
administrator accepted (`domains:read`, `dns:write`…). By default the
extension acts with administrator reach within those permissions; with the
header `X-OpenPlex-User: <id>` it acts as that panel user, so the user's own
access rules apply too. `GET /api/v1/extension` returns the extension's
settings, licence key and the URL prefix of its pages.

### Talking to the extension: HTTP on its port

The extension listens on `$PORT` (manifest `backend.port`). OpenPlex calls:

- `POST /openplex/events` with events it subscribed to (`domain.created`,
  `domain.deleted`, `dns.changed`, `settings.changed`, `installed`);
- any path under its pages, proxied for signed-in users (below).

### User interface: proxied pages in a sandboxed frame

The manifest declares **pages** (in the Extensions section of the menu) and
**site tabs** (on a website's page), with the roles that see them and, for
site tabs, the website area a shared user needs. The dashboard shows them in
an `<iframe>`. Opening one creates a short session; the frame loads
`/ext/<id>/s/<session>/<path>`, which OpenPlex proxies to the container,
adding `X-OpenPlex-User`, `-Username`, `-Role`, `-Domain`, `-Lang`, `-Theme`
and `-Base`. Responses get `Content-Security-Policy: sandbox …` (no
same-origin), so extension pages run in an opaque origin and cannot reach the
panel's session even though they are served from the panel's host.

### Packages, signatures and the marketplace

`.opx` = tar of `package.tar.gz` and an optional `signature.json`
(Ed25519 over the SHA-256 of `package.tar.gz`, with a key id).
`openplexd ext pack` builds and signs packages. Packages signed with a key
trusted by the binary (Avakiam's) show as **verified**; others can be
uploaded by an administrator after an explicit warning. The marketplace
(`OPENPLEX_MARKETPLACE_URL`) serves a catalogue and signed packages; paid
packages are downloaded with the customer's licence key, which OpenPlex
stores (encrypted) and hands to the extension, which may check it with the
marketplace.

### Administration

Only administrators install, update, configure, enable/disable and remove
extensions. Settings are declared in the manifest (text, secret, boolean,
select) and stored encrypted; secrets are never sent back to the browser.
Removing an extension deletes its container, network, socket and data.

## Consequences

- Any language can be used for an extension (it is just an HTTP server in a
  container), and the licence boundary is a process boundary.
- Extensions cannot change OpenPlex internals beyond the API; new extension
  needs mean new API endpoints or events in the core.
- Extension data is not yet part of panel backups.
