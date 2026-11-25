package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

// This file holds what the collectors write: the same tables and columns the
// Laravel commands filled, so both panels can read one database.
//
// Identifiers are left unquoted where MariaDB allows it; only `key` in
// `settings` is reserved, and Go raw strings cannot hold a backtick, so that
// one statement uses a quoted string.

const bandwidthCursorKey = "INSERT INTO settings (`key`, `value`, created_at, updated_at) " +
	"VALUES ('bandwidth.cursor', ?, NOW(), NOW()) " +
	"ON DUPLICATE KEY UPDATE `value` = VALUES(`value`), updated_at = NOW()"

// BandwidthCursor returns where the last bandwidth count stopped, or nil when
// no count has run yet.
func (s *Store) BandwidthCursor(ctx context.Context) (json.RawMessage, error) {
	var raw []byte

	err := s.db.QueryRowContext(ctx, "SELECT `value` FROM settings WHERE `key` = 'bandwidth.cursor'").Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}

	if err != nil {
		return nil, err
	}

	return json.RawMessage(raw), nil
}

// AccountIDsByName maps a Unix username to the hosting account's id. Traffic of
// a name the panel does not know is left out rather than guessed at.
func (s *Store) AccountIDsByName(ctx context.Context) (map[string]int64, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT id, username FROM users")
	if err != nil {
		return nil, err
	}

	defer func() { _ = rows.Close() }()

	ids := map[string]int64{}

	for rows.Next() {
		var id int64
		var username string

		if err := rows.Scan(&id, &username); err != nil {
			return nil, err
		}

		ids[username] = id
	}

	return ids, rows.Err()
}

// DomainOwner is a website's row id and the hosting account that owns it.
type DomainOwner struct {
	ID     int64
	UserID int64
}

// DomainOwners maps a website name to its owner, for charging traffic.
func (s *Store) DomainOwners(ctx context.Context) (map[string]DomainOwner, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT id, user_id, name FROM domains")
	if err != nil {
		return nil, err
	}

	defer func() { _ = rows.Close() }()

	owners := map[string]DomainOwner{}

	for rows.Next() {
		var owner DomainOwner
		var name string

		if err := rows.Scan(&owner.ID, &owner.UserID, &name); err != nil {
			return nil, err
		}

		owners[name] = owner
	}

	return owners, rows.Err()
}

// FTPOwners maps an FTP login to the hosting account it belongs to.
func (s *Store) FTPOwners(ctx context.Context) (map[string]int64, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT user_id, login FROM ftp_accounts")
	if err != nil {
		return nil, err
	}

	defer func() { _ = rows.Close() }()

	owners := map[string]int64{}

	for rows.Next() {
		var userID int64
		var login string

		if err := rows.Scan(&userID, &login); err != nil {
			return nil, err
		}

		owners[login] = userID
	}

	return owners, rows.Err()
}

// Accounting is one account's traffic for one day, from the three logs.
type Accounting struct {
	UserID int64
	Date   string
	Web    uint64
	Mail   uint64
	FTP    uint64
}

// RecordBandwidth writes a whole count and its cursor in one transaction. Bytes
// and cursor move together: a crash leaves the cursor where it was, so the next
// count reads the same log again instead of skipping it.
func (s *Store) RecordBandwidth(ctx context.Context, accounts []Accounting, domains map[int64]map[string]uint64, cursor json.RawMessage) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}

	defer func() { _ = tx.Rollback() }()

	for _, entry := range accounts {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO bandwidth_usages (user_id, `+"`date`"+`, web_bytes, mail_bytes, ftp_bytes)
			VALUES (?, ?, ?, ?, ?)
			ON DUPLICATE KEY UPDATE
				web_bytes = web_bytes + VALUES(web_bytes),
				mail_bytes = mail_bytes + VALUES(mail_bytes),
				ftp_bytes = ftp_bytes + VALUES(ftp_bytes)`,
			entry.UserID, entry.Date, entry.Web, entry.Mail, entry.FTP); err != nil {
			return err
		}
	}

	for domainID, days := range domains {
		for date, bytes := range days {
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO domain_bandwidths (domain_id, `+"`date`"+`, bytes)
				VALUES (?, ?, ?)
				ON DUPLICATE KEY UPDATE bytes = bytes + VALUES(bytes)`,
				domainID, date, bytes); err != nil {
				return err
			}
		}
	}

	if cursor != nil {
		if _, err := tx.ExecContext(ctx, bandwidthCursorKey, []byte(cursor)); err != nil {
			return err
		}
	}

	return tx.Commit()
}

