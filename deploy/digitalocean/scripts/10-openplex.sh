#!/bin/bash
# Updates the system and installs OpenPlex in image mode: Docker, the
# firewall, the openplexd service and the container images, without starting
# OpenPlex (each Droplet initialises itself on first boot).
set -Eeuo pipefail

apt-get -qq update
apt-get -qqy -o Dpkg::Options::=--force-confdef -o Dpkg::Options::=--force-confold full-upgrade
apt-get -qqy install curl ca-certificates

# Building the PHP image needs more memory than the smallest Droplet has.
if ! swapon --show | grep -q /swap.build; then
  fallocate -l 2G /swap.build
  chmod 600 /swap.build
  mkswap -q /swap.build
  swapon /swap.build
fi

version=${OPENPLEX_VERSION:-latest}
if [[ "$version" == latest ]]; then
  url=https://github.com/avakiam/openplex/releases/latest/download/install.sh
else
  url=https://github.com/avakiam/openplex/releases/download/$version/install.sh
fi
curl -fsSL "$url" -o /tmp/openplex-install.sh
args=(--image --version "$version")
[[ -n "${OPENPLEX_PHP:-}" ]] && args+=(--php "$OPENPLEX_PHP")
bash /tmp/openplex-install.sh "${args[@]}"
rm -f /tmp/openplex-install.sh

swapoff /swap.build
rm -f /swap.build
apt-get -qqy autoremove
apt-get -qqy clean
