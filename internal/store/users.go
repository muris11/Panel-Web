package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

// userRaw holds one row's scan targets. Scanning into a struct's fields and
// converting afterwards keeps the nullable columns from leaking into the
// types the rest of the panel works with.
type userRaw struct {
	id       int64
	username string
	name     string
	email    string
	password string
	role     string
	status   string

	ownerID   sql.NullInt64
	packageID sql.NullInt64
	uid       sql.NullInt32
	gid       sql.NullInt32

	reason    sql.NullString
	secret    sql.NullString
	confirmed sql.NullTime
	lastLogin sql.NullTime
	created   sql.NullTime
}

func (r *userRaw) dests() []any {
	return []any{
		&r.id, &r.username, &r.name, &r.email, &r.password, &r.role, &r.status,
		&r.ownerID, &r.packageID, &r.uid, &r.gid, &r.reason,
		&r.secret, &r.confirmed, &r.lastLogin, &r.created,
	}
}

func (r *userRaw) user() User {
	user := User{
		ID:           r.id,
		Username:     r.username,
		Name:         r.name,
		Email:        r.email,
		PasswordHash: r.password,
		Role:         Role(r.role),
		Status:       AccountStatus(r.status),
	}

	if r.ownerID.Valid {
		user.OwnerID = &r.ownerID.Int64
	}

	if r.packageID.Valid {
		user.PackageID = &r.packageID.Int64
	}

	if r.uid.Valid {
		user.UID = &r.uid.Int32
	}

	if r.gid.Valid {
		user.GID = &r.gid.Int32
	}

	if r.reason.Valid {
		user.SuspendReason = r.reason.String
	}

	// A secret this panel cannot read is treated as "no second factor" rather
	// than an error: one broken row must not lock an account out entirely.
	if r.secret.Valid {
		user.TwoFactorSecret = r.secret.String
	}

	if r.confirmed.Valid {
		user.TwoFactorConfirmedAt = &r.confirmed.Time
	}

	if r.lastLogin.Valid {
		user.LastLoginAt = &r.lastLogin.Time
	}

	if r.created.Valid {
		user.CreatedAt = r.created.Time
	} else {
		user.CreatedAt = Now()
	}

	return user
}

// UserByID reads one account.
func (s *Store) UserByID(ctx context.Context, id int64) (User, error) {
	return s.one(ctx, `SELECT `+userColumns+` FROM users u WHERE u.id = ?`, id)
}

// UserByUsername reads one account by its username.
func (s *Store) UserByUsername(ctx context.Context, username string) (User, error) {
	return s.one(ctx, `SELECT `+userColumns+` FROM users u WHERE u.username = ?`, strings.ToLower(strings.TrimSpace(username)))
}

func (s *Store) one(ctx context.Context, query string, args ...any) (User, error) {
	var raw userRaw

	if err := s.db.QueryRowContext(ctx, query, args...).Scan(raw.dests()...); err != nil {
		return User{}, wrap("read the account", translate(err))
	}

	return raw.user(), nil
}

// UserByLogin finds an account by username or by email.
//
// Emails are not unique any more (hosting accounts may share one), so an
// email that matches several rows is refused instead of guessed: signing in
// to the wrong hosting account would be worse than asking for the username.
func (s *Store) UserByLogin(ctx context.Context, login string) (User, error) {
	login = strings.TrimSpace(login)

	user, err := s.UserByUsername(ctx, login)
	if err == nil {
		return user, nil
	}

	if err != ErrNotFound {
		return User{}, err
	}

	rows, err := s.db.QueryContext(ctx, `SELECT `+userColumns+` FROM users u WHERE u.email = ? LIMIT 2`, login)
	if err != nil {
		return User{}, wrap("read the account by email", err)
	}

	defer rows.Close()

	found := []User{}

	for rows.Next() {
		var raw userRaw

		if err := rows.Scan(raw.dests()...); err != nil {
			return User{}, wrap("read the account by email", err)
		}

		found = append(found, raw.user())
	}

	if err := rows.Err(); err != nil {
		return User{}, wrap("read the account by email", err)
	}

	switch len(found) {
	case 0:
		return User{}, ErrNotFound
	case 1:
		return found[0], nil
	default:
		return User{}, fmt.Errorf("that email belongs to more than one account; sign in with the username")
	}
}

// TouchLogin records a successful sign-in.
func (s *Store) TouchLogin(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, `UPDATE users SET last_login_at = ?, updated_at = ? WHERE id = ?`, Now(), Now(), id)

	return wrap("record the sign-in", err)
}

// UpdatePassword stores a new hash and signs every browser out, which is what
// a password change has to mean.
func (s *Store) UpdatePassword(ctx context.Context, id int64, hash string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return wrap("change the password", err)
	}

	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx, `UPDATE users SET password = ?, updated_at = ? WHERE id = ?`, hash, Now(), id); err != nil {
		return wrap("change the password", err)
	}

	if _, err := tx.ExecContext(ctx, `DELETE FROM panel_sessions WHERE user_id = ?`, id); err != nil {
		return wrap("sign out other sessions", err)
	}

	return wrap("change the password", tx.Commit())
}

