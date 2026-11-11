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
mailboxes. A published API reference is planned before 1.0.

## Important: don't make an image of an initialised server

On its first start `openplexd` generates the server's secret key, the admin
account and its password, and certificates. If you snapshot a server where
OpenPlex already ran and use it as a template, **every server created from
it shares those secrets**. Install on first boot instead (as above) until
the image mode below exists.

## Planned for providers

- **Image mode:** prepare a template with OpenPlex and its container images
  already downloaded but not initialised, so a new server finishes setting
  itself up (secrets, host name, IP addresses) in seconds on first boot.
- **Handing over access:** a one-time sign-in link, or an admin password
  passed through cloud-init, so you can e-mail it to the customer.
- **Signed releases** and an update channel.
- **API reference** and billing integrations (for example a WHMCS module).
- **Official extensions**, free and paid, and an open extension interface
  for your own.

Interested in offering OpenPlex? Open an issue in the
[main repository](https://github.com/avakiam/openplex).
