#!/bin/sh
# Installs the Go panel next to the existing RAYXO install.
#
#   sudo ./deploy/install.sh ./bin/panel
#
# It does not touch the Laravel panel: the Go panel gets its own binary, its
# own env file (/etc/rayxo/panel.env), its own unit and its own Nginx site.
# Both can read the same MariaDB schema, so the cut-over is a matter of which
# one is enabled — but only one may run the queue at a time.
#
# Environment:
#   RAYXO_DB_DSN        e.g. rayxo@unix(/run/mysqld/mysqld.sock)/rayxo
#   RAYXO_APP_KEY       the Laravel APP_KEY, so existing 2FA secrets still read
#   RAYXO_LISTEN        default 127.0.0.1:8080
#   RAYXO_PANEL_PORT    default 2223, so it does not collide with the Laravel
#                       panel on 2222 while both are installed
#   RAYXO_SERVER_NAME   default: the machine's hostname
#   RAYXO_PUBLIC_IP     optional, written into new zones' A records
#   RAYXO_NAMESERVERS   optional, comma separated, shown on the Domains page

set -eu

BINARY=${1:-}
PREFIX=/usr/local/bin
CONF_DIR=/etc/rayxo
PANEL_ENV="$CONF_DIR/panel.env"
UNIT=/etc/systemd/system/rayxo-panel.service
NGINX_SITE=/etc/nginx/sites-available/rayxo-panel.conf

if [ "$(id -u)" -ne 0 ]; then
    echo "run this as root: sudo $0 $BINARY" >&2
    exit 1
fi

if [ -z "$BINARY" ] || [ ! -x "$BINARY" ]; then
    echo "usage: sudo $0 /path/to/panel" >&2
    exit 2
fi

if [ -z "${RAYXO_DB_DSN:-}" ]; then
    echo "set RAYXO_DB_DSN first, e.g." >&2
    echo "  RAYXO_DB_DSN='rayxo@unix(/run/mysqld/mysqld.sock)/rayxo' sudo -E $0 $BINARY" >&2
    exit 3
fi

port=${RAYXO_PANEL_PORT:-2223}
listen=${RAYXO_LISTEN:-127.0.0.1:8080}
server_name=${RAYXO_SERVER_NAME:-$(hostname -f 2>/dev/null || hostname)}

echo "== user and group"
# rayxo already exists on a RAYXO server; creating it again would change its
# uid, which every home directory depends on.
if ! id rayxo >/dev/null 2>&1; then
    adduser --system --group --home /home/rayxo --shell /usr/sbin/nologin rayxo
fi

if ! getent group rayxo-nginx >/dev/null 2>&1; then
    addgroup --system rayxo-nginx
fi

usermod -aG rayxo-nginx rayxo

echo "== binary"
install -m 0755 "$BINARY" "$PREFIX/rayxo-panel"

echo "== configuration"
install -d -m 0750 -o root -g rayxo-nginx "$CONF_DIR"

umask 077
cat > "$PANEL_ENV" <<EOF
# Written by deploy/install.sh. Keep this file 0600: it holds the database
# password and the APP_KEY.
RAYXO_DB_DSN=$RAYXO_DB_DSN
RAYXO_LISTEN=$listen
RAYXO_AGENT_SOCKET=/run/rayxo/agent.sock
RAYXO_PUBLIC_IP=${RAYXO_PUBLIC_IP:-}
RAYXO_NAMESERVERS=${RAYXO_NAMESERVERS:-}
EOF

if [ -n "${RAYXO_APP_KEY:-}" ]; then
    printf 'RAYXO_APP_KEY=%s\n' "$RAYXO_APP_KEY" >> "$PANEL_ENV"
fi

chown rayxo:rayxo-nginx "$PANEL_ENV"
chmod 0640 "$PANEL_ENV"
umask 022

echo "== schema"
# The migration is idempotent and only adds what is missing.
set -a
. "$PANEL_ENV"
set +a
sudo -u rayxo -H "$PREFIX/rayxo-panel" migrate

echo "== systemd"
install -m 0644 "$(dirname "$0")/rayxo-panel.service" "$UNIT"
systemctl daemon-reload
systemctl enable --now rayxo-panel.service

echo "== nginx"
if [ ! -s "$CONF_DIR/panel.crt" ]; then
    openssl req -x509 -nodes -newkey rsa:2048 -days 3650 \
        -subj "/CN=$server_name" \
        -keyout "$CONF_DIR/panel.key" -out "$CONF_DIR/panel.crt" >/dev/null 2>&1
    chmod 0640 "$CONF_DIR/panel.crt" "$CONF_DIR/panel.key"
    chgrp rayxo-nginx "$CONF_DIR/panel.crt" "$CONF_DIR/panel.key"
fi

sed -e "s/listen 2222/listen $port/" \
    -e "s/\[::\]:2222/[::]:$port/" \
    -e "s/server_name panel.example.com;/server_name $server_name;/" \
    "$(dirname "$0")/nginx.conf" > "$NGINX_SITE"

ln -sf "$NGINX_SITE" /etc/nginx/sites-enabled/rayxo-panel.conf

if nginx -t >/dev/null 2>&1; then
    systemctl reload nginx
else
    echo "nginx refused the site; run 'nginx -t' to see why" >&2
fi

echo
echo "panel:   https://$server_name:$port/"
echo "env:     $PANEL_ENV"
echo
echo "Create the first administrator only if the database has none:"
echo "  sudo -u rayxo $PREFIX/rayxo-panel create-admin"
echo
echo "The Laravel panel is untouched. Disable whichever one you are retiring:"
echo "  systemctl disable --now rayxo-queue.service"