// SetTwoFactorSecret stores a new secret and its recovery codes, leaving the
// second factor unconfirmed: signing in still works without a code until the
// account proves it can generate one.
func (s *Store) SetTwoFactorSecret(ctx context.Context, id int64, secret, recoveryCodes string) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE users
		SET two_factor_secret = ?, two_factor_recovery_codes = ?, two_factor_confirmed_at = NULL, updated_at = ?
		WHERE id = ?`, secret, recoveryCodes, Now(), id)

	return wrap("store the two-factor secret", err)
}

// ConfirmTwoFactor switches the second factor on.
func (s *Store) ConfirmTwoFactor(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE users SET two_factor_confirmed_at = ?, updated_at = ? WHERE id = ?`, Now(), Now(), id)

	return wrap("confirm two-factor", err)
}

// ClearTwoFactor forgets the secret, the recovery codes and the confirmation.
func (s *Store) ClearTwoFactor(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE users
		SET two_factor_secret = NULL, two_factor_recovery_codes = NULL, two_factor_confirmed_at = NULL, updated_at = ?
		WHERE id = ?`, Now(), id)

	return wrap("turn off two-factor", err)
}

// RecoveryCodes reads the stored recovery codes, in whatever form the account
// saved them: a plain JSON array or a Laravel-encrypted string.
func (s *Store) RecoveryCodes(ctx context.Context, id int64) (string, error) {
	var codes sql.NullString

	if err := s.db.QueryRowContext(ctx, `SELECT two_factor_recovery_codes FROM users WHERE id = ?`, id).Scan(&codes); err != nil {
		return "", wrap("read the recovery codes", err)
	}

	return codes.String, nil
}

// SetRecoveryCodes replaces the remaining single-use codes.
func (s *Store) SetRecoveryCodes(ctx context.Context, id int64, recoveryCodes string) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE users SET two_factor_recovery_codes = ?, updated_at = ? WHERE id = ?`, recoveryCodes, Now(), id)

	return wrap("store the recovery codes", err)
}

// accountColumns are appended after userColumns when reading a list.
const accountColumns = `p.id, p.name, p.description, p.disk_mb, p.bandwidth_mb,
	p.domains, p.databases, p.email_accounts, p.ftp_accounts, p.cron_jobs,
	p.subdomains, p.email_forwarders, p.inodes,
	(SELECT COUNT(*) FROM domains d WHERE d.user_id = u.id),
	COALESCE(o.username, '')`

// ListAccounts returns the hosting accounts an owner manages: an
// administrator sees every account, a reseller only the ones it owns.
func (s *Store) ListAccounts(ctx context.Context, owner *User, limit, offset int) ([]Account, error) {
	query := `SELECT ` + userColumns + `, ` + accountColumns + `
		FROM users u
		LEFT JOIN packages p ON p.id = u.package_id
		LEFT JOIN users o ON o.id = u.owner_id
		WHERE u.role = 'user'`

	args := []any{}

	if owner != nil && !owner.IsAdmin() {
		query += ` AND u.owner_id = ?`
		args = append(args, owner.ID)
	}

	query += ` ORDER BY u.id DESC LIMIT ? OFFSET ?`
	args = append(args, limit, offset)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, wrap("list the accounts", err)
	}

	defer rows.Close()

	accounts := []Account{}

	for rows.Next() {
		account, err := scanAccount(rows.Scan)
		if err != nil {
			return nil, wrap("list the accounts", err)
		}

		accounts = append(accounts, account)
	}

	return accounts, wrap("list the accounts", rows.Err())
}

func scanAccount(scan func(...any) error) (Account, error) {
	var (
		raw userRaw

		pkgID     sql.NullInt64
		pkgName   sql.NullString
		pkgDesc   sql.NullString
		disk      sql.NullInt64
		bandwidth sql.NullInt64
		domains   sql.NullInt64
		databases sql.NullInt64
		mailboxes sql.NullInt64
		ftp       sql.NullInt64
		cron      sql.NullInt64
		subs      sql.NullInt64
		forwarder sql.NullInt64
		inodes    sql.NullInt64

		domainCount int
		ownerName   string
	)

	dests := append(raw.dests(),
		&pkgID, &pkgName, &pkgDesc, &disk, &bandwidth, &domains, &databases,
		&mailboxes, &ftp, &cron, &subs, &forwarder, &inodes,
		&domainCount, &ownerName,
	)

	if err := scan(dests...); err != nil {
		return Account{}, err
	}

	account := Account{User: raw.user(), DomainCount: domainCount, OwnerName: ownerName}

	if pkgID.Valid {
		account.Package = &Package{
			ID:             pkgID.Int64,
			Name:           pkgName.String,
			Description:    pkgDesc.String,
			DiskMB:         int64OrNil(disk),
			BandwidthMB:    int64OrNil(bandwidth),
			Domains:        int64OrNil(domains),
			Databases:      int64OrNil(databases),
			EmailAccounts:  int64OrNil(mailboxes),
			FTPAccounts:    int64OrNil(ftp),
			CronJobs:       int64OrNil(cron),
			Subdomains:     int64OrNil(subs),
			EmailForwarder: int64OrNil(forwarder),
			Inodes:         int64OrNil(inodes),
		}
	}

	return account, nil
}

