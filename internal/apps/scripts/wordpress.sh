step "downloading WP-CLI"
wp_cli

case "$OPX_LANG" in
es) locale=es_ES ;;
ca) locale=ca ;;
*) locale=en_US ;;
esac

step "downloading WordPress"
wp core download
step "configuring"
wp config create --dbname="$OPX_DB_NAME" --dbuser="$OPX_DB_USER" --dbpass="$OPX_DB_PASSWORD" \
	--dbhost="$OPX_DB_HOST" --skip-check
step "installing"
wp core install --url="$OPX_URL" --title="$OPX_TITLE" --admin_user="$OPX_ADMIN_USER" \
	--admin_password="$OPX_ADMIN_PASSWORD" --admin_email="$OPX_ADMIN_EMAIL" --skip-email
wp rewrite structure '/%postname%/'
# Translations come as language packs, which can lag a new release by a few
# days: WordPress then stays in English and offers them later in Updates.
if [ "$locale" != en_US ]; then
	step "installing the $locale language pack"
	wp language core install "$locale" --activate || echo "the $locale language pack is not available yet"
fi
version=$(wp core version)
echo "OPX_VERSION=$version"
