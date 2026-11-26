package store

import (
	"context"
	"database/sql"
	"time"
)

// DomainsByUser lists the websites of one hosting account.
func (s *Store) DomainsByUser(ctx context.Context, userID int64) ([]Domain, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, user_id, name, type, php_version, is_primary,
			COALESCE(document_root, ''), status, COALESCE(ssl_status, ''), created_at
		FROM domains
		WHERE user_id = ?
		ORDER BY is_primary DESC, name`, userID)
	if err != nil {
		return nil, wrap("list the domains", err)
	}

	defer rows.Close()

	domains := []Domain{}

	for rows.Next() {
		var (
			domain  Domain
			primary bool
			created sql.NullTime
		)

		if err := rows.Scan(&domain.ID, &domain.UserID, &domain.Name, &domain.Type,
			&domain.PHPVersion, &primary, &domain.DocumentRoot, &domain.Status,
			&domain.SSLStatus, &created); err != nil {
			return nil, wrap("list the domains", err)
		}

		domain.IsPrimary = primary

		if created.Valid {
			domain.CreatedAt = created.Time
		}

		domains = append(domains, domain)
	}

	return domains, wrap("list the domains", rows.Err())
}

// CreateDomain stores a website in the pending state; the agent's answer
// moves it to active.
func (s *Store) CreateDomain(ctx context.Context, userID int64, name, domainType, phpVersion string, primary bool) (Domain, error) {
	now := Now()

	result, err := s.db.ExecContext(ctx, `
		INSERT INTO domains (user_id, name, type, php_version, is_primary, status, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, 'pending', ?, ?)`,
		userID, name, domainType, phpVersion, primary, now, now)
	if err != nil {
		return Domain{}, wrap("create the domain", err)
	}

	id, err := result.LastInsertId()
	if err != nil {
		return Domain{}, wrap("create the domain", err)
	}

	var domain Domain
	var created sql.NullTime

	if err := s.db.QueryRowContext(ctx, `
		SELECT id, user_id, name, type, php_version, is_primary,
			COALESCE(document_root, ''), status, COALESCE(ssl_status, ''), created_at
		FROM domains WHERE id = ?`, id).
		Scan(&domain.ID, &domain.UserID, &domain.Name, &domain.Type, &domain.PHPVersion,
			&primary, &domain.DocumentRoot, &domain.Status, &domain.SSLStatus, &created); err != nil {
		return Domain{}, wrap("read the domain", translate(err))
	}

	domain.IsPrimary = primary

	if created.Valid {
		domain.CreatedAt = created.Time
	}

	return domain, nil
}

// SetDomainStatus stores the agent's verdict for a website.
func (s *Store) SetDomainStatus(ctx context.Context, name, status string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE domains SET status = ?, updated_at = ? WHERE name = ?`, status, Now(), name)

	return wrap("update the domain status", err)
}

