step "looking up the latest Joomla release"
url=$(fetch https://api.github.com/repos/joomla/joomla-cms/releases/latest | php -r '
$r = json_decode(stream_get_contents(STDIN), true);
foreach ($r["assets"] ?? [] as $a) {
	if (preg_match("/^Joomla_[0-9.]+-Stable-Full_Package\.tar\.gz$/", $a["name"])) { echo $a["browser_download_url"]; break; }
}')
[ -n "$url" ] || { echo "no Joomla package found in the latest release" >&2; exit 1; }
version=$(echo "$url" | sed -n 's#.*/Joomla_\([0-9.]*\)-Stable-Full_Package.tar.gz#\1#p')

step "downloading Joomla $version"
fetch "$url" | tar -xz
step "installing"
prefix="$(head -c 256 /dev/urandom | tr -dc a-z | head -c 5)_"
php -d memory_limit=384M installation/joomla.php install -n \
	--site-name="$OPX_TITLE" --admin-user="$OPX_ADMIN_USER" --admin-username="$OPX_ADMIN_USER" \
	--admin-password="$OPX_ADMIN_PASSWORD" --admin-email="$OPX_ADMIN_EMAIL" \
	--db-type=mysqli --db-host="$OPX_DB_HOST" --db-user="$OPX_DB_USER" --db-pass="$OPX_DB_PASSWORD" \
	--db-name="$OPX_DB_NAME" --db-prefix="$prefix" --db-encryption=0
rm -rf installation
echo "OPX_VERSION=$version"
