#!/bin/sh
# MurisCore panel installer.
#
#   sudo ./deploy/install.sh ./bin/panel
#
# One script for the distributions a hosting server is usually built on:
# Ubuntu, Debian, and their family (Mint, Pop, Kali, Devuan), plus the RHEL
# family (AlmaLinux, Rocky, CentOS Stream, Fedora, Oracle) and openSUSE. It
# installs only what is missing, so it is safe to re-run.
#
# It installs the panel *beside* an existing RAYXO/Laravel panel. Nothing in
# /var/www is touched. Both panels can share one MariaDB schema, so cutting
# over is a matter of which one is enabled — but only one may drain the task
# queue at a time: Laravel encrypts tasks.payload and this panel writes plain
# JSON.
#
# Environment:
#   MURISCORE_DB_DSN      MariaDB DSN. If unset, the installer reuses the DSN
#                         from /var/www/rayxo/.env, or creates a database.
#   MURISCORE_APP_KEY     the Laravel APP_KEY, so existing 2FA secrets still read
#   MURISCORE_ADMIN_EMAIL if set, the first administrator is created with a
#                         generated password printed once
#   MURISCORE_PANEL_PORT  default 2222, or 2223 when 2222 is already in use
#   MURISCORE_LISTEN      default 127.0.0.1:8080
#   MURISCORE_SERVER_NAME default: this machine's hostname
#   MURISCORE_PUBLIC_IP   optional, written into new zones' A records
#   MURISCORE_NAMESERVERS optional, comma separated
#   MURISCORE_USER        service account, default "muriscore"
#
# Flags:
#   --print-only          show what would happen, change nothing
#   --no-nginx            install the binary and the unit, skip Nginx

set -eu

case "${1:-}" in
    --*) BINARY= ;;
    *) BINARY=${1:-} ;;
esac

MODE=install

for argument in "$@"; do
    case "$argument" in
        --print-only) MODE=dry ;;
        --no-nginx) NO_NGINX=1 ;;
    esac
done

[ "$MODE" = dry ] || MODE=install

PANEL_USER=${MURISCORE_USER:-muriscore}
SERVICE=muriscore-panel
BIN_DIR=/usr/local/bin
CONF_DIR=/etc/muriscore
ENV_FILE="$CONF_DIR/muriscore.env"
LISTEN=${MURISCORE_LISTEN:-127.0.0.1:8080}
DATABASE_NAME=rayxo
NO_NGINX=${NO_NGINX:-0}

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
    die "run this as root: sudo $0 $BINARY"
fi

if [ "$MODE" = install ]; then
    [ -n "$BINARY" ] && [ -f "$BINARY" ] || die "usage: sudo $0 /path/to/panel"
fi

# ---------------------------------------------------------------------------
# Which distribution is this?

OS_ID=unknown
OS_VERSION=
OS_NAME=unknown
OS_LIKE=
FAMILY=unknown

if [ -r /etc/os-release ]; then
    # shellcheck disable=SC1091
    . /etc/os-release
    OS_ID=${ID:-unknown}
    OS_VERSION=${VERSION_ID:-}
    OS_NAME=${PRETTY_NAME:-${NAME:-unknown}}
    OS_LIKE=${ID_LIKE:-}
fi

case "$OS_ID" in
    debian|ubuntu|linuxmint|pop|elementary|zorin|kali|devuan|raspbian|mx) FAMILY=debian ;;
    rhel|centos|almalinux|rocky|fedora|ol|amzn) FAMILY=rhel ;;
    opensuse*|sles|suse) FAMILY=suse ;;
    *)
        case " $OS_LIKE " in
            *debian*|*ubuntu*) FAMILY=debian ;;
            *rhel*|*fedora*|*centos*) FAMILY=rhel ;;
            *suse*) FAMILY=suse ;;
        esac
        ;;
esac

say "host"
note "$OS_NAME ($OS_ID ${OS_VERSION:-unknown}), family $FAMILY"
note "architecture $(uname -m), kernel $(uname -r)"

if [ "$FAMILY" = unknown ]; then
    note "this distribution is not in the list; packages will not be installed."
    note "the panel binary and the unit still work if nginx and MariaDB are already here."
fi

pkg_install() {
    [ "$FAMILY" = unknown ] && return 1

    case "$FAMILY" in
        debian)
            export DEBIAN_FRONTEND=noninteractive
            run apt-get update -qq
            run apt-get install -y -qq "$@"
            ;;
        rhel)
            if command -v dnf >/dev/null 2>&1; then
                run dnf install -y -q "$@"
            else
                run yum install -y -q "$@"
            fi
            ;;
        suse) run zypper --non-interactive install "$@" ;;
    esac
}

