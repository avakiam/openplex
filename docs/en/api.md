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

The request and response bodies follow what the dashboard shows on each page;
the easiest way to see one is the browser's network tab while doing the same
thing in the dashboard.

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
| `DELETE /users/{id}` | Delete it |
| `POST /users/{id}/password` | Set its password |
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
