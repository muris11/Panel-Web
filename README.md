# MurisCore

A hosting control panel: the same server agent (`muriscored`), the same MariaDB
tables, the same job as the MURISCORE panel it replaces — written in Go with a React
single-page interface and shipped as one static binary.

MurisCore is its own panel, not a skin over the old one. It runs unprivileged,
never shells out, and reaches the server only through named calls to `muriscored`.
The Laravel panel can stay installed beside it and share the same database while
the new one is proven; only one of them should be *running* at a time, because
both write `tasks`.

```
browser ──HTTPS──▶ muriscore-panel ──JSON-RPC over a unix socket──▶ muriscored (root)
                    │                                               │
                    └─────────────── MariaDB ────────────────────────┘
```

## What is here

| Path                | What it is                                                      |
| ------------------- | --------------------------------------------------------------- |
| `cmd/panel`         | the binary: `serve`, `migrate`, `create-admin`, `collect`, `doctor`, `version` |
| `internal/httpapi`  | JSON API under `/api/v1`, sessions, CSRF, the SPA fallback       |
| `internal/store`    | every SQL statement, written out; no query builder               |
| `internal/agent`    | JSON-RPC client for `muriscored`, plus a fake driver for development  |
| `internal/worker`   | claims queued tasks, calls the agent, writes the result back     |
| `internal/collector`| scheduled traffic, resource and certificate accounting           |
| `internal/auth`     | bcrypt (reads PHP `$2y$`), TOTP, `$6$` crypt, APP_KEY crypto     |
| `internal/rules`    | what a username, domain, email and password may be               |
| `internal/db`       | pool, embedded schema, migration runner                          |
| `internal/web`      | the built SPA, embedded with `go:embed`                          |
| `web`               | React 19 + Vite sources for that SPA                             |
| `deploy`            | installer, uninstaller, systemd unit, Nginx site                 |
| `SCOPE.md`          | what is covered, what differs on purpose, what is still open     |

## What it can do

Every screen is wired to a store method and a named agent action; nothing shells
out.

| Screen         | What it does                                                                 |
| -------------- | ---------------------------------------------------------------------------- |
| Overview       | counts, load, memory, disk, recent server work                                |
| Accounts       | create, suspend, unsuspend, delete, per-account detail                        |
| Domains        | add a website (document root + nginx block + zone), edit PHP/HTTPS/docroot    |
| Domain detail  | certificate issue/renew/remove, DNS records add/edit/delete                   |
| Mail           | mail domains, mailboxes with generated passwords, forwarders, DKIM state      |
| Databases      | MySQL databases and logins, grants, password reset, drop                      |
| PostgreSQL     | PostgreSQL databases and logins, grants, password rotation, drop              |
| Redis          | one cache instance per account, password rotation, memory ceiling, removal    |
| FTP            | logins pinned to one account and folder, `$6$` crypt passwords                |
| Packages       | create, edit and delete hosting packages; a changed quota is re-applied        |
| Node apps      | one long-running Node.js process behind a website, port auto-allocated          |
| PHP versions   | the agent's installed versions fill a dropdown on every website form            |
| Files          | browse, read, write, mkdir, delete, compress and extract through the agent    |
| SSH            | turn a shell on when the package has one, add and remove public keys          |
| Tickets        | an account asks for help; an administrator or the owning reseller answers     |
| Announcements  | an administrator or reseller writes once to many accounts, with a reach count |
| Cron           | per-account jobs, applied as a whole crontab                                  |
| Backups        | list archives from disk, start a backup, restore, delete                      |
| Server         | service status/restart/reload, firewall rules, hostname and timezone          |
| Activity       | the audit log of every change                                                 |
| Settings       | own name, email and password, sessions, two-factor, API tokens                |

Behind those screens the panel does its own scheduled work, so a host that runs
only this binary needs no PHP scheduler:

| Collector     | What it keeps current                                                         |
| ------------- | ----------------------------------------------------------------------------- |
| `bandwidth`   | traffic per account and per website per day, from the agent's log cursor       |
| `resources`   | CPU milliseconds and peak memory per account per day                           |
| `ssl_expiry`  | when each website's certificate expires, so certbot renewals show up           |

They run every fifteen minutes inside the panel (`MURISCORE_COLLECT_INTERVAL`),
record the outcome of each run in `collector_runs`, and can be driven once by
hand or from cron with `muriscore-panel collect`. Bandwidth is counted from a
cursor the agent returns: the bytes and the cursor are written in one
transaction, so a crash re-reads a log instead of losing it.

A shell is a package feature twice over: the account's package has to include
SSH, and so does the reseller package of whoever owns the account. The installer
also points `muriscored` at the panel's own user (`--allow-user`), because the agent
accepts exactly one caller besides root.

Two-factor works the Laravel way: the secret is written unconfirmed, signing in
needs no code until the account proves it can generate one, and eight single-use
recovery codes are handed out at setup. Secrets and recovery codes are
APP_KEY-encrypted when a key is configured.