// ResourceCounter is the last CPU reading of an account's cgroup.
type ResourceCounter struct {
	CgroupID uint64
	Usec     uint64
}

// ResourceCounters returns the stored CPU readings, by user id.
func (s *Store) ResourceCounters(ctx context.Context) (map[int64]ResourceCounter, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT user_id, cgroup_id, cpu_usec FROM resource_counters")
	if err != nil {
		return nil, err
	}

	defer func() { _ = rows.Close() }()

	counters := map[int64]ResourceCounter{}

	for rows.Next() {
		var userID int64
		var counter ResourceCounter

		if err := rows.Scan(&userID, &counter.CgroupID, &counter.Usec); err != nil {
			return nil, err
		}

		counters[userID] = counter
	}

	return counters, rows.Err()
}

// AccountUsage is one account's CPU and peak memory for a day.
type AccountUsage struct {
	UserID     int64
	CPUms      uint64
	MemoryPeak int64
}

// RecordResources writes the day's CPU time and peak memory and the counters
// they were derived from.
func (s *Store) RecordResources(ctx context.Context, date string, usage []AccountUsage, counters map[int64]ResourceCounter) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}

	defer func() { _ = tx.Rollback() }()

	for _, entry := range usage {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO resource_usages (user_id, `+"`date`"+`, cpu_ms, memory_peak_mb)
			VALUES (?, ?, ?, ?)
			ON DUPLICATE KEY UPDATE
				cpu_ms = cpu_ms + VALUES(cpu_ms),
				memory_peak_mb = GREATEST(memory_peak_mb, VALUES(memory_peak_mb))`,
			entry.UserID, date, entry.CPUms, entry.MemoryPeak); err != nil {
			return err
		}
	}

	for userID, counter := range counters {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO resource_counters (user_id, cgroup_id, cpu_usec)
			VALUES (?, ?, ?)
			ON DUPLICATE KEY UPDATE cgroup_id = VALUES(cgroup_id), cpu_usec = VALUES(cpu_usec)`,
			userID, counter.CgroupID, counter.Usec); err != nil {
			return err
		}
	}

	return tx.Commit()
}

// DomainCertificate is a website whose certificate expiry is worth reading.
type DomainCertificate struct {
	ID   int64
	Name string
}

// DomainsForExpiry lists the websites that should have a certificate.
func (s *Store) DomainsForExpiry(ctx context.Context) ([]DomainCertificate, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, name FROM domains
		WHERE status = 'active' AND type <> 'subdomain'
		ORDER BY id
		LIMIT 500`)
	if err != nil {
		return nil, err
	}

	defer func() { _ = rows.Close() }()

	var domains []DomainCertificate

	for rows.Next() {
		var domain DomainCertificate

		if err := rows.Scan(&domain.ID, &domain.Name); err != nil {
			return nil, err
		}

		domains = append(domains, domain)
	}

	return domains, rows.Err()
}

// SetDomainExpiry records when a website's certificate expires, or clears it
// when the agent no longer reports one.
func (s *Store) SetDomainExpiry(ctx context.Context, name string, expiresAt *time.Time) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE domains SET ssl_expires_at = ?, updated_at = NOW() WHERE name = ?`,
		expiresAt, name)

	return err
}

