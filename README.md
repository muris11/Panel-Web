# RAYXO panel (Go)

A replacement control panel for [RAYXO](../README.md): the same server agent
(`rayxod`), the same MariaDB tables, the same job — written in Go with a React
single-page interface, and shipped as one static binary.

This directory is a parallel implementation, not a refactor of the Laravel
panel. The two can point at the same database while the new one grows; only
one of them should be *running* at a time, because they both write `tasks`.

```
browser ──HTTPS──▶ panel (this program)  ──JSON-RPC over a unix socket──▶ rayxod (root)
                    │                                                    │
                    └────────────── MariaDB ──────────────────────────────┘
```

The panel never runs a command and never runs as root. Every change to the
server is a named call to `rayxod`, which validates it again on its own side.

## What is here

| Path                     | What it is                                                     |
| ------------------------ | -------------------------------------------------------------- |
| `cmd/panel`              | the binary: `serve`, `migrate`, `create-admin`, `version`       |
| `internal/httpapi`       | JSON API under `/api/v1`, sessions, CSRF, the SPA fallback      |
| `internal/store`         | every SQL statement, written out; no query builder              |
| `internal/agent`         | JSON-RPC client for `rayxod`, plus a fake driver for development |
| `internal/worker`        | claims queued tasks, calls the agent, writes the result back    |
| `internal/auth`          | bcrypt (reads PHP `$2y$`), TOTP, `$6$` crypt, APP_KEY crypto    |
| `internal/rules`         | what a username, domain, email and password may be              |
| `internal/db`            | pool, embedded schema, migration runner                         |
| `internal/web`           | the built SPA, embedded with `go:embed`                         |
| `web`                    | React 19 + Vite sources for that SPA                            |
| `scripts`                | `build.sh`, screenshot and layout-measurement helpers           |

## What it can do

Every screen below is wired to a store method and a named agent action; nothing
shells out.

| Screen            | What it does                                                                 |
| ----------------- | ---------------------------------------------------------------------------- |
| Overview          | counts, load, memory, disk, recent server work                                |
| Accounts          | create, suspend, unsuspend, delete, per-account detail                        |
| Domains           | add a website (document root + nginx block + zone), edit PHP/HTTPS/docroot    |
| Domain detail     | certificate issue/renew/remove, DNS records add/edit/delete                   |
| Mail              | mail domains, mailboxes with generated passwords, forwarders, DKIM state      |
| Databases         | databases and MySQL logins, grants, password reset, drop                     |
| FTP               | logins pinned to one account and folder, `$6$` crypt passwords                |
| Files             | browse, read, write, mkdir, delete, compress and extract through the agent    |
| Cron              | per-account jobs, applied as a whole crontab                                  |
| Backups           | list archives from disk, start a backup, restore, delete                      |
| Server (admin)    | service status/restart/reload, firewall rules, hostname and timezone          |
| Activity          | the audit log of every change                                                 |
| Settings          | sessions, two-factor enrolment with recovery codes, API tokens                |

Two-factor works the Laravel way: the secret is written unconfirmed, signing in
needs no code until the account proves it can generate one, and eight single-use
recovery codes are handed out at setup. Secrets and recovery codes are
APP_KEY-encrypted when a key is configured.

API tokens are Bearer credentials for scripts: the plaintext is shown once, only
a SHA-256 is stored, and a token request skips CSRF because a Bearer header is
not an ambient credential.


## Requirements

- Go 1.24 or newer.
- Node 20 or newer, with npm, to build the interface.
- MariaDB 10.6+ or MySQL 8 (the SQL is written for one, not for both).
- `rayxod` for anything real to happen; without it, `RAYXO_AGENT_DRIVER=fake`
  lets the panel run end to end and change nothing.

## Build

```sh
cd panel-go

# 1. the interface → internal/web/dist (which is embedded next)
npm --prefix web ci
npm --prefix web run build

# 2. the binary
go build -trimpath -ldflags "-s -w -X main.version=$(cat VERSION 2>/dev/null || echo dev)" \
    -o bin/panel ./cmd/panel
```

`scripts/build.sh` does both. The result is a single file:

```sh
$ ls -lh bin/panel
-rwxr-xr-x 1 root root 12M bin/panel
```

## Run it

```sh
export RAYXO_DB_DSN='rayxo:secret@tcp(127.0.0.1:3306)/rayxo?parseTime=true&loc=UTC'
export RAYXO_AGENT_DRIVER=socket
export RAYXO_AGENT_SOCKET=/run/rayxo/agent.sock
export RAYXO_LISTEN=127.0.0.1:8080
export RAYXO_SECURE_COOKIES=true

./bin/panel migrate                 # creates what is missing, changes nothing else
./bin/panel create-admin admin you@example.com
./bin/panel serve
```

`create-admin` prints the generated password once. It is not written to a log
on purpose: logs get copied to backups.

### Configuration

| Variable                  | Default                | Notes                                                       |
| ------------------------- | ---------------------- | ----------------------------------------------------------- |
| `RAYXO_DB_DSN`            | — (required)           | must include `parseTime=true`                                |
| `RAYXO_LISTEN`            | `127.0.0.1:8080`       | behind Nginx on the same host                                |
| `RAYXO_AGENT_DRIVER`      | `fake`                 | `socket` on a real server                                    |
| `RAYXO_AGENT_SOCKET`      | `/run/rayxo/agent.sock`|                                                              |
| `RAYXO_AGENT_TIMEOUT`     | `30s`                  | one call a page waits on                                     |
| `RAYXO_AGENT_TASK_TIMEOUT`| `5m`                   | one queued task                                              |
| `RAYXO_SESSION_TTL`       | `12h`                  |                                                              |
| `RAYXO_SECURE_COOKIES`    | `false`                | `true` behind HTTPS, which production always is              |
| `RAYXO_APP_KEY`           | —                      | the old panel's key, to read encrypted two-factor secrets    |
| `RAYXO_TRUSTED_PROXIES`   | —                      | comma-separated addresses allowed to set `X-Forwarded-For`   |

