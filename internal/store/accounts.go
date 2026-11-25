package store

import (
	"context"
	"fmt"
)

// ActivateAccount records the uid and gid the agent gave the account and
// makes it usable.
func (s *Store) ActivateAccount(ctx context.Context, id int64, uid, gid int32) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE users SET uid = ?, gid = ?, status = 'active', updated_at = ?
		WHERE id = ?`, uid, gid, Now(), id)

	return wrap("activate the account", err)
}

// DeleteAccount removes a hosting account row.
//
// The server side is gone already: the caller runs this after the agent
// confirmed the account's files, databases and mail removed. Domains cascade
// with the row.
func (s *Store) DeleteAccount(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM users WHERE id = ? AND role = 'user'`, id)

	return wrap("delete the account", err)
}

// DeleteDomain removes a website row after the agent took it off the server.
func (s *Store) DeleteDomain(ctx context.Context, name string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM domains WHERE name = ?`, name)

	return wrap("delete the domain", err)
}

// SetUserName updates the display name of an account or panel user.
func (s *Store) SetUserName(ctx context.Context, id int64, name string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE users SET name = ?, updated_at = ? WHERE id = ?`, name, Now(), id)

	return wrap("rename the account", err)
}

// SetUserEmail changes the address an account signs in with. The caller checks
// that nobody else has it.
func (s *Store) SetUserEmail(ctx context.Context, id int64, email string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE users SET email = ?, email_verified_at = NULL, updated_at = ? WHERE id = ?`,
		email, Now(), id)

	return wrap("change the email address", err)
}

// CreatePanelUser inserts an administrator or reseller. These live only in
// the panel: they have no Linux user, no home and nothing on the server, so
// they are active immediately.
func (s *Store) CreatePanelUser(ctx context.Context, username, name, email, hash string, role Role, packageID *int64) (User, error) {
	if !role.IsPanelRole() {
		return User{}, fmt.Errorf("role %q cannot be created as a panel account", role)
	}

	now := Now()

	result, err := s.db.ExecContext(ctx, `
		INSERT INTO users (username, name, email, email_verified_at, password, role, status,
			reseller_package_id, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, 'active', ?, ?, ?)`,
		username, name, email, now, hash, string(role), NullInt64(packageID), now, now)
	if err != nil {
		return User{}, wrap("create the account", err)
	}

	id, err := result.LastInsertId()
	if err != nil {
		return User{}, wrap("create the account", err)
	}

	return s.UserByID(ctx, id)
}
