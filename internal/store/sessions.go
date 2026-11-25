package store

import (
	"context"
	"database/sql"
	"time"
)

// Session is one signed-in browser. The token lives only in the cookie; the
// row keeps its hash.
type Session struct {
	ID         string
	UserID     int64
	TokenHash  string
	CSRFToken  string
	IP         string
	UserAgent  string
	CreatedAt  time.Time
	LastSeenAt time.Time
	ExpiresAt  time.Time
}

// CreateSession stores a new session, ending the oldest ones of the same
// account so one account cannot pile up forever.
func (s *Store) CreateSession(ctx context.Context, session Session) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return wrap("create the session", err)
	}

	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO panel_sessions (id, user_id, token_hash, csrf_token, ip_address, user_agent, created_at, last_seen_at, expires_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		session.ID, session.UserID, session.TokenHash, session.CSRFToken, session.IP, session.UserAgent,
		session.CreatedAt, session.CreatedAt, session.ExpiresAt); err != nil {
		return wrap("create the session", err)
	}

	// Keep the ten newest sessions per account; the rest are old browsers.
	if _, err := tx.ExecContext(ctx, `
		DELETE FROM panel_sessions
		WHERE user_id = ? AND id NOT IN (
			SELECT id FROM (
				SELECT id FROM panel_sessions WHERE user_id = ? ORDER BY last_seen_at DESC LIMIT 10
			) AS keep
		)`, session.UserID, session.UserID); err != nil {
		return wrap("trim old sessions", err)
	}

	return wrap("create the session", tx.Commit())
}

// SessionByToken reads a session that has not expired.
func (s *Store) SessionByToken(ctx context.Context, tokenHash string, now time.Time) (Session, error) {
	var session Session

	err := s.db.QueryRowContext(ctx, `
		SELECT id, user_id, token_hash, csrf_token, COALESCE(ip_address, ''), COALESCE(user_agent, ''),
			created_at, last_seen_at, expires_at
		FROM panel_sessions
		WHERE token_hash = ? AND expires_at > ?`, tokenHash, now).
		Scan(&session.ID, &session.UserID, &session.TokenHash, &session.CSRFToken,
			&session.IP, &session.UserAgent, &session.CreatedAt, &session.LastSeenAt, &session.ExpiresAt)
	if err != nil {
		return Session{}, wrap("read the session", translate(err))
	}

	return session, nil
}

// TouchSession extends a session the panel is actively using. It is called at
// most once a minute per session, so a page that loads ten requests does not
// write ten times.
func (s *Store) TouchSession(ctx context.Context, id string, seenAt, expiresAt time.Time) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE panel_sessions SET last_seen_at = ?, expires_at = ? WHERE id = ?`,
		seenAt, expiresAt, id)

	return wrap("extend the session", err)
}

// DeleteSession signs one browser out.
func (s *Store) DeleteSession(ctx context.Context, tokenHash string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM panel_sessions WHERE token_hash = ?`, tokenHash)

	return wrap("end the session", err)
}

// DeleteExpiredSessions clears rows nobody can use any more.
func (s *Store) DeleteExpiredSessions(ctx context.Context, now time.Time) (int64, error) {
	result, err := s.db.ExecContext(ctx, `DELETE FROM panel_sessions WHERE expires_at <= ?`, now)
	if err != nil {
		return 0, wrap("clear expired sessions", err)
	}

	return result.RowsAffected()
}

// SessionInfo is what the account page lists under "signed-in browsers".
// SessionInfo is one row of the sheet an operator uses to see where they are
// signed in. It carries no token and no hash: there is nothing here to replay.
type SessionInfo struct {
	ID        string    `json:"id"`
	IP        string    `json:"ip"`
	UserAgent string    `json:"userAgent"`
	LastSeen  time.Time `json:"lastSeenAt"`
	CreatedAt time.Time `json:"createdAt"`
	Current   bool      `json:"current"`
}

// Sessions lists an account's live sessions, newest first.
func (s *Store) Sessions(ctx context.Context, userID int64, currentID string, now time.Time) ([]SessionInfo, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, COALESCE(ip_address, ''), COALESCE(user_agent, ''), last_seen_at, created_at
		FROM panel_sessions WHERE user_id = ? AND expires_at > ?
		ORDER BY last_seen_at DESC`, userID, now)
	if err != nil {
		return nil, wrap("list the sessions", err)
	}

	defer rows.Close()

	sessions := []SessionInfo{}

	for rows.Next() {
		var info SessionInfo
		var lastSeen, created sql.NullTime

		if err := rows.Scan(&info.ID, &info.IP, &info.UserAgent, &lastSeen, &created); err != nil {
			return nil, wrap("list the sessions", err)
		}

		if lastSeen.Valid {
			info.LastSeen = lastSeen.Time
		}

		if created.Valid {
			info.CreatedAt = created.Time
		}

		info.Current = info.ID == currentID

		sessions = append(sessions, info)
	}

	return sessions, wrap("list the sessions", rows.Err())
}

// DeleteSessionByID ends one of an account's sessions, scoped by user so a
// stolen session id is not enough to sign somebody else out.
func (s *Store) DeleteSessionByID(ctx context.Context, userID int64, id string) error {
	result, err := s.db.ExecContext(ctx, `DELETE FROM panel_sessions WHERE id = ? AND user_id = ?`, id, userID)
	if err != nil {
		return wrap("end the session", err)
	}

	removed, err := result.RowsAffected()
	if err != nil {
		return wrap("end the session", err)
	}

	if removed == 0 {
		return ErrNotFound
	}

	return nil
}
