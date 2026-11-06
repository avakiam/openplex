#!/usr/bin/env sh
# Run any command inside the pinned Go toolchain container (for hosts without Go).
# Usage: scripts/go-docker.sh make check
set -eu
cd "$(dirname "$0")/.."
ROOT=$(pwd -W 2>/dev/null || pwd) # pwd -W gives a Windows path under Git Bash
MSYS_NO_PATHCONV=1 exec docker run --rm -v "$ROOT":/src -v openplex-gomod:/go/pkg/mod \
  -v openplex-gocache:/root/.cache/go-build -w /src -e CGO_ENABLED=0 golang:1.26-bookworm "$@"
