# OpenPlex — Akamai (Linode) Marketplace listing

The app information and assets that the Akamai Quick Deploy Apps team asks for
with a new app. Keep them in step with the website and the DigitalOcean listing.

## App name

OpenPlex

## Version

0.33.0 (the `openplex_version` in `apps/linode-marketplace-openplex/roles/openplex/defaults/main.yml`)

## Description (100–125 words)

OpenPlex is an open-source hosting control panel that turns a Linode into a
complete web host, managed from a modern dashboard in English, Spanish or
Catalan. Host PHP, Node.js, Python, Go or static websites, each in its own
container with free HTTPS certificates, staging copies and Git deploys. Install
WordPress, Joomla or Drupal in one click, and keep them updated and hardened.
Create MariaDB and PostgreSQL databases, mail domains and mailboxes with DKIM
and Roundcube webmail, and authoritative DNS zones. Manage files in the browser
or over SFTP, and schedule encrypted backups to S3-compatible storage such as
Linode Object Storage. OpenPlex includes its own firewall, Fail2Ban and
two-factor authentication, and serves customers and resellers with their own
limits, API keys and a REST API.

## Support URL

<https://github.com/avakiam/openplex/issues> (documentation: <https://openplex.dev>, e-mail: info@openplex.dev)

## Operating system

Ubuntu 24.04 LTS

## Suggested plan

Shared CPU, 4 GB (`g6-standard-2`); 2 GB works for a few small websites without mail.

## Licence

GNU AGPL-3.0 (<https://github.com/avakiam/openplex>)

## Assets

- Brand colour 1: `#2A62D6`
- Brand colour 2: `#0F172A`
- Logo, full colour: `assets/openplex-logo-color.svg`
- Logo, white: `assets/openplex-logo-white.svg`
- Icon: `assets/openplex-icon-color.svg`

Zip them for the submission: `cd deploy/linode && zip -r openplex-assets.zip assets`.