// CountAccounts counts the accounts an owner manages.
func (s *Store) CountAccounts(ctx context.Context, owner *User) (int, error) {
	query := `SELECT COUNT(*) FROM users WHERE role = 'user'`
	args := []any{}

	if owner != nil && !owner.IsAdmin() {
		query += ` AND owner_id = ?`
		args = append(args, owner.ID)
	}

	var count int

	err := s.db.QueryRowContext(ctx, query, args...).Scan(&count)

	return count, wrap("count the accounts", err)
}

// CreateAccount inserts a hosting account in the pending state. The row
// exists before the server does, exactly like the Laravel panel: the agent's
// answer fills in the uid, gid and status.
func (s *Store) CreateAccount(ctx context.Context, username, name, email, hash string, ownerID int64, packageID *int64) (User, error) {
	now := Now()

	result, err := s.db.ExecContext(ctx, `
		INSERT INTO users (username, name, email, email_verified_at, password, role, status,
			owner_id, package_id, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, 'user', 'pending', ?, ?, ?, ?)`,
		username, name, email, now, hash, ownerID, NullInt64(packageID), now, now)
	if err != nil {
		return User{}, wrap("create the account", err)
	}

	id, err := result.LastInsertId()
	if err != nil {
		return User{}, wrap("create the account", err)
	}

	return s.UserByID(ctx, id)
}

// UpdateAccountStatus stores the outcome of a suspend or unsuspend.
func (s *Store) UpdateAccountStatus(ctx context.Context, id int64, status AccountStatus, reason string) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE users SET status = ?, suspend_reason = ?, updated_at = ? WHERE id = ?`,
		string(status), reason, Now(), id)

	return wrap("update the account status", err)
}

// UsernameTaken reports whether a username is already in use.
func (s *Store) UsernameTaken(ctx context.Context, username string) (bool, error) {
	var exists bool

	err := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM users WHERE username = ?)`, username).Scan(&exists)

	return exists, wrap("check the username", err)
}

// EmailTaken reports whether an email belongs to another account.
func (s *Store) EmailTaken(ctx context.Context, email string, exceptID int64) (bool, error) {
	var exists bool

	err := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM users WHERE email = ? AND id <> ?)`, email, exceptID).Scan(&exists)

	return exists, wrap("check the email", err)
}

// Packages lists the packages an owner may hand out.
func (s *Store) Packages(ctx context.Context, owner *User) ([]Package, error) {
	query := `SELECT id, name, description, disk_mb, bandwidth_mb, domains, databases,
		email_accounts, ftp_accounts, cron_jobs, subdomains, email_forwarders, inodes
		FROM packages`
	args := []any{}

	if owner != nil && !owner.IsAdmin() {
		query += ` WHERE owner_id = ?`
		args = append(args, owner.ID)
	}

	query += ` ORDER BY name`

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, wrap("list the packages", err)
	}

	defer rows.Close()

	packages := []Package{}

	for rows.Next() {
		var (
			pkg     Package
			desc    sql.NullString
			disk    sql.NullInt64
			band    sql.NullInt64
			domains sql.NullInt64
			dbs     sql.NullInt64
			mail    sql.NullInt64
			ftp     sql.NullInt64
			cron    sql.NullInt64
			subs    sql.NullInt64
			fwd     sql.NullInt64
			inodes  sql.NullInt64
		)

		if err := rows.Scan(&pkg.ID, &pkg.Name, &desc, &disk, &band, &domains, &dbs,
			&mail, &ftp, &cron, &subs, &fwd, &inodes); err != nil {
			return nil, wrap("list the packages", err)
		}

		pkg.Description = desc.String
		pkg.DiskMB = int64OrNil(disk)
		pkg.BandwidthMB = int64OrNil(band)
		pkg.Domains = int64OrNil(domains)
		pkg.Databases = int64OrNil(dbs)
		pkg.EmailAccounts = int64OrNil(mail)
		pkg.FTPAccounts = int64OrNil(ftp)
		pkg.CronJobs = int64OrNil(cron)
		pkg.Subdomains = int64OrNil(subs)
		pkg.EmailForwarder = int64OrNil(fwd)
		pkg.Inodes = int64OrNil(inodes)

		packages = append(packages, pkg)
	}

	return packages, wrap("list the packages", rows.Err())
}

func int64OrNil(value sql.NullInt64) *int64 {
	if !value.Valid {
		return nil
	}

	out := value.Int64

	return &out
}