`RAYXO_APP_KEY` matters for one thing: the Laravel panel stored
`users.two_factor_secret` encrypted with its `APP_KEY`. Without the key those
accounts sign in with their password only, until they enrol again.

## Deployment

`deploy/install.sh` does the whole install on a RAYXO server, next to the
existing Laravel panel rather than over it:

```sh
sudo RAYXO_DB_DSN='rayxo@unix(/run/mysqld/mysqld.sock)/rayxo' \
     RAYXO_APP_KEY="$(grep ^APP_KEY /var/www/rayxo/.env | cut -d= -f2-)" \
     ./deploy/install.sh ./bin/panel
```

It creates the `rayxo` user if it is missing, adds it to `rayxo-nginx` so the
agent socket is reachable, writes `/etc/rayxo/panel.env` (0640), installs the
binary to `/usr/local/bin/rayxo-panel`, runs `migrate`, enables
`rayxo-panel.service`, and writes an Nginx site on port `2223` — deliberately
not `2222`, so both panels can be tried before one is retired. A self-signed
certificate is generated unless `/etc/rayxo/panel.crt` already exists.

`deploy/rayxo-panel.service` and `deploy/nginx.conf` are the files it installs;
they work on their own too. The parts that matter:

- the panel runs as the unprivileged `rayxo` user and may read
  `/run/rayxo/agent.sock` (group `rayxo-nginx`);
- Nginx listens on `:2223` while the Laravel panel keeps `:2222`, and proxies
  to `127.0.0.1:8080`;
- the panel sets its own security headers, including a CSP that allows only
  its own bundle, so Nginx does not repeat them.

Only one panel may run the task queue against a database at a time: Laravel
encrypted `tasks.payload` and this panel writes plain JSON.

## Security model

- No shell. Every change is a method on `rayxod`; the panel has no `exec`.
- Sessions: 32 random bytes in a cookie, only a SHA-256 of the token in the
  database, ten concurrent sessions per account at most, one row per sign-in
  so an operator can end a session they do not recognise.
- CSRF: a token in the cookie before sign-in, then a per-session token sent as
  `X-CSRF-Token`; both are compared in constant time.
- Sign-in limits: 20 attempts per address and 8 per username in 15 minutes.
- A suspended hosting account cannot sign in, and the reason is shown.
- Resellers see only their own accounts; another reseller's account answers
  `404`, not `403`, so usernames are not enumerable.
- Passwords are bcrypt cost 12; a hash of a different cost is upgraded on the
  next successful sign-in.
- Every change writes an `audit_logs` row, and agent work writes a `tasks`
  row before it is dispatched, so work that never ran is still visible.

## Compatibility with the Laravel panel

The tables are the ones the Laravel panel already uses: `users`, `packages`,
`domains`, `tasks`, `audit_logs`. Data written by either panel is readable by
the other, with two exceptions:

1. **Task payloads.** Laravel encrypted the `tasks.payload` column; this panel
   writes plain JSON. Do not run both panels against one database and expect
   queues to work — pick one.
2. **Statuses.** Both use `queued`/`running`/`success`/`failed`/`cancelled`
   for tasks and `pending`/`active`/`suspended`/`deleting`/`failed` for
   accounts, which is why those strings live in `internal/store/store.go` and
   `App\Enums` rather than in the handlers.

The reserved-username list is necessarily duplicated: `internal/rules`,
`agent/internal/validate`, and `App\Rules\ValidUsername`. All three must agree.

## Tests

```sh
go test ./...                       # unit tests, no database needed

# the API tests need a throwaway database whose name ends in _test:
RAYXO_TEST_DSN='root@tcp(127.0.0.1:3307)/rayxo_test?parseTime=true&loc=UTC' \
    go test ./internal/httpapi/ -v
```

The API tests create a panel user, sign in, create a hosting account, drain
the queue through the fake agent, and then check that the account is active,
that a wrong password is refused, that CSRF is enforced, that a reseller
cannot read another reseller's account, and that deleting needs the username
typed back.

## Looking at it

```sh
node scripts/screenshot.mjs    # PNGs of every screen into .shots/
node scripts/measure.mjs       # fails on overflow, clipping or overlap at 1440/1024/390px
```

Both need a running panel and `RAYXO_PASSWORD`.

## What is not here yet

The interface now covers the hosting modules the agent implements: websites and
DNS, mail, databases, FTP, files, cron, backups, server controls, two-factor and
API tokens. What is still missing is the long tail:

- reseller package editing (packages are read, not written);
- the WHMCS / billing hooks and the phpMyAdmin and Roundcube single sign-on
  ticket (`App\Support\SignOnTicket`, sodium secretbox);
- firewall rule editing beyond status, and the WAF controls;
- Redis, PostgreSQL, Node app and PHP extension screens;
- Indonesian strings (the Laravel panel is `APP_LOCALE=id`; this one is English
  only so far).

The agent already implements those actions; the work is the panel side: a store
method, a handler, and a screen each.

## Licence

AGPL-3.0, the same as RAYXO.
