package store

import (
	"context"
	"database/sql"
	"strings"
)

// Zone is a DNS zone: one per registered domain or pointer.
type Zone struct {
	ID         int64    `json:"id"`
	UserID     int64    `json:"userId"`
	DomainID   *int64   `json:"domainId"`
	Name       string   `json:"name"`
	DomainName string   `json:"domainName"`
	Username   string   `json:"username"`
	Records    []Record `json:"records,omitempty"`
}

// Record is one line of a zone.
type Record struct {
	ID       int64  `json:"id"`
	ZoneID   int64  `json:"zoneId"`
	Name     string `json:"name"`
	Type     string `json:"type"`
	Content  string `json:"content"`
	TTL      int64  `json:"ttl"`
	Priority *int64 `json:"priority"`
}

// ZoneByDomain loads the zone that belongs to a domain, if one exists.
func (s *Store) ZoneByDomain(ctx context.Context, domainID int64) (Zone, error) {
	var zone Zone

	err := s.db.QueryRowContext(ctx, `
		SELECT z.id, z.user_id, z.domain_id, z.name
		FROM dns_zones z
		WHERE z.domain_id = ?`, domainID).
		Scan(&zone.ID, &zone.UserID, &zone.DomainID, &zone.Name)
	if err != nil {
		return Zone{}, wrap("read the DNS zone", translate(err))
	}

	zone.Records, err = s.RecordsByZone(ctx, zone.ID)
	if err != nil {
		return Zone{}, err
	}

	return zone, nil
}

// ZonesByAccount lists the zones of one hosting account, so an operator can
// see what the panel has published for it.
func (s *Store) ZonesByAccount(ctx context.Context, owner *User, username string) ([]Zone, error) {
	query := `
		SELECT z.id, z.user_id, z.domain_id, z.name, d.name
		FROM dns_zones z
		JOIN users u ON u.id = z.user_id
		JOIN domains d ON d.id = z.domain_id
		WHERE u.username = ?`
	args := []any{username}

	query, args = scopeOwner(query, args, owner)
	query += ` ORDER BY z.name`

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, wrap("list the DNS zones", err)
	}

	defer rows.Close()

	zones := []Zone{}

	for rows.Next() {
		var (
			zone     Zone
			domainID sql.NullInt64
			domain   sql.NullString
		)

		if err := rows.Scan(&zone.ID, &zone.UserID, &domainID, &zone.Name, &domain); err != nil {
			return nil, wrap("list the DNS zones", err)
		}

		if domainID.Valid {
			zone.DomainID = &domainID.Int64
		}

		zone.DomainName = domain.String
		zones = append(zones, zone)
	}

	return zones, wrap("list the DNS zones", rows.Err())
}

// CreateZone gives a domain an empty zone. The default records are written by
// the caller so the template is in one place, not in SQL.
func (s *Store) CreateZone(ctx context.Context, userID, domainID int64, name string) (Zone, error) {
	now := Now()

	result, err := s.db.ExecContext(ctx, `
		INSERT INTO dns_zones (user_id, domain_id, name, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?)`, userID, domainID, name, now, now)
	if err != nil {
		return Zone{}, wrap("create the DNS zone", err)
	}

	id, err := result.LastInsertId()
	if err != nil {
		return Zone{}, wrap("create the DNS zone", err)
	}

	return Zone{ID: id, UserID: userID, DomainID: &domainID, Name: name}, nil
}

// DeleteZoneForDomain removes the zone of a domain, if it has one.
func (s *Store) DeleteZoneForDomain(ctx context.Context, domainID int64) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM dns_zones WHERE domain_id = ?`, domainID)

	return wrap("delete the DNS zone", err)
}

// RecordsByZone lists a zone's records in the order an operator expects to
// read them: apex first, then by type and name.
func (s *Store) RecordsByZone(ctx context.Context, zoneID int64) ([]Record, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, dns_zone_id, name, type, content, ttl, priority
		FROM dns_records
		WHERE dns_zone_id = ?
		ORDER BY (name = '@') DESC, name, FIELD(type, 'SOA', 'NS', 'A', 'AAAA', 'CNAME', 'MX', 'TXT', 'SRV', 'CAA'), id`, zoneID)
	if err != nil {
		return nil, wrap("list the DNS records", err)
	}

	defer rows.Close()

	records := []Record{}

	for rows.Next() {
		var (
			record   Record
			priority sql.NullInt64
		)

		if err := rows.Scan(&record.ID, &record.ZoneID, &record.Name, &record.Type,
			&record.Content, &record.TTL, &priority); err != nil {
			return nil, wrap("list the DNS records", err)
		}

		if priority.Valid {
			record.Priority = &priority.Int64
		}

		records = append(records, record)
	}

	return records, wrap("list the DNS records", rows.Err())
}

