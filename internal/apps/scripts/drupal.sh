step "downloading Drupal"
fetch https://www.drupal.org/download-latest/tar.gz | tar -xz --strip-components=1
cp sites/default/default.settings.php sites/default/settings.php
mkdir -p sites/default/files

cat >"$HOME/install.php" <<'PHP'
<?php
// Unattended install through Drupal's installer API, with MariaDB.
$autoloader = require 'autoload.php';
require_once 'core/includes/install.core.inc';
$url = parse_url(getenv('OPX_URL'));
$https = ($url['scheme'] ?? 'http') === 'https';
$_SERVER += [
  'HTTP_HOST' => $url['host'], 'SERVER_NAME' => $url['host'], 'SERVER_PORT' => $https ? '443' : '80',
  'HTTPS' => $https ? 'on' : 'off', 'REQUEST_URI' => '/', 'SCRIPT_NAME' => '/index.php',
  'SCRIPT_FILENAME' => getcwd() . '/index.php', 'REMOTE_ADDR' => '127.0.0.1', 'REQUEST_METHOD' => 'GET',
  'SERVER_SOFTWARE' => 'OpenPlex',
];
$driver = 'Drupal\\mysql\\Driver\\Database\\mysql';
$db = [
  'database' => getenv('OPX_DB_NAME'), 'username' => getenv('OPX_DB_USER'), 'password' => getenv('OPX_DB_PASSWORD'),
  'host' => getenv('OPX_DB_HOST'), 'port' => '3306', 'prefix' => '',
];
$pass = getenv('OPX_ADMIN_PASSWORD');
install_drupal($autoloader, [
  'parameters' => ['profile' => 'standard', 'langcode' => getenv('OPX_LANG') ?: 'en'],
  'forms' => [
    'install_settings_form' => ['driver' => $driver, $driver => $db, 'op' => 'Save and continue'],
    'install_configure_form' => [
      'site_name' => getenv('OPX_TITLE'), 'site_mail' => getenv('OPX_ADMIN_EMAIL'),
      'account' => ['name' => getenv('OPX_ADMIN_USER'), 'mail' => getenv('OPX_ADMIN_EMAIL'), 'pass' => ['pass1' => $pass, 'pass2' => $pass]],
      'enable_update_status_module' => TRUE, 'enable_update_status_emails' => TRUE, 'op' => 'Save and continue',
    ],
  ],
]);
PHP
step "installing"
php -d memory_limit=384M "$HOME/install.php"
rm -f "$HOME/install.php"
echo "OPX_VERSION=$(php -r 'include "core/lib/Drupal.php"; echo Drupal::VERSION;')"
