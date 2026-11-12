# Applies the security fixes listed in OPX_FIX to a Drupal site: settings
# are added at the end of settings.php (one line per fix, marked so they are
# replaced, not repeated). OPX_HOSTS lists the website's names, for the
# trusted host patterns. Undone if the website stops working.
chmod u+w sites/default sites/default/settings.php
keep_copy sites/default/settings.php
php -r '
$f = "sites/default/settings.php";
$fix = " " . getenv("OPX_FIX") . " ";
$lines = [];
if (str_contains($fix, " drupal_trusted_hosts ")) {
	echo "==> accepting only the names of this website\n";
	$p = array_map(fn ($h) => "^" . preg_quote($h) . "\$", preg_split("/\s+/", trim(getenv("OPX_HOSTS"))));
	$lines["drupal_trusted_hosts"] = "\$settings[\"trusted_host_patterns\"] = " . var_export($p, true) . ";";
}
if (str_contains($fix, " drupal_update_free_access ")) {
	echo "==> closing update.php\n";
	$lines["drupal_update_free_access"] = "\$settings[\"update_free_access\"] = FALSE;";
}
if (str_contains($fix, " drupal_error_level ")) {
	echo "==> hiding PHP errors\n";
	$lines["drupal_error_level"] = "\$config[\"system.logging\"][\"error_level\"] = \"hide\";";
}
if ($lines) {
	$s = file_get_contents($f);
	foreach ($lines as $id => $code) {
		$s = preg_replace("~\n// OpenPlex security: $id\n.*?// end $id\n~s", "\n", $s);
		$s = rtrim($s, "\n") . "\n// OpenPlex security: $id\n" . str_replace("\n", " ", $code) . "\n// end $id\n";
	}
	file_put_contents($f, $s);
}
'
restore_if_broken sites/default/settings.php
# drupal_settings_perms: always, since this script made them writable.
step "protecting settings.php"
chmod 444 sites/default/settings.php
chmod 555 sites/default
