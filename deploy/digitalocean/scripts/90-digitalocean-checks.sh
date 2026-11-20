#!/bin/bash
# DigitalOcean's own clean-up and image check (from its marketplace-partners
# repository, Apache 2.0), at a fixed commit and checked by their SHA-256.
set -Eeuo pipefail
commit=fb9f872e7ade13b530da5ef2b7a3e7938c0f3774
base=https://raw.githubusercontent.com/digitalocean/marketplace-partners/$commit/scripts
declare -A sums=(
  [90-cleanup.sh]=5e2fe7ce30892a26ed2731238d9f26c40ae8b1084b070ce095f3b99ab1a2cc81
  [99-img-check.sh]=91ff2b1880439c97ccdc49554c5ed8901b89ef250c8ac5fffe52811c68abac49
)
dir=$(mktemp -d)
for f in 90-cleanup.sh 99-img-check.sh; do
  curl -fsSL "$base/$f" -o "$dir/$f"
  echo "${sums[$f]}  $dir/$f" | sha256sum -c --quiet -
done
bash "$dir/90-cleanup.sh"
bash "$dir/99-img-check.sh"
rm -rf "$dir"