have() { command -v "$1" >/dev/null 2>&1; }

# ---------------------------------------------------------------------------
# Packages the panel and its deployment need.

say "packages"
missing=
for command in nginx openssl; do
    have "$command" || missing="$missing $command"
done

mysql_client=mariadb
have mariadb || have mysql || mysql_client=

if [ -z "$mysql_client" ]; then
    case "$FAMILY" in
        debian) missing="$missing mariadb-client" ;;
        rhel) missing="$missing mariadb" ;;
        suse) missing="$missing mariadb-client" ;;
    esac
fi

if [ -n "$missing" ]; then
    note "installing:$missing"
    # shellcheck disable=SC2086
    if ! pkg_install $missing; then
        note "could not install:$missing — install them yourself, or re-run with --no-nginx"
    fi
else
    note "nginx, openssl and a MariaDB client are already present"
fi

have nginx || [ "$NO_NGINX" = 1 ] || die "nginx is missing; install it, or pass --no-nginx"

# ---------------------------------------------------------------------------
# Account. A hosting panel runs unprivileged, so the service account exists
# only to own one socket and one env file.

say "service account"
if id "$PANEL_USER" >/dev/null 2>&1; then
    note "$PANEL_USER already exists, left alone"
else
    nologin=/usr/sbin/nologin
    [ -x "$nologin" ] || nologin=/sbin/nologin

    run useradd --system --create-home --home-dir "/home/$PANEL_USER" --shell "$nologin" "$PANEL_USER"
    [ "$MODE" = dry ] || note "created $PANEL_USER"
fi

# The panel reaches rayxod through a group-shared socket. Whatever group owns
# that socket, the panel user has to be in it.
AGENT_SOCKET=${MURISCORE_AGENT_SOCKET:-/run/rayxo/agent.sock}
AGENT_GROUP=

if [ -S "$AGENT_SOCKET" ]; then
    AGENT_GROUP=$(stat -c %G "$AGENT_SOCKET" 2>/dev/null || stat -f %Sg "$AGENT_SOCKET" 2>/dev/null || true)
fi

if [ -n "$AGENT_GROUP" ] && [ "$AGENT_GROUP" != "$PANEL_USER" ]; then
    note "agent socket belongs to group $AGENT_GROUP, adding $PANEL_USER to it"
    run usermod -aG "$AGENT_GROUP" "$PANEL_USER"
else
    note "agent socket not found yet at $AGENT_SOCKET (the doctor will report it)"
fi

# ---------------------------------------------------------------------------
# Database. Three ways, in order of trust: an explicit DSN, the DSN of the
# Laravel panel already installed, or a database this script creates.

say "database"
DB_DSN=${MURISCORE_DB_DSN:-${RAYXO_DB_DSN:-}}
DB_SOURCE="MURISCORE_DB_DSN"

if [ -z "$DB_DSN" ] && [ -r /var/www/rayxo/.env ]; then
    note "reading the existing panel's database settings from /var/www/rayxo/.env"

    env_value() {
        sed -n "s/^$1=//p" /var/www/rayxo/.env | head -n 1 | sed -e 's/^"//' -e 's/"$//'
    }

    DB_NAME=$(env_value DB_DATABASE)
    DB_USER=$(env_value DB_USERNAME)
    DB_PASS=$(env_value DB_PASSWORD)

    if [ -n "$DB_NAME" ] && [ -n "$DB_USER" ]; then
        DATABASE_NAME=$DB_NAME
        DB_DSN="$DB_USER:$DB_PASS@unix(/run/mysqld/mysqld.sock)/$DB_NAME?parseTime=true&loc=UTC"
        DB_SOURCE="/var/www/rayxo/.env"
    fi
fi

