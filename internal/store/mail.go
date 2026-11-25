package store

import (
	"context"
	"database/sql"
	"strings"
)

// MailDomain is a hosted domain's mail service, which hangs off a website.
type MailDomain struct {
	ID            int64  `json:"id"`
	DomainID      int64  `json:"domainId"`
	DomainName    string `json:"domainName"`
	Username      string `json:"username"`
	DKIMSelector  string `json:"dkimSelector"`
	DKIMPublicKey string `json:"dkimPublicKey"`
	CatchAll      string `json:"catchAll"`
	Status        string `json:"status"`
	SSLStatus     string `json:"sslStatus"`
	Mailboxes     int    `json:"mailboxes"`
	Forwarders    int    `json:"forwarders"`
}

// Mailbox is one address on a mail domain.
type Mailbox struct {
	ID             int64  `json:"id"`
	MailDomainID   int64  `json:"mailDomainId"`
	LocalPart      string `json:"localPart"`
	PasswordHash   string `json:"-"`
	QuotaMB        *int64 `json:"quotaMb"`
	DailySendLimit *int64 `json:"dailySendLimit"`
	Status         string `json:"status"`
}

// Forwarder sends one address somewhere else instead of storing it.
type Forwarder struct {
	ID           int64    `json:"id"`
	MailDomainID int64    `json:"mailDomainId"`
	LocalPart    string   `json:"localPart"`
	Destinations []string `json:"destinations"`
}

// MailDomains lists the mail domains an operator may see.
func (s *Store) MailDomains(ctx context.Context, owner *User) ([]MailDomain, error) {
	query := `
		SELECT m.id, m.domain_id, d.name, u.username, m.dkim_selector,
			COALESCE(m.dkim_public_key, ''), COALESCE(m.catchall_target, ''),
			m.status, COALESCE(m.ssl_status, ''),
			(SELECT COUNT(*) FROM mailboxes b WHERE b.mail_domain_id = m.id),
			(SELECT COUNT(*) FROM mail_forwarders f WHERE f.mail_domain_id = m.id)
		FROM mail_domains m
		JOIN domains d ON d.id = m.domain_id
		JOIN users u ON u.id = d.user_id
		WHERE 1 = 1`

	query, args := scopeOwner(query, nil, owner)
	query += ` ORDER BY d.name`

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, wrap("list the mail domains", err)
	}

	defer rows.Close()

	domains := []MailDomain{}

	for rows.Next() {
		var domain MailDomain

		if err := rows.Scan(&domain.ID, &domain.DomainID, &domain.DomainName, &domain.Username,
			&domain.DKIMSelector, &domain.DKIMPublicKey, &domain.CatchAll, &domain.Status,
			&domain.SSLStatus, &domain.Mailboxes, &domain.Forwarders); err != nil {
			return nil, wrap("list the mail domains", err)
		}

		domains = append(domains, domain)
	}

	return domains, wrap("list the mail domains", rows.Err())
}

// MailDomainByID loads one mail domain with the address of its owner.
func (s *Store) MailDomainByID(ctx context.Context, id int64) (MailDomain, error) {
	var domain MailDomain

	err := s.db.QueryRowContext(ctx, `
		SELECT m.id, m.domain_id, d.name, u.username, m.dkim_selector,
			COALESCE(m.dkim_public_key, ''), COALESCE(m.catchall_target, ''),
			m.status, COALESCE(m.ssl_status, '')
		FROM mail_domains m
		JOIN domains d ON d.id = m.domain_id
		JOIN users u ON u.id = d.user_id
		WHERE m.id = ?`, id).
		Scan(&domain.ID, &domain.DomainID, &domain.DomainName, &domain.Username,
			&domain.DKIMSelector, &domain.DKIMPublicKey, &domain.CatchAll, &domain.Status, &domain.SSLStatus)
	if err != nil {
		return MailDomain{}, wrap("read the mail domain", translate(err))
	}

	return domain, nil
}

// CreateMailDomain starts mail for a domain that already exists.
func (s *Store) CreateMailDomain(ctx context.Context, domainID int64, selector string) (MailDomain, error) {
	now := Now()

	result, err := s.db.ExecContext(ctx, `
		INSERT INTO mail_domains (domain_id, dkim_selector, status, mode, created_at, updated_at)
		VALUES (?, ?, 'pending', 'local', ?, ?)`, domainID, selector, now, now)
	if err != nil {
		return MailDomain{}, wrap("add the mail domain", err)
	}

	id, err := result.LastInsertId()
	if err != nil {
		return MailDomain{}, wrap("add the mail domain", err)
	}

	return s.MailDomainByID(ctx, id)
}

