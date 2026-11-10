#!/usr/bin/env bash
# OpenPlex unattended installer for Ubuntu 22.04+/Debian 12+ (amd64, arm64).
#
#   curl -fsSL https://get.openplex.dev | sudo bash -s -- --panel-domain panel.example.com --acme-email you@example.com
#
# Idempotent: re-running upgrades the binary and keeps all data.
set -Eeuo pipefail

OPENPLEX_VERSION="${OPENPLEX_VERSION:-latest}"
RELEASE_URL="${OPENPLEX_RELEASE_URL:-https://github.com/openplex-dev/openplex/releases}"
BINARY=""
PANEL_DOMAIN=""
ACME_EMAIL=""
LISTEN="127.0.0.1:7080"
FIREWALL=1
DATA_DIR=/var/lib/openplex
ETC_DIR=/etc/openplex
BIN_PATH=/usr/local/bin/openplexd
UNIT_PATH=/etc/systemd/system/openplexd.service

usage() {
  cat <<EOF
Usage: install.sh [options]
  --version VERSION       release to install (default: latest)
  --binary PATH           install this local openplexd binary instead of downloading
  --panel-domain HOST     serve the panel at https://HOST through the edge proxy
  --acme-email EMAIL      contact address for Let's Encrypt / ZeroSSL
  --listen ADDR           direct panel listen address (default: 127.0.0.1:7080)
  --no-firewall           leave the host firewall alone (no nftables rules, no Fail2Ban)
  -h, --help              show this help

Rebuilding a lost server: install with the same options, then
  systemctl stop openplexd
  openplexd restore-panel --repo /path/to/repository   (or s3:https://host/bucket/prefix)
  systemctl start openplexd
(see: openplexd restore-panel --help)
EOF
}

log()  { printf '\033[1;34m==>\033[0m %s\n' "$*"; }
warn() { printf '\033[1;33m!!\033[0m %s\n' "$*" >&2; }
die()  { printf '\033[1;31mxx\033[0m %s\n' "$*" >&2; exit 1; }
trap 'die "failed at line $LINENO: $BASH_COMMAND"' ERR

while [[ $# -gt 0 ]]; do
  case "$1" in
    --version) OPENPLEX_VERSION="$2"; shift 2 ;;
    --binary) BINARY="$2"; shift 2 ;;
    --panel-domain) PANEL_DOMAIN="$2"; shift 2 ;;
    --acme-email) ACME_EMAIL="$2"; shift 2 ;;
    --listen) LISTEN="$2"; shift 2 ;;
    --no-firewall) FIREWALL=0; shift ;;
    -h|--help) usage; exit 0 ;;
    *) usage; die "unknown option: $1" ;;
  esac
done

# ---------- preflight ----------
[[ $EUID -eq 0 ]] || die "run as root (sudo)"
[[ -r /etc/os-release ]] || die "cannot detect OS"
# shellcheck disable=SC1091
. /etc/os-release
case "${ID}:${VERSION_ID%%.*}" in
  ubuntu:22|ubuntu:24|ubuntu:25|ubuntu:26|debian:12|debian:13) ;;
  *) die "unsupported OS ${PRETTY_NAME:-unknown}; need Ubuntu 22.04+ or Debian 12+" ;;
esac
case "$(uname -m)" in
  x86_64) ARCH=amd64 ;;
  aarch64|arm64) ARCH=arm64 ;;
  *) die "unsupported architecture $(uname -m)" ;;
