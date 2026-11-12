# Installation, upgrades and recovery

**English** · [Español](../es/installation.md) · [Català](../ca/installation.md)

## Before you start

- A fresh server with **Ubuntu 22.04+** or **Debian 12+** (amd64 or arm64),
  root access and a public IP address. Other services must not use ports 80
  and 443.
- 2 GB of RAM or more is recommended; 1 GB works for a few small websites
  without mail.
- A host name for the panel (for example `panel.example.com`) with an A
  (and, if you have IPv6, AAAA) record pointing at the server. Let's Encrypt
  needs it to issue the panel's certificate.
- If you will host mail: a provider that allows outgoing traffic on port 25,
  and a reverse DNS (PTR) record of the server's IP set to the mail host name.

## Install

```bash
curl -fsSL https://github.com/avakiam/openplex/releases/latest/download/install.sh | sudo bash -s -- \
  --panel-domain panel.example.com --acme-email you@example.com
```

The installer is unattended and safe to run again. It:

1. installs Docker Engine from Docker's official repository (if missing),
   nftables and Fail2Ban;
2. downloads `openplexd`, checks its SHA-256 checksum and installs it in
   `/usr/local/bin`;
3. writes `/etc/openplex/openplex.env` and the `openplexd` systemd service;
4. starts the service, which creates the admin account, the edge proxy and
   the firewall rules.

### Options

| Option | Meaning |
|---|---|
| `--panel-domain HOST` | Serve the panel at `https://HOST` through the edge proxy |
| `--acme-email EMAIL` | Contact address for Let's Encrypt / ZeroSSL |
| `--version VERSION` | Install this release instead of the latest |
| `--binary PATH` | Install a local `openplexd` binary instead of downloading one |
| `--listen ADDR` | Direct panel address (default `127.0.0.1:7080`, only reachable from the server) |
| `--no-firewall` | Leave the host firewall alone (no nftables rules, no Fail2Ban) |

## First sign-in

Open `https://panel.example.com` and sign in as **`admin`**. The password is
in `/var/lib/openplex/initial-admin-password`:

```bash
sudo cat /var/lib/openplex/initial-admin-password
```

Change it under your profile and turn on two-factor authentication. The file
is deleted automatically once that password is changed. If you ever lose the
admin password:

```bash
sudo openplexd reset-password admin
```

## Ports

| Port | Used by |
|---|---|
| 22 | SSH (always allowed by the firewall) |
| 80, 443 (TCP), 443 (UDP) | Websites, panel and webmail (edge proxy) |
| 25, 465, 587, 143, 993, 110, 995, 4190 | Mail, when mail is turned on |
| 53 (TCP/UDP) | DNS, when the DNS server is turned on |
| 2222 | SFTP for website files (always allowed by the firewall; the installer also opens it in `ufw`) |
| 3306, 5432 | Remote database access, only while some database has it on, and only to the addresses it lists |

The firewall is managed from **Firewall** in the panel. It is left off when
`ufw` is active, so they don't fight over the rules.

## Configuration

Settings live in `/etc/openplex/openplex.env` (`KEY=value`, restart the
service after a change). The most useful ones:

| Variable | Default | Meaning |
|---|---|---|
| `OPENPLEX_PANEL_DOMAIN` | — | Panel host name |
| `OPENPLEX_ACME_EMAIL` | — | Certificate contact address |
| `OPENPLEX_DATA_DIR` | `/var/lib/openplex` | All state: database, sites, keys, certificates |
| `OPENPLEX_LISTEN` | `127.0.0.1:7080` | Direct panel address |
| `OPENPLEX_FIREWALL` | `true` | Manage the host firewall |
| `OPENPLEX_CAPTCHA` | `true` | `false` turns the login captcha off (e.g. after entering a wrong key) |
| `OPENPLEX_TENANT_NETWORKS` | `10.213.0.0/16` | Address pool for the private network of each account (one /24 each) |
| `OPENPLEX_SFTP_LISTEN` | `:2222` | SFTP server for website files (empty turns it off) |
| `OPENPLEX_DB_REMOTE_MARIADB` | `:3306` | Remote access to MariaDB databases (empty turns it off) |
| `OPENPLEX_DB_REMOTE_POSTGRES` | `:5432` | Remote access to PostgreSQL databases (empty turns it off) |

