# Updates through Composer, then runs the database updates with Drush (added
# to the project the first time). When Composer fails, the previous
# composer.json, composer.lock and code are put back.
composer_get
cp composer.json "$HOME/composer.json"
cp composer.lock "$HOME/composer.lock"
drush() { php -d memory_limit=512M vendor/drush/drush/drush.php --yes "$@"; }
maintenance() { drush state:set system.maintenance_mode "$1" --input-format=integer >/dev/null 2>&1 || true; }
rollback() {
	step "putting the previous version back"
	cp "$HOME/composer.json" composer.json
	cp "$HOME/composer.lock" composer.lock
	composer install >/dev/null 2>&1 || echo "composer install failed: restore the backup taken before the update" >&2
	maintenance 0
	exit 1
}
# Composer refuses plugins it has not been told about: any other new
# dependency that brings one is installed with its plugin disabled.
run_composer() {
	for _ in 1 2 3 4 5; do
		if composer "$@" >"$HOME/out.txt" 2>&1; then
			return 0
		fi
		plugin=$(sed -n 's#^ *\([a-z0-9_.-]*/[a-z0-9_.-]*\) contains a Composer plugin which is blocked.*#\1#p' "$HOME/out.txt" | head -n 1)
		if [ -z "$plugin" ]; then
			tail -n 15 "$HOME/out.txt" >&2
			return 1
		fi
		echo "disabling the Composer plugin of $plugin"
		composer config --no-plugins "allow-plugins.$plugin" false
	done
	return 1
}
# Drupal 11.3+ boots through symfony/runtime's generated autoloader.
composer config --no-plugins allow-plugins.symfony/runtime true
if [ ! -f vendor/drush/drush/drush.php ]; then
	step "adding Drush"
	run_composer require drush/drush || rollback
fi
packages=""
if [ "$OPX_CORE" = 1 ]; then
	packages="drupal/core*"
fi
if [ "$OPX_EXTENSIONS" = 1 ]; then
	packages="$packages $(php -r '
$c = json_decode(file_get_contents("composer.json"), true);
echo implode(" ", array_filter(array_keys($c["require"] ?? []), fn($n) => str_starts_with($n, "drupal/") && !str_starts_with($n, "drupal/core")));
')"
fi
maintenance 1
if [ -n "${packages# }" ]; then
	step "updating with Composer"
	set -f # "drupal/core*" is for Composer, not the shell
	# shellcheck disable=SC2086
	run_composer update $packages --with-all-dependencies || rollback
	set +f
fi
step "updating the database"
drush updatedb
drush cache:rebuild
maintenance 0
echo "OPX_VERSION=$(php -r 'include "core/lib/Drupal.php"; echo Drupal::VERSION;')"
site_check