esac
command -v systemctl >/dev/null || die "systemd is required"
if [[ -n "$PANEL_DOMAIN" && ! "$PANEL_DOMAIN" =~ ^([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z]{2,63}$ ]]; then
  die "invalid --panel-domain '$PANEL_DOMAIN' (lowercase hostname expected)"
fi
if [[ -n "$ACME_EMAIL" && ! "$ACME_EMAIL" =~ ^[^@[:space:]]+@[^@[:space:]]+\.[a-zA-Z]{2,}$ ]]; then
  die "invalid --acme-email '$ACME_EMAIL'"
fi
if [[ -n "$BINARY" && ! -x "$BINARY" ]]; then
  die "--binary $BINARY is not an executable file"
fi
for port in 80 443; do
  if ss -Hltn "sport = :$port" | grep -q . && ! docker ps --format '{{.Names}}' 2>/dev/null | grep -qx openplex-edge; then
    die "port $port is already in use by another service ($(ss -Hltnp "sport = :$port" | awk '{print $NF}' | head -1)); stop it first"
  fi
done

export DEBIAN_FRONTEND=noninteractive

# ---------- packages ----------
log "Installing base packages"
apt-get update -qq
apt-get install -y -qq ca-certificates curl gnupg tar gzip iproute2 nftables >/dev/null

# ---------- Docker Engine (official apt repository) ----------
if ! command -v docker >/dev/null 2>&1; then
  log "Installing Docker Engine from download.docker.com"
  install -m 0755 -d /etc/apt/keyrings
  curl -fsSL "https://download.docker.com/linux/${ID}/gpg" -o /etc/apt/keyrings/docker.asc
  chmod a+r /etc/apt/keyrings/docker.asc
  echo "deb [arch=${ARCH} signed-by=/etc/apt/keyrings/docker.asc] https://download.docker.com/linux/${ID} ${VERSION_CODENAME} stable" \
    > /etc/apt/sources.list.d/docker.list
  apt-get update -qq
  apt-get install -y -qq docker-ce docker-ce-cli containerd.io docker-buildx-plugin docker-compose-plugin >/dev/null
fi
systemctl enable --now docker >/dev/null
docker info >/dev/null 2>&1 || die "Docker is installed but not responding"
log "Docker $(docker version --format '{{.Server.Version}}') ready"

# ---------- binary ----------
TMP=$(mktemp -d)
cleanup() { rm -rf "$TMP"; }
trap cleanup EXIT
if [[ -n "$BINARY" ]]; then
  log "Using local binary $BINARY"
  cp "$BINARY" "$TMP/openplexd"
else
  if [[ "$OPENPLEX_VERSION" == latest ]]; then
    base="$RELEASE_URL/latest/download"
  else
    base="$RELEASE_URL/download/$OPENPLEX_VERSION"
  fi
  log "Downloading openplexd ($OPENPLEX_VERSION, linux/$ARCH)"
  curl -fsSL "$base/openplexd-linux-$ARCH" -o "$TMP/openplexd-linux-$ARCH"
  curl -fsSL "$base/SHA256SUMS" -o "$TMP/SHA256SUMS"
  (cd "$TMP" && grep " openplexd-linux-$ARCH\$" SHA256SUMS | sha256sum -c --quiet -) || die "checksum verification failed"
  mv "$TMP/openplexd-linux-$ARCH" "$TMP/openplexd"
fi
chmod 0755 "$TMP/openplexd"
"$TMP/openplexd" version >/dev/null || die "downloaded binary does not run on this system"
install -m 0755 "$TMP/openplexd" "$BIN_PATH"
log "Installed $BIN_PATH ($("$BIN_PATH" version))"

# ---------- configuration ----------
install -d -m 0700 "$DATA_DIR"
install -d -m 0750 "$ETC_DIR"
ENV_FILE="$ETC_DIR/openplex.env"
if [[ ! -f "$ENV_FILE" ]]; then
  log "Writing $ENV_FILE"
  cat > "$ENV_FILE" <<EOF
# openplexd configuration (see: openplexd --help)
OPENPLEX_DATA_DIR=$DATA_DIR
OPENPLEX_LISTEN=$LISTEN
OPENPLEX_PANEL_DOMAIN=$PANEL_DOMAIN
OPENPLEX_ACME_EMAIL=$ACME_EMAIL
OPENPLEX_FIREWALL=$( [[ $FIREWALL -eq 1 ]] && echo true || echo false )
EOF
  chmod 0640 "$ENV_FILE"
else
  # Update only the values passed explicitly on this run.
  set_kv() { grep -q "^$1=" "$ENV_FILE" && sed -i "s|^$1=.*|$1=$2|" "$ENV_FILE" || echo "$1=$2" >> "$ENV_FILE"; }
  [[ -n "$PANEL_DOMAIN" ]] && set_kv OPENPLEX_PANEL_DOMAIN "$PANEL_DOMAIN"
  [[ -n "$ACME_EMAIL" ]] && set_kv OPENPLEX_ACME_EMAIL "$ACME_EMAIL"
  [[ $FIREWALL -eq 0 ]] && set_kv OPENPLEX_FIREWALL false
  log "Kept existing $ENV_FILE"
fi

# ---------- systemd ----------
log "Installing systemd unit"
cat > "$UNIT_PATH" <<'EOF'
# OpenPlex control-plane daemon. Keep in sync with deploy/openplexd.service.
[Unit]
Description=OpenPlex hosting control panel
Documentation=https://openplex.dev
After=network-online.target docker.service
Wants=network-online.target
Requires=docker.service

[Service]
Type=simple
EnvironmentFile=-/etc/openplex/openplex.env
ExecStart=/usr/local/bin/openplexd
Restart=on-failure
RestartSec=3
TimeoutStopSec=30
LimitNOFILE=65536

# Hardening. openplexd runs as root because it manages the Docker engine,
# file ownership of site directories and the host firewall (nftables).
NoNewPrivileges=yes
PrivateTmp=yes
ProtectHome=yes
ProtectSystem=full
ProtectKernelModules=yes
ProtectKernelTunables=yes
ProtectControlGroups=yes
ProtectClock=yes
ProtectHostname=yes
RestrictSUIDSGID=yes
RestrictRealtime=yes
LockPersonality=yes
RestrictNamespaces=yes
SystemCallArchitectures=native
ReadWritePaths=/var/lib/openplex
# Site files must be world-readable for the edge proxy; secrets are created 0600 explicitly.
UMask=0022

[Install]
WantedBy=multi-user.target
EOF
systemctl daemon-reload
systemctl enable openplexd >/dev/null 2>&1
systemctl restart openplexd

# ---------- host firewall & Fail2Ban ----------
# The host firewall is an nftables table (inet openplex) owned by openplexd and
# edited in the panel (Firewall page). On a server where ufw is already active it
# is imported but left off, so the existing setup keeps working unchanged.
if [[ $FIREWALL -eq 1 ]]; then
  if command -v ufw >/dev/null 2>&1 && ufw status 2>/dev/null | grep -qiE '^(status|estado): (active|activo)'; then
    log "ufw is active: keeping it. The panel imported its rules: turn on its firewall (Firewall page), then run 'ufw disable'."
  else
    log "Host firewall: managed by OpenPlex (SSH open, everything else closed; edit it on the Firewall page)"
  fi
  log "Installing Fail2Ban (SSH and panel login protection)"
  apt-get install -y -qq fail2ban python3-systemd >/dev/null
  install -d -m 0755 /etc/fail2ban/filter.d /etc/fail2ban/jail.d
  cat > /etc/fail2ban/filter.d/openplex.conf <<'F2B'
# Managed by the OpenPlex installer. Failed panel logins (openplexd JSON log).
[Definition]
failregex = "msg":"authentication failed","ip":"<ADDR>"
journalmatch = _SYSTEMD_UNIT=openplexd.service
F2B
  cat > /etc/fail2ban/jail.d/openplex.local <<'F2B'
# Managed by the OpenPlex installer; re-running it overwrites this file.
# Put your own changes in another /etc/fail2ban/jail.d/*.local file.
[DEFAULT]
# A ban drops every packet from the address, websites included: the chain
# hooks prerouting before Docker forwards traffic to the edge proxy.
banaction = nftables[type=custom, table=f2b-openplex, chain=bans, chain_hook=prerouting, chain_priority=-300, blocktype=drop]
banaction_allports = %(banaction)s
ignoreip = 127.0.0.1/8 ::1
bantime = 1h
findtime = 10m
maxretry = 5

[sshd]
enabled = true
backend = systemd

[openplex]
enabled = true
backend = systemd
filter = openplex
maxretry = 8
F2B
  systemctl enable fail2ban >/dev/null 2>&1
  systemctl restart fail2ban
else
  log "Skipping host firewall and Fail2Ban (--no-firewall)"
fi

# ---------- wait for health ----------
log "Waiting for openplexd to become healthy"
HEALTH_URL="http://${LISTEN/0.0.0.0/127.0.0.1}/api/v1/system/health"
for _ in $(seq 1 60); do
  if curl -fsS "$HEALTH_URL" >/dev/null 2>&1; then break; fi
  sleep 1
done
curl -fsS "$HEALTH_URL" >/dev/null || { journalctl -u openplexd -n 50 --no-pager; die "openplexd did not become healthy"; }

echo
log "OpenPlex is installed and running."
if [[ -f "$DATA_DIR/initial-admin-password" ]]; then
  echo "    Username: admin"
  echo "    Password: $(cat "$DATA_DIR/initial-admin-password")"
  echo "    (stored in $DATA_DIR/initial-admin-password — delete it after changing the password)"
fi
if [[ -n "$PANEL_DOMAIN" ]]; then
  echo "    Panel:    https://$PANEL_DOMAIN  (point its DNS A/AAAA record at this server)"
else
  echo "    Panel:    ssh -L 7080:$LISTEN root@<server>  then open http://localhost:7080"
fi
echo "    Logs:     journalctl -u openplexd -f"
echo "    Rebuilding a lost server instead? See: openplexd restore-panel --help"
