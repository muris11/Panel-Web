package store

import (
	"context"
	"database/sql"
	"time"
)

// --- FTP ------------------------------------------------------------------

// FTPAccount is one FTP login, "name@domain".
type FTPAccount struct {
	ID           int64  `json:"id"`
	UserID       int64  `json:"userId"`
	DomainID     int64  `json:"domainId"`
	Login        string `json:"login"`
	PasswordHash string `json:"-"`
	Directory    string `json:"directory"`
	QuotaMB      *int64 `json:"quotaMb"`
	Status       string `json:"status"`
	Username     string `json:"username"`
	DomainName   string `json:"domainName"`
}

// FTPAccounts lists the FTP logins an operator may see.
func (s *Store) FTPAccounts(ctx context.Context, owner *User) ([]FTPAccount, error) {
	query := `
		SELECT f.id, f.user_id, f.domain_id, f.login, f.password_hash, f.directory,
			f.quota_mb, f.status, u.username, d.name
		FROM ftp_accounts f
		JOIN users u ON u.id = f.user_id
		JOIN domains d ON d.id = f.domain_id
		WHERE 1 = 1`

	query, args := scopeOwner(query, nil, owner)
	query += ` ORDER BY f.login`

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, wrap("list the FTP accounts", err)
	}

	defer rows.Close()

	accounts := []FTPAccount{}

	for rows.Next() {
		var (
			account FTPAccount
			quota   sql.NullInt64
		)

		if err := rows.Scan(&account.ID, &account.UserID, &account.DomainID, &account.Login,
			&account.PasswordHash, &account.Directory, &quota, &account.Status,
			&account.Username, &account.DomainName); err != nil {
			return nil, wrap("list the FTP accounts", err)
		}

		if quota.Valid {
			account.QuotaMB = &quota.Int64
		}

		accounts = append(accounts, account)
	}

	return accounts, wrap("list the FTP accounts", rows.Err())
}

// FTPAccountByID loads one FTP login.
func (s *Store) FTPAccountByID(ctx context.Context, id int64) (FTPAccount, error) {
	var (
		account FTPAccount
		quota   sql.NullInt64
	)

	err := s.db.QueryRowContext(ctx, `
		SELECT f.id, f.user_id, f.domain_id, f.login, f.password_hash, f.directory,
			f.quota_mb, f.status, u.username, d.name
		FROM ftp_accounts f
		JOIN users u ON u.id = f.user_id
		JOIN domains d ON d.id = f.domain_id
		WHERE f.id = ?`, id).
		Scan(&account.ID, &account.UserID, &account.DomainID, &account.Login, &account.PasswordHash,
			&account.Directory, &quota, &account.Status, &account.Username, &account.DomainName)
	if err != nil {
		return FTPAccount{}, wrap("read the FTP account", translate(err))
	}

	if quota.Valid {
		account.QuotaMB = &quota.Int64
	}

	return account, nil
}

// CreateFTPAccount stores a login; the caller has hashed the password in the
// form vsftpd reads.
func (s *Store) CreateFTPAccount(ctx context.Context, userID, domainID int64, login, hash, directory string, quotaMB *int64) (FTPAccount, error) {
	now := Now()

	result, err := s.db.ExecContext(ctx, `
		INSERT INTO ftp_accounts (user_id, domain_id, login, password_hash, directory, quota_mb, status, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, 'pending', ?, ?)`,
		userID, domainID, login, hash, directory, quotaMB, now, now)
	if err != nil {
		return FTPAccount{}, wrap("create the FTP account", err)
	}

	id, err := result.LastInsertId()
	if err != nil {
		return FTPAccount{}, wrap("create the FTP account", err)
	}

	return s.FTPAccountByID(ctx, id)
}

// SetFTPStatus records the agent's answer.
func (s *Store) SetFTPStatus(ctx context.Context, id int64, status string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE ftp_accounts SET status = ?, updated_at = ? WHERE id = ?`, status, Now(), id)

	return wrap("update the FTP account", err)
}

// DeleteFTPAccount removes a login.
func (s *Store) DeleteFTPAccount(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM ftp_accounts WHERE id = ?`, id)

	return wrap("delete the FTP account", err)
}

// --- cron -----------------------------------------------------------------

// CronJob is one line of an account's crontab.
type CronJob struct {
	ID          int64  `json:"id"`
	UserID      int64  `json:"userId"`
	Schedule    string `json:"schedule"`
	Command     string `json:"command"`
	Description string `json:"description"`
	Enabled     bool   `json:"enabled"`
}