// SetMailDomainActive records what the agent answered when it wrote the
// domain's configuration.
func (s *Store) SetMailDomainActive(ctx context.Context, id int64, status, publicKey string) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE mail_domains
		SET status = ?, dkim_public_key = NULLIF(?, ''), updated_at = ?
		WHERE id = ?`, status, publicKey, Now(), id)

	return wrap("update the mail domain", err)
}

// DeleteMailDomain removes a domain and, by cascade, its boxes and forwarders.
func (s *Store) DeleteMailDomain(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM mail_domains WHERE id = ?`, id)

	return wrap("delete the mail domain", err)
}

// SetCatchAll stores the address that receives mail for unknown local parts,
// or clears it with an empty string.
func (s *Store) SetCatchAll(ctx context.Context, id int64, target string) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE mail_domains SET catchall_target = NULLIF(?, ''), updated_at = ? WHERE id = ?`,
		target, Now(), id)

	return wrap("set the catch-all address", err)
}

// Mailboxes lists a mail domain's addresses.
func (s *Store) Mailboxes(ctx context.Context, mailDomainID int64) ([]Mailbox, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, mail_domain_id, local_part, password_hash, quota_mb, daily_send_limit, status
		FROM mailboxes
		WHERE mail_domain_id = ?
		ORDER BY local_part`, mailDomainID)
	if err != nil {
		return nil, wrap("list the mailboxes", err)
	}

	defer rows.Close()

	boxes := []Mailbox{}

	for rows.Next() {
		var (
			box    Mailbox
			quota  sql.NullInt64
			limit  sql.NullInt64
			status sql.NullString
		)

		if err := rows.Scan(&box.ID, &box.MailDomainID, &box.LocalPart, &box.PasswordHash,
			&quota, &limit, &status); err != nil {
			return nil, wrap("list the mailboxes", err)
		}

		if quota.Valid {
			box.QuotaMB = &quota.Int64
		}

		if limit.Valid {
			box.DailySendLimit = &limit.Int64
		}

		box.Status = status.String
		boxes = append(boxes, box)
	}

	return boxes, wrap("list the mailboxes", rows.Err())
}

// MailboxByID loads one mailbox and the mail domain it belongs to.
func (s *Store) MailboxByID(ctx context.Context, id int64) (Mailbox, MailDomain, error) {
	var (
		box    Mailbox
		domain MailDomain
		quota  sql.NullInt64
		limit  sql.NullInt64
	)

	err := s.db.QueryRowContext(ctx, `
		SELECT b.id, b.mail_domain_id, b.local_part, b.password_hash, b.quota_mb,
			b.daily_send_limit, COALESCE(b.status, 'active'),
			m.id, m.domain_id, d.name, u.username, m.dkim_selector,
			COALESCE(m.dkim_public_key, ''), COALESCE(m.catchall_target, ''),
			m.status, COALESCE(m.ssl_status, '')
		FROM mailboxes b
		JOIN mail_domains m ON m.id = b.mail_domain_id
		JOIN domains d ON d.id = m.domain_id
		JOIN users u ON u.id = d.user_id
		WHERE b.id = ?`, id).
		Scan(&box.ID, &box.MailDomainID, &box.LocalPart, &box.PasswordHash, &quota, &limit,
			&box.Status, &domain.ID, &domain.DomainID, &domain.DomainName, &domain.Username,
			&domain.DKIMSelector, &domain.DKIMPublicKey, &domain.CatchAll, &domain.Status, &domain.SSLStatus)
	if err != nil {
		return Mailbox{}, MailDomain{}, wrap("read the mailbox", translate(err))
	}

	if quota.Valid {
		box.QuotaMB = &quota.Int64
	}

	if limit.Valid {
		box.DailySendLimit = &limit.Int64
	}

	return box, domain, nil
}

// CreateMailbox inserts an address; the caller has already validated the
// local part and hashed the password.
func (s *Store) CreateMailbox(ctx context.Context, mailDomainID int64, localPart, hash string, quotaMB *int64) (Mailbox, error) {
	now := Now()

	result, err := s.db.ExecContext(ctx, `
		INSERT INTO mailboxes (mail_domain_id, local_part, password_hash, quota_mb, status, created_at, updated_at)
		VALUES (?, ?, ?, ?, 'active', ?, ?)`, mailDomainID, localPart, hash, quotaMB, now, now)
	if err != nil {
		return Mailbox{}, wrap("create the mailbox", err)
	}

	id, err := result.LastInsertId()
	if err != nil {
		return Mailbox{}, wrap("create the mailbox", err)
	}

	return Mailbox{ID: id, MailDomainID: mailDomainID, LocalPart: localPart, PasswordHash: hash, QuotaMB: quotaMB, Status: "active"}, nil
}

