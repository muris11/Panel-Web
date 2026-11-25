package store

import (
	"context"
	"database/sql"
)

// Database is a MySQL database owned by a hosting account.
type Database struct {
	ID       int64  `json:"id"`
	UserID   int64  `json:"userId"`
	Name     string `json:"name"`
	Status   string `json:"status"`
	SizeByte *int64 `json:"sizeBytes"`
	Username string `json:"username"`
}

// DatabaseUser is a MySQL user, one row per account login.
type DatabaseUser struct {
	ID           int64    `json:"id"`
	UserID       int64    `json:"userId"`
	Name         string   `json:"name"`
	PasswordHash string   `json:"-"`
	Status       string   `json:"status"`
	Username     string   `json:"username"`
	Databases    []string `json:"databases"`
}

// databasesTable is the table's quoted name, because MySQL 8 reserves
// "databases" and refuses it unquoted. Go raw strings cannot hold a backtick,
// so the four statements that name the table concatenate this instead.
const databasesTable = "`databases`"

// Databases lists the databases an operator may see, with their owner.
func (s *Store) Databases(ctx context.Context, owner *User) ([]Database, error) {
	query := `
		SELECT d.id, d.user_id, d.name, d.status, d.size_bytes, u.username
		FROM ` + databasesTable + ` d
		JOIN users u ON u.id = d.user_id
		WHERE 1 = 1`

	query, args := scopeOwner(query, nil, owner)
	query += ` ORDER BY d.name`

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, wrap("list the databases", err)
	}

	defer rows.Close()

	databases := []Database{}

	for rows.Next() {
		var (
			database Database
			size     sql.NullInt64
		)

		if err := rows.Scan(&database.ID, &database.UserID, &database.Name, &database.Status,
			&size, &database.Username); err != nil {
			return nil, wrap("list the databases", err)
		}

		if size.Valid {
			database.SizeByte = &size.Int64
		}

		databases = append(databases, database)
	}

	return databases, wrap("list the databases", rows.Err())
}

// DatabaseByID loads one database and the username that owns it.
func (s *Store) DatabaseByID(ctx context.Context, id int64) (Database, error) {
	var (
		database Database
		size     sql.NullInt64
	)

	err := s.db.QueryRowContext(ctx, `
		SELECT d.id, d.user_id, d.name, d.status, d.size_bytes, u.username
		FROM `+databasesTable+` d
		JOIN users u ON u.id = d.user_id
		WHERE d.id = ?`, id).
		Scan(&database.ID, &database.UserID, &database.Name, &database.Status, &size, &database.Username)
	if err != nil {
		return Database{}, wrap("read the database", translate(err))
	}

	if size.Valid {
		database.SizeByte = &size.Int64
	}

	return database, nil
}

// CreateDatabase stores a database in the pending state.
func (s *Store) CreateDatabase(ctx context.Context, userID int64, name string) (Database, error) {
	now := Now()

	result, err := s.db.ExecContext(ctx, `
		INSERT INTO `+databasesTable+` (user_id, name, status, created_at, updated_at)
		VALUES (?, ?, 'pending', ?, ?)`, userID, name, now, now)
	if err != nil {
		return Database{}, wrap("create the database", err)
	}

	id, err := result.LastInsertId()
	if err != nil {
		return Database{}, wrap("create the database", err)
	}

	return s.DatabaseByID(ctx, id)
}

// SetDatabaseStatus is what the agent's answer writes back.
func (s *Store) SetDatabaseStatus(ctx context.Context, id int64, status string) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE `+databasesTable+` SET status = ?, updated_at = ? WHERE id = ?`, status, Now(), id)

	return wrap("update the database", err)
}

// SetDatabaseStatusByName marks a database once the agent has created it. The
// agent answers with the name it acted on, not the row id.
func (s *Store) SetDatabaseStatusByName(ctx context.Context, name, status string) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE `+databasesTable+` SET status = ?, updated_at = ? WHERE name = ?`, status, Now(), name)

	return wrap("update the database", err)
}

