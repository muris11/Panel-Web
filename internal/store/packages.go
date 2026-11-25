package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
)

// Package editing errors. They carry no field name because the handler decides
// which field to blame.
var (
	// ErrPackageNameTaken means another package in the same scope has the name.
	ErrPackageNameTaken = errors.New("a package with that name already exists")
	// ErrPackageInUse means accounts still sit on the package.
	ErrPackageInUse = errors.New("accounts still use this package")
	// ErrPackageMissing means the id names no package the caller may edit.
	ErrPackageMissing = errors.New("no such package")
)

// PackageInput is one submitted package. Nil limits mean unlimited, which is
// how the columns already read; the pointer is what separates "no limit" from
// "zero", and zero is refused for disk and inodes because the server reads a
// quota of zero as unlimited.
type PackageInput struct {
	Name        string
	Description string
	DiskMB      *int64
	Inodes      *int64
	BandwidthMB *int64
	Domains     *int64
	Subdomains  *int64
	Pointers    *int64
	Mailboxes   *int64
	Forwarders  *int64
	Databases   *int64
	FTPAccounts *int64
	CronJobs    *int64
	SSL         bool
	CGI         bool
	SSH         bool
}

const packageColumns = "id, owner_id, name, description, disk_mb, inodes, bandwidth_mb, " +
	"domains, subdomains, pointers, email_accounts, email_forwarders, `databases`, " +
	"ftp_accounts, cron_jobs, `ssl`, cgi, ssh"

// scanPackage reads one row of packageColumns.
func scanPackage(row interface {
	Scan(...any) error
}) (Package, error) {
	var (
		pkg                     Package
		owner                   sql.NullInt64
		description             sql.NullString
		disk, inodes, bandwidth sql.NullInt64
		domains, subdomains     sql.NullInt64
		pointers                sql.NullInt64
		mailboxes, forwarders   sql.NullInt64
		databases, ftp, cron    sql.NullInt64
		ssl, cgi, ssh           bool
	)
	if err := row.Scan(&pkg.ID, &owner, &pkg.Name, &description, &disk, &inodes, &bandwidth,
		&domains, &subdomains, &pointers, &mailboxes, &forwarders, &databases,
		&ftp, &cron, &ssl, &cgi, &ssh); err != nil {
		return Package{}, err
	}

	pkg.OwnerID = int64OrNil(owner)
	pkg.Description = description.String
	pkg.DiskMB = int64OrNil(disk)
	pkg.Inodes = int64OrNil(inodes)
	pkg.BandwidthMB = int64OrNil(bandwidth)
	pkg.Domains = int64OrNil(domains)
	pkg.Subdomains = int64OrNil(subdomains)
	pkg.Pointers = int64OrNil(pointers)
	pkg.EmailAccounts = int64OrNil(mailboxes)
	pkg.EmailForwarder = int64OrNil(forwarders)
	pkg.Databases = int64OrNil(databases)
	pkg.FTPAccounts = int64OrNil(ftp)
	pkg.CronJobs = int64OrNil(cron)
	pkg.SSL = ssl
	pkg.CGI = cgi
	pkg.SSH = ssh

	return pkg, nil
}

// PackagesFor lists every package the caller controls with the number of
// accounts on each. A reseller sees its own packages only.
func (s *Store) PackagesFor(ctx context.Context, caller *User) ([]Package, error) {
	query := "SELECT " + packageColumns + " FROM `packages`"
	args := []any{}

	if caller != nil && !caller.IsAdmin() {
		query += " WHERE owner_id = ?"
		args = append(args, caller.ID)
	}

	query += " ORDER BY name"

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, wrap("list the packages", err)
	}

	defer rows.Close()

	packages := []Package{}

	for rows.Next() {
		pkg, err := scanPackage(rows)
		if err != nil {
			return nil, wrap("list the packages", err)
		}

		packages = append(packages, pkg)
	}

	if err := rows.Err(); err != nil {
		return nil, wrap("list the packages", err)
	}

	counts, err := s.packageAccountCounts(ctx)
	if err != nil {
		return nil, err
	}

	for i := range packages {
		packages[i].Accounts = counts[packages[i].ID]
	}

	return packages, nil
}

