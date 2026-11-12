# Joomla's own update checks fill its #__updates table, read here.
php cli/joomla.php core:update:check -n >/dev/null
php cli/joomla.php update:extensions:check -n >/dev/null
php -r '
define("_JEXEC", 1);
require "configuration.php";
require "libraries/src/Version.php";
$c = new JConfig();
$version = (new Joomla\CMS\Version())->getShortVersion();
mysqli_report(MYSQLI_REPORT_ERROR | MYSQLI_REPORT_STRICT);
[$host, $port] = array_pad(explode(":", $c->host, 2), 2, 3306);
$db = new mysqli($host, $c->user, $c->password, $c->db, (int) $port);
$p = $c->dbprefix;
$core = "";
$items = [];
$rows = $db->query("SELECT u.name, u.element, u.type, u.version, e.manifest_cache FROM {$p}updates u JOIN {$p}extensions e ON e.extension_id = u.extension_id WHERE u.extension_id <> 0");
foreach ($rows as $r) {
	if ($r["type"] === "file" && $r["element"] === "joomla") {
		if (version_compare($r["version"], $version, ">") && version_compare($r["version"], $core ?: "0", ">")) $core = $r["version"];
		continue;
	}
	$cur = json_decode($r["manifest_cache"] ?: "{}", true)["version"] ?? "";
	if ($cur !== "" && !version_compare($r["version"], $cur, ">")) continue;
	$items[] = ["kind" => $r["type"], "name" => $r["element"], "title" => $r["name"], "version" => $cur, "latest" => $r["version"]];
}
echo "OPX_RESULT=", json_encode(["version" => $version, "core" => $core, "items" => $items, "manual_extensions" => true], JSON_UNESCAPED_SLASHES | JSON_UNESCAPED_UNICODE), "\n";
'
