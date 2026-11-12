<h1>
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="docs/assets/logo-dark.svg">
    <img src="docs/assets/logo-light.svg" alt="OpenPlex" height="56">
  </picture>
</h1>

**English** · [Español](README.es.md) · [Català](README.ca.md)

OpenPlex is an open-source web hosting control panel, an alternative to Plesk
and cPanel built on containers. It installs on a fresh Ubuntu or Debian
server with one command and gives you, your team and your customers a modern
dashboard for websites, databases, mail, DNS, backups and security.

> **Status: pre-release.** OpenPlex is under active development and has not
> had its first public release yet. Expect breaking changes until 1.0.

## Why OpenPlex

- **One binary.** `openplexd` is a single static Go program with the
  dashboard built in and its state in an embedded SQLite database. Nothing
  else to install on the host but Docker.
- **Every website in its own container**, running as its own user, on a
  private network per account. One customer's site can't read, reach or slow
  down another's.
- **Automatic HTTPS** for every site, alias and the panel itself, through a
  Caddy edge proxy (Let's Encrypt / ZeroSSL).
- **Free software** (AGPL-3.0) with an open extension model: anyone can build
  extensions, free or paid.

## Features

| | |
|---|---|
| **Websites** | Static, PHP 7.4–8.4 (with the extensions WordPress, Laravel or Drupal need), Node.js, Python and Go; PHP settings per website (memory, upload size, time limits, time zone…); one-click WordPress, Joomla and Drupal installs (latest release, own database, in English, Spanish or Catalan) and their updates, with a backup first; security checks with one-click fixes; staging copies on a subdomain, pushed back to the live site; aliases and subdomains; redirects; environment variables; start/stop/restart; access and error logs; resource limits |
| **Files** | Web file manager with editor, upload, archive/extract and permissions; SFTP (port 2222, SSH keys or an SFTP-only password); web terminal in the site's container |
| **Databases** | MariaDB, PostgreSQL and Redis; one-click Adminer; password rotation |
| **Mail** | Mailboxes, aliases and forwarding, quotas, DKIM/SPF/DMARC records, Roundcube webmail with one-click login |
| **DNS** | Authoritative DNS (PowerDNS) with zones created for your domains |
| **Backups** | Encrypted, deduplicated backups (restic) to a local disk or S3, scheduled; restore of a site, database, mail domain or single mailbox; full server rebuild with `openplexd restore-panel` |
| **Security** | Host firewall (nftables) with safe apply/rollback, Fail2Ban, login captcha (Turnstile, hCaptcha, reCAPTCHA), two-factor authentication, audit log |
| **Users** | Administrators, resellers and customers with quotas; share a website (only some areas, optionally read-only) or a database with another user; API keys |
| **Tasks & monitoring** | Scheduled tasks per website; server and container CPU, memory, disk and network; e-mail alerts (failed backup, website down, disk almost full, certificate not renewing, updates) |
| **Languages** | English, Spanish and Catalan |

## Requirements

- Ubuntu 22.04 or newer, or Debian 12 or newer, on amd64 or arm64.
- Root access, a public IP address, ports 80 and 443 free.
- 2 GB of RAM or more is recommended (mail and databases use most of it).
- A domain name pointing at the server, for the panel and for HTTPS.

## Install

Once the first release is published:

```bash
curl -fsSL https://github.com/avakiam/openplex/releases/latest/download/install.sh | sudo bash -s -- \
  --panel-domain panel.example.com --acme-email you@example.com
```

The installer sets up Docker, the `openplexd` service and the firewall, then
prints the panel address. Sign in as `admin` with the password stored in
`/var/lib/openplex/initial-admin-password` (the file is deleted when you
change that password).

See the [installation guide](docs/en/installation.md) for every option,
upgrades and rebuilding a lost server.

## Documentation

- [Installation, upgrades and recovery](docs/en/installation.md)
- [Websites: applications, PHP settings and SFTP](docs/en/websites.md)
- [OpenPlex for hosting providers](docs/en/hosting-providers.md)
- [Licensing, extensions and trademark](docs/en/licensing.md)
- [Building an extension](docs/en/extensions.md)

## Building from source

You need Go 1.26 or newer and Node.js 22 or newer.

```bash
make build   # dashboard (Next.js static export) + bin/openplexd
make check   # go vet, tests and a CGO-free build
```

No Go toolchain? Use the official image:

```bash
docker run --rm -v "$PWD":/src -w /src golang:1.26-bookworm make check
```

## Licence

Copyright © 2026 Avakiam.

The OpenPlex core is licensed under the
[GNU Affero General Public License v3.0 only](LICENSE). If you modify
OpenPlex and offer it to others, including over a network, you must publish
your changes under the same licence.

Extensions that use only the documented extension interface may use any
licence, including proprietary and paid ones: see the
[OpenPlex Extension Exception](LICENSE-EXCEPTION.md). OpenPlex will offer
official extensions, some free and open source, some paid.

"OpenPlex" and its logos are trademarks of Avakiam: see
[TRADEMARKS.md](TRADEMARKS.md).

## Security

Found a vulnerability? Please report it privately; see [SECURITY.md](SECURITY.md).

## Contributing

Bug reports, ideas and translation fixes are welcome as issues. Code
contributions are not accepted yet; see [CONTRIBUTING.md](CONTRIBUTING.md).
