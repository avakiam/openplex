# Joomla updates itself from the command line; its extensions are updated
# from its administration (System > Update > Extensions).
if [ "$OPX_CORE" = 1 ]; then
	step "updating Joomla"
	php -d memory_limit=512M cli/joomla.php core:update -n
fi
echo "OPX_VERSION=$(php -r 'define("_JEXEC", 1); require "libraries/src/Version.php"; echo (new Joomla\CMS\Version())->getShortVersion();')"
site_check
