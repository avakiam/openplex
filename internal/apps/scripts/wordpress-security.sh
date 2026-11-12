# Security checks of a WordPress site (WP-CLI is already there: this runs
# after wordpress-check.sh).
W="--skip-plugins --skip-themes"
v=$(wp config get DISALLOW_FILE_EDIT $W 2>/dev/null || true)
[ "$v" = 1 ] && sec wp_file_edit 1 || sec wp_file_edit 0
v=$(wp config get WP_DEBUG $W 2>/dev/null || true)
[ "$v" = 1 ] && sec wp_debug 0 || sec wp_debug 1
# XML-RPC answers a method list when it is on.
if curl -s --max-time 30 -H "Host: $OPX_HOST" -H 'Content-Type: text/xml' \
	--data '<?xml version="1.0"?><methodCall><methodName>system.listMethods</methodName></methodCall>' \
	http://127.0.0.1:8080/xmlrpc.php | grep -q '<string>'; then
	sec wp_xmlrpc 0
else
	sec wp_xmlrpc 1
fi
mode=$(stat -c %a wp-config.php)
case "$mode" in *0) sec wp_config_perms 1 "$mode" ;; *) sec wp_config_perms 0 "$mode" ;; esac
admins=$(wp user list --role=administrator --field=user_login $W | tr '\n' ' ')
case " $admins " in *" admin "*) sec wp_admin_user 0 ;; *) sec wp_admin_user 1 ;; esac
inactive=$(wp plugin list --status=inactive --field=name $W | tr '\n' ' ')
[ -z "${inactive% }" ] && sec wp_inactive_plugins 1 || sec wp_inactive_plugins 0 "${inactive% }"
sec_report
