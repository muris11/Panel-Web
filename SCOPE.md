# What this panel covers

This is not a re-skin of the Laravel panel and it is not a line-by-line port of
it. It is the panel built again for the jobs a hosting account actually needs,
with the parts that only existed to keep PHP working left behind. This file says
what is done, what is different on purpose, and what is still open, so nobody has
to guess from the file list.

Every row here is checked by a test in `internal/httpapi` or `internal/store`
unless it says otherwise. "Agent" is the private call the panel makes over
`/run/rayxo/agent.sock`; the panel never shells out.

## Accounts and people

| Feature | Where |
|---|---|
| Sign in, sign out, session probe, CSRF | `internal/httpapi/auth.go` |
| Sign in by username **or** email | `store.UserByLogin` |
| Two-factor (TOTP) with 8 single-use recovery codes | `internal/httpapi/twofactor.go` |
| Turn two-factor off only with the current password | `handleTwoFactor` |
| Change own name, email, password | `internal/httpapi/profile.go` |
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

1. **`RAYXO_AGENT_DRIVER=socket` has never talked to a real `rayxod`.** Every
   end-to-end test uses the fake agent. The client is small and follows
   `agent/internal/client/client.go`, but a Linux box is the proof.
2. **`deploy/install.sh` has not been run on Ubuntu 24.04.** The unit, socket
   group, nginx vhost and certificate steps are written but unproven.
3. **The Laravel panel is still what `installer/setup.sh` installs.** The Go
   panel installs beside it on `:2223`. Retiring the PHP panel means removing
   its queue worker, and only one panel may drain `tasks` at a time: Laravel
   encrypts `tasks.payload`, this panel writes plain JSON.
4. **Scheduled work still lives in Laravel commands.** Bandwidth, resource and
   quota collection, DNS sync, SSL expiry sync, alerts, drift and update checks
   are `app/Console/Commands`. The panel reads their results
   (`usage.bandwidth`, `system.info`, `quota.report`) but does not run them yet.
5. **Not rebuilt**: reseller and hosting package editing (packages are read
   only), account import/transfer, announcements, tickets, branding, brute-force
   and WAF editing, mail queue, antivirus quarantine, server/system/remote
   backup schedules and retention, statistics and login history screens,
   python/node app management, PHP extension toggles, redirects, SSH key
   management, phpMyAdmin/Roundcube single sign-on, passkeys, the WHMCS API.
6. **No Indonesian strings.** The Laravel panel ships `APP_LOCALE=id`; this one
   is English only.
7. **No CI.** `go test ./...` needs a real MariaDB through `RAYXO_TEST_DSN`, and
   nothing runs it on push yet.
8. **Small known edges.** The file manager has no drag-and-drop upload or
   syntax highlighting; a subdomain does not yet pick its parent zone in the
   drawer; mail has no quota or send-limit field; database size is only what the
   last collection run measured.
