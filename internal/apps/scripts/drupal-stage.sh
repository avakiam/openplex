# Adapts a copy of a Drupal site. OPX_MODE=staging: settings.php was copied
# from the live site: point it at the staging database (a block appended at
# its end). OPX_MODE=live: the live settings.php was kept. Either way the
# caches, which hold the other site's addresses, are rebuilt.
if [ "$OPX_MODE" = staging ]; then
	step "pointing the copy at its own database"
	chmod u+w sites/default sites/default/settings.php
	php -r '
$f = "sites/default/settings.php";
$s = preg_replace("~\n// OpenPlex staging copy.*\z~s", "", file_get_contents($f));
$s .= "\n// OpenPlex staging copy: this site uses its own database.\n";
foreach (["database" => "OPX_DB_NAME", "username" => "OPX_DB_USER", "password" => "OPX_DB_PASSWORD", "host" => "OPX_DB_HOST"] as $k => $env)
	$s .= "\$databases[\"default\"][\"default\"][" . var_export($k, true) . "] = " . var_export(getenv($env), true) . ";\n";
file_put_contents($f, $s);
'
	chmod a-w sites/default/settings.php sites/default
fi
if [ -f vendor/drush/drush/drush.php ]; then
	step "rebuilding the caches"
	php -d memory_limit=512M vendor/drush/drush/drush.php --yes cache:rebuild
else
	step "emptying the caches"
	php -r '
$app_root = getcwd(); $site_path = "sites/default"; $settings = []; $databases = [];
include "sites/default/settings.php";
$d = $databases["default"]["default"];
$m = new mysqli($d["host"], $d["username"], $d["password"], $d["database"], (int) ($d["port"] ?? 3306));
$p = $d["prefix"] ?? "";
foreach ($m->query("SHOW TABLES LIKE " . var_export($p . "cache%", true)) as $r) $m->query("TRUNCATE TABLE `" . current($r) . "`");
'
fi
echo "OPX_VERSION=$(php -r 'include "core/lib/Drupal.php"; echo Drupal::VERSION;')"
site_check
