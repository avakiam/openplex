# Applies the security fixes listed in OPX_FIX to a Joomla site; undoes the
# configuration changes if the website stops working.
keep_copy configuration.php
chmod u+w configuration.php
php -r '
$f = "configuration.php";
$s = file_get_contents($f);
$fix = " " . getenv("OPX_FIX") . " ";
$set = function ($key, $value) use (&$s) {
	$line = "public \$" . $key . " = " . var_export($value, true) . ";";
	$s = preg_replace("/public \\\$" . $key . " = .*?;\n/", addcslashes($line, "\\\\$") . "\n", $s, 1, $n);
	if ($n !== 1) { fwrite(STDERR, "configuration.php has no \$$key\n"); exit(1); }
};
if (str_contains($fix, " joomla_debug ")) { echo "==> turning off debug mode\n"; $set("debug", false); }
if (str_contains($fix, " joomla_error_reporting ")) { echo "==> hiding PHP errors\n"; $set("error_reporting", "none"); }
if (str_contains($fix, " joomla_force_ssl ")) { echo "==> forcing HTTPS on the whole site\n"; $set("force_ssl", 2); }
file_put_contents($f, $s);
'
if fix joomla_installation_dir; then
	step "deleting the installation folder"
	rm -rf installation
fi
if fix joomla_config_perms; then
	step "making configuration.php private"
	chmod 600 configuration.php
fi
restore_if_broken configuration.php
