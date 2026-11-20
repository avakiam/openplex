# OpenPlex — DigitalOcean Marketplace listing

Texts for the Vendor Portal. Keep them in step with the website.

## App name

OpenPlex

## Tagline (short description)

Open-source hosting control panel: websites, databases, e-mail, DNS and backups on your own Droplet.

## Categories

Control Panels; Hosting

## Description

OpenPlex is an open-source hosting control panel for Linux servers. It turns a
Droplet into a complete web host, managed from a modern dashboard in English,
Spanish or Catalan.

- **Websites:** PHP, Node.js, Python, Go or static sites, each in its own
  container, with free HTTPS certificates, staging copies and Git deploys.
- **One-click apps:** WordPress, Joomla and Drupal, installed, updated and hardened.
- **Databases:** MariaDB and PostgreSQL, with size limits and optional remote access.
- **E-mail:** mail domains, mailboxes and aliases with DKIM, and Roundcube webmail.
- **DNS:** authoritative DNS for your domains.
- **Files:** a web file manager and a built-in SFTP server.
- **Backups:** scheduled, encrypted backups to S3-compatible storage, and
  restoring the whole panel to a new server.
- **Security:** its own firewall, Fail2Ban, two-factor authentication and
  login protection.
- **Accounts:** customers and resellers with their own limits, API keys and a
  REST API; modules for WHMCS and Blesta.
- **Updates:** signed releases installed from the dashboard.

## Software included

| Software | Version |
|---|---|
| OpenPlex | the version of the image (see the release notes) |
| Ubuntu | 24.04 LTS |
| Docker Engine | current from Docker's repository at build time |
| Fail2Ban, nftables | from Ubuntu |

Licence: OpenPlex is free software under the GNU AGPL-3.0 (<https://github.com/avakiam/openplex>).

## Getting started (shown after creating the Droplet)

1. Point a domain name at the Droplet's IP address with an A record, such as
   `panel.example.com`. (Naming the Droplet with that domain also works.)
2. Log in to the Droplet as root over SSH (or with the web console). The
   OpenPlex assistant starts on the first login: enter the panel's domain and,
   optionally, an e-mail address for Let's Encrypt.
3. Open the link it prints: it signs you in to the panel once, and you choose
   your password. Run `openplex-setup` again any time to change the domain, and
   `openplexd login-link` for a new sign-in link.

We recommend a Droplet with 2 GB of RAM or more; 1 GB works for a few small
websites. Mail needs the Droplet's outbound port 25, which DigitalOcean blocks
on new accounts: ask their support to open it, or send through an SMTP relay.

## Support

- Documentation: <https://openplex.dev>
- Issues: <https://github.com/avakiam/openplex/issues>
- E-mail: info@openplex.dev