// UpdateMailbox changes the password, quota and status of an address. An
// empty hash keeps the current password.
func (s *Store) UpdateMailbox(ctx context.Context, id int64, hash string, quotaMB *int64, status string) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE mailboxes
		SET password_hash = COALESCE(NULLIF(?, ''), password_hash),
			quota_mb = ?, status = ?, updated_at = ?
		WHERE id = ?`, hash, quotaMB, status, Now(), id)

	return wrap("update the mailbox", err)
}

// DeleteMailbox removes an address.
func (s *Store) DeleteMailbox(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM mailboxes WHERE id = ?`, id)

	return wrap("delete the mailbox", err)
}

// Forwarders lists a mail domain's forwards, destinations split apart.
func (s *Store) Forwarders(ctx context.Context, mailDomainID int64) ([]Forwarder, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, mail_domain_id, local_part, destinations
		FROM mail_forwarders
		WHERE mail_domain_id = ?
		ORDER BY local_part`, mailDomainID)
	if err != nil {
		return nil, wrap("list the forwarders", err)
	}

	defer rows.Close()

	forwarders := []Forwarder{}

	for rows.Next() {
		var (
			forwarder    Forwarder
			destinations string
		)

		if err := rows.Scan(&forwarder.ID, &forwarder.MailDomainID, &forwarder.LocalPart, &destinations); err != nil {
			return nil, wrap("list the forwarders", err)
		}

		forwarder.Destinations = splitDestinations(destinations)
		forwarders = append(forwarders, forwarder)
	}

	return forwarders, wrap("list the forwarders", rows.Err())
}

// ForwarderByID loads one forwarder with the mail domain it belongs to.
func (s *Store) ForwarderByID(ctx context.Context, id int64) (Forwarder, MailDomain, error) {
	var (
		forwarder    Forwarder
		domain       MailDomain
		destinations string
	)

	err := s.db.QueryRowContext(ctx, `
		SELECT f.id, f.mail_domain_id, f.local_part, f.destinations,
			m.id, m.domain_id, d.name, u.username, m.status
		FROM mail_forwarders f
		JOIN mail_domains m ON m.id = f.mail_domain_id
		JOIN domains d ON d.id = m.domain_id
		JOIN users u ON u.id = d.user_id
		WHERE f.id = ?`, id).
		Scan(&forwarder.ID, &forwarder.MailDomainID, &forwarder.LocalPart, &destinations,
			&domain.ID, &domain.DomainID, &domain.DomainName, &domain.Username, &domain.Status)
	if err != nil {
		return Forwarder{}, MailDomain{}, wrap("read the forwarder", translate(err))
	}

	forwarder.Destinations = splitDestinations(destinations)

	return forwarder, domain, nil
}

// SaveForwarder inserts or updates a forward, keyed by address.
func (s *Store) SaveForwarder(ctx context.Context, mailDomainID int64, localPart string, destinations []string) error {
	now := Now()

	_, err := s.db.ExecContext(ctx, `
		INSERT INTO mail_forwarders (mail_domain_id, local_part, destinations, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?)
		ON DUPLICATE KEY UPDATE destinations = VALUES(destinations), updated_at = VALUES(updated_at)`,
		mailDomainID, localPart, joinDestinations(destinations), now, now)

	return wrap("save the forwarder", err)
}

// DeleteForwarder removes a forward.
func (s *Store) DeleteForwarder(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM mail_forwarders WHERE id = ?`, id)

	return wrap("delete the forwarder", err)
}

// splitDestinations reads the comma-separated column back into a list.
func splitDestinations(value string) []string {
	destinations := []string{}

	for _, part := range strings.Split(value, ",") {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			destinations = append(destinations, trimmed)
		}
	}

	return destinations
}

func joinDestinations(destinations []string) string {
	joined := ""

	for i, destination := range destinations {
		if i > 0 {
			joined += ","
		}

		joined += destination
	}

	return joined
}

// SetMailDomainActiveByName marks a mail domain once the agent has written its
// DKIM key.
func (s *Store) SetMailDomainActiveByName(ctx context.Context, name, status, publicKey string) error {
	// mail_domains has no name of its own: the name lives on the website it
	// belongs to, so the update joins to domains.
	_, err := s.db.ExecContext(ctx,
		"UPDATE `mail_domains` m JOIN `domains` d ON d.id = m.`domain_id` "+
			"SET m.`status` = ?, m.`dkim_public_key` = NULLIF(?, ''), m.`updated_at` = ? "+
			"WHERE d.`name` = ?",
		status, publicKey, Now(), name)

	return wrap("update the mail domain", err)
}
