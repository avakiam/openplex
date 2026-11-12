# Security checks of a Joomla site (OPX_SSL=1 when the website has HTTPS).
php -r '
define("_JEXEC", 1);
require "configuration.php";
$c = new JConfig();
$out = function ($id, $ok, $detail = "") { echo $id, "\t", $ok ? 1 : 0, "\t", $detail, "\n"; };
$out("joomla_debug", empty($c->debug));
$out("joomla_error_reporting", in_array($c->error_reporting ?? "default", ["none", "default"], true), (string) ($c->error_reporting ?? ""));
if (getenv("OPX_SSL") === "1") $out("joomla_force_ssl", (int) ($c->force_ssl ?? 0) === 2);
$m = new mysqli($c->host, $c->user, $c->password, $c->db);
$n = $m->query("SELECT COUNT(*) FROM {$c->dbprefix}users WHERE username = \"admin\"")->fetch_row()[0];
$out("joomla_admin_user", (int) $n === 0);
' >>"$HOME/security.tsv"
[ -d installation ] && sec joomla_installation_dir 0 || sec joomla_installation_dir 1
mode=$(stat -c %a configuration.php)
case "$mode" in *0) sec joomla_config_perms 1 "$mode" ;; *) sec joomla_config_perms 0 "$mode" ;; esac
sec_report