// DomainExists reports whether a name is already taken on this server,
// including by another account.
func (s *Store) DomainExists(ctx context.Context, name string) (bool, error) {
	var exists bool

	err := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM domains WHERE name = ?)`, name).Scan(&exists)

	return exists, wrap("check the domain", err)
}

// DomainByID loads one website with the account that owns it.
func (s *Store) DomainByID(ctx context.Context, id int64) (Domain, error) {
	var (
		domain  Domain
		primary bool
		created sql.NullTime
		root    sql.NullString
		ssl     sql.NullString
	)

	err := s.db.QueryRowContext(ctx, `
		SELECT id, user_id, name, type, php_version, is_primary,
			document_root, status, ssl_status, created_at
		FROM domains WHERE id = ?`, id).
		Scan(&domain.ID, &domain.UserID, &domain.Name, &domain.Type, &domain.PHPVersion,
			&primary, &root, &domain.Status, &ssl, &created)
	if err != nil {
		return Domain{}, wrap("read the website", translate(err))
	}

	domain.IsPrimary = primary
	domain.DocumentRoot = root.String
	domain.SSLStatus = ssl.String

	if created.Valid {
		domain.CreatedAt = created.Time
	}

	return domain, nil
}

// AllDomains lists every website an operator may see, newest first, with the
// account that owns each one.
func (s *Store) AllDomains(ctx context.Context, owner *User, query string) ([]DomainsRow, error) {
	querySQL := `
		SELECT d.id, d.name, d.type, d.php_version, d.status, COALESCE(d.ssl_status, ''),
			COALESCE(d.force_https, 0), u.username, d.created_at
		FROM domains d
		JOIN users u ON u.id = d.user_id
		WHERE u.role = 'user'`

	args := []any{}

	if query != "" {
		querySQL += ` AND (d.name LIKE ? OR u.username LIKE ?)`
		args = append(args, "%"+query+"%", "%"+query+"%")
	}

	querySQL, args = scopeOwnerAny(querySQL, args, owner)
	querySQL += ` ORDER BY d.name LIMIT 500`

	rows, err := s.db.QueryContext(ctx, querySQL, args...)
	if err != nil {
		return nil, wrap("list the websites", err)
	}

	defer rows.Close()

	domains := []DomainsRow{}

	for rows.Next() {
		var (
			row      DomainsRow
			force    bool
			created  sql.NullTime
			username string
		)

		if err := rows.Scan(&row.ID, &row.Name, &row.Type, &row.PHPVersion, &row.Status,
			&row.SSLStatus, &force, &username, &created); err != nil {
			return nil, wrap("list the websites", err)
		}

		row.ForceHTTPS = force
		row.Username = username

		if created.Valid {
			row.CreatedAt = created.Time
		}

		domains = append(domains, row)
	}

	return domains, wrap("list the websites", rows.Err())
}

// UpdateDomainSettings stores what the operator changed about a website before
// the agent applies it.
func (s *Store) UpdateDomainSettings(ctx context.Context, id int64, phpVersion string, phpEnabled, forceHTTPS bool, wwwMode, documentRoot string) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE domains
		SET php_version = ?, php_enabled = ?, force_https = ?, www_mode = ?,
			document_root = NULLIF(?, ''), updated_at = ?
		WHERE id = ?`,
		phpVersion, phpEnabled, forceHTTPS, wwwMode, documentRoot, Now(), id)

	return wrap("update the website", err)
}

// SetDomainSSL records the certificate the agent reported.
func (s *Store) SetDomainSSL(ctx context.Context, id int64, status, source string, expires *time.Time) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE domains SET ssl_status = NULLIF(?, ''), ssl_source = NULLIF(?, ''),
			ssl_expires_at = ?, updated_at = ?
		WHERE id = ?`, status, source, expires, Now(), id)

	return wrap("update the certificate", err)
}

// scopeOwnerAny narrows a query that already joins users as u. An
// administrator sees everything; a reseller sees the accounts it owns; a
// plain account sees its own rows -- its owner_id is the reseller above it,
// never itself, so the reseller clause would hide everything.
func scopeOwnerAny(query string, args []any, owner *User) (string, []any) {
	if owner == nil || owner.IsAdmin() {
		return query, args
	}

	if owner.Role == RoleReseller {
		query += ` AND u.owner_id = ?`
		args = append(args, owner.ID)

		return query, args
	}

	query += ` AND d.user_id = ?`
	args = append(args, owner.ID)

	return query, args
}

// SetDomainSSLByName updates a certificate by domain name, which is what the
// agent reports back.
func (s *Store) SetDomainSSLByName(ctx context.Context, name, status, source string, expires *time.Time) error {
	_, err := s.db.ExecContext(ctx,
		"UPDATE `domains` SET `ssl_status` = NULLIF(?, ''), `ssl_source` = NULLIF(?, ''), "+
			"`ssl_expires_at` = ?, `updated_at` = ? WHERE `name` = ?",
		status, source, expires, Now(), name)

	return wrap("update the certificate", err)
}
