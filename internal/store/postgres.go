package store

import (
	"context"
	"database/sql"
)

// PostgresDatabase is a PostgreSQL database owned by a hosting account. Its
// name is always "<account>_<suffix>".
type PostgresDatabase struct {
	ID       int64  `json:"id"`
	UserID   int64  `json:"userId"`
	Name     string `json:"name"`
	Status   string `json:"status"`
	SizeByte *int64 `json:"sizeBytes"`
	Username string `json:"username"`
}

// PostgresUser is a PostgreSQL login. Unlike MySQL it cannot take a pre-hashed
// password, so the row keeps one the agent can send.
type PostgresUser struct {
	ID        int64    `json:"id"`
	UserID    int64    `json:"userId"`
	Name      string   `json:"name"`
	Status    string   `json:"status"`
	Username  string   `json:"username"`
	Databases []string `json:"databases"`
}

// PostgresDatabases lists the PostgreSQL databases an operator may see.
func (s *Store) PostgresDatabases(ctx context.Context, owner *User) ([]PostgresDatabase, error) {
	query := `
		SELECT p.id, p.user_id, p.name, p.status, p.size_bytes, u.username
		FROM postgres_databases p
		JOIN users u ON u.id = p.user_id
		WHERE 1 = 1`

	query, args := scopeOwner(query, nil, owner)
	query += ` ORDER BY p.name`

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, wrap("list the PostgreSQL databases", err)
	}

	defer rows.Close()

	databases := []PostgresDatabase{}

	for rows.Next() {
		var (
			database PostgresDatabase
			size     sql.NullInt64
		)

		if err := rows.Scan(&database.ID, &database.UserID, &database.Name, &database.Status,
			&size, &database.Username); err != nil {
			return nil, wrap("list the PostgreSQL databases", err)
		}

		if size.Valid {
			database.SizeByte = &size.Int64
		}

		databases = append(databases, database)
	}

	return databases, wrap("list the PostgreSQL databases", rows.Err())
}

// PostgresDatabaseByID loads one PostgreSQL database.
func (s *Store) PostgresDatabaseByID(ctx context.Context, id int64) (PostgresDatabase, error) {
	var (
		database PostgresDatabase
		size     sql.NullInt64
	)

	err := s.db.QueryRowContext(ctx, `
		SELECT p.id, p.user_id, p.name, p.status, p.size_bytes, u.username
		FROM postgres_databases p
		JOIN users u ON u.id = p.user_id
		WHERE p.id = ?`, id).
		Scan(&database.ID, &database.UserID, &database.Name, &database.Status, &size, &database.Username)
	if err != nil {
		return PostgresDatabase{}, wrap("read the PostgreSQL database", translate(err))
	}

	if size.Valid {
		database.SizeByte = &size.Int64
	}

	return database, nil
}

// CreatePostgresDatabase stores a database in the pending state.
func (s *Store) CreatePostgresDatabase(ctx context.Context, userID int64, name string) (PostgresDatabase, error) {
	now := Now()

	result, err := s.db.ExecContext(ctx, `
		INSERT INTO postgres_databases (user_id, name, status, created_at, updated_at)
		VALUES (?, ?, 'pending', ?, ?)`, userID, name, now, now)
	if err != nil {
		return PostgresDatabase{}, wrap("create the PostgreSQL database", err)
	}

	id, err := result.LastInsertId()
	if err != nil {
		return PostgresDatabase{}, wrap("create the PostgreSQL database", err)
	}

	return s.PostgresDatabaseByID(ctx, id)
}

// SetPostgresDatabaseStatus records the agent's answer by row id.
func (s *Store) SetPostgresDatabaseStatus(ctx context.Context, id int64, status string) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE postgres_databases SET status = ?, updated_at = ? WHERE id = ?`, status, Now(), id)

	return wrap("update the PostgreSQL database", err)
}

// SetPostgresDatabaseStatusByName is the same, keyed by the name the agent
// answered with.
func (s *Store) SetPostgresDatabaseStatusByName(ctx context.Context, name, status string) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE postgres_databases SET status = ?, updated_at = ? WHERE name = ?`, status, Now(), name)

	return wrap("update the PostgreSQL database", err)
}

