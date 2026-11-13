# API reference

**English** · [Español](../es/api.md) · [Català](../ca/api.md)

Everything the dashboard does goes through the same REST API, so a billing
system, a script or your own portal can do it too. This page lists every
endpoint; a test in the source tree fails when the list and the server
disagree.

## Basics

- **Base URL:** `https://<panel domain>/api/v1` (or the panel's direct address,
  `http://127.0.0.1:7080/api/v1` by default).
- **Authentication:** create an API key on the **API keys** page (or with
  `POST /api-keys`) and send it on every request:
  `Authorization: Bearer opx_…`. A key acts as the account that created it,
  with that account's role and access: an admin's key can do everything, a
  customer's key only what that customer can. Keys can expire
  (`expires_in_days`) and are deleted on the same page.
- **Format:** JSON in and out, field names in `snake_case`, times in RFC 3339
  UTC, IDs as integers.
- **Errors:** a status code of 4xx or 5xx with
  `{"error": "not_found", "message": "resource not found"}`. `error` is a
  stable code to branch on; `message` is English text for people. Messages with
  values also carry `template` and `params`.
- **Roles:** sections marked *admin* answer `403` to other accounts. Elsewhere
  an account sees and changes only what it owns, or what was shared with it;
  anything else answers `404`.
- **WebSockets** (the `…/ws` endpoints): browsers can't send headers there, so
  first get a single-use ticket with `POST /auth/ws-ticket` and connect to
  `wss://…/ws?ticket=…`.
- **Sign-in without an API key:** `POST /auth/login` returns a short-lived
  access token (sent as `Bearer` like a key) and sets a refresh cookie for
  `POST /auth/refresh`. This is what the dashboard uses; integrations should
  use API keys.

Example: create a website and read its status.

```bash
curl -s -H "Authorization: Bearer $KEY" -H "Content-Type: application/json" \
  -d '{"name": "example.com", "runtime": "php", "runtime_version": "8.3"}' \
  https://panel.example.com/api/v1/domains
curl -s -H "Authorization: Bearer $KEY" https://panel.example.com/api/v1/domains/12
```

