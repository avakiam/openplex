#!/bin/bash
# Builds openplex-vultr.sh, the single-file Vendor Data script for the Vultr
# Marketplace, from openplex-vultr.sh.in: each @@FILE path@@ line is replaced
# by that file (relative to this directory).
set -euo pipefail
cd "$(dirname "$0")"

awk '
  /^@@FILE .*@@$/ {
    path = substr($0, 8, length($0) - 9)
    while ((getline line < path) > 0) print line
    close(path)
    next
  }
  { print }
' openplex-vultr.sh.in >openplex-vultr.sh
chmod 0755 openplex-vultr.sh
bash -n openplex-vultr.sh
echo "wrote $(pwd)/openplex-vultr.sh"
