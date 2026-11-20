# OpenPlex 1-Click for the DigitalOcean Marketplace

A [Packer](https://www.packer.io) template that builds the OpenPlex 1-Click
image: Ubuntu 24.04 with OpenPlex installed in image mode (`install.sh --image`):
Docker, the firewall, Fail2Ban, the `openplexd` service and the container
images, but nothing initialised. Each Droplet creates its own secret key, admin
account and certificates on first boot.

## What the customer gets

1. They create a Droplet from the 1-Click (2 GB of RAM or more recommended).
2. OpenPlex starts on first boot. The panel listens on `127.0.0.1:7080` until it
   has a domain; a Droplet named with a full domain (`panel.example.com`) uses it.
3. On the first SSH login (or the web console), `openplex-setup` asks for the
   panel's domain, checks that it points to the Droplet, sets it (and an
   optional e-mail for Let's Encrypt), restarts OpenPlex and prints a one-time
   sign-in link for the admin account. Without a domain, it explains how to reach
   the panel through an SSH tunnel. It can be run again any time.
4. The message of the day shows where the panel is.

## Build

Needs Packer 1.8.7 or later and a DigitalOcean personal access token with write
scope. The build uses a temporary `s-1vcpu-1gb` Droplet (DigitalOcean asks for
the smallest size, so that the 1-Click works on every size), takes about 20
minutes and leaves a snapshot named `openplex-<version>-<date>`.

```bash
export DIGITALOCEAN_TOKEN=dop_v1_…
packer init deploy/digitalocean
packer build -var openplex_version=v0.33.0 deploy/digitalocean
```

Variables: `openplex_version` (default `latest`), `php` (PHP versions to build
ahead, e.g. `8.2,8.3`), `region` (default `fra1`), `size`.

The build runs DigitalOcean's own clean-up and image check from
[marketplace-partners](https://github.com/digitalocean/marketplace-partners)
(fetched at a fixed commit and checked by SHA-256), and fails if the check finds
a critical problem. The check warns that neither UFW nor firewalld is enabled:
OpenPlex manages its own nftables firewall (`table inet openplex`, with
Fail2Ban), which only opens SSH, HTTP/HTTPS, DNS, mail and SFTP.

## Submit

1. Sign up as a vendor at <https://marketplace.digitalocean.com/vendors>; the
   Vendor Portal login comes by e-mail (or write to one-clicks-team@digitalocean.com).
2. In the [Vendor Portal](https://cloud.digitalocean.com/vendorportal), create the
   1-Click App with the texts in [LISTING.md](LISTING.md), and choose the snapshot.
3. For a new OpenPlex release: build again with its version and submit the new
   snapshot as an update.

## Files

| Path | |
|---|---|
| `openplex.pkr.hcl` | the Packer template |
| `scripts/10-openplex.sh` | updates the system and runs `install.sh --image` (with a temporary swap file) |
| `scripts/20-first-login.sh` | runs `openplex-setup` on root's first login; resets the machine ID |
| `scripts/90-digitalocean-checks.sh` | DigitalOcean's clean-up and image check |
| `../common/files/usr/local/bin/openplex-setup` | the first-login assistant (shared with Vultr) |
| `../common/files/etc/update-motd.d/99-openplex` | the message of the day (shared with Vultr) |
