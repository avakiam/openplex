# Applies the security fixes listed in OPX_FIX to a WordPress site; undoes
# them if the website stops working.
wp_cli
W="--skip-plugins --skip-themes"
keep_copy wp-config.php
mu=wp-content/mu-plugins/openplex-disable-xmlrpc.php
if fix wp_file_edit; then
	step "turning off the theme and plugin editor"
	wp config set DISALLOW_FILE_EDIT true --raw $W
fi
if fix wp_debug; then
	step "turning off debug mode"
	wp config set WP_DEBUG false --raw $W
fi
if fix wp_xmlrpc; then
	step "turning off XML-RPC"
	mkdir -p wp-content/mu-plugins
	cat >"$mu" <<'PHP'
<?php
/*
 * Plugin Name: Disable XML-RPC (OpenPlex)
 * Description: Turns off XML-RPC and pingbacks. Added by the OpenPlex security checks; delete this file to turn XML-RPC back on.
 */
if (defined('XMLRPC_REQUEST') && XMLRPC_REQUEST) {
	http_response_code(403);
	header('Content-Type: text/plain');
	exit("XML-RPC is turned off on this site.\n");
}
add_filter('xmlrpc_enabled', '__return_false');
add_filter('wp_headers', function ($h) { unset($h['X-Pingback']); return $h; });
PHP
fi
if fix wp_config_perms; then
	step "making wp-config.php private"
	chmod 600 wp-config.php
fi
if fix wp_xmlrpc && ! site_ok; then
	rm -f "$mu"
fi
restore_if_broken wp-config.php