// packageAccountCounts counts the hosting accounts on each package.
func (s *Store) packageAccountCounts(ctx context.Context) (map[int64]int64, error) {
	rows, err := s.db.QueryContext(ctx,
		"SELECT package_id, COUNT(*) FROM `users` WHERE package_id IS NOT NULL GROUP BY package_id")
	if err != nil {
		return nil, wrap("count the accounts on each package", err)
	}

	defer rows.Close()

	counts := map[int64]int64{}

	for rows.Next() {
		var (
			id    int64
			count int64
		)
		if err := rows.Scan(&id, &count); err != nil {
			return nil, wrap("count the accounts on each package", err)
		}

		counts[id] = count
	}

	return counts, wrap("count the accounts on each package", rows.Err())
}

// PackageByID reads one package.
func (s *Store) PackageByID(ctx context.Context, id int64) (Package, error) {
	pkg, err := scanPackage(s.db.QueryRowContext(ctx,
		"SELECT "+packageColumns+" FROM `packages` WHERE id = ?", id))
	if errors.Is(err, sql.ErrNoRows) {
		return Package{}, ErrPackageMissing
	}
	if err != nil {
		return Package{}, wrap("read the package", err)
	}

	counts, err := s.packageAccountCounts(ctx)
	if err != nil {
		return Package{}, err
	}

	pkg.Accounts = counts[pkg.ID]

	return pkg, nil
}

// packageNameTaken reports whether the name is already used by a package in
// the same scope: an administrator's packages share one namespace, a
// reseller's own packages another. MySQL does not compare NULL owner ids, so
// the database's unique index cannot answer this on its own.
func (s *Store) packageNameTaken(ctx context.Context, name string, ownerID *int64, exceptID int64) (bool, error) {
	query := "SELECT EXISTS(SELECT 1 FROM `packages` WHERE name = ? AND id <> ? AND "
	args := []any{name, exceptID}

	if ownerID == nil {
		// An administrator's namespace is every package with no reseller
		// owner, which includes packages whose owner login was removed.
		query += "(owner_id IS NULL OR owner_id IN (SELECT id FROM `users` WHERE `role` IN ('admin', 'reseller') AND `role` = 'admin')))"
	} else {
		query += "owner_id = ?)"
		args = append(args, *ownerID)
	}

	var exists bool

	err := s.db.QueryRowContext(ctx, query, args...).Scan(&exists)

	return exists, wrap("check the package name", err)
}

