#!/bin/bash
# Runs openplex-setup the first time root logs in (until it is done).
set -Eeuo pipefail
chmod 0755 /usr/local/bin/openplex-setup /etc/update-motd.d/99-openplex
grep -q openplex-setup /root/.bashrc || cat >> /root/.bashrc <<'EOF'

# OpenPlex 1-Click: set the panel's domain on the first login.
if [ -t 0 ] && [ -x /usr/local/bin/openplex-setup ] && [ ! -e /root/.openplex-setup-done ]; then
  /usr/local/bin/openplex-setup
fi
EOF

# Each Droplet gets its own machine ID on first boot.
truncate -s 0 /etc/machine-id
rm -f /var/lib/dbus/machine-id