// RecordCollectorRun keeps the last outcome per job. A collector that fails
// quietly is a collector nobody notices is broken.
func (s *Store) RecordCollectorRun(ctx context.Context, job string, started time.Time, ok bool, detail string) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO collector_runs (job, started_at, finished_at, ok, detail)
		VALUES (?, ?, NOW(), ?, ?)
		ON DUPLICATE KEY UPDATE
			started_at = VALUES(started_at),
			finished_at = NOW(),
			ok = VALUES(ok),
			detail = VALUES(detail)`,
		job, started.UTC(), ok, truncate(detail, 255))

	return err
}

func truncate(value string, limit int) string {
	if len(value) <= limit {
		return value
	}

	return value[:limit]
}

// BandwidthTotal is traffic for one calendar month, split by where it moved.
type BandwidthTotal struct {
	Web   uint64
	Mail  uint64
	FTP   uint64
	Bytes uint64
}

// MonthBandwidth adds up the traffic of one month. A nil userIDs means every
// account, which is what an administrator's server page asks for; a reseller
// passes the accounts they own, so the number is never somebody else's.
func (s *Store) MonthBandwidth(ctx context.Context, month time.Time, userIDs []int64) (BandwidthTotal, error) {
	first := time.Date(month.Year(), month.Month(), 1, 0, 0, 0, 0, time.UTC)
	last := first.AddDate(0, 1, -1)

	query := `
		SELECT COALESCE(SUM(web_bytes), 0), COALESCE(SUM(mail_bytes), 0), COALESCE(SUM(ftp_bytes), 0)
		FROM bandwidth_usages
		WHERE ` + "`date`" + ` BETWEEN ? AND ?`

	args := []any{first.Format("2006-01-02"), last.Format("2006-01-02")}

	if userIDs != nil {
		if len(userIDs) == 0 {
			return BandwidthTotal{}, nil
		}

		placeholders := make([]string, len(userIDs))
		for i, id := range userIDs {
			placeholders[i] = "?"
			args = append(args, id)
		}

		query += " AND user_id IN (" + strings.Join(placeholders, ", ") + ")"
	}

	var total BandwidthTotal

	if err := s.db.QueryRowContext(ctx, query, args...).Scan(&total.Web, &total.Mail, &total.FTP); err != nil {
		return BandwidthTotal{}, err
	}

	total.Bytes = total.Web + total.Mail + total.FTP

	return total, nil
}

// CollectorRun is the last outcome of one collector job.
type CollectorRun struct {
	Job        string
	FinishedAt time.Time
	OK         bool
	Detail     string
}

// CollectorRuns returns the last run of every job, for the server page to show
// whether the numbers are current.
func (s *Store) CollectorRuns(ctx context.Context) ([]CollectorRun, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT job, finished_at, ok, COALESCE(detail, '') FROM collector_runs ORDER BY job`)
	if err != nil {
		return nil, err
	}

	defer func() { _ = rows.Close() }()

	var runs []CollectorRun

	for rows.Next() {
		var run CollectorRun
		var finished sql.NullTime

		if err := rows.Scan(&run.Job, &finished, &run.OK, &run.Detail); err != nil {
			return nil, err
		}

		if finished.Valid {
			run.FinishedAt = finished.Time
		}

		runs = append(runs, run)
	}

	return runs, rows.Err()
}

// AccountIDsOwnedBy lists the hosting accounts a person may see: their own and
// the ones they own as a reseller.
func (s *Store) AccountIDsOwnedBy(ctx context.Context, userID int64) ([]int64, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id FROM users WHERE id = ? OR owner_id = ?`, userID, userID)
	if err != nil {
		return nil, err
	}

	defer func() { _ = rows.Close() }()

	var ids []int64

	for rows.Next() {
		var id int64

		if err := rows.Scan(&id); err != nil {
			return nil, err
		}

		ids = append(ids, id)
	}

	return ids, rows.Err()
}
