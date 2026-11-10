# OpenPlex static-site web server: Caddy serving one site's files as the
# site's uid. The binary is copied so it loses the file capabilities of the
# official image (they can't be used with CapDrop ALL + no-new-privileges).
FROM caddy:2.10-alpine AS caddy
FROM caddy:2.10-alpine
COPY --from=caddy /usr/bin/caddy /usr/bin/caddy
COPY Caddyfile /etc/openplex/Caddyfile
EXPOSE 8080
CMD ["caddy", "run", "--adapter", "caddyfile", "--config", "/etc/openplex/Caddyfile"]
