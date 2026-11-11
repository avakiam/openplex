# Building an extension

**English** · [Español](../es/extensions.md) · [Català](../ca/extensions.md)

An OpenPlex extension adds features to the panel: its own pages in the menu,
tabs on a website's page, settings, and reactions to what happens on the
server (a website is created, a DNS record changes…). Anyone can build one,
in any language, and distribute it free or paid, open or closed: see
[licensing](licensing.md).

A complete, copyable example lives in
[`examples/extension-hello`](../../examples/extension-hello) (0BSD licence:
use it as you like).

## How it works

- An extension is a **container** plus a **manifest**
  (`openplex-extension.json`), shipped as a package (`.opx`).
- OpenPlex runs the container with its own user, no capabilities, a
  read-only root filesystem and its own network (internet access, nothing
  else of the server). It keeps `/data` between restarts and upgrades.
- The extension listens for HTTP on `$PORT`. OpenPlex sends it **events**
  and proxies its **pages** to signed-in users.
- The extension calls the **OpenPlex API** through a unix socket,
  `$OPENPLEX_SOCKET` (`/run/openplex/panel.sock`), limited to the
  **permissions** the administrator accepted when installing it.

```
 browser ──▶ OpenPlex ──(pages, events: HTTP :$PORT)──▶ extension container
                ▲                                              │
                └────────── API over /run/openplex/panel.sock ─┘
```

## Quick start

```bash
cd examples/extension-hello
docker build -t openplex-examples/hello:1.0.0 .
mkdir -p images && docker save -o images/amd64.tar openplex-examples/hello:1.0.0
openplexd ext pack .            # → example.hello-1.0.0.opx
```

Then, in the panel: **Extensions → Upload extension**, review it, install.
"Hello" appears in the menu, and each website gets a "Hello DNS" tab.

## The manifest

```json
{
  "schema": 1,
  "id": "yourcompany.backups-s3",
  "name": { "en": "S3 backups", "es": "Copias en S3", "ca": "Còpies a S3" },
  "description": "Short description (a string is taken as English).",
  "version": "1.0.0",
  "publisher": { "name": "Your Company", "url": "https://example.com" },
  "homepage": "https://example.com/openplex",
  "openplex": ">=0.16.0",
  "permissions": ["domains:read"],
  "events": ["installed", "settings.changed", "domain.created"],
  "backend": {
    "image": "ghcr.io/yourcompany/backups-s3:1.0.0",
    "archives": { "amd64": "images/amd64.tar", "arm64": "images/arm64.tar" },
    "port": 8080,
    "memory_mb": 128,
    "cpus": 0.5
  },
  "settings": [
    { "key": "api_token", "type": "secret", "label": "API token", "required": true },
    { "key": "region", "type": "select", "label": "Region", "options": [{ "value": "eu", "label": "Europe" }] }
  ],
  "pages": [{ "id": "main", "title": "S3 backups", "path": "/", "roles": ["admin"] }],
  "site_tabs": [{ "id": "site", "title": "S3", "path": "/site", "area": "backups" }]
}
```

| Field | Meaning |
|---|---|
| `id` | Unique, lowercase, letters/digits/`.`/`-`. Prefix it with your name: `yourcompany.thing`. It can't change between versions. |
| `name`, `description`, titles, labels | A string (English) or `{"en": …, "es": …, "ca": …}`. The panel shows the user's language and falls back to English. |
| `version` | `MAJOR.MINOR.PATCH`. Installing another version of the same `id` upgrades it and keeps settings and `/data`. |
| `openplex` | Minimum panel version, `">=X.Y.Z"`. |
| `permissions` | What the extension may do through the API (table below). Shown to the administrator before installing. |
| `events` | Events it wants to receive (below). |
| `backend.image` | Image reference. If `archives` has a file for the server's architecture, OpenPlex loads it (`docker save` output, tagged with this exact reference); otherwise it pulls the image from its registry. |
| `backend.port` | Port the container listens on (1024–65535); also in `$PORT`. |
| `backend.memory_mb`, `cpus` | Limits; defaults 128 MB and 0.5 CPU, at most 1024 MB and 2 CPUs. |
| `settings` | Fields of the settings form: `text`, `secret` (never shown again), `boolean` (`"true"`/`"false"`), `select` (with `options`). `required`, `default`, `help` are optional. |
| `pages` | Pages in the menu, under *Extensions*. `roles`: `admin`, `reseller`, `customer` (default: admin only). |
| `site_tabs` | Tabs on a website's page. `roles` default to everyone who can open the website; `area` (`site`, `files`, `dns`, `mail`, `backups`) is required of users the website was shared with. |

## Permissions

| Permission | API requests allowed |
|---|---|
| `domains:read` | `GET /api/v1/domains`, `GET /api/v1/domains/{id}` |
| `dns:read` | `GET /api/v1/domains/{id}/dns` |
| `dns:write` | `POST /api/v1/domains/{id}/dns/records`, `PATCH` and `DELETE /api/v1/domains/{id}/dns/records/{rid}` |
| `mail:read` | `GET /api/v1/domains/{id}/mail` |
| `databases:read` | `GET /api/v1/databases`, `GET /api/v1/databases/{id}` |
| `users:read` | `GET /api/v1/users`, `GET /api/v1/users/{id}` |
| `system:read` | `GET /api/v1/system/info`, `GET /api/v1/system/stats` |

Always allowed: `GET /api/v1/extension`, `GET /api/v1/auth/me`,
`GET /api/v1/system/health`. Everything else answers `403`. Need something
else? Open an issue: new permissions are added to the core.

