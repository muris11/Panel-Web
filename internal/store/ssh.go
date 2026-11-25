package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// The two refusals a caller has to tell apart from a database failure.
var (
	// ErrTooManyKeys is returned when an account reached the key limit.
	ErrTooManyKeys = errors.New("too many keys")
	// ErrKeyExists is returned when the same key is added twice.
	ErrKeyExists = errors.New("the key is already added")
)

// MaxSSHKeys is how many keys an account may add, the same number the agent
// allows (App\Services\SshService::MAX_KEYS in the Laravel panel).
const MaxSSHKeys = 20

// SSHKey is one public key an account added.
type SSHKey struct {
	ID          int64
	UserID      int64
	Name        string
	PublicKey   string
	Fingerprint string
	CreatedAt   time.Time
}

// SSHKeys lists an account's keys in the order they were added.
func (s *Store) SSHKeys(ctx context.Context, userID int64) ([]SSHKey, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, user_id, name, public_key, fingerprint, created_at
		FROM ssh_keys WHERE user_id = ? ORDER BY name, id`, userID)
	if err != nil {
		return nil, wrap("read the SSH keys", err)
	}

	defer func() { _ = rows.Close() }()

	keys := []SSHKey{}

	for rows.Next() {
		var (
			key     SSHKey
			created sql.NullTime
		)

		if err := rows.Scan(&key.ID, &key.UserID, &key.Name, &key.PublicKey, &key.Fingerprint, &created); err != nil {
			return nil, wrap("read the SSH keys", err)
		}

		if created.Valid {
			key.CreatedAt = created.Time
		}

		keys = append(keys, key)
	}

	return keys, wrap("read the SSH keys", rows.Err())
}

// SSHKeyOwner returns the account a key belongs to, so a caller can check it
// may touch it before anything is removed.
func (s *Store) SSHKeyOwner(ctx context.Context, id int64) (int64, error) {
	var userID int64

	err := s.db.QueryRowContext(ctx, `SELECT user_id FROM ssh_keys WHERE id = ?`, id).Scan(&userID)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, ErrNotFound
	}

	return userID, wrap("find the SSH key", err)
}

// AddSSHKey stores a key for an account. The account row is locked first, so
// two keys added at once both count against the limit and a duplicate cannot
// slip past the check.
func (s *Store) AddSSHKey(ctx context.Context, userID int64, name, publicKey, fingerprint string) (SSHKey, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return SSHKey{}, wrap("add the SSH key", err)
	}

	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx, `SELECT id FROM users WHERE id = ? FOR UPDATE`, userID); err != nil {
		return SSHKey{}, wrap("lock the account", err)
	}

	var existing int

	if err := tx.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM ssh_keys WHERE user_id = ? AND fingerprint = ?`, userID, fingerprint).Scan(&existing); err != nil {
		return SSHKey{}, wrap("look for the key", err)
	}

	if existing > 0 {
		return SSHKey{}, ErrKeyExists
	}

	var count int

	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM ssh_keys WHERE user_id = ?`, userID).Scan(&count); err != nil {
		return SSHKey{}, wrap("count the keys", err)
	}

	if count >= MaxSSHKeys {
		return SSHKey{}, ErrTooManyKeys
	}

	now := Now()

	result, err := tx.ExecContext(ctx, `
		INSERT INTO ssh_keys (user_id, name, public_key, fingerprint, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?)`, userID, name, publicKey, fingerprint, now, now)
	if err != nil {
		return SSHKey{}, wrap("add the SSH key", err)
	}

	id, err := result.LastInsertId()
	if err != nil {
		return SSHKey{}, wrap("add the SSH key", err)
	}

	if err := tx.Commit(); err != nil {
		return SSHKey{}, wrap("add the SSH key", err)
	}

	return SSHKey{
		ID:          id,
		UserID:      userID,
		Name:        name,
		PublicKey:   publicKey,
		Fingerprint: fingerprint,
		CreatedAt:   now,
	}, nil
}

// DeleteSSHKey removes one key of one account.
func (s *Store) DeleteSSHKey(ctx context.Context, userID, id int64) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM ssh_keys WHERE id = ? AND user_id = ?`, id, userID); err != nil {
		return wrap("remove the SSH key", err)
	}

	return nil
}

// SetSSHEnabled records whether an account asked for a shell.
func (s *Store) SetSSHEnabled(ctx context.Context, userID int64, enabled bool) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE users SET ssh_enabled = ?, updated_at = ? WHERE id = ?`, enabled, Now(), userID)

	return wrap("save the SSH access", err)
}

// SSHPasswordSet reports whether the account has reset its SFTP password. The
// column belongs to the Laravel schema; a database without it simply answers
// false instead of failing the page.
func (s *Store) SSHPasswordSet(ctx context.Context, userID int64) bool {
	var set sql.NullTime

	err := s.db.QueryRowContext(ctx, `SELECT sftp_password_set_at FROM users WHERE id = ?`, userID).Scan(&set)
	if err != nil {
		return false
	}

	return set.Valid
}

// SSHEnabled reports whether the account asked for a shell.
func (s *Store) SSHEnabled(ctx context.Context, userID int64) (bool, error) {
	var enabled bool

	err := s.db.QueryRowContext(ctx, `SELECT ssh_enabled FROM users WHERE id = ?`, userID).Scan(&enabled)
	if errors.Is(err, sql.ErrNoRows) {
		return false, ErrNotFound
	}

	return enabled, wrap("read the SSH access", err)
}