API tokens are Bearer credentials for scripts: the plaintext is shown once, only
a SHA-256 is stored, and a token request skips CSRF because a Bearer header is
not an ambient credential.

## Supported hosts

The panel is one static binary, so the binary itself runs anywhere Linux does.
The installer is what knows about distributions:

| Family | Tested names | Package manager |
| ------ | ------------ | --------------- |
| Debian | Debian 12/13, Ubuntu 22.04/24.04, Mint, Pop, Kali, Devuan | `apt` |
| RHEL   | AlmaLinux 9, Rocky 9, CentOS Stream 9, Fedora, Oracle Linux | `dnf`/`yum` |
| SUSE   | openSUSE Leap/Tumbleweed, SLES | `zypper` |

Anything else still works with `--no-nginx` when Nginx and MariaDB are already
installed; `muriscore-panel doctor` says what is missing. On the RHEL family the
installer sets the SELinux boolean a reverse proxy needs and opens the panel port
in `firewalld`; on Ubuntu it does the same for `ufw`, but only when that firewall
is already active.

What the panel needs on any host:

- MariaDB 10.6+ or MySQL 8;
- Nginx (or any reverse proxy) for TLS in front of `127.0.0.1:8080`;
- `muriscored` for anything real to happen. Without it, `MURISCORE_AGENT_DRIVER=fake`
  lets the panel run end to end and change nothing.

## Build

```sh
npm --prefix web ci
npm --prefix web run build          # → internal/web/dist, which is embedded next
go build -trimpath -ldflags "-s -w -X main.version=$(cat VERSION 2>/dev/null || echo dev)" \
    -o bin/panel ./cmd/panel
```

`scripts/build.sh` does both. The result is a single file of about 8.5 MB with
`-s -w`; `CGO_ENABLED=0` keeps it static, so it can be copied to any Linux host.

## Run it

```sh
export MURISCORE_DB_DSN='muriscore:secret@tcp(127.0.0.1:3306)/muriscore?parseTime=true&loc=UTC'
export MURISCORE_AGENT_DRIVER=socket
export MURISCORE_AGENT_SOCKET=/run/muriscore/agent.sock
export MURISCORE_LISTEN=127.0.0.1:8080
export MURISCORE_SECURE_COOKIES=true

./bin/panel migrate
./bin/panel create-admin admin you@example.com
./bin/panel serve
```

`create-admin` prints the generated password once. It is not written to a log on
purpose: logs get copied to backups.

### Configuration

Every setting is read as `MURISCORE_<NAME>`. Nothing reads a second, older
prefix: one name, one place to look.

| Variable                     | Default                 | Notes                                                     |
| ---------------------------- | ----------------------- | --------------------------------------------------------- |
| `MURISCORE_DB_DSN`           | — (required)            | must include `parseTime=true`                              |
| `MURISCORE_LISTEN`           | `127.0.0.1:8080`        | behind Nginx on the same host                              |
| `MURISCORE_AGENT_DRIVER`     | `fake`                  | `socket` on a real server                                  |
| `MURISCORE_AGENT_SOCKET`     | `/run/muriscore/agent.sock` | the agent owns this path; the panel only reads it          |
| `MURISCORE_AGENT_TIMEOUT`    | `30s`                   | one call a page waits on                                   |
| `MURISCORE_AGENT_TASK_TIMEOUT` | `5m`                  | one queued task                                            |
| `MURISCORE_SESSION_TTL`      | `12h`                   |                                                            |
| `MURISCORE_SECURE_COOKIES`   | `false`                 | `true` behind HTTPS, which production always is            |
| `MURISCORE_APP_KEY`          | —                       | the old panel's key, to read encrypted two-factor secrets  |
| `MURISCORE_TRUSTED_PROXIES`  | —                       | comma-separated addresses allowed to set `X-Forwarded-For` |
| `MURISCORE_PUBLIC_IP`        | —                       | written into new zones' A records                          |
| `MURISCORE_NAMESERVERS`      | —                       | comma separated, the NS rows every new zone gets           |
| `MURISCORE_COLLECT_INTERVAL` | `15m`                   | collectors run this often; `off` disables them             |

`MURISCORE_APP_KEY` matters for one thing: the Laravel panel stored
`users.two_factor_secret` encrypted with its `APP_KEY`. Without the key those
accounts sign in with their password only, until they enrol again.

## Deployment

```sh
sudo MURISCORE_DB_DSN='muriscore@unix(/run/mysqld/mysqld.sock)/muriscore' \
     MURISCORE_APP_KEY="$(grep ^APP_KEY /var/www/muriscore/.env | cut -d= -f2-)" \
     ./deploy/install.sh ./bin/panel
```

The installer:

- detects the distribution and installs only what is missing (`nginx`, a MariaDB
  client, `openssl`) with that distribution's package manager;