## Calling the API

Requests on the socket are already authenticated as your extension: there
is no token to store. Responses are the panel's normal JSON API.

```bash
curl --unix-socket "$OPENPLEX_SOCKET" http://openplex/api/v1/domains
```

```go
client := &http.Client{Transport: &http.Transport{
    DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
        return (&net.Dialer{}).DialContext(ctx, "unix", os.Getenv("OPENPLEX_SOCKET"))
    }}}
res, err := client.Get("http://openplex/api/v1/domains")
```

```js
// Node.js
http.get({ socketPath: process.env.OPENPLEX_SOCKET, path: "/api/v1/domains" }, (res) => { /* … */ });
```

```python
import http.client, json, os, socket

class Panel(http.client.HTTPConnection):
    def connect(self):
        self.sock = socket.socket(socket.AF_UNIX)
        self.sock.connect(os.environ["OPENPLEX_SOCKET"])

c = Panel("openplex")
c.request("GET", "/api/v1/domains")
domains = json.load(c.getresponse())
```

**Acting as a user.** By default the extension sees everything its
permissions allow, on every account. When you answer a user, add the header
`X-OpenPlex-User: <id>` (the id OpenPlex sends you with each page request):
the API then also applies that user's own access rules, so a customer only
gets their own websites.

**`GET /api/v1/extension`** returns your settings (secrets included), the
licence key, the panel version and your **token**:

```json
{ "id": "example.hello", "version": "1.0.0", "settings": { "greeting": "Hello" },
  "license_key": "", "token": "…", "panel_version": "0.16.0" }
```

## Requests from OpenPlex

Every request OpenPlex sends to your container carries
`X-OpenPlex-Token: <token>`. **Check it** (constant-time comparison) and
reject anything else.

### Events

`POST /openplex/events` with:

```json
{ "type": "domain.created", "time": "2026-11-11T08:17:11Z",
  "data": { "domain_id": 8, "name": "shop.example.com", "kind": "primary", "owner_id": 3 } }
```

| Event | `data` |
|---|---|
| `installed` | — (after installing or upgrading) |
| `settings.changed` | — (read the new values with `GET /api/v1/extension`) |
| `domain.created`, `domain.deleted` | `domain_id`, `name`, `kind`, `owner_id` |
| `dns.changed` | `domain_id`, `zone`, `change` (`created`/`updated`/`deleted`), `record_id` |

Answer with any 2xx status. On an error or 5xx OpenPlex retries a few times.
Delivery isn't guaranteed: if something must not be missed, also reconcile
from the API when you start.

### Pages and website tabs

When a user opens one of your pages, the dashboard shows it in a frame and
OpenPlex proxies the request to your `path`, adding:

| Header | Value |
|---|---|
| `X-OpenPlex-User`, `X-OpenPlex-Username`, `X-OpenPlex-Role` | Who is looking |
| `X-OpenPlex-Lang` | `en`, `es` or `ca` |
| `X-OpenPlex-Domain`, `X-OpenPlex-Read-Only` | Website tabs: the website's id, and whether the user may only look |
| `X-OpenPlex-Base` | URL prefix of your pages, e.g. `/ext/example.hello/s/…/` |

The page URL also gets `?lang=…&theme=light|dark`. Rules for pages:

- Use **relative links** (`href="detail"`, `action="save"`), or build them
  from `X-OpenPlex-Base`. Normal forms work (answer a POST with a `303`
  redirect). The page has an opaque origin, so `fetch` to your own paths is
  cross-origin: answer it with `Access-Control-Allow-Origin: *` (the session in
  the URL is what authorizes the request).
- Pages run **sandboxed** (no same-origin): no cookies, no `localStorage`,
  no access to the panel. Keep state on your server (`/data`), keyed by user.
- Respect `X-OpenPlex-Read-Only` on website tabs.
- Load your own CSS and scripts from your container; follow `theme`.

## Storage and limits

- `/data` — persistent, yours; kept across restarts and upgrades, deleted
  when the extension is removed.
- `/tmp` — 64 MB, cleared on restart. Everything else is read-only.
- Environment: `PORT`, `OPENPLEX_SOCKET`, `OPENPLEX_DATA`,
  `OPENPLEX_EXTENSION`, `OPENPLEX_EXTENSION_VERSION`.
- The container runs as an unprivileged uid chosen by OpenPlex (don't rely
  on `USER` in your Dockerfile) and is restarted if it stops.

## Packaging and signing

```bash
openplexd ext pack [-key FILE] [-o FILE] DIR
```

checks the manifest and the files it references and writes `<id>-<version>.opx`
(files starting with `.` are skipped). For each architecture you support,
build the image and `docker save` it into the path given in `archives`, or
publish the image in a registry and leave `archives` out.

**Signing.** Create a key once (`openplexd ext keygen -id yourcompany-2026 -o key.json`)
and pack with `-key key.json`. OpenPlex marks packages signed with the
OpenPlex project's key as *verified by OpenPlex*; any other package — unsigned
or signed with your own key — can still be uploaded by an administrator, who
is warned that OpenPlex hasn't checked it. Upgrades must keep the same `id`.

## The marketplace

Official extensions, free and paid, are installed from **Extensions →
Marketplace**. Paid extensions ask for the licence key received when buying
them; OpenPlex stores it and gives it to the extension (`license_key`), which
may check it with its vendor. To publish an extension on the OpenPlex
marketplace, contact us through an issue in the
[main repository](https://github.com/avakiam/openplex).
