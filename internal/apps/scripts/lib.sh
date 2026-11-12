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

# After an update: the website must still answer (a redirect or 4xx is fine,
# a server error is not).
site_check() {
	step "checking the website"
	code=$(curl -s -o /dev/null -w '%{http_code}' --max-time 120 -H "Host: $OPX_HOST" http://127.0.0.1:8080/ || true)
	case "$code" in
	2* | 3* | 4*) echo "the website answers (HTTP $code)" ;;
	*)
		echo "the website does not work after the update (HTTP ${code:-no answer})" >&2
		exit 4
		;;
	esac
}
