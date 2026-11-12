# Security checks of a Drupal site (Composer is already there: this runs
# after drupal-check.sh).
php -r '
$app_root = getcwd(); $site_path = "sites/default"; $settings = []; $databases = []; $config = [];
include "sites/default/settings.php";
$out = function ($id, $ok, $detail = "") { echo $id, "\t", $ok ? 1 : 0, "\t", $detail, "\n"; };
$out("drupal_trusted_hosts", !empty($settings["trusted_host_patterns"]));
$out("drupal_update_free_access", empty($settings["update_free_access"]));
$d = $databases["default"]["default"];
$m = new mysqli($d["host"], $d["username"], $d["password"], $d["database"], (int) ($d["port"] ?? 3306));
$p = $d["prefix"] ?? "";
$level = $config["system.logging"]["error_level"] ?? null;
if ($level === null) {
	$r = $m->query("SELECT data FROM {$p}config WHERE collection = \"\" AND name = \"system.logging\"")->fetch_row();
	$level = $r ? (unserialize($r[0], ["allowed_classes" => false])["error_level"] ?? "hide") : "hide";
}
$out("drupal_error_level", $level === "hide", $level);
$n = $m->query("SELECT COUNT(*) FROM {$p}users_field_data WHERE name = \"admin\"")->fetch_row()[0];
$out("drupal_admin_user", (int) $n === 0);
' >>"$HOME/security.tsv"
if [ -w sites/default/settings.php ] || [ -w sites/default ]; then
	sec drupal_settings_perms 0
else
	sec drupal_settings_perms 1
fi
# Known vulnerabilities of the installed packages (Drupal's and others').
if composer audit --locked --format=json >"$HOME/audit.json" 2>/dev/null || [ -s "$HOME/audit.json" ]; then
	php -r '
$a = json_decode(file_get_contents(getenv("HOME") . "/audit.json"), true)["advisories"] ?? [];
$names = array_keys(array_filter($a));
echo "drupal_advisories\t", $names ? 0 : 1, "\t", implode(" ", $names), "\n";
' >>"$HOME/security.tsv"
fi
sec_report
