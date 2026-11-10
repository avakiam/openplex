#!/bin/sh
# OpenPlex PHP runtime: PHP-FPM (127.0.0.1:9000) and the site's web server
# (:8080). The container stops when either of them does.
php-fpm -F &
fpm=$!
caddy run --adapter caddyfile --config /etc/openplex/Caddyfile &
web=$!
trap 'kill -TERM $fpm $web 2>/dev/null' TERM INT
while kill -0 $fpm 2>/dev/null && kill -0 $web 2>/dev/null; do
	sleep 1
done
kill -TERM $fpm $web 2>/dev/null
wait
exit 1
