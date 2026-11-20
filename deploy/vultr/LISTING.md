# OpenPlex — Vultr Marketplace listing

The texts for the Vultr vendor console. Keep them in step with the website and
the other marketplace listings.

## Application profile

| Field | Value |
|---|---|
| App Name | OpenPlex |
| App Name ID | `openplex` |
| Repo URL | https://github.com/avakiam/openplex |
| Support Email | info@openplex.dev |
| Support URL | https://github.com/avakiam/openplex/issues |
| Operating System | Ubuntu 24.04 LTS x64 |
| Categories | Control Panels, Hosting (and, if offered, Website Builders / CMS) |
| Description | Open-source hosting control panel: websites, databases, e-mail, DNS and backups on your own server. |

Icons: `deploy/linode/assets/openplex-icon-color.svg` exported to PNG
(512×512) for light mode, and the same icon for dark mode (it has its own
blue background).

## Readme

OpenPlex is an open-source hosting control panel for Linux servers. It turns a
Vultr instance into a complete web host, managed from a modern dashboard in
English, Spanish or Catalan.

- **Websites:** PHP, Node.js, Python, Go or static sites, each in its own
  container, with free HTTPS certificates, staging copies and Git deploys.
- **One-click apps:** WordPress, Joomla and Drupal, installed, updated and hardened.
- **Databases:** MariaDB and PostgreSQL, with size limits and optional remote access.
- **E-mail:** mail domains, mailboxes and aliases with DKIM, and Roundcube webmail.
- **DNS:** authoritative DNS for your domains.
- **Files:** a web file manager and a built-in SFTP server.
- **Backups:** scheduled, encrypted backups to S3-compatible storage such as
  Vultr Object Storage, and restoring the whole panel to a new server.
- **Security:** its own firewall, Fail2Ban, two-factor authentication and login protection.
- **Accounts:** customers and resellers with their own limits, API keys and a
  REST API; modules for WHMCS and Blesta.
- **Updates:** signed releases installed from the dashboard.

OpenPlex is free software under the GNU AGPL-3.0. We recommend 2 GB of RAM or
more; 1 GB works for a few small websites without mail.

## App variables

| Label | Name | Type | Required |
|---|---|---|---|
| Panel domain (e.g. panel.example.com, with an A record to the server) | `panel_domain` | User supplied, text field | No |
| E-mail for Let's Encrypt certificate notices | `acme_email` | User supplied, text field | No |

## Instructions

```markdown
## OpenPlex is installing

The installation takes about 5 minutes after the server starts. SSH opens
when it is done.

## Sign in to the panel

1. Point your panel domain's A record at `{{ip}}` (if you haven't yet).
2. Log in over SSH: `ssh root@{{ip}}` (root password: `{{root_password}}`).
3. The OpenPlex assistant starts: confirm or enter the panel's domain. It prints
   a one-time sign-in link for the `admin` account; open it and choose your password.

Run `openplex-setup` again any time to change the domain, and
`openplexd login-link` for a new sign-in link. The panel gets its HTTPS
certificate from Let's Encrypt as soon as the domain points to the server.

## Sending e-mail

Vultr blocks outbound SMTP (port 25) on new accounts. To send mail from the
server, ask Vultr support to open it and set the server's reverse DNS to your
mail host name.

Documentation: https://openplex.dev — Support: https://github.com/avakiam/openplex/issues
```
