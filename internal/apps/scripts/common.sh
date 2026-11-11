# Runs as the website's user inside its PHP container. Settings come from the
# environment (OPX_*), never from the command line of this shell.
set -eu
umask 022
cd "$OPX_DOCROOT"
if [ -f index.html ] && grep -q "This site is hosted with OpenPlex" index.html; then
	rm -f index.html
fi
if [ -n "$(ls -A)" ]; then
	echo "the document root is not empty" >&2
	exit 3
fi
fetch() { curl -fsSL --retry 3 --connect-timeout 20 "$@"; }
step() { echo "==> $*"; }
