# Drupal sites are Composer projects: the updates are what Composer would
# install within the project's version constraints (a dry run).
composer_get
composer update "drupal/*" --with-all-dependencies --dry-run >"$HOME/dry.txt" 2>&1 || { tail -n 15 "$HOME/dry.txt" >&2; exit 1; }
php -r '
$core = "";
$items = [];
preg_match_all("#^\s*- Upgrading drupal/([a-z0-9_.-]+) \(([^ ]+) => ([^ )]+)\)#m", file_get_contents(getenv("HOME") . "/dry.txt"), $m, PREG_SET_ORDER);
foreach ($m as [, $name, $from, $to]) {
	if ($name === "core") { $core = $to; continue; }
	if (str_starts_with($name, "core-") || isset($items[$name])) continue;
	$items[$name] = ["kind" => "module", "name" => $name, "title" => $name, "version" => $from, "latest" => $to];
}
include "core/lib/Drupal.php";
echo "OPX_RESULT=", json_encode(["version" => Drupal::VERSION, "core" => $core, "items" => array_values($items)], JSON_UNESCAPED_SLASHES | JSON_UNESCAPED_UNICODE), "\n";
'