// CreatePackage stores a package owned by the caller.
func (s *Store) CreatePackage(ctx context.Context, input PackageInput, owner *User) (Package, error) {
	ownerID := owner.ID

	taken, err := s.packageNameTaken(ctx, input.Name, &ownerID, 0)
	if err != nil {
		return Package{}, err
	}

	if taken {
		return Package{}, ErrPackageNameTaken
	}

	now := Now()

	result, err := s.db.ExecContext(ctx, `
		INSERT INTO `+"`packages`"+` (owner_id, name, description, disk_mb, inodes, bandwidth_mb,
			domains, subdomains, pointers, email_accounts, email_forwarders, `+"`databases`"+`,
			ftp_accounts, cron_jobs, `+"`ssl`"+`, cgi, ssh, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		ownerID, input.Name, nullString(input.Description), input.DiskMB, input.Inodes, input.BandwidthMB,
		input.Domains, input.Subdomains, input.Pointers, input.Mailboxes, input.Forwarders,
		input.Databases, input.FTPAccounts, input.CronJobs, input.SSL, input.CGI, input.SSH, now, now)
	if err != nil {
		return Package{}, wrap("create the package", err)
	}

	id, err := result.LastInsertId()
	if err != nil {
		return Package{}, wrap("create the package", err)
	}

	return s.PackageByID(ctx, id)
}

// UpdatePackage writes the submitted limits and reports which columns changed,
// so the handler can re-apply quotas or SSH only when it matters.
func (s *Store) UpdatePackage(ctx context.Context, id int64, input PackageInput) (Package, []string, error) {
	before, err := s.PackageByID(ctx, id)
	if err != nil {
		return Package{}, nil, err
	}

	taken, err := s.packageNameTaken(ctx, input.Name, before.OwnerID, id)
	if err != nil {
		return Package{}, nil, err
	}

	if taken {
		return Package{}, nil, ErrPackageNameTaken
	}

	now := Now()

	if _, err := s.db.ExecContext(ctx, `
		UPDATE `+"`packages`"+` SET name = ?, description = ?, disk_mb = ?, inodes = ?,
			bandwidth_mb = ?, domains = ?, subdomains = ?, pointers = ?,
			email_accounts = ?, email_forwarders = ?, `+"`databases`"+` = ?, ftp_accounts = ?,
			cron_jobs = ?, `+"`ssl`"+` = ?, cgi = ?, ssh = ?, updated_at = ?
		WHERE id = ?`,
		input.Name, nullString(input.Description), input.DiskMB, input.Inodes,
		input.BandwidthMB, input.Domains, input.Subdomains, input.Pointers,
		input.Mailboxes, input.Forwarders, input.Databases, input.FTPAccounts,
		input.CronJobs, input.SSL, input.CGI, input.SSH, now, id); err != nil {
		return Package{}, nil, wrap("change the package", err)
	}

	after, err := s.PackageByID(ctx, id)
	if err != nil {
		return Package{}, nil, err
	}

	changed := []string{}

	if before.Name != after.Name {
		changed = append(changed, "name")
	}

	if before.Description != after.Description {
		changed = append(changed, "description")
	}

	for _, field := range []struct {
		name   string
		before *int64
		after  *int64
	}{
		{"disk_mb", before.DiskMB, after.DiskMB},
		{"inodes", before.Inodes, after.Inodes},
		{"bandwidth_mb", before.BandwidthMB, after.BandwidthMB},
		{"domains", before.Domains, after.Domains},
		{"subdomains", before.Subdomains, after.Subdomains},
		{"pointers", before.Pointers, after.Pointers},
		{"email_accounts", before.EmailAccounts, after.EmailAccounts},
		{"email_forwarders", before.EmailForwarder, after.EmailForwarder},
		{"databases", before.Databases, after.Databases},
		{"ftp_accounts", before.FTPAccounts, after.FTPAccounts},
		{"cron_jobs", before.CronJobs, after.CronJobs},
	} {
		if !sameLimit(field.before, field.after) {
			changed = append(changed, field.name)
		}
	}

	if before.SSL != after.SSL {
		changed = append(changed, "ssl")
	}

	if before.CGI != after.CGI {
		changed = append(changed, "cgi")
	}

	if before.SSH != after.SSH {
		changed = append(changed, "ssh")
	}

	return after, changed, nil
}

// sameLimit compares two optional limits.
func sameLimit(a, b *int64) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}

	return *a == *b
}

// DeletePackage removes a package no account uses.
func (s *Store) DeletePackage(ctx context.Context, id int64) error {
	pkg, err := s.PackageByID(ctx, id)
	if err != nil {
		return err
	}

	if pkg.Accounts > 0 {
		return fmt.Errorf("%w: %d account(s) are on it", ErrPackageInUse, pkg.Accounts)
	}

	_, err = s.db.ExecContext(ctx, "DELETE FROM `packages` WHERE id = ?", id)

	return wrap("delete the package", err)
}

// PackageAccounts lists the accounts on a package, which is what a changed
// quota has to reach.
func (s *Store) PackageAccounts(ctx context.Context, packageID int64) ([]User, error) {
	rows, err := s.db.QueryContext(ctx,
		"SELECT username FROM `users` WHERE package_id = ? AND `role` = 'user' AND `status` = 'active' ORDER BY username",
		packageID)
	if err != nil {
		return nil, wrap("list the accounts on the package", err)
	}

	defer rows.Close()

	names := []string{}

	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, wrap("list the accounts on the package", err)
		}

		names = append(names, name)
	}

	if err := rows.Err(); err != nil {
		return nil, wrap("list the accounts on the package", err)
	}

	users := make([]User, 0, len(names))

	for _, name := range names {
		user, err := s.UserByUsername(ctx, name)
		if err != nil {
			return nil, err
		}

		users = append(users, user)
	}

	return users, nil
}

// QueueQuota asks the agent to put a package's disk and inode limits on every
// account that uses it. The task id is derived from the account so two edits in
// a row do not leave two identical jobs queued.
func (s *Store) QueueQuota(ctx context.Context, pkg Package, actor *User) (int, error) {
	accounts, err := s.PackageAccounts(ctx, pkg.ID)
	if err != nil {
		return 0, err
	}

	disk := int64(0)
	if pkg.DiskMB != nil {
		disk = *pkg.DiskMB
	}

	inodes := int64(0)
	if pkg.Inodes != nil {
		inodes = *pkg.Inodes
	}

	queued := 0

	for _, account := range accounts {
		id := NewTaskID()

		var actorID *int64
		if actor != nil {
			actorID = &actor.ID
		}

		if err := s.CreateTask(ctx, id, &account.ID, actorID, "account.set_quota",
			taskPayload(map[string]any{"username": account.Username, "disk_mb": disk, "inodes": inodes})); err != nil {
			return queued, err
		}

		queued++
	}

	return queued, nil
}

// QueueSSH re-applies a package's SSH flag to the accounts on it that have a
// shell today, so turning SSH off in a package really closes the shells it
// already gave out. Accounts that never had a shell are left alone.
func (s *Store) QueueSSH(ctx context.Context, pkg Package, actor *User) (int, error) {
	rows, err := s.db.QueryContext(ctx,
		"SELECT username FROM `users` WHERE package_id = ? AND `role` = 'user' "+
			"AND `status` = 'active' AND ssh_enabled = 1 ORDER BY username", pkg.ID)
	if err != nil {
		return 0, wrap("list the shells on the package", err)
	}

	names := []string{}

	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			rows.Close()

			return 0, wrap("list the shells on the package", err)
		}

		names = append(names, name)
	}

	rows.Close()

	if err := rows.Err(); err != nil {
		return 0, wrap("list the shells on the package", err)
	}

	sort.Strings(names)

	queued := 0

	for _, name := range names {
		account, err := s.UserByUsername(ctx, name)
		if err != nil {
			return queued, err
		}

		if err := s.SetSSHEnabled(ctx, account.ID, pkg.SSH); err != nil {
			return queued, err
		}

		var actorID *int64
		if actor != nil {
			actorID = &actor.ID
		}

		if err := s.CreateTask(ctx, NewTaskID(), &account.ID, actorID, "account.set_ssh",
			taskPayload(map[string]any{"username": account.Username, "enabled": pkg.SSH})); err != nil {
			return queued, err
		}

		queued++
	}

	return queued, nil
}

// NewTaskID returns a UUID v4 for the tasks table, whose id column already
// holds that shape in the Laravel panel.
func NewTaskID() string {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		panic("no randomness for task ids: " + err.Error())
	}

	raw[6] = (raw[6] & 0x0f) | 0x40
	raw[8] = (raw[8] & 0x3f) | 0x80

	encoded := hex.EncodeToString(raw)

	return encoded[0:8] + "-" + encoded[8:12] + "-" + encoded[12:16] + "-" + encoded[16:20] + "-" + encoded[20:32]
}

// taskPayload encodes the params of an agent call.
func taskPayload(params map[string]any) string {
	encoded, err := json.Marshal(params)
	if err != nil {
		panic("a task payload could not be encoded: " + err.Error())
	}

	return string(encoded)
}
