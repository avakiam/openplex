#!/bin/bash
# OpenPlex for the Vultr Marketplace: the Vendor Data script that Vultr runs
# on a fresh Ubuntu 24.04 server when a customer deploys the app.
#
# It reads the app variables (panel_domain, acme_email), updates the system,
# installs the latest signed OpenPlex release with install.sh, and sets up the
# first-login assistant (openplex-setup) and the message of the day.
# Log: /var/log/openplex-vultr.log
#
# Generated from deploy/vultr/openplex-vultr.sh.in by deploy/vultr/build.sh:
# edit the template, not the generated file.
set -Eeuo pipefail

LOG=/var/log/openplex-vultr.log
touch "$LOG" && chmod 600 "$LOG"
exec > >(tee -a "$LOG") 2>&1

META=${OPENPLEX_VULTR_METADATA:-http://169.254.169.254}
RELEASES=https://github.com/avakiam/openplex/releases
export DEBIAN_FRONTEND=noninteractive

log() { printf '[openplex-vultr] %s\n' "$*"; }

# get_var NAME: an app variable from Vultr's metadata service (empty if unset).
get_var() {
  curl -fsS --max-time 5 -H "METADATA-TOKEN: vultr" "$META/v1/internal/app-$1" 2>/dev/null || true
}

# SSH stays off while OpenPlex installs (Vultr's guidance for provisioning
# scripts) and comes back when the script ends, whether it worked or not.
ssh_units=()
for unit in ssh.socket ssh.service; do
  if systemctl is-active -q "$unit"; then ssh_units+=("$unit"); fi
done
restore_ssh() {
  for unit in "${ssh_units[@]}"; do systemctl start "$unit" || true; done
}
trap restore_ssh EXIT
if ((${#ssh_units[@]})); then
  systemctl stop "${ssh_units[@]}" || true
fi

domain=$(get_var panel_domain | tr '[:upper:]' '[:lower:]' | tr -d '[:space:]')
email=$(get_var acme_email | tr -d '[:space:]')
if [[ -n "$domain" && ! "$domain" =~ ^([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z]{2,63}$ ]]; then
  log "ignoring the panel domain \"$domain\": not a valid host name (set it later with openplex-setup)"
  domain=""
fi
if [[ -n "$email" && ! "$email" =~ ^[^@[:space:]]+@[^@[:space:]]+\.[a-zA-Z]{2,}$ ]]; then
  log "ignoring the e-mail address \"$email\": not valid"
  email=""
fi

log "Updating the system"
apt_get() { apt-get -o DPkg::Lock::Timeout=600 -qqy "$@"; }
apt_get update
apt_get -o Dpkg::Options::=--force-confdef -o Dpkg::Options::=--force-confold full-upgrade
apt_get install curl ca-certificates

# OpenPlex manages the host firewall itself (nftables table inet openplex, with
# SSH always open); an active ufw would keep it off.
if command -v ufw >/dev/null && ufw status | grep -q '^Status: active'; then
  log "Turning ufw off: the OpenPlex firewall takes over"
  ufw --force disable
fi

log "Installing OpenPlex"
installer=$(mktemp)
curl -fsSL "$RELEASES/latest/download/install.sh" -o "$installer"
args=()
[[ -n "$domain" ]] && args+=(--panel-domain "$domain")
[[ -n "$email" ]] && args+=(--acme-email "$email")
# The installer prints the initial admin password: keep it out of the log.
bash "$installer" "${args[@]}" 2>&1 | sed -u '/Password:/d'
rm -f "$installer"

log "Setting up the first-login assistant"
install -d -m 0755 /usr/local/bin /etc/update-motd.d
cat >/usr/local/bin/openplex-setup <<'OPENPLEX_SETUP_EOF'
#!/bin/bash
# OpenPlex marketplace images (DigitalOcean, Vultr): gives the panel its
# domain and prints a one-time sign-in link for the admin account. Runs on
# root's first login; run it again any time to change the domain.
set -uo pipefail

ENV_FILE=/etc/openplex/openplex.env
HEALTH=http://127.0.0.1:7080/api/v1/system/health
DONE=/root/.openplex-setup-done

bold() { printf '\033[1m%s\033[0m\n' "$*"; }

ip=$(ip -4 route get 1.1.1.1 2>/dev/null | awk '{for (i = 1; i < NF; i++) if ($i == "src") print $(i + 1)}')
[[ -n "$ip" ]] || ip=$(hostname -I | awk '{print $1}')

set_kv() {
  if grep -q "^$1=" "$ENV_FILE"; then
    sed -i "s|^$1=.*|$1=$2|" "$ENV_FILE"
  else
    echo "$1=$2" >>"$ENV_FILE"
  fi
}

wait_healthy() {
  for _ in $(seq 1 90); do
    curl -fsS "$HEALTH" >/dev/null 2>&1 && return 0
    sleep 1
  done
  return 1
}

echo
bold "Welcome to OpenPlex"
echo
echo "OpenPlex is running on this server. Its control panel needs a domain name"
echo "that points to this server, such as panel.example.com, with an A record to"
echo "    $ip"
echo "Create that record at your DNS provider first; the panel gets its HTTPS"
echo "certificate from Let's Encrypt as soon as the name resolves."
echo

if ! wait_healthy; then
  echo "OpenPlex is still starting (or failed to start): see journalctl -u openplexd"
  echo "Run openplex-setup again in a minute."
  exit 0
fi

current=$(sed -n 's/^OPENPLEX_PANEL_DOMAIN=//p' "$ENV_FILE")
if [[ "$current" == auto || -z "$current" ]]; then
  # A server named with a full domain (panel.example.com) suggests it.
  current=$(hostname -f 2>/dev/null || true)
  [[ "$current" == *.* ]] || current=""
fi
read -rp "Panel domain${current:+ [$current]} (empty to do it later): " domain
domain=$(printf '%s' "${domain:-$current}" | tr '[:upper:]' '[:lower:]' | tr -d '[:space:]')

if [[ -z "$domain" ]]; then
  echo
  echo "No domain for now. Until you set one, reach the panel through an SSH tunnel:"
  echo "    ssh -L 7080:127.0.0.1:7080 root@$ip"
  echo "then open http://localhost:7080 and sign in with the link from"
  echo "    openplexd login-link        (replace <panel address> with localhost:7080)"
  echo "or the password in /var/lib/openplex/initial-admin-password (user: admin)."
  echo "Run openplex-setup whenever you have the domain."
  touch "$DONE"
  exit 0
fi
if [[ ! "$domain" =~ ^([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z]{2,63}$ ]]; then
  echo "\"$domain\" is not a valid domain name. Run openplex-setup again."
  exit 0
fi
resolved=$(getent ahostsv4 "$domain" 2>/dev/null | awk 'NR==1 {print $1}')
if [[ "$resolved" != "$ip" ]]; then
  echo
  echo "Note: $domain ${resolved:+resolves to $resolved, }${resolved:-does not resolve yet, }not to $ip."
  echo "The panel will get its certificate once the record is in place."
  read -rp "Use $domain anyway? [y/N] " yes
  [[ "$yes" =~ ^[Yy] ]] || { echo "Run openplex-setup again when the record is ready."; exit 0; }
fi

email=$(sed -n 's/^OPENPLEX_ACME_EMAIL=//p' "$ENV_FILE")
read -rp "E-mail for Let's Encrypt notices${email:+ [$email]} (optional): " newemail
email=${newemail:-$email}

set_kv OPENPLEX_PANEL_DOMAIN "$domain"
set_kv OPENPLEX_ACME_EMAIL "$email"
systemctl restart openplexd
if ! wait_healthy; then
  echo "OpenPlex did not come back: see journalctl -u openplexd"
  exit 1
fi
touch "$DONE"

echo
bold "Done. Your panel: https://$domain"
echo
echo "Sign in once with this link (valid for 24 hours), then choose your password:"
openplexd login-link --ttl 24h 2>/dev/null || echo "    (run: openplexd login-link)"
echo
echo "The admin password is also in /var/lib/openplex/initial-admin-password until you change it."
echo "Documentation: https://openplex.dev"
OPENPLEX_SETUP_EOF
cat >/etc/update-motd.d/99-openplex <<'OPENPLEX_MOTD_EOF'
#!/bin/sh
# OpenPlex marketplace images: where the panel is.
domain=$(sed -n 's/^OPENPLEX_PANEL_DOMAIN=//p' /etc/openplex/openplex.env 2>/dev/null)
echo
echo "********************************************************************************"
echo "OpenPlex control panel"
if [ -n "$domain" ] && [ "$domain" != auto ]; then
  echo "  Panel:        https://$domain"
  echo "  Sign-in link: openplexd login-link"
else
  echo "  The panel has no domain yet: run openplex-setup"
fi
echo "  Status:       systemctl status openplexd   (logs: journalctl -u openplexd)"
echo "  Docs:         https://openplex.dev"
echo "********************************************************************************"
OPENPLEX_MOTD_EOF
chmod 0755 /usr/local/bin/openplex-setup /etc/update-motd.d/99-openplex
grep -q openplex-setup /root/.bashrc || cat >>/root/.bashrc <<'EOF'

# OpenPlex: set the panel's domain and get a sign-in link on the first login.
if [ -t 0 ] && [ -x /usr/local/bin/openplex-setup ] && [ ! -e /root/.openplex-setup-done ]; then
  /usr/local/bin/openplex-setup
fi
EOF

apt_get autoremove
apt_get clean
log "OpenPlex is installed${domain:+: https://$domain}"