[Provisioning fields](#provisioning-fields) below details the bodies a billing
or provisioning system uses (accounts, websites, databases, mail, sharing, API
keys). For the rest, the bodies follow what the dashboard shows on each page;
the easiest way to see one is the browser's network tab while doing the same
thing in the dashboard.

## Provisioning fields

What a billing or provisioning system sends and gets back. In `PATCH`
requests every field is optional: only the ones sent change. Unknown fields
are refused (`400`). A limit of `0` means *no limit*.

Using WHMCS? The [OpenPlex module for WHMCS](https://github.com/avakiam/openplex-whmcs) does all of
this for you. A typical new customer: `POST /users` (with quotas), `POST /domains` with its
`owner_id`, `POST /domains/{id}/mail` and `POST /domains/{id}/mail/mailboxes`,
`POST /databases` with its `owner_id`. Afterwards:

- **Suspending** it (unpaid invoice): `POST /users/{id}/suspend`. The
  account can't sign in nor use its API keys, and its websites — a
  reseller's, its customers' too — go offline (status `disabled`; visitors
  get an unavailable page). `POST /users/{id}/unsuspend` gives each website
  back the status it had. (`PATCH /users/{id}` `{"disabled": true}` only
  stops signing in.)
- **Signing it in** from the billing system's client area:
  `POST /users/{id}/login-link` `{"expires_in_seconds"}` (optional: 300 by
  default, at most 86400) answers `201` with `url` (when the panel has a
  domain), `path` (`/login/?link=…`, to put after the panel's address) and
  `expires_at`. The link works once; add `&next=/domains/` to land on a
  given page. It signs in even accounts with two-factor authentication
  (whoever may create it could turn that off), and it doesn't let the
  account set a password without the current one.
- **Terminating** it: `DELETE /users/{id}?purge=true` deletes its websites
  (files, mail, DNS) and databases first, then the account; `409` if it
  still has customers. Without `purge`, an account that owns anything is
  refused (`409`).

### Accounts

`POST /users` (`201`, the account) and `PATCH /users/{id}`:

| Field | Type | |
|---|---|---|
| `username` | string | 3–32 characters: lowercase letters, digits, `_`, `.`, `-`, starting with a letter. Only on creation |
| `password` | string | at least 10 characters with letters and digits. Only on creation; later `POST /users/{id}/password` `{"password"}` |
| `email` | string | contact address (administrators also get the e-mail alerts there) |
| `role` | string | `customer` (default), `reseller` or `admin`. Resellers can only create customers |
| `parent_id` | integer | an admin creating a customer for a reseller: the reseller's ID. A reseller's customers get it automatically |
| `max_domains` | integer | websites the account (a reseller: all its customers together) may have; `0` = no limit |
| `max_databases` | integer | databases, the same way |
| `max_db_size_mb` | integer | default size limit of the account's databases (a database's own limit, `PUT /databases/{id}/limit`, overrides it); a reseller's customers stay within the reseller's |
| `locale` | string | dashboard language: `en`, `es`, `ca`, or `""` to follow the browser |
| `disabled` | boolean | `PATCH` only: `true` suspends the account (it can't sign in; its API keys stop working) |

The account as returned (`GET /users`, `GET /users/{id}`):

| Field | Type | |
|---|---|---|
| `id` | integer | |
| `username`, `email`, `role`, `parent_id`, `max_domains`, `max_databases`, `max_db_size_mb`, `locale`, `disabled` | | as above |
| `totp_enabled` | boolean | two-factor authentication is on |
| `created_at`, `updated_at` | time | |

### Websites

`POST /domains` (`201`, the website) and `PATCH /domains/{id}`:

| Field | Type | |
|---|---|---|
| `name` | string | host name, lowercase (`example.com`, `shop.example.com`). Only on creation |
| `kind` | string | `primary` (default: a website), `subdomain` (a website under a primary domain) or `alias` (another name for a website). Only on creation |
| `parent_id` | integer | for `subdomain` and `alias`: the website it belongs to |
| `owner_id` | integer | the account that owns it (default: the caller). Admins and resellers create websites for their accounts with it |
| `runtime` | string | `static` (default), `php`, `node`, `python` or `go` (`GET /runtimes` lists them) |
| `runtime_version` | string | for example `8.3` for PHP; default: the runtime's default |
| `document_root` | string | folder served, relative to the website's files (default `public`) |
| `start_command` | string | node, python and go: the command that starts the app |
| `app_port` | integer | node, python and go: the port the app listens on |
| `ssl_enabled` | boolean | HTTPS certificate (default `true`) |
| `force_https` | boolean | redirect HTTP to HTTPS (default `true`) |
| `memory_mb` | integer | memory limit of the website's container (default 512) |
| `cpus` | number | CPU limit (default 1) |
| `env` | object | environment variables for the app (`{"NAME": "value"}`) |

The website as returned also has `id`, `status` (`pending`, `running`,
`stopped`, `error`, `disabled`), `status_message`, `container_id`, `created_at` and
`updated_at`. Creating one answers at once; the container starts in the
background, so poll `GET /domains/{id}` until `status` is `running`.
`403` with `quota_exceeded` means the owner (or its reseller) reached
`max_domains`.

### Databases

`POST /databases` (`201`, the database):

| Field | Type | |
|---|---|---|
| `engine` | string | `mariadb`, `postgres` or `redis` (`GET /database-engines`) |
| `name` | string | the database name |
| `owner_id` | integer | the account that owns it (default: the caller) |

The database as returned: `id`, `owner_id`, `engine`, `name`, `username`,
`status` (`pending`, `running`, `error`), `status_message`, `max_size_mb`,
`size_locked`, `created_at`, `updated_at`. The password is not in it:
`GET /databases/{id}/credentials` answers `host`, `port`, `database`,
`username`, `password` and `url` (a connection string).
`PUT /databases/{id}/limit` `{"max_size_mb"}` sets its size limit (`0`: the owner's
`max_db_size_mb`).

### Mail

`POST /domains/{id}/mail` turns mail on for a website's domain (no body;
`201`, the mail domain: `id`, `domain_id`, `name`, `dkim_selector`). The DNS
records it needs are added to the domain's zone when OpenPlex serves its DNS;
`GET /domains/{id}/mail` lists them with the mailboxes and aliases.

`POST /domains/{id}/mail/mailboxes` (`201`) and
`PATCH /domains/{id}/mail/mailboxes/{mid}`:

| Field | Type | |
|---|---|---|
| `local_part` | string | the part before `@`. Only on creation |
| `password` | string | the mailbox password, 10 to 128 characters |
| `quota_mb` | integer | mailbox size, 10 MB to 100 GB (default 1024) |
| `enabled` | boolean | `PATCH` only: `false` stops it receiving and signing in |

`POST /domains/{id}/mail/aliases` (`201`) and
`PATCH /domains/{id}/mail/aliases/{aid}`:

| Field | Type | |
|---|---|---|
| `local_part` | string | the part before `@` |
| `destinations` | array | 1 to 50 addresses the mail is forwarded to |

Mailboxes and aliases come back with `id`, `mail_domain_id`, `created_at` and
`updated_at` besides the fields above (never the password).

### Sharing

`PUT /users/{id}/access` lets an account work on a website or database
someone else owns:

| Field | Type | |
|---|---|---|
| `kind` | string | `site` or `database` |
| `resource_id` | integer | the website's or database's ID |
| `areas` | array | websites only, at least one of `site` (settings, start/stop, logs, redirects, cron), `files` (file manager and terminal), `dns`, `mail`, `backups` |
| `read_only` | boolean | websites only: see but not change |

### API keys

`POST /api-keys` `{"name", "expires_in_days"}` (`0` = never, at most 3650)
answers `201` with `key` — the full key, shown only this once — and
`api_key`, its description: `id`, `user_id`, `name`, `prefix` (the start of
the key, to recognise it), `last_used_at`, `expires_at`, `created_at`.

## Endpoints

`{id}` and the other names in braces are IDs from the list endpoints.

### Sign-in and session (no authentication)

| Endpoint | What it does |
|---|---|
| `GET /system/health` | Liveness check: status and version |
| `POST /auth/login` | Sign in with username, password (and the 2FA code); returns an access token |
| `POST /auth/link` | Sign in with a one-time link from `openplexd login-link` |
| `GET /auth/captcha` | The login captcha's provider and site key, when one is set |
| `POST /auth/refresh` | New access token from the refresh cookie |
| `POST /auth/logout` | End the session |
| `POST /hooks/git/{id}/{token}` | Git push webhook for a website's deploys |

### Your account

| Endpoint | What it does |
|---|---|
| `GET /auth/me` | The signed-in account |
| `PATCH /auth/me` | Change your e-mail address, language and preferences |
| `POST /auth/password` | Change your password (within 30 minutes of a sign-in link, without the current one) |
| `POST /auth/2fa/setup` | Start setting up two-factor authentication (returns the secret) |
| `POST /auth/2fa/enable` | Turn two-factor authentication on with a code |
| `POST /auth/2fa/disable` | Turn it off |
| `POST /auth/ws-ticket` | Single-use ticket for a WebSocket |
| `GET /auth/me/sftp` | Your SFTP user name and address |
| `PUT /auth/me/sftp` | Set an SFTP password |
| `DELETE /auth/me/sftp` | Remove the SFTP password (keys only) |
| `GET /api-keys` | Your API keys |
| `POST /api-keys` | Create one (the key is shown only in this answer) |
| `DELETE /api-keys/{id}` | Delete one |
| `GET /ssh-keys` | Your SSH keys (SFTP and the terminal) |
| `POST /ssh-keys` | Add one |
| `DELETE /ssh-keys/{id}` | Remove one |

### Users

Admins manage every account, resellers their own customers.

| Endpoint | What it does |
|---|---|
| `GET /users` | Accounts you manage |
| `POST /users` | Create an account |
| `GET /users/{id}` | One account |
| `PATCH /users/{id}` | Change it (role, limits, disabled, …) |
| `DELETE /users/{id}` | Delete it (`?purge=true`: with its websites and databases) |
| `POST /users/{id}/password` | Set its password |
| `POST /users/{id}/suspend` | Suspend it: no sign-in, its websites offline |
| `POST /users/{id}/unsuspend` | Undo a suspension |
| `POST /users/{id}/login-link` | A one-time sign-in link for it (single sign-on) |
| `POST /users/{id}/2fa/reset` | Turn off its two-factor authentication |
| `GET /users/{id}/access` | Websites and databases shared with it |
| `PUT /users/{id}/access` | Share one more with it |
| `DELETE /users/{id}/access/{gid}` | Stop sharing one |

### Catalogs

| Endpoint | What it does |
|---|---|
| `GET /runtimes` | Website runtimes and their versions |
| `GET /apps` | Applications that can be installed (WordPress, Joomla, Drupal) |
| `GET /database-engines` | Database engines and their versions |
| `POST /cron/preview` | Next run times of a cron schedule |

### Websites

| Endpoint | What it does |
|---|---|
| `GET /domains` | Your websites |
| `POST /domains` | Create one |
| `GET /domains/{id}` | One website and its status |
| `PATCH /domains/{id}` | Change its settings (aliases, runtime, certificates, limits, …) |
| `DELETE /domains/{id}` | Delete it and its files |
| `POST /domains/{id}/actions/deploy` | Re-create its container with the current settings |
| `POST /domains/{id}/actions/start` | Start it |
| `POST /domains/{id}/actions/stop` | Stop it |
| `POST /domains/{id}/actions/restart` | Restart it |
| `GET /domains/{id}/stats` | CPU, memory and traffic |
| `GET /domains/{id}/php` | PHP settings |
| `PUT /domains/{id}/php` | Change them |
| `GET /domains/{id}/logs/ws` | Live access and error logs (WebSocket) |
| `GET /domains/{id}/terminal/ws` | Shell in the website's container (WebSocket) |
| `GET /domains/{id}/redirects` | Redirects |
| `POST /domains/{id}/redirects` | Add one |
| `DELETE /domains/{id}/redirects/{rid}` | Remove one |
| `GET /domains/{id}/backups` | The website's snapshots |
| `POST /domains/{id}/backups` | Back it up now |
| `POST /domains/{id}/backups/{snap}/restore` | Restore a snapshot |

### Applications

| Endpoint | What it does |
|---|---|
| `GET /domains/{id}/app` | The application installed on the website, and its state |
| `POST /domains/{id}/app` | Install one |
| `DELETE /domains/{id}/app` | Forget it (the files stay) |
| `POST /domains/{id}/app/check` | Check versions, plugins and security |
| `POST /domains/{id}/app/update` | Update it |
| `POST /domains/{id}/app/harden` | Apply the security hardening |
| `POST /domains/{id}/app/staging` | Copy it to a staging website |
| `POST /domains/{id}/app/push` | Push the staging copy to production |

### Git deploys

| Endpoint | What it does |
|---|---|
| `GET /domains/{id}/git` | Deploy settings and the deploy key |
| `PUT /domains/{id}/git` | Set them |
| `DELETE /domains/{id}/git` | Turn Git deploys off |
| `POST /domains/{id}/git/deploy` | Deploy now |

### Scheduled tasks

| Endpoint | What it does |
|---|---|
| `GET /domains/{id}/cron` | The website's scheduled tasks |
| `POST /domains/{id}/cron` | Add one |
| `PATCH /domains/{id}/cron/{jid}` | Change it |
| `DELETE /domains/{id}/cron/{jid}` | Delete it |
| `POST /domains/{id}/cron/{jid}/run` | Run it now |
| `GET /domains/{id}/cron/{jid}/runs` | Its last runs and their output |

### Files

`path` is relative to the website's folder.

| Endpoint | What it does |
|---|---|
| `GET /domains/{id}/files` | List a folder (`?path=`) |
| `GET /domains/{id}/files/content` | Read a text file |
| `PUT /domains/{id}/files/content` | Write a text file |
| `GET /domains/{id}/files/download` | Download a file |
| `POST /domains/{id}/files/upload` | Upload files (multipart) |
| `POST /domains/{id}/files/mkdir` | Create a folder |
| `POST /domains/{id}/files/rename` | Rename or move |
| `POST /domains/{id}/files/delete` | Delete |
| `POST /domains/{id}/files/chmod` | Change permissions |
| `POST /domains/{id}/files/extract` | Extract an archive |
| `POST /domains/{id}/files/compress` | Make a zip archive |

### DNS

| Endpoint | What it does |
|---|---|
| `GET /domains/{id}/dns` | The domain's zone and records |
| `POST /domains/{id}/dns/records` | Add a record |
| `PATCH /domains/{id}/dns/records/{rid}` | Change one |
| `DELETE /domains/{id}/dns/records/{rid}` | Delete one |

### Mail

| Endpoint | What it does |
|---|---|
| `GET /domains/{id}/mail` | The domain's mail: mailboxes, aliases, DNS state |
| `POST /domains/{id}/mail` | Turn mail on for the domain |
| `DELETE /domains/{id}/mail` | Turn it off |
| `POST /domains/{id}/mail/mailboxes` | Create a mailbox |
| `PATCH /domains/{id}/mail/mailboxes/{mid}` | Change it (password, quota, forwarding, …) |
| `DELETE /domains/{id}/mail/mailboxes/{mid}` | Delete it |
| `POST /domains/{id}/mail/mailboxes/{mid}/webmail` | Single sign-on link to its webmail |
| `POST /domains/{id}/mail/aliases` | Create an alias |
| `PATCH /domains/{id}/mail/aliases/{aid}` | Change it |
| `DELETE /domains/{id}/mail/aliases/{aid}` | Delete it |
| `GET /domains/{id}/mail/backups` | The domain's mail snapshots |
| `POST /domains/{id}/mail/backups` | Back its mail up now |
| `POST /domains/{id}/mail/backups/{snap}/restore` | Restore a snapshot |

### Databases

| Endpoint | What it does |
|---|---|
| `GET /databases` | Your databases |
| `POST /databases` | Create one |
| `GET /databases/sizes` | Sizes of your databases |
| `GET /databases/remote` | Remote access: address, ports and whether it is on |
| `GET /databases/remote/certificate` | The CA certificate remote clients verify |
| `GET /databases/{id}` | One database |
| `DELETE /databases/{id}` | Delete it |
| `GET /databases/{id}/credentials` | Its user and password, and an Adminer ticket |
| `POST /databases/{id}/rotate-password` | Set a new password |
| `PUT /databases/{id}/remote` | Addresses allowed to connect from outside |
| `PUT /databases/{id}/limit` | Size limit |
| `GET /databases/{id}/backups` | Its snapshots |
| `POST /databases/{id}/backups` | Back it up now |
| `POST /databases/{id}/backups/{snap}/restore` | Restore a snapshot |

### Extensions

| Endpoint | What it does |
|---|---|
| `GET /extensions/ui` | Pages and menu entries of the enabled extensions |
| `POST /extensions/{id}/session` | Open an extension's page |
| `GET /extensions` | Installed extensions (*admin*) |
| `POST /extensions/upload` | Upload an `.opx` package (*admin*) |
| `GET /extensions/marketplace` | The marketplace catalog (*admin*) |
| `POST /extensions/marketplace/download` | Download a package from it (*admin*) |
| `POST /extensions/install` | Install an uploaded or downloaded package (*admin*) |
| `DELETE /extensions/pending/{token}` | Discard it instead (*admin*) |
| `PATCH /extensions/{id}` | Enable or disable one (*admin*) |
| `PUT /extensions/{id}/settings` | Its settings (*admin*) |
| `PUT /extensions/{id}/license` | Its license key (*admin*) |
| `DELETE /extensions/{id}` | Uninstall it (*admin*) |

### Server (*admin*)

| Endpoint | What it does |
|---|---|
| `GET /system/info` | Host, versions and settings |
| `GET /system/stats` | CPU, memory, disk and network history |
| `GET /system/stats/ws` | The same, live (WebSocket) |
| `GET /system/containers` | Containers OpenPlex runs |
| `POST /system/reconcile` | Re-create whatever is missing or out of date |
| `GET /system/update` | Installed and latest version |
| `POST /system/update/check` | Look for a new release now |
| `POST /system/update/install` | Install it |
| `PUT /system/update/settings` | Automatic checks on or off |
| `GET /system/alerts` | E-mail alert settings |
| `PUT /system/alerts` | Change them |
| `POST /system/alerts/test` | Send a test alert |
| `GET /audit` | Audit log |

### Backups (*admin*)

| Endpoint | What it does |
|---|---|
| `GET /backups/settings` | Repository, schedule and retention |
| `PUT /backups/settings` | Change them |
| `POST /backups/test` | Test a repository |
| `GET /backups/recovery-key` | The repository's recovery key |
| `POST /backups/run` | Back everything up now |
| `GET /backups/runs` | Past runs |
| `GET /backups/panel-snapshots` | Snapshots of the panel's own state |

### Security (*admin*)

| Endpoint | What it does |
|---|---|
| `GET /firewall` | Firewall rules and state |
| `POST /firewall/apply` | Apply changed rules (they roll back unless confirmed) |
| `POST /firewall/confirm` | Keep the applied rules |
| `POST /firewall/revert` | Roll them back now |
| `POST /firewall/blocks` | Block an address |
| `DELETE /firewall/blocks/{id}` | Unblock it |
| `GET /fail2ban` | Fail2Ban jails and banned addresses |
| `POST /fail2ban/unban` | Unban an address |
| `GET /captcha` | Login captcha settings |
| `PUT /captcha` | Change them |

### DNS and mail servers (*admin*)

| Endpoint | What it does |
|---|---|
| `GET /dns` | The DNS server: state and name servers |
| `PUT /dns/settings` | Change its settings |
| `GET /mail` | The mail server: state and host name |
| `PUT /mail/settings` | Change its settings |