// CronJobs lists an account's jobs in insertion order.
func (s *Store) CronJobs(ctx context.Context, userID int64) ([]CronJob, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, user_id, schedule, command, COALESCE(description, ''), enabled
		FROM cron_jobs
		WHERE user_id = ?
		ORDER BY id`, userID)
	if err != nil {
		return nil, wrap("list the cron jobs", err)
	}

	defer rows.Close()

	jobs := []CronJob{}

	for rows.Next() {
		var job CronJob

		if err := rows.Scan(&job.ID, &job.UserID, &job.Schedule, &job.Command,
			&job.Description, &job.Enabled); err != nil {
			return nil, wrap("list the cron jobs", err)
		}

		jobs = append(jobs, job)
	}

	return jobs, wrap("list the cron jobs", rows.Err())
}

// CronJobByID loads one job with the account that owns it.
func (s *Store) CronJobByID(ctx context.Context, id int64) (CronJob, User, error) {
	var job CronJob

	err := s.db.QueryRowContext(ctx, `
		SELECT id, user_id, schedule, command, COALESCE(description, ''), enabled
		FROM cron_jobs WHERE id = ?`, id).
		Scan(&job.ID, &job.UserID, &job.Schedule, &job.Command, &job.Description, &job.Enabled)
	if err != nil {
		return CronJob{}, User{}, wrap("read the cron job", translate(err))
	}

	account, err := s.UserByID(ctx, job.UserID)
	if err != nil {
		return CronJob{}, User{}, err
	}

	return job, account, nil
}

// CreateCronJob adds a job.
func (s *Store) CreateCronJob(ctx context.Context, userID int64, schedule, command, description string, enabled bool) (CronJob, error) {
	now := Now()

	result, err := s.db.ExecContext(ctx, `
		INSERT INTO cron_jobs (user_id, schedule, command, description, enabled, created_at, updated_at)
		VALUES (?, ?, ?, NULLIF(?, ''), ?, ?, ?)`,
		userID, schedule, command, description, enabled, now, now)
	if err != nil {
		return CronJob{}, wrap("create the cron job", err)
	}

	id, err := result.LastInsertId()
	if err != nil {
		return CronJob{}, wrap("create the cron job", err)
	}

	return CronJob{ID: id, UserID: userID, Schedule: schedule, Command: command, Description: description, Enabled: enabled}, nil
}

// UpdateCronJob changes a job in place.
func (s *Store) UpdateCronJob(ctx context.Context, id int64, schedule, command, description string, enabled bool) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE cron_jobs
		SET schedule = ?, command = ?, description = NULLIF(?, ''), enabled = ?, updated_at = ?
		WHERE id = ?`, schedule, command, description, enabled, Now(), id)

	return wrap("update the cron job", err)
}

// DeleteCronJob removes a job.
func (s *Store) DeleteCronJob(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM cron_jobs WHERE id = ?`, id)

	return wrap("delete the cron job", err)
}

// --- API tokens -----------------------------------------------------------

// APIToken is a credential a billing system uses instead of a session.
type APIToken struct {
	ID         int64      `json:"id"`
	UserID     int64      `json:"userId"`
	Name       string     `json:"name"`
	TokenHash  string     `json:"-"`
	LastUsedAt *time.Time `json:"lastUsedAt"`
	ExpiresAt  *time.Time `json:"expiresAt"`
	CreatedAt  time.Time  `json:"createdAt"`
}

// CreateToken stores a token by its hash; the plain token is shown once.
func (s *Store) CreateToken(ctx context.Context, userID int64, name, hash string, expiresAt *time.Time) (APIToken, error) {
	now := Now()

	result, err := s.db.ExecContext(ctx, `
		INSERT INTO api_tokens (user_id, name, token_hash, expires_at, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?)`, userID, name, hash, expiresAt, now, now)
	if err != nil {
		return APIToken{}, wrap("create the token", err)
	}

	id, err := result.LastInsertId()
	if err != nil {
		return APIToken{}, wrap("create the token", err)
	}

	return APIToken{ID: id, UserID: userID, Name: name, TokenHash: hash, ExpiresAt: expiresAt, CreatedAt: now}, nil
}

// Tokens lists one user's tokens; the hash never leaves the database.
func (s *Store) Tokens(ctx context.Context, userID int64) ([]APIToken, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, user_id, name, token_hash, last_used_at, expires_at, created_at
		FROM api_tokens
		WHERE user_id = ?
		ORDER BY created_at DESC`, userID)
	if err != nil {
		return nil, wrap("list the tokens", err)
	}

	defer rows.Close()

	tokens := []APIToken{}

	for rows.Next() {
		var (
			token   APIToken
			used    sql.NullTime
			expires sql.NullTime
			created sql.NullTime
		)

		if err := rows.Scan(&token.ID, &token.UserID, &token.Name, &token.TokenHash,
			&used, &expires, &created); err != nil {
			return nil, wrap("list the tokens", err)
		}

		if used.Valid {
			token.LastUsedAt = &used.Time
		}

		if expires.Valid {
			token.ExpiresAt = &expires.Time
		}

		if created.Valid {
			token.CreatedAt = created.Time
		}

		tokens = append(tokens, token)
	}

	return tokens, wrap("list the tokens", rows.Err())
}

// UserByTokenHash finds the account a token signs in as, if it is still valid.
func (s *Store) UserByTokenHash(ctx context.Context, hash string) (User, error) {
	var id int64

	err := s.db.QueryRowContext(ctx, `
		SELECT user_id FROM api_tokens
		WHERE token_hash = ? AND (expires_at IS NULL OR expires_at > ?)`, hash, Now()).Scan(&id)
	if err != nil {
		return User{}, wrap("read the token", translate(err))
	}

	return s.UserByID(ctx, id)
}

// TouchToken records that a token was used.
func (s *Store) TouchToken(ctx context.Context, hash string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE api_tokens SET last_used_at = ? WHERE token_hash = ?`, Now(), hash)

	return wrap("update the token", err)
}

// DeleteToken revokes a token. A user may only revoke its own.
func (s *Store) DeleteToken(ctx context.Context, userID, id int64) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM api_tokens WHERE id = ? AND user_id = ?`, id, userID)

	return wrap("delete the token", err)
}

// UpdateFTPAccount changes a login's password, quota or status.
func (s *Store) UpdateFTPAccount(ctx context.Context, id int64, hash string, quotaMB *int64, status string) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE ftp_accounts SET password_hash = ?, quota_mb = ?, status = ?, updated_at = ?
		WHERE id = ?`, hash, quotaMB, status, Now(), id)

	return wrap("update the FTP account", err)
}
