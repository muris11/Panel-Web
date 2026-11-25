package store

import (
	"context"
	"database/sql"
	"encoding/json"
)

// Audit appends one line to the audit log.
//
// The log is written whether or not the action succeeded, and it is never
// edited afterwards: it is the record of who asked for what.
func (s *Store) Audit(ctx context.Context, actorID *int64, action string, subjectType string, subjectID string, ip, userAgent string, changes map[string]any) error {
	var encoded any

	if len(changes) > 0 {
		body, err := json.Marshal(changes)
		if err != nil {
			return wrap("record the audit line", err)
		}

		encoded = string(body)
	}

	_, err := s.db.ExecContext(ctx, `
		INSERT INTO audit_logs (actor_id, action, subject_type, subject_id, ip_address, user_agent, changes, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		NullInt64(actorID), action, nullString(subjectType), nullString(subjectID),
		nullString(ip), nullString(userAgent), encoded, Now())

	return wrap("record the audit line", err)
}

func nullString(value string) any {
	if value == "" {
		return nil
	}

	return value
}

// AuditEntries lists the audit log, newest first.
func (s *Store) AuditEntries(ctx context.Context, limit, offset int) ([]AuditEntry, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT a.id, a.actor_id, a.action, COALESCE(a.subject_id, ''),
			COALESCE(a.ip_address, ''), a.created_at, COALESCE(u.username, 'system')
		FROM audit_logs a
		LEFT JOIN users u ON u.id = a.actor_id
		ORDER BY a.created_at DESC, a.id DESC
		LIMIT ? OFFSET ?`, limit, offset)
	if err != nil {
		return nil, wrap("list the audit log", err)
	}

	defer rows.Close()

	entries := []AuditEntry{}

	for rows.Next() {
		var (
			entry     AuditEntry
			actorID   sql.NullInt64
			created   sql.NullTime
			actorName string
		)

		if err := rows.Scan(&entry.ID, &actorID, &entry.Action, &entry.SubjectID,
			&entry.IP, &created, &actorName); err != nil {
			return nil, wrap("list the audit log", err)
		}

		if actorID.Valid {
			entry.ActorID = &actorID.Int64
		}

		if created.Valid {
			entry.CreatedAt = created.Time
		}

		entry.ActorName = actorName

		entries = append(entries, entry)
	}

	return entries, wrap("list the audit log", rows.Err())
}
