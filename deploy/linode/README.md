# OpenPlex Quick Deploy App for the Akamai (Linode) Marketplace

The OpenPlex app in the format of Akamai's
[marketplace-apps](https://github.com/akamai-compute-marketplace/marketplace-apps)
repository: a StackScript that sets up Ansible, and a playbook that installs
OpenPlex with `install.sh` on a new Ubuntu 24.04 Linode.

## What the customer gets

1. In Cloud Manager they choose OpenPlex, enter an e-mail address for Let's
   Encrypt and a limited sudo user and, optionally, a Linode API token with a
   domain to create the DNS record.
2. The playbook creates the sudo user, updates the system and installs the
   pinned OpenPlex release (Docker Engine, `openplexd`, its nftables firewall
   and Fail2Ban) with the panel at the given domain, or at the Linode's rDNS
   name (`<ip>.ip.linodeusercontent.com`). The panel gets its certificate on its own.
3. `/home/<user>/.credentials` holds the sudo password and the panel's admin
   password; the message of the day says where the panel is.

The installer prints the admin password; the playbook drops that line so it
never reaches `/var/log/stackscript.log` or the serial console.

## Layout

| Path | In marketplace-apps |
|---|---|
| `apps/linode-marketplace-openplex/` | `apps/linode-marketplace-openplex/` |
| `deployment_scripts/linode-marketplace-openplex/` | `deployment_scripts/linode-marketplace-openplex/` |
| `DOCUMENTATION.md` | sent with the pull request (Akamai publishes it on techdocs) |
| `LISTING.md`, `assets/` | the app information and brand assets for the form |

## Test

Lint, as the repository's CI does (from a checkout of marketplace-apps with
the app copied in and the `apps/linode_helpers` submodule initialised):

```bash
cd apps/linode-marketplace-openplex
python3 -m venv env && . env/bin/activate
pip install -r requirements.txt && ansible-galaxy install -r collections.yml
yamllint . && ansible-lint && ansible-playbook --syntax-check provision.yml site.yml
```

On Linode: paste `openplex-deploy.sh` into a new StackScript of your own, with
`GH_USER` and `BRANCH` pointing at your fork, and deploy it on a 4 GB Linode.
The log is in `/var/log/stackscript.log`.

## Submit

1. Register as a partner on Akamai's "Register your app" form.
2. Fork marketplace-apps, copy the two folders in, and open a pull request
   against `develop` with the README, `DOCUMENTATION.md`, the app information in
   [LISTING.md](LISTING.md) and the zipped `assets/`.
3. For a new OpenPlex release: change `openplex_version` and the version in the
   README and open another pull request.