## E-mail alerts

**Alerts** (administrators) e-mails the addresses you choose when something
needs attention. It checks every minute:

| Alert | When |
|---|---|
| A backup fails | each failed backup, once |
| A website is down | after two minutes in error, and again when it is back up |
| The disk is almost full | above 90 % of the data disk, and again below 85 % |
| A database goes over its size limit | when it becomes read-only, and again when it is back under (see [databases](databases.md#size-limits)) |
| A certificate is not renewing | when one for a name still in use is close to expiring (renewal has failed for a while: usually DNS or port 80) |
| A new OpenPlex version | once per version |
| An OpenPlex update fails | when an update is undone or fails |

An ongoing problem is sent once, reminded every day while it lasts and
reported when it is over; restarting the panel sends nothing twice. The
e-mails are in English, Spanish or Catalan.

They go out through this server's mail service (when **Mail** is on) or
through an SMTP server such as your provider's (STARTTLS, TLS or none; the
password is stored encrypted). With the local mail service, use a sender
address of a mail domain hosted here whose SPF record allows the server, or
messages may land in spam. **Send a test e-mail** checks the settings before
you save them; the page also lists the last 90 days of alerts and why one
could not be sent.

## Managing the service

```bash
systemctl status openplexd
journalctl -u openplexd -f
sudo systemctl restart openplexd
```

## Upgrades

**From the panel** (OpenPlex 0.18 or later): **Updates** shows the installed
version and, twice a day, looks for a new stable release on GitHub; the menu
entry gets a dot when there is one. **Update to …** then:

1. downloads the release and checks it: the checksum, and the signature made
   with Avakiam's release key (a release signed otherwise is refused);
2. stops the panel for about a minute (websites, databases and mail keep
   running), backs up the current binary and its database to
   `/var/lib/openplex/updates/`, installs the new version and starts it;
3. if the new version doesn't start correctly within three minutes, puts the
   previous binary and database back on its own.

The result, and who started it, is in the audit log. Turn off **Check for
updates automatically** if you manage versions yourself (the check then only
runs when you click **Check now**).

**With the installer**: run it again with the same options. It replaces the
binary (also checking the signature) and keeps all data; at start the daemon
migrates its database and updates the containers it manages. Take a backup
first.

## Backups and rebuilding a lost server

Set up backups under **Backups**: a local folder or an S3 bucket, a schedule,
and how many copies to keep. **Keep the recovery key shown there somewhere
safe and off the server**: without it the backups can't be read.

To rebuild a server that is gone:

1. Install OpenPlex on the new server with the same options.
2. Restore the panel from the backup repository:

   ```bash
   sudo systemctl stop openplexd
   sudo OPENPLEX_RECOVERY_KEY='…' openplexd restore-panel --repo s3:https://s3.example.com/bucket/openplex
   sudo systemctl start openplexd
   ```

   For S3, also set `AWS_ACCESS_KEY_ID` and `AWS_SECRET_ACCESS_KEY`. For a
   local or mounted repository, pass its path to `--repo`.

3. At start OpenPlex restores the websites, databases, mail and webmail from
   the same backup run. Point your DNS at the new IP if it changed (zones
   hosted by OpenPlex are updated automatically).

`openplexd restore-panel --help` lists every option (`--snapshot`,
`--ipv4`, `--ipv6`, `--force`…).

## Uninstall

```bash
sudo systemctl disable --now openplexd
sudo docker rm -f $(sudo docker ps -aq --filter label=openplex.managed=true)
sudo docker volume rm openplex-mail-data openplex-webmail-db
sudo docker network prune -f --filter label=openplex.managed=true
sudo rm -rf /var/lib/openplex /etc/openplex /usr/local/bin/openplexd /etc/systemd/system/openplexd.service
```

This deletes every website, database and mailbox on the server.
