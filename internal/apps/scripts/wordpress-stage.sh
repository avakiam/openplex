# Adapts a copy of a WordPress site. OPX_MODE=staging: the files and database
# were just copied from the live site (OPX_FROM_HOST) to this staging site
# (OPX_HOST): point the configuration at the staging database, rewrite the
# addresses and keep search engines away. OPX_MODE=live: the staging
# database was copied over the live one: rewrite the addresses back and
# restore the live search engine setting.
wp_cli
if [ "$OPX_MODE" = staging ]; then
	step "pointing the copy at its own database"
	wp config set DB_NAME "$OPX_DB_NAME" --skip-plugins --skip-themes
	wp config set DB_USER "$OPX_DB_USER" --skip-plugins --skip-themes
	wp config set DB_PASSWORD "$OPX_DB_PASSWORD" --skip-plugins --skip-themes
	wp config set DB_HOST "$OPX_DB_HOST" --skip-plugins --skip-themes
fi
if [ -n "${OPX_FROM_HOST:-}" ] && [ "$OPX_FROM_HOST" != "$OPX_HOST" ]; then
	step "replacing $OPX_FROM_HOST with $OPX_HOST"
	wp search-replace "//$OPX_FROM_HOST" "//$OPX_HOST" --all-tables-with-prefix --skip-columns=guid --skip-plugins --skip-themes
fi
if [ "$OPX_MODE" = staging ]; then
	step "hiding the copy from search engines"
	wp option update opx_live_blog_public "$(wp option get blog_public --skip-plugins --skip-themes)" --skip-plugins --skip-themes
	wp option update blog_public 0 --skip-plugins --skip-themes
else
	if live=$(wp option get opx_live_blog_public --skip-plugins --skip-themes 2>/dev/null); then
		wp option update blog_public "$live" --skip-plugins --skip-themes
		wp option delete opx_live_blog_public --skip-plugins --skip-themes
	fi
fi
wp cache flush --skip-plugins --skip-themes || true
echo "OPX_VERSION=$(wp core version)"
site_check