- creates the unprivileged `muriscore` account and adds it to whatever group owns
  `/run/muriscore/agent.sock`, so the socket is reachable and nothing else is;
- takes the database from `MURISCORE_DB_DSN`, else from `/var/www/muriscore/.env`,
  else creates a database and a user with a generated password;
- writes `/etc/muriscore/muriscore.env` (0600) and a self-signed certificate;
- installs `muriscore-panel` to `/usr/local/bin`, runs `migrate`, and enables
  `muriscore-panel.service` under systemd;
- enables the Nginx site on port **2222**, or **2223** when 2222 is already
  listening (which is what a host with the Laravel panel sees);
- sets the SELinux boolean and opens the firewall port where those exist;
- finishes by running `muriscore-panel doctor` and printing the health answer.

`--print-only` shows every step without changing anything; `--no-nginx` stops
after the service. Both are safe on a host where only the binary should change.

To take it off again:

```sh
sudo ./deploy/uninstall.sh             # service, site, binary; keeps /etc/muriscore
sudo ./deploy/uninstall.sh --purge     # also removes the env file and certificate
```

It leaves MariaDB, `muriscored`, the hosting accounts and the panel's tables alone,
because another panel may still be reading them.

`deploy/muriscore-panel.service` and `deploy/nginx.conf` are the files it
installs and work on their own too. The parts that matter:

- the panel runs as an unprivileged account and may read the agent socket
  (group-shared, `0660`);
- the unit sets `ProtectSystem=strict`, `PrivateTmp`, `NoNewPrivileges` and a
  syscall filter, and the panel writes nothing to disk;
- Nginx terminates TLS and proxies to `127.0.0.1:8080`, and the panel sets its
  own CSP and security headers so there is one place to change them.

Only one panel may drain the task queue against a database at a time: Laravel
encrypted `tasks.payload` and this panel writes plain JSON.

### Checking a host

```sh
muriscore-panel doctor
```

It reports the distribution, systemd, nginx, the MariaDB client, the agent
socket, the database, whether the listen address is free, the config directory
and the service account — `OK`, `WARN` or `FAIL` per line, and a non-zero exit
when something would stop the panel from running. This is the first command to
run on a distribution nobody tried before.

## Security model

- No shell. Every change is a method on `muriscored`; the panel has no `exec`.
- Sessions: 32 random bytes in a cookie, only a SHA-256 of the token in the
  database, ten concurrent sessions per account at most, one row per sign-in so
  an operator can end a session they do not recognise.
- CSRF: a token in the cookie before sign-in, then a per-session token sent as
  `X-CSRF-Token`; both are compared in constant time.
- Sign-in limits: 20 attempts per address and 8 per username in 15 minutes.
- A suspended hosting account cannot sign in, and the reason is shown.
- Resellers see only their own accounts; another reseller's account answers
  `404`, not `403`, so usernames are not enumerable.
- Passwords are bcrypt cost 12; a hash of a different cost is upgraded on the
  next successful sign-in.
- Every change writes an `audit_logs` row, and agent work writes a `tasks` row
  before it is dispatched, so work that never ran is still visible.

## Compatibility with the Laravel panel

The tables are the ones the Laravel panel already uses: `users`, `packages`,
`domains`, `tasks`, `audit_logs`. Data written by either panel is readable by the
other, with two exceptions:

1. **Task payloads.** Laravel encrypted the `tasks.payload` column; this panel
   writes plain JSON. Do not run both panels against one database and expect
   queues to work — pick one.
2. **Statuses.** Both use `queued`/`running`/`success`/`failed`/`cancelled` for
   tasks and `pending`/`active`/`suspended`/`deleting`/`failed` for accounts,
   which is why those strings live in `internal/store/store.go` and
   `App\Enums` rather than in the handlers.

The reserved-username list is necessarily duplicated: `internal/rules`,
`agent/internal/validate`, and `App\Rules\ValidUsername`. All three must agree.

## Tests

```sh
go test ./...                       # unit tests, no database needed

# the API tests need a throwaway database whose name ends in _test:
MURISCORE_TEST_DSN='root@tcp(127.0.0.1:3307)/muriscore_test?parseTime=true&loc=UTC' \
    go test ./internal/httpapi/ -v
```

The API tests create a panel user, sign in, create a hosting account, drain the
queue through the fake agent, and then check that the account is active, that a
wrong password is refused, that CSRF is enforced, that a reseller cannot read
another reseller's account, and that deleting needs the username typed back.

## Looking at it

```sh
node scripts/screenshot.mjs    # PNGs of every screen into .shots/
node scripts/measure.mjs       # fails on overflow, clipping or overlap at 1440/1024/390px
```

Both need a running panel and `MURISCORE_PASSWORD`.

## Licence

AGPL-3.0, the same as the MURISCORE server it drives. See `LICENSE`.
