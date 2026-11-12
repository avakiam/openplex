# Plugins and themes are not loaded: a broken one must not stop the check.
wp_cli
wp core check-update --format=json >"$HOME/core.json"
wp plugin list --skip-plugins --skip-themes --update=available --fields=name,title,version,update_version --format=json >"$HOME/plugins.json"
wp theme list --skip-plugins --skip-themes --update=available --fields=name,title,version,update_version --format=json >"$HOME/themes.json"
php -r '
$j = fn($f) => json_decode(@file_get_contents(getenv("HOME") . "/$f.json") ?: "[]", true) ?: [];
$core = "";
foreach ($j("core") as $u) if (version_compare($u["version"], $core ?: "0", ">")) $core = $u["version"];
$items = [];
foreach (["plugin" => "plugins", "theme" => "themes"] as $kind => $f)
	foreach ($j($f) as $p) $items[] = ["kind" => $kind, "name" => $p["name"], "title" => html_entity_decode(strip_tags($p["title"])), "version" => $p["version"], "latest" => $p["update_version"]];
include "wp-includes/version.php";
echo "OPX_RESULT=", json_encode(["version" => $wp_version, "core" => $core, "items" => $items], JSON_UNESCAPED_SLASHES | JSON_UNESCAPED_UNICODE), "\n";
'