// DeletePostgresDatabase forgets a database and its grants.
func (s *Store) DeletePostgresDatabase(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM postgres_databases WHERE id = ?`, id)

	return wrap("delete the PostgreSQL database", err)
}

// PostgresUsers lists the PostgreSQL logins of an operator.
func (s *Store) PostgresUsers(ctx context.Context, owner *User) ([]PostgresUser, error) {
	query := `
		SELECT p.id, p.user_id, p.name, p.status, u.username
		FROM postgres_users p
		JOIN users u ON u.id = p.user_id
		WHERE 1 = 1`

	query, args := scopeOwner(query, nil, owner)
	query += ` ORDER BY p.name`

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, wrap("list the PostgreSQL users", err)
	}

	users := []PostgresUser{}

	for rows.Next() {
		var user PostgresUser

		if err := rows.Scan(&user.ID, &user.UserID, &user.Name, &user.Status, &user.Username); err != nil {
			rows.Close()

			return nil, wrap("list the PostgreSQL users", err)
		}

		users = append(users, user)
	}

	rows.Close()

	if err := rows.Err(); err != nil {
		return nil, wrap("list the PostgreSQL users", err)
	}

	for i := range users {
		grants, err := s.postgresGrants(ctx, users[i].ID)
		if err != nil {
			return nil, err
		}

		users[i].Databases = grants
	}

	return users, nil
}

// PostgresUserByID loads one PostgreSQL login with its grants.
func (s *Store) PostgresUserByID(ctx context.Context, id int64) (PostgresUser, error) {
	var user PostgresUser

	err := s.db.QueryRowContext(ctx, `
		SELECT p.id, p.user_id, p.name, p.status, u.username
		FROM postgres_users p
		JOIN users u ON u.id = p.user_id
		WHERE p.id = ?`, id).
		Scan(&user.ID, &user.UserID, &user.Name, &user.Status, &user.Username)
	if err != nil {
		return PostgresUser{}, wrap("read the PostgreSQL user", translate(err))
	}

	user.Databases, err = s.postgresGrants(ctx, id)
	if err != nil {
		return PostgresUser{}, err
	}

	return user, nil
}

// PostgresPassword returns the stored password in the form the agent expects.
// It is stored encrypted when the panel has an APP_KEY, and plain otherwise.
func (s *Store) PostgresPassword(ctx context.Context, id int64) (string, error) {
	var stored string

	if err := s.db.QueryRowContext(ctx, `SELECT password FROM postgres_users WHERE id = ?`, id).Scan(&stored); err != nil {
		return "", wrap("read the PostgreSQL password", translate(err))
	}

	return stored, nil
}

// postgresGrants names the databases a PostgreSQL login may use.
func (s *Store) postgresGrants(ctx context.Context, postgresUserID int64) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT d.name
		FROM postgres_database_postgres_user p
		JOIN postgres_databases d ON d.id = p.postgres_database_id
		WHERE p.postgres_user_id = ?
		ORDER BY d.name`, postgresUserID)
	if err != nil {
		return nil, wrap("list the PostgreSQL grants", err)
	}

	defer rows.Close()

	names := []string{}

	for rows.Next() {
		var name string

		if err := rows.Scan(&name); err != nil {
			return nil, wrap("list the PostgreSQL grants", err)
		}

		names = append(names, name)
	}

	return names, wrap("list the PostgreSQL grants", rows.Err())
}

// SavePostgresUser inserts or updates a PostgreSQL login and replaces its
// grants. password is stored as given: encrypt it before calling when the panel
// has a key.
func (s *Store) SavePostgresUser(ctx context.Context, userID int64, name, password string, grants []int64) (PostgresUser, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return PostgresUser{}, wrap("save the PostgreSQL user", err)
	}

	defer func() { _ = tx.Rollback() }()

	now := Now()

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO postgres_users (user_id, name, password, status, created_at, updated_at)
		VALUES (?, ?, ?, 'pending', ?, ?)
		ON DUPLICATE KEY UPDATE password = VALUES(password), updated_at = VALUES(updated_at)`,
		userID, name, password, now, now); err != nil {
		return PostgresUser{}, wrap("save the PostgreSQL user", err)
	}

	var id int64
	if err := tx.QueryRowContext(ctx, `SELECT id FROM postgres_users WHERE name = ?`, name).Scan(&id); err != nil {
		return PostgresUser{}, wrap("save the PostgreSQL user", translate(err))
	}

	if _, err := tx.ExecContext(ctx, `DELETE FROM postgres_database_postgres_user WHERE postgres_user_id = ?`, id); err != nil {
		return PostgresUser{}, wrap("save the PostgreSQL user", err)
	}

	for _, databaseID := range grants {
		if _, err := tx.ExecContext(ctx, `
			INSERT IGNORE INTO postgres_database_postgres_user (postgres_database_id, postgres_user_id)
			VALUES (?, ?)`, databaseID, id); err != nil {
			return PostgresUser{}, wrap("save the PostgreSQL user", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return PostgresUser{}, wrap("save the PostgreSQL user", err)
	}

	return s.PostgresUserByID(ctx, id)
}

// SetPostgresUserStatus records the agent's answer by row id.
func (s *Store) SetPostgresUserStatus(ctx context.Context, id int64, status string) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE postgres_users SET status = ?, updated_at = ? WHERE id = ?`, status, Now(), id)

	return wrap("update the PostgreSQL user", err)
}

// SetPostgresUserStatusByName is the same, keyed by name.
func (s *Store) SetPostgresUserStatusByName(ctx context.Context, name, status string) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE postgres_users SET status = ?, updated_at = ? WHERE name = ?`, status, Now(), name)

	return wrap("update the PostgreSQL user", err)
}

// DeletePostgresUser removes a PostgreSQL login and its grants.
func (s *Store) DeletePostgresUser(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM postgres_users WHERE id = ?`, id)

	return wrap("delete the PostgreSQL user", err)
}

// PostgresGrants names what a login may use, which is what the agent wants.
func (s *Store) PostgresGrants(ctx context.Context, postgresUserID int64) ([]string, error) {
	return s.postgresGrants(ctx, postgresUserID)
}