if [ -z "$DB_DSN" ]; then
    MYSQL=
    for candidate in mariadb mysql; do
        have "$candidate" && MYSQL=$candidate && break
    done

    for socket in /run/mysqld/mysqld.sock /var/lib/mysql/mysql.sock /run/mysql/mysql.sock; do
        [ -S "$socket" ] || continue
        "$MYSQL" --protocol=socket --socket="$socket" -u root -e 'SELECT 1' >/dev/null 2>&1 || continue
        MYSQL_SOCKET=$socket
        break
    done

    if [ -n "${MYSQL_SOCKET:-}" ]; then
        password=$(head -c 48 /dev/urandom | base64 | tr -dc 'a-zA-Z0-9' | head -c 24)

        note "creating database $DATABASE_NAME and user $PANEL_USER (password stored in $ENV_FILE)"
        run "$MYSQL" --protocol=socket --socket="$MYSQL_SOCKET" -u root -e "
            CREATE DATABASE IF NOT EXISTS \`$DATABASE_NAME\`
                CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci;
            CREATE USER IF NOT EXISTS '$PANEL_USER'@'localhost' IDENTIFIED BY '$password';
            GRANT ALL PRIVILEGES ON \`$DATABASE_NAME\`.* TO '$PANEL_USER'@'localhost';
            FLUSH PRIVILEGES;"

        DB_DSN="$PANEL_USER:$password@unix($MYSQL_SOCKET)/$DATABASE_NAME?parseTime=true&loc=UTC"
        DB_SOURCE="created by this installer"
    else
        die "no database: set MURISCORE_DB_DSN, or make sure MariaDB is running as root over its socket"
    fi
fi

note "database $DATABASE_NAME, from $DB_SOURCE"

# ---------------------------------------------------------------------------
# Certificate and port. The Laravel panel already owns 2222 on a server that
# has one, so a second panel gets 2223 instead of fighting over the port.

say "certificate and port"
PORT=${MURISCORE_PANEL_PORT:-}
if [ -z "$PORT" ]; then
    PORT=2222
    if have ss && ss -ltn 2>/dev/null | grep -q '[:.]2222 '; then
        PORT=2223
        note "2222 is already listening (probably the Laravel panel), using 2223"
    fi
fi

SERVER_NAME=${MURISCORE_SERVER_NAME:-$(hostname -f 2>/dev/null || hostname 2>/dev/null || echo panel.local)}
CERT=$CONF_DIR/panel.crt
KEY=$CONF_DIR/panel.key

run install -d -m 0750 -o root -g "$PANEL_USER" "$CONF_DIR"

if [ ! -s "$CERT" ]; then
    run openssl req -x509 -nodes -newkey rsa:2048 -days 3650 \
        -subj "/CN=$SERVER_NAME" \
        -keyout "$KEY" -out "$CERT"
    [ "$MODE" = dry ] || chmod 0640 "$CERT" "$KEY"
    note "self-signed certificate for $SERVER_NAME (replace it with a real one when DNS points here)"
else
    note "keeping the existing certificate at $CERT"
fi

# ---------------------------------------------------------------------------
# Files.

say "binary and settings"
run install -m 0755 "$BINARY" "$BIN_DIR/$SERVICE"

if [ "$MODE" = dry ]; then
    note "would write $ENV_FILE"
else
    umask 077
    {
        echo "# Written by deploy/install.sh on $(date -u +%Y-%m-%dT%H:%M:%SZ)."
        echo "# 0600: it holds the database password and possibly the APP_KEY."
        echo "MURISCORE_DB_DSN=$DB_DSN"
        echo "MURISCORE_LISTEN=$LISTEN"
        echo "MURISCORE_AGENT_DRIVER=socket"
        echo "MURISCORE_AGENT_SOCKET=$AGENT_SOCKET"
        echo "MURISCORE_SECURE_COOKIES=true"
        echo "MURISCORE_PUBLIC_IP=${MURISCORE_PUBLIC_IP:-}"
        echo "MURISCORE_NAMESERVERS=${MURISCORE_NAMESERVERS:-}"
        if [ -n "${MURISCORE_APP_KEY:-${RAYXO_APP_KEY:-}}" ]; then
            echo "MURISCORE_APP_KEY=${MURISCORE_APP_KEY:-${RAYXO_APP_KEY:-}}"
        fi
    } > "$ENV_FILE"

    chown "$PANEL_USER:$PANEL_USER" "$ENV_FILE"
    chmod 0600 "$ENV_FILE"
    umask 022
fi

# ---------------------------------------------------------------------------
# Schema. The migration only adds what is missing, so it is safe on a database
# the Laravel panel already filled.

say "schema"
if [ "$MODE" = dry ]; then
    note "would run: $SERVICE migrate"
else
    set -a
    # shellcheck disable=SC1090
    . "$ENV_FILE"
    set +a

    sudo -u "$PANEL_USER" -H "$BIN_DIR/$SERVICE" migrate
fi

# ---------------------------------------------------------------------------
# Service.

say "service"
UNIT=/etc/systemd/system/$SERVICE.service

if [ "$MODE" = dry ]; then
    note "would install $UNIT"
elif [ -d /run/systemd/system ]; then
    sed -e "s|@PANEL_USER@|$PANEL_USER|g" \
        -e "s|@PANEL_GROUP@|$PANEL_USER|g" \
        -e "s|@ENV_FILE@|$ENV_FILE|g" \
        -e "s|@BINARY@|$BIN_DIR/$SERVICE|g" \
        "$(dirname "$0")/muriscore-panel.service" > "$UNIT"
    chmod 0644 "$UNIT"

    systemctl daemon-reload
    systemctl enable --now "$SERVICE.service"
    sleep 2
    systemctl is-active --quiet "$SERVICE.service" ||
        die "$SERVICE did not start; run: journalctl -u $SERVICE -n 50 --no-pager"
    note "$SERVICE is running"
else
    note "systemd is not running; start $BIN_DIR/$SERVICE serve with $ENV_FILE yourself"
fi

# ---------------------------------------------------------------------------
# Nginx.

if [ "$NO_NGINX" = 1 ]; then
    note "skipping nginx (--no-nginx)"
else
    say "nginx"

    if [ -d /etc/nginx/sites-available ]; then
        SITE=/etc/nginx/sites-available/$SERVICE.conf
        LINK=/etc/nginx/sites-enabled/$SERVICE.conf
    else
        SITE=/etc/nginx/conf.d/$SERVICE.conf
        LINK=
    fi

    if [ "$MODE" = dry ]; then
        note "would write $SITE on port $PORT"
    else
        sed -e "s|@UPSTREAM@|$LISTEN|g" \
            -e "s|@PORT@|$PORT|g" \
            -e "s|@SERVER_NAME@|$SERVER_NAME|g" \
            -e "s|@CERT@|$CERT|g" \
            -e "s|@KEY@|$KEY|g" \
            "$(dirname "$0")/nginx.conf" > "$SITE"

        [ -n "$LINK" ] && ln -sf "$SITE" "$LINK"

        # Other distributions ship a default site on 80 only, so it does not
        # collide with the panel's port; nothing is removed here.

        if nginx -t >/dev/null 2>&1; then
            systemctl reload nginx 2>/dev/null || nginx -s reload 2>/dev/null || true
            note "site enabled on port $PORT"
        else
            note "nginx refused the site; run 'nginx -t' to see why"
        fi
    fi

    # SELinux on the RHEL family blocks the reverse proxy by default.
    if have selinuxenabled && selinuxenabled 2>/dev/null; then
        run setsebool -P httpd_can_network_connect 1 || true
        note "SELinux: allowed nginx to reach the panel"
    fi

    # Open the port where a firewall is already running; do not install one.
    if have firewall-cmd && firewall-cmd --state >/dev/null 2>&1; then
        run firewall-cmd --permanent --add-port="$PORT/tcp" >/dev/null || true
        run firewall-cmd --reload >/dev/null || true
        note "firewalld: opened $PORT/tcp"
    elif have ufw && ufw status 2>/dev/null | grep -q '^Status: active'; then
        run ufw allow "$PORT/tcp" >/dev/null || true
        note "ufw: opened $PORT/tcp"
    fi
fi

# ---------------------------------------------------------------------------
# First administrator.

if [ -n "${MURISCORE_ADMIN_EMAIL:-}" ] && [ "$MODE" = install ]; then
    say "first administrator"
    if sudo -u "$PANEL_USER" -H "$BIN_DIR/$SERVICE" create-admin admin "$MURISCORE_ADMIN_EMAIL"; then
        note "the password above is shown once; store it now"
    else
        note "no administrator was created (one may already exist); use create-admin later"
    fi
fi

# ---------------------------------------------------------------------------
# Proof.

if [ "$MODE" = install ] && [ -d /run/systemd/system ]; then
    say "doctor"
    # shellcheck disable=SC1090
    ( set -a; . "$ENV_FILE"; set +a; sudo -u "$PANEL_USER" -H "$BIN_DIR/$SERVICE" doctor ) || true

    if have curl; then
        health=$(curl -fsS --max-time 5 "http://$LISTEN/api/v1/health" 2>/dev/null || true)
        [ -n "$health" ] && note "health: $health"
    fi
fi

printf '\n'
printf '  panel:  https://%s:%s/\n' "$SERVER_NAME" "$PORT"
printf '  env:    %s\n' "$ENV_FILE"
printf '  doctor: %s doctor\n' "$BIN_DIR/$SERVICE"
printf '\n'
printf '  If the database had no administrator, create one now:\n'
printf '    sudo -u %s %s create-admin admin you@example.com\n' "$PANEL_USER" "$BIN_DIR/$SERVICE"
printf '\n'
printf '  Only one panel may drain the task queue. When retiring the Laravel one:\n'
printf '    systemctl disable --now rayxo-queue.service rayxo-scheduler.timer\n'
printf '\n'
