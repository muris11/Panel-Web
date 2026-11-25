#!/bin/sh
# Removes the MurisCore panel from this host.
#
#   sudo ./deploy/uninstall.sh              # service, site, binary
#   sudo ./deploy/uninstall.sh --keep-env   # also keep /etc/muriscore
#   sudo ./deploy/uninstall.sh --print-only # show the steps, change nothing
#
# It does not touch MariaDB, the agent, the hosting accounts or anything under
# /home. The panel's own tables stay in the database, because another panel may
# be reading them.
#
# The env file holds the only copy of the database password in a form the panel
# can use, so it is kept unless --purge is given explicitly. Certificates are
# kept too: they belong to the host name, not to this program.

set -eu

MODE=remove
KEEP_ENV=1

for argument in "$@"; do
    case "$argument" in
        --print-only) MODE=dry ;;
        --purge) KEEP_ENV=0 ;;
        --keep-env) KEEP_ENV=1 ;;
    esac
done

SERVICE=muriscore-panel
BIN=/usr/local/bin/$SERVICE
UNIT=/etc/systemd/system/$SERVICE.service
CONF_DIR=/etc/muriscore

say() { printf '\n== %s\n' "$*"; }
note() { printf '   %s\n' "$*"; }
die() {
    printf '\n!! %s\n' "$*" >&2
    exit 1
}

run() {
    if [ "$MODE" = dry ]; then
        printf '   would run: %s\n' "$*"
        return 0
    fi

    "$@"
}

if [ "$MODE" != dry ] && [ "$(id -u)" -ne 0 ]; then
    die "run this as root: sudo $0"
fi

say "service"
if [ -d /run/systemd/system ]; then
    if systemctl list-unit-files "$SERVICE.service" >/dev/null 2>&1; then
        run systemctl disable --now "$SERVICE.service" || true
    fi
fi

if [ -f "$UNIT" ]; then
    run rm -f "$UNIT"
    [ "$MODE" = dry ] || systemctl daemon-reload 2>/dev/null || true
    note "removed $UNIT"
else
    note "no unit at $UNIT"
fi

say "nginx"
for site in /etc/nginx/sites-available/$SERVICE.conf /etc/nginx/conf.d/$SERVICE.conf; do
    [ -f "$site" ] || continue

    run rm -f "$site"
    note "removed $site"
done

if [ -L "/etc/nginx/sites-enabled/$SERVICE.conf" ]; then
    run rm -f "/etc/nginx/sites-enabled/$SERVICE.conf"
    note "removed the enabled link"
fi

if [ "$MODE" = dry ]; then
    note "would reload nginx"
elif command -v nginx >/dev/null 2>&1; then
    if nginx -t >/dev/null 2>&1; then
        run systemctl reload nginx 2>/dev/null || run nginx -s reload 2>/dev/null || true
        note "nginx reloaded"
    else
        note "nginx is unhappy about another site; nothing was reloaded"
    fi
fi

say "binary"
if [ -f "$BIN" ]; then
    run rm -f "$BIN"
    note "removed $BIN"
else
    note "no binary at $BIN"
fi

say "settings"
if [ "$KEEP_ENV" -eq 1 ]; then
    note "kept $CONF_DIR (it holds the database password and the certificate)"
    note "remove it yourself with: rm -rf $CONF_DIR"
else
    run rm -rf "$CONF_DIR"
    note "removed $CONF_DIR"
fi

say "left alone on purpose"
note "the muriscore account, its home and any database rows"
note "muriscored, MariaDB, Nginx and every hosting account"

printf '\n'
printf '  The host is back to whatever else is installed on it.\n'
printf '  If another panel was disabled while this one ran, enable it again:\n'
printf '    systemctl enable --now muriscore-queue.service muriscore-scheduler.timer\n'
printf '\n'