// DeleteDatabase forgets a database, including which users could reach it.
func (s *Store) DeleteDatabase(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM `+databasesTable+` WHERE id = ?`, id)

	return wrap("delete the database", err)
}

// DatabaseUsers lists the MySQL logins of a hosting account.
func (s *Store) DatabaseUsers(ctx context.Context, owner *User) ([]DatabaseUser, error) {
	query := `
		SELECT du.id, du.user_id, du.name, du.password_hash, du.status, u.username
		FROM database_users du
		JOIN users u ON u.id = du.user_id
		WHERE 1 = 1`

	query, args := scopeOwner(query, nil, owner)
	query += ` ORDER BY du.name`

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, wrap("list the database users", err)
	}

	users := []DatabaseUser{}

	for rows.Next() {
		var user DatabaseUser

		if err := rows.Scan(&user.ID, &user.UserID, &user.Name, &user.PasswordHash,
			&user.Status, &user.Username); err != nil {
			rows.Close()

			return nil, wrap("list the database users", err)
		}

		users = append(users, user)
	}

	rows.Close()

	if err := rows.Err(); err != nil {
		return nil, wrap("list the database users", err)
	}

	for i := range users {
		grants, err := s.grantedDatabases(ctx, users[i].ID)
		if err != nil {
			return nil, err
		}

		users[i].Databases = grants
	}

	return users, nil
}

// DatabaseUserByID loads one MySQL login, with its grants.
func (s *Store) DatabaseUserByID(ctx context.Context, id int64) (DatabaseUser, error) {
	var user DatabaseUser

	err := s.db.QueryRowContext(ctx, `
		SELECT du.id, du.user_id, du.name, du.password_hash, du.status, u.username
		FROM database_users du
		JOIN users u ON u.id = du.user_id
		WHERE du.id = ?`, id).
		Scan(&user.ID, &user.UserID, &user.Name, &user.PasswordHash, &user.Status, &user.Username)
	if err != nil {
		return DatabaseUser{}, wrap("read the database user", translate(err))
	}

	user.Databases, err = s.grantedDatabases(ctx, id)
	if err != nil {
		return DatabaseUser{}, err
	}

	return user, nil
}

// grantedDatabases returns the databases a MySQL user may use.
func (s *Store) grantedDatabases(ctx context.Context, databaseUserID int64) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT d.name
		FROM database_database_user p
		JOIN `+databasesTable+` d ON d.id = p.database_id
		WHERE p.database_user_id = ?
		ORDER BY d.name`, databaseUserID)
	if err != nil {
		return nil, wrap("list the database grants", err)
	}

	defer rows.Close()

	names := []string{}

	for rows.Next() {
		var name string

		if err := rows.Scan(&name); err != nil {
			return nil, wrap("list the database grants", err)
		}

		names = append(names, name)
	}

	return names, wrap("list the database grants", rows.Err())
}

// SaveDatabaseUser inserts a MySQL login and replaces its grants. hash is the
// native password hash the agent gives to CREATE USER.
func (s *Store) SaveDatabaseUser(ctx context.Context, userID int64, name, hash string, grants []int64) (DatabaseUser, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return DatabaseUser{}, wrap("save the database user", err)
	}

	defer func() { _ = tx.Rollback() }()

	now := Now()

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO database_users (user_id, name, password_hash, status, created_at, updated_at)
		VALUES (?, ?, ?, 'pending', ?, ?)
		ON DUPLICATE KEY UPDATE password_hash = VALUES(password_hash), updated_at = VALUES(updated_at)`,
		userID, name, hash, now, now); err != nil {
		return DatabaseUser{}, wrap("save the database user", err)
	}

	var id int64
	if err := tx.QueryRowContext(ctx, `SELECT id FROM database_users WHERE name = ?`, name).Scan(&id); err != nil {
		return DatabaseUser{}, wrap("save the database user", translate(err))
	}

	if _, err := tx.ExecContext(ctx, `DELETE FROM database_database_user WHERE database_user_id = ?`, id); err != nil {
		return DatabaseUser{}, wrap("save the database user", err)
	}

	for _, databaseID := range grants {
		if _, err := tx.ExecContext(ctx, `
			INSERT IGNORE INTO database_database_user (database_id, database_user_id) VALUES (?, ?)`,
			databaseID, id); err != nil {
			return DatabaseUser{}, wrap("save the database user", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return DatabaseUser{}, wrap("save the database user", err)
	}

	return s.DatabaseUserByID(ctx, id)
}

// SetDatabaseUserStatus records the agent's answer.
func (s *Store) SetDatabaseUserStatus(ctx context.Context, id int64, status string) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE database_users SET status = ?, updated_at = ? WHERE id = ?`, status, Now(), id)

	return wrap("update the database user", err)
}

// DeleteDatabaseUser removes a MySQL login and its grants.
func (s *Store) DeleteDatabaseUser(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM database_users WHERE id = ?`, id)

	return wrap("delete the database user", err)
}

// DatabaseGrants names the databases a MySQL user can reach, by name, which is
// what the agent wants.
func (s *Store) DatabaseNamesFor(ctx context.Context, databaseUserID int64) ([]string, error) {
	return s.grantedDatabases(ctx, databaseUserID)
}

// SetDatabaseUserStatusByName marks a MySQL user once the agent has created it.
func (s *Store) SetDatabaseUserStatusByName(ctx context.Context, user, status string) error {
	_, err := s.db.ExecContext(ctx,
		"UPDATE `database_users` SET `status` = ?, `updated_at` = ? WHERE `name` = ?",
		status, Now(), user)

	return wrap("update the database user", err)
}
