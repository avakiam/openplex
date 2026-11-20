# OpenPlex for the Vultr Marketplace

OpenPlex is published on Vultr as a **Vendor Data** app: there is no image to
build or keep up to date. When a customer deploys it, Vultr starts a fresh
Ubuntu 24.04 server and runs [`openplex-vultr.sh`](openplex-vultr.sh), which:

1. reads the app variables `panel_domain` and `acme_email` from Vultr's
   metadata service;
2. turns SSH off while it works (Vultr's guidance) and back on at the end;
3. updates the system, turns ufw off if it is active (OpenPlex runs its own
   nftables firewall and Fail2Ban, with SSH always open);
4. installs the latest signed OpenPlex release with `install.sh`, with the
   panel at `panel_domain` when one was given;
5. installs the first-login assistant `openplex-setup` and the message of the
   day, shared with the DigitalOcean image (`deploy/common/files`).

On the first SSH login as root, `openplex-setup` sets (or confirms) the panel's
domain and prints a one-time sign-in link for the admin account. The installer's
password line is kept out of the log (`/var/log/openplex-vultr.log`).

## Build the script

`openplex-vultr.sh` is generated: edit `openplex-vultr.sh.in` (or the shared
files) and run

```bash
deploy/vultr/build.sh
```

which embeds the assistant and the MOTD into the single file Vultr needs.

## Publish

1. Apply as a vendor at <https://www.vultr.com/marketplace/become-a-verified-vendor/>.
   Once approved, open **Marketplace** in the Vultr console and create the vendor user.
2. Create the app profile and variables with the texts in [LISTING.md](LISTING.md),
   and upload the icons (light and dark, square, 100×100 px or larger).
3. **Builds → Build App Image From Vendor Data**: choose Ubuntu 24.04 and paste
   `openplex-vultr.sh` (LF line endings). Deploy it once to test, set the
   version, then **Publish Image**.
4. **Settings → Make Public**: Vultr reviews the app and replies by e-mail.

Each deployment installs the latest OpenPlex release, so a new release needs no
new build. Rebuild only when the script or the shared files change.
