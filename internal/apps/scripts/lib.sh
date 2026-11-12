# Runs as the website's user inside its PHP container. Settings come from the
# environment (OPX_*), never from the command line of this shell.
set -eu
umask 022
HOME=$(mktemp -d /tmp/opx.XXXXXX)
export HOME COMPOSER_HOME="$HOME/composer" COMPOSER_NO_INTERACTION=1
trap 'rm -rf "$HOME"' EXIT
cd "$OPX_DOCROOT"
fetch() { curl -fsSL --retry 3 --connect-timeout 20 "$@"; }
step() { echo "==> $*"; }

# WP-CLI, checked against its published SHA-512.
wp_cli() {
	fetch -o "$HOME/wp-cli.phar" https://raw.githubusercontent.com/wp-cli/builds/gh-pages/phar/wp-cli.phar
	sum=$(fetch https://raw.githubusercontent.com/wp-cli/builds/gh-pages/phar/wp-cli.phar.sha512 | cut -c1-128)
	echo "$sum  $HOME/wp-cli.phar" | sha512sum -c -s || { echo "WP-CLI checksum mismatch" >&2; exit 1; }
}
wp() { php -d memory_limit=512M "$HOME/wp-cli.phar" --path="$OPX_DOCROOT" --quiet "$@"; }

# Composer (latest stable), checked against its published SHA-256.
composer_get() {
	p=$(fetch https://getcomposer.org/versions | php -r '$v = json_decode(stream_get_contents(STDIN), true); echo $v["stable"][0]["path"] ?? "";')
	case "$p" in /download/*/composer.phar) ;; *) echo "could not find the latest Composer release" >&2; exit 1 ;; esac
	fetch -o "$HOME/composer.phar" "https://getcomposer.org$p"
	sum=$(fetch "https://getcomposer.org$p.sha256sum" | cut -c1-64)
	echo "$sum  $HOME/composer.phar" | sha256sum -c -s || { echo "Composer checksum mismatch" >&2; exit 1; }
}
composer() { php -d memory_limit=1536M "$HOME/composer.phar" "$@"; }

# The website must still answer (a redirect or 4xx is fine, a server error
# is not).
site_ok() {
	code=$(curl -s -o /dev/null -w '%{http_code}' --max-time 120 -H "Host: $OPX_HOST" http://127.0.0.1:8080/ || true)
	case "$code" in
	2* | 3* | 4*) return 0 ;;
	*) return 1 ;;
	esac
}
site_check() {
	step "checking the website"
	if site_ok; then
		echo "the website answers (HTTP $code)"
	else
		echo "the website does not work after the update (HTTP ${code:-no answer})" >&2
		exit 4
	fi
}

# Security checks: sec <id> <1 when fine, else 0> [detail]; sec_report prints
# them as one "OPX_SECURITY=<json>" line.
sec() {
	printf '%s\t%s\t%s\n' "$1" "$2" "$(printf '%s' "${3:-}" | tr '\t\n' '  ' | cut -c1-300)" >>"$HOME/security.tsv"
}
sec_report() {
	touch "$HOME/security.tsv"
	php -r '
$o = [];
foreach (file(getenv("HOME") . "/security.tsv", FILE_IGNORE_NEW_LINES) ?: [] as $l) {
	[$id, $ok, $d] = array_pad(explode("\t", $l, 3), 3, "");
	$o[] = ["id" => $id, "ok" => $ok === "1", "detail" => $d];
}
echo "OPX_SECURITY=", json_encode($o, JSON_UNESCAPED_SLASHES | JSON_UNESCAPED_UNICODE), "\n";
'
}

# Hardening: OPX_FIX lists the fixes to apply; fix <id> says whether one is.
fix() { case " $OPX_FIX " in *" $1 "*) return 0 ;; esac; return 1; }
# Keeps a copy of a configuration file to put back when the website stops
# working after the fixes (restore_if_broken).
keep_copy() { cp -p "$1" "$HOME/kept.$(echo "$1" | tr / _)"; }
restore_if_broken() {
	step "checking the website"
	if site_ok; then
		echo "the website answers (HTTP $code)"
		return 0
	fi
	for f in "$@"; do
		cp -p "$HOME/kept.$(echo "$f" | tr / _)" "$f" 2>/dev/null || true
	done
	echo "the website stopped working (HTTP ${code:-no answer}) after the changes: they were undone" >&2
	exit 4
}
