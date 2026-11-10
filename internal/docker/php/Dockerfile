# OpenPlex PHP runtime: official PHP-FPM image + the extensions common PHP apps
# (WordPress, Laravel, Symfony, Drupal…) expect, and Caddy serving the site.
# Built locally by openplexd.
ARG PHP_VERSION
FROM php:${PHP_VERSION}-fpm-alpine

RUN set -eux; \
    apk add --no-cache --virtual .build-deps $PHPIZE_DEPS linux-headers \
        icu-dev libzip-dev libpng-dev libjpeg-turbo-dev freetype-dev libwebp-dev libpq-dev; \
    docker-php-ext-configure gd --with-freetype --with-jpeg --with-webp; \
    docker-php-ext-install -j"$(nproc)" bcmath exif gd intl mysqli opcache pdo_mysql pdo_pgsql pgsql zip; \
    pecl install redis-6.1.0; \
    docker-php-ext-enable redis; \
    runDeps="$(scanelf --needed --nobanner --format '%n#p' --recursive /usr/local/lib/php/extensions \
        | tr ',' '\n' | sort -u \
        | awk 'system("[ -e /usr/local/lib/" $1 " ]") == 0 { next } { print "so:" $1 }')"; \
    apk add --no-cache $runDeps icu-data-full; \
    apk del --no-network .build-deps; \
    rm -rf /tmp/pear ~/.pearrc; \
    cp "$PHP_INI_DIR/php.ini-production" "$PHP_INI_DIR/php.ini"; \
    php -m | grep -qi pdo_mysql

COPY openplex.ini $PHP_INI_DIR/conf.d/zz-openplex.ini
COPY fpm.conf /usr/local/etc/php-fpm.d/zz-openplex.conf
COPY --from=caddy:2.10-alpine /usr/bin/caddy /usr/bin/caddy
COPY Caddyfile /etc/openplex/Caddyfile
COPY entrypoint.sh /usr/local/bin/openplex-entrypoint
RUN chmod 0755 /usr/local/bin/openplex-entrypoint
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/openplex-entrypoint"]
CMD []
