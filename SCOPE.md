# What MurisCore covers

MurisCore is its own panel, not a re-skin of the Laravel one and not a
line-by-line port of it. It is built again for the jobs a hosting account
actually needs, with the parts that only existed to keep PHP working left
behind, and it installs on the distributions a hosting server is usually built
on (see "Hosts" below). This file says
what is done, what is different on purpose, and what is still open, so nobody has
to guess from the file list.

Every row here is checked by a test in `internal/httpapi` or `internal/store`
unless it says otherwise. "Agent" is the private call the panel makes over
`/run/muriscore/agent.sock`; the panel never shells out.

## Accounts and people

| Feature | Where |
|---|---|
| Sign in, sign out, session probe, CSRF | `internal/httpapi/auth.go` |
| Sign in by username **or** email | `store.UserByLogin` |
| Two-factor (TOTP) with 8 single-use recovery codes | `internal/httpapi/twofactor.go` |
| Turn two-factor off only with the current password | `handleTwoFactor` |
| Change own name, email, password | `internal/httpapi/profile.go` |
| `muriscore-panel doctor` reports what a host is missing | `cmd/panel/doctor.go` |
| Changing the password drops every other session | `store.UpdatePassword` + `startSession` |
| Sessions list and revoke | `handleSessions`, `handleRevokeSession` |
| API tokens (Bearer, SHA-256 at rest, shown once) | `internal/httpapi/tokens.go` |
| Create, suspend, unsuspend, delete accounts | `internal/httpapi/accounts.go` |
| Reseller isolation: another owner's account is a 404, not a 403 | `mayManage` |
| Activity log of every change | `internal/store/audit.go` |

## Hosting modules

| Module | Covered by | Agent calls |
|---|---|---|
| Websites: domain, subdomain, pointer, document root, PHP version, HTTPS, WWW, no-PHP | `domains.go` | `web.apply_domain`, `web.remove_domain` |
| TLS: issue, install, remove, expiry in the row | `domains.go` | `ssl.issue`, `ssl.install`, `ssl.delete` |
| DNS: zone per domain, add/edit/delete records, zone shown as synced after the agent answers | `dns.go` | `dns.sync_zone` |
| Mail: domain, mailbox, forwarder, DKIM key from the agent | `mail.go` | `mail.domain_apply` |
| MySQL: database, login, grants, password reset, drop | `databases.go` | `db.create_database`, `db.apply_user`, `db.drop_database` |
| PostgreSQL: database, login, grants, password rotation, drop | `postgres.go` | `pg.create_database`, `pg.apply_user`, `pg.delete_database`, `pg.delete_user` |
| Redis: one instance per account, password rotate, memory ceiling, remove | `redis.go` | `redis.provision`, `redis.deprovision` |
| FTP: login tied to a domain and directory, quota, `$6$` crypt password | `resources.go` | `ftp.ensure_dir` |
| Cron: per account, whole crontab with `mail_to` | `resources.go` | `cron.apply` |
| Backups: list archives on disk, create, restore, delete | `resources.go`, `files.go` | `backup.create`, `backup.restore`, `fs.list` |
| Files: browse, read, write, mkdir, rename, chmod, delete, compress, extract, search | `files.go` | `fs.*` |
| Server (admin): services, firewall rules, hostname, timezone, bandwidth | `system.go` | `service.*`, `firewall.status`, `system.*`, `usage.bandwidth` |
| Tickets between an account and whoever answers it | `support.go` | none |
| Announcements to a role, or to a reseller's own accounts | `support.go` | none |
| Hosting packages: create, edit, delete; quota and shell re-applied on change | `packages.go` | `account.set_quota`, `account.set_ssh` |
| Node.js apps: create, restart, delete; env written 0600 by the agent | `nodeapps.go` | `nodeapp.provision`, `nodeapp.restart`, `nodeapp.deprovision` |
| PHP versions offered as a dropdown from the agent's installed list | `nodeapps.go` | `php.versions` |

## Deliberate differences from the Laravel panel

These are choices, not gaps.

- **One binary.** The SPA is embedded, so a deploy is a file plus an env file.
  There is no `artisan`, no queue worker unit, no scheduler timer, no Composer.
- **No PHP-level hardening needed.** Nothing runs under PHP-FPM, so the
  `disable_functions` list and the PHP panel vhost simply do not exist here.
