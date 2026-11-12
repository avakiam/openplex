# Adapts a copy of a Joomla site. OPX_MODE=staging: configuration.php was
# copied from the live site: point it at the staging database, its own
# temporary and log folders, and keep search engines away. OPX_MODE=live:
# the live configuration.php was kept; nothing to change. Joomla stores
# relative addresses, so the database needs no rewriting.
if [ "$OPX_MODE" = staging ]; then
	step "pointing the copy at its own database"
	chmod u+w configuration.php
	php -r '
$f = "configuration.php";
$s = file_get_contents($f);
$set = function ($key, $value) use (&$s) {
	$line = "public \$" . $key . " = " . var_export($value, true) . ";";
	$s = preg_replace("/public \\\$" . $key . " = .*?;\n/", addcslashes($line, "\\\\$") . "\n", $s, 1, $n);
	if ($n !== 1) { fwrite(STDERR, "configuration.php has no \$$key\n"); exit(1); }
};
$set("host", getenv("OPX_DB_HOST"));
$set("user", getenv("OPX_DB_USER"));
$set("password", getenv("OPX_DB_PASSWORD"));
$set("db", getenv("OPX_DB_NAME"));
$set("robots", "noindex, nofollow");
$from = getenv("OPX_FROM_DOCROOT");
$to = getenv("OPX_DOCROOT");
define("_JEXEC", 1);
require $f;
$c = new JConfig();
foreach (["tmp_path", "log_path"] as $k) {
	$v = $c->$k ?? "";
	if (is_string($v) && str_starts_with($v, $from . "/")) $set($k, $to . substr($v, strlen($from)));
}
file_put_contents($f, $s);
'
fi
echo "OPX_VERSION=$(php -r 'define("_JEXEC", 1); require "libraries/src/Version.php"; echo (new Joomla\CMS\Version())->getShortVersion();')"
site_check
