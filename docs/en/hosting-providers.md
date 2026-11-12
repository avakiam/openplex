# OpenPlex for hosting providers

**English** · [Español](../es/hosting-providers.md) · [Català](../ca/hosting-providers.md)

OpenPlex is meant to be offered the way providers offer Plesk or cPanel: a
server that comes with the panel ready, for customers who want to host
websites, mail and databases without administering Linux. It needs no
licence server and no per-server fee, and an **unmodified** release may be
offered under the OpenPlex name (see [licensing](licensing.md)).

## What works today

### Install on first boot (cloud-init or post-install script)

The installer is unattended, so any provider that runs a script on a new
server can deliver OpenPlex. With cloud-init:

```yaml
#cloud-config
runcmd:
  - curl -fsSL https://github.com/avakiam/openplex/releases/latest/download/install.sh | bash -s -- --panel-domain "$(hostname -f)" --acme-email ops@provider.example
```

The server's own host name (for example the one you assign, such as
`vps-1234.provider.example`) becomes the panel address, with its
certificate. The customer then signs in as `admin` with the password in
`/var/lib/openplex/initial-admin-password`.

Most providers also set, for every server:

- **reverse DNS** (PTR) of its IP addresses → the host name (needed for mail);
- **port 25** open for outgoing mail, or a documented way to request it;
- an **A/AAAA record** for the host name.

### Several customers on one server

One OpenPlex server can host many accounts: administrators, **resellers**
(who create and manage their own customers within their quotas) and
**customers**. Each account's websites run in their own containers, users
and private network, with memory and CPU limits that only administrators
change.

### Automation through the API

Everything the dashboard does goes through the HTTP API under `/api/v1`,
authenticated with API keys (created on the **API keys** page). A billing or
provisioning system can create users with quotas, websites, databases and
mailboxes. The [API reference](api.md) lists every endpoint.

### A template with OpenPlex ready (image mode)

To create servers from your own image instead of installing on each one,
prepare the template on a **fresh** server with `--image`:

```bash
curl -fsSL https://github.com/avakiam/openplex/releases/latest/download/install.sh | bash -s -- --image --acme-email ops@provider.example
```

It installs Docker, the firewall and the `openplexd` service, and downloads
and builds the container images (web server, databases, DNS, mail, PHP), but
does **not** start OpenPlex: the template holds no secrets. Options:
`--php 8.2,8.3` builds those PHP versions ahead (the default one otherwise),
`--no-mail` leaves out the mail images (about 1 GB). It refuses to run on a
server where OpenPlex already started.

Then generalise the system as you usually do (`cloud-init clean`, machine ID,
SSH host keys), power it off and take the image. On its first boot, each
server made from it creates its own secret key, admin account and
certificates. The panel answers at the server's host name
(`OPENPLEX_PANEL_DOMAIN=auto`: the full name the system has, or the one
cloud-init puts in `/etc/hosts`), so give servers a full name such as
`vps-1234.provider.example` with its DNS record.

### Handing the server over

`openplexd login-link` prints a one-time sign-in link for the admin account,
valid for 24 hours (`--ttl 72h` for up to a week, `--user NAME` for another
account). The customer opens it, is signed in once and lands on the account
page to set a password of their own. On first boot it waits for the panel to
create its admin account, so cloud-init can run it:

```yaml
#cloud-config
runcmd:
  - openplexd login-link --ttl 72h > /root/openplex-link
```

and your provisioning system reads the file (over SSH or your guest agent)
and sends the link to the customer. A link works once; it doesn't sign in
accounts with two-factor authentication. The admin password also stays in
`/var/lib/openplex/initial-admin-password`.

### Updates

Releases are signed, and the panel checks the signature before installing
one. By default it looks for a new release on GitHub twice a day and the
administrator installs it from the **Updates** page.

To choose which release your servers take (for example after testing it),
serve your own file and pass `--update-url https://…` to the installer
(`OPENPLEX_UPDATE_URL` in `/etc/openplex/openplex.env`). The file has the
format of GitHub's release API (`tag_name`, `name`, `body`, `html_url`,
`published_at` and `assets` with `name` and `browser_download_url`): a copy of
the answer of `https://api.github.com/repos/avakiam/openplex/releases/tags/vX.Y.Z`
works, and its download addresses can point to your own mirror. The
signatures are still checked against OpenPlex's release keys, so your file
chooses among the official releases; it can't change them.

## Planned for providers

- **Billing integrations** (for example a WHMCS module).
- **Official extensions**, free and paid, and an open extension interface
  for your own.

Interested in offering OpenPlex? Open an issue in the
[main repository](https://github.com/avakiam/openplex).