- **The panel still never runs privileged.** Every change is a named agent call,
  which is why the file manager waits on the agent instead of touching disk.
- **Two-factor stores the secret unconfirmed first.** A half-finished enrolment
  cannot lock an account out, which is what Laravel does too, and the panel
  writes the same columns so both can read one database.
- **Passwords stay interoperable.** `$2y$` bcrypt for panel users, `$6$` crypt
  for FTP, MySQL native hashes for MySQL logins, and `APP_KEY` encryption for
  two-factor secrets, PostgreSQL passwords and Redis passwords.
- **The interface is its own.** Grouped navigation, one drawer per task, tables
  that say when a value is unknown rather than showing a dash.

## Still open

Honest list, roughly in the order that matters.

1. **The socket driver has not talked to a real `muriscored` yet.** The protocol is
   tested against a real Unix socket in `internal/agent/agent_test.go` (framing,
   JSON-RPC ids, refusals with a reason, a null-id rejection, a missing socket),
   but a Linux box with the daemon running is still the proof.
2. **`deploy/install.sh` has not been run on a real server.** It detects the
   Debian, RHEL and SUSE families and handles `apt`, `dnf`/`yum` and `zypper`,
   SELinux and `firewalld`/`ufw`, but every step here was written on Windows.
   `muriscore-panel doctor` and `--print-only` exist so the first run can be
   checked before anything changes.
3. **The Laravel panel is still what `installer/setup.sh` installs.** The Go
   panel installs beside it on `:2223`. Retiring the PHP panel means removing
   its queue worker, and only one panel may drain `tasks` at a time: Laravel
   encrypts `tasks.payload`, this panel writes plain JSON.
4. **SSH needs both packages to allow it, as in Laravel.** The account's own
   package has to include SSH and so does the reseller package of its owner
   (`store.AllowsSSH`); an account with no package at all is not restricted,
   which is what `App\Support\AccountLimits::allowsSsh` answers too.
   `reseller_packages` is created by `0008_support.sql` so an existing database
   keeps the rows the Laravel panel wrote.

5. **Some scheduled work still lives in Laravel commands.** Traffic, resources
   and certificate expiry are collected by the panel itself now
   (`internal/collector`, every fifteen minutes or `muriscore-panel collect`).
   Quota application, DNS sync, alerts, drift and update checks are still
   `app/Console/Commands` and are not run by this panel yet.
6. **Not rebuilt**: reseller package editing (a reseller's own ceiling is set by
   an administrator's `reseller_packages` row), account import/transfer, ticket attachments, the email that an
   announcement would send (`emailed` is recorded, no mail is dispatched),
   branding, brute-force and WAF editing, mail queue, antivirus quarantine,
   server/system/remote backup schedules and retention, statistics and login
   history screens, python/node app management, PHP extension toggles,
   redirects, phpMyAdmin and Roundcube single sign-on, passkeys, the WHMCS API.
7. **No Indonesian strings.** The Laravel panel ships `APP_LOCALE=id`; this one
   is English only.
8. **No CI.** `go test ./...` needs a real MariaDB through `MURISCORE_TEST_DSN`, and
   nothing runs it on push yet.
9. **Small known edges.** The file manager has no drag-and-drop upload or
   syntax highlighting; a subdomain does not yet pick its parent zone in the
   drawer; mail has no quota or send-limit field; database size is only what the
   last collection run measured.

## Licence

AGPL-3.0, the same as the agent this panel drives. The repository was
MIT when it only held a placeholder; that is corrected here.

## Hosts

| Family | Names | Package manager | Extras handled |
| ------ | ----- | --------------- | -------------- |
| Debian | Debian 12/13, Ubuntu 22.04/24.04, Mint, Pop, Kali, Devuan, Raspbian | `apt` | `ufw` when active |
| RHEL   | AlmaLinux 9, Rocky 9, CentOS Stream, Fedora, Oracle Linux, Amazon Linux | `dnf` / `yum` | SELinux boolean, `firewalld` |
| SUSE   | openSUSE Leap/Tumbleweed, SLES | `zypper` | — |

Anything else: the binary is static and works, `--no-nginx` skips the web server,
and `doctor` names what is missing instead of failing at the first request.

On a host where 2222 is already taken (the Laravel panel), the installer picks
2223 rather than fighting over the port, so both can be tried before one is
retired.
