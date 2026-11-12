# OPX_CORE=1 updates WordPress itself, OPX_EXTENSIONS=1 its plugins and
# themes; translations always follow.
wp_cli
if [ "$OPX_CORE" = 1 ]; then
	step "updating WordPress"
	wp core update
	wp core update-db
fi
if [ "$OPX_EXTENSIONS" = 1 ]; then
	step "updating plugins"
	wp plugin update --all --skip-plugins --skip-themes
	step "updating themes"
	wp theme update --all --skip-plugins --skip-themes
fi
step "updating translations"
for what in core "plugin --all" "theme --all"; do
	# shellcheck disable=SC2086
	wp language $what update --skip-plugins --skip-themes 2>/dev/null || echo "some translations could not be updated"
done
echo "OPX_VERSION=$(wp core version)"
site_check