// RecordByID loads one record together with the zone and account it belongs
// to, which is what a handler needs to check permission.
func (s *Store) RecordByID(ctx context.Context, id int64) (Record, Zone, error) {
	var (
		record   Record
		zone     Zone
		priority sql.NullInt64
		domainID sql.NullInt64
	)

	err := s.db.QueryRowContext(ctx, `
		SELECT r.id, r.dns_zone_id, r.name, r.type, r.content, r.ttl, r.priority,
			z.id, z.user_id, z.domain_id, z.name, u.username
		FROM dns_records r
		JOIN dns_zones z ON z.id = r.dns_zone_id
		JOIN users u ON u.id = z.user_id
		WHERE r.id = ?`, id).
		Scan(&record.ID, &record.ZoneID, &record.Name, &record.Type, &record.Content,
			&record.TTL, &priority, &zone.ID, &zone.UserID, &domainID, &zone.Name, &zone.Username)
	if err != nil {
		return Record{}, Zone{}, wrap("read the DNS record", translate(err))
	}

	if priority.Valid {
		record.Priority = &priority.Int64
	}

	if domainID.Valid {
		zone.DomainID = &domainID.Int64
	}

	return record, zone, nil
}

// AddRecord appends a record to a zone.
func (s *Store) AddRecord(ctx context.Context, zoneID int64, name, recordType, content string, ttl int64, priority *int64) (Record, error) {
	now := Now()

	result, err := s.db.ExecContext(ctx, `
		INSERT INTO dns_records (dns_zone_id, name, type, content, ttl, priority, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		zoneID, name, recordType, content, ttl, priority, now, now)
	if err != nil {
		return Record{}, wrap("add the DNS record", err)
	}

	id, err := result.LastInsertId()
	if err != nil {
		return Record{}, wrap("add the DNS record", err)
	}

	return Record{ID: id, ZoneID: zoneID, Name: name, Type: recordType, Content: content, TTL: ttl, Priority: priority}, nil
}

// UpdateRecord changes a record in place.
func (s *Store) UpdateRecord(ctx context.Context, id int64, name, recordType, content string, ttl int64, priority *int64) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE dns_records
		SET name = ?, type = ?, content = ?, ttl = ?, priority = ?, updated_at = ?
		WHERE id = ?`, name, recordType, content, ttl, priority, Now(), id)

	return wrap("update the DNS record", err)
}

// DeleteRecord removes one record.
func (s *Store) DeleteRecord(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM dns_records WHERE id = ?`, id)

	return wrap("delete the DNS record", err)
}

// scopeOwner narrows a query to the accounts a reseller owns. An
// administrator passes nil and sees everything.
func scopeOwner(query string, args []any, owner *User) (string, []any) {
	if owner == nil || owner.IsAdmin() {
		return query, args
	}

	query += ` AND (u.owner_id = ? OR u.id = ?)`
	args = append(args, owner.ID, owner.ID)

	return query, args
}

// trimmedName turns a record name the operator typed into what the zone
// stores: relative, without trailing dots, apex as "@".
func TrimRecordName(name, zone string) string {
	name = strings.TrimSpace(strings.ToLower(name))
	name = strings.TrimSuffix(name, ".")
	zone = strings.TrimSuffix(strings.ToLower(zone), ".")

	if name == "" || name == zone || name == "@" {
		return "@"
	}

	if strings.HasSuffix(name, "."+zone) {
		return strings.TrimSuffix(name, "."+zone)
	}

	return name
}

// MarkZoneSynced records that the agent wrote the zone file.
func (s *Store) MarkZoneSynced(ctx context.Context, zone string) error {
	_, err := s.db.ExecContext(ctx,
		"UPDATE `dns_zones` SET `status` = 'synced', `last_synced_at` = ?, `updated_at` = ? "+
			"WHERE `zone` = ?", Now(), Now(), zone)

	return wrap("mark the zone synced", err)
}
