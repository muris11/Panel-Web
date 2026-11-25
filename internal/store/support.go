package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// ErrTicketClosed is returned when a reply lands on a ticket that is done.
var ErrTicketClosed = errors.New("the ticket is closed")

// ------------------------------------------------------------------ limits

// ResellerAllowsSSH reports whether the reseller package a person holds
// includes shell access. A reseller without a package is uncapped, which is
// how App\Support\ResellerLimits reads the same rows.
func (s *Store) ResellerAllowsSSH(ctx context.Context, ownerID int64) (bool, error) {
	var ssh sql.NullBool

	err := s.db.QueryRowContext(ctx,
		"SELECT rp.`ssh` FROM `users` u JOIN `reseller_packages` rp ON rp.id = u.reseller_package_id WHERE u.id = ?",
		ownerID).Scan(&ssh)
	if errors.Is(err, sql.ErrNoRows) {
		return true, nil
	}

	if err != nil {
		return false, err
	}

	return !ssh.Valid || ssh.Bool, nil
}

// AllowsSSH answers the whole question App\Support\AccountLimits::allowsSsh
// answers: the owner's reseller package has to include SSH, and so does the
// account's own package. An account with no package is not restricted.
func (s *Store) AllowsSSH(ctx context.Context, userID int64) (bool, error) {
	var (
		ownerID   sql.NullInt64
		ownerRole sql.NullString
		pkgSSH    sql.NullBool
	)

	err := s.db.QueryRowContext(ctx,
		"SELECT u.owner_id, o.`role`, p.ssh "+
			"FROM `users` u "+
			"LEFT JOIN `users` o ON o.id = u.owner_id "+
			"LEFT JOIN `packages` p ON p.id = u.package_id "+
			"WHERE u.id = ?", userID).Scan(&ownerID, &ownerRole, &pkgSSH)
	if errors.Is(err, sql.ErrNoRows) {
		return false, ErrNotFound
	}

	if err != nil {
		return false, err
	}

	if ownerID.Valid && ownerRole.String == string(RoleReseller) {
		allowed, err := s.ResellerAllowsSSH(ctx, ownerID.Int64)
		if err != nil {
			return false, err
		}

		if !allowed {
			return false, nil
		}
	}

	return !pkgSSH.Valid || pkgSSH.Bool, nil
}

// ----------------------------------------------------------------- tickets

// Ticket is one conversation between an account and whoever answers it.
type Ticket struct {
	ID        int64      `json:"id"`
	UserID    int64      `json:"-"`
	Subject   string     `json:"subject"`
	Status    string     `json:"status"`
	Priority  string     `json:"priority"`
	Account   string     `json:"account"`
	Messages  int        `json:"messages"`
	Opened    time.Time  `json:"opened_at"`
	LastReply *time.Time `json:"last_message_at"`
}

// TicketMessage is one turn of a ticket.
type TicketMessage struct {
	ID      int64     `json:"id"`
	Author  string    `json:"author"`
	Staff   bool      `json:"staff"`
	Body    string    `json:"body"`
	Created time.Time `json:"created_at"`
}

// TicketFilter narrows a ticket list.
type TicketFilter struct {
	// Box is "mine" for the tickets the caller opened, or "inbox" for the ones
	// they answer. An account that answers nobody always gets its own.
	Box    string
	Status string
}

// The statuses and priorities the Laravel panel uses.
var (
	ticketStatuses   = []string{"open", "answered", "closed"}
	ticketPriorities = []string{"low", "normal", "high"}
)

// ValidTicketStatus reports whether status is one the panel knows.
func ValidTicketStatus(status string) bool { return contains(ticketStatuses, status) }

// ValidTicketPriority reports whether priority is one the panel knows.
func ValidTicketPriority(priority string) bool { return contains(ticketPriorities, priority) }

func contains(values []string, value string) bool {
	for _, candidate := range values {
		if candidate == value {
			return true
		}
	}

	return false
}

// Tickets lists what the viewer may read, newest activity first.
func (s *Store) Tickets(ctx context.Context, viewer *User, filter TicketFilter) ([]Ticket, error) {
	where := []string{}
	args := []any{}

	switch {
	case viewer.Role == RoleAdmin && filter.Box != "mine":
		// An administrator answers everybody.
	case viewer.Role == RoleReseller && filter.Box != "mine":
		where = append(where, "(t.`reseller_id` = ? OR t.`user_id` IN (SELECT id FROM `users` WHERE `owner_id` = ?))")
		args = append(args, viewer.ID, viewer.ID)
	default:
		where = append(where, "t.`user_id` = ?")
		args = append(args, viewer.ID)
	}

	if filter.Status != "" && ValidTicketStatus(filter.Status) {
		where = append(where, "t.`status` = ?")
		args = append(args, filter.Status)
	}

	query := "SELECT t.id, t.user_id, t.subject, t.`status`, t.priority, " +
		"COALESCE(u.username, ''), COALESCE(m.messages, 0), t.created_at, t.last_message_at " +
		"FROM `tickets` t " +
		"LEFT JOIN `users` u ON u.id = t.user_id " +
		"LEFT JOIN (SELECT ticket_id, COUNT(*) AS messages FROM `ticket_messages` GROUP BY ticket_id) m ON m.ticket_id = t.id"

	if len(where) > 0 {
		query += " WHERE " + strings.Join(where, " AND ")
	}

	query += " ORDER BY COALESCE(t.last_message_at, t.created_at) DESC, t.id DESC LIMIT 200"

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}

	defer func() { _ = rows.Close() }()

	tickets := []Ticket{}

	for rows.Next() {
		var (
			ticket Ticket
			opened sql.NullTime
			last   sql.NullTime
		)

		if err := rows.Scan(&ticket.ID, &ticket.UserID, &ticket.Subject, &ticket.Status,
			&ticket.Priority, &ticket.Account, &ticket.Messages, &opened, &last); err != nil {
			return nil, err
		}

		if opened.Valid {
			ticket.Opened = opened.Time.UTC()
		}

		if last.Valid {
			at := last.Time.UTC()
			ticket.LastReply = &at
		}

		tickets = append(tickets, ticket)
	}

	return tickets, rows.Err()
}

// TicketReader returns one ticket the viewer may read, or ErrNotFound.
func (s *Store) TicketReader(ctx context.Context, viewer *User, ticketID int64) (Ticket, error) {
	var (
		ticket Ticket
		opened sql.NullTime
		last   sql.NullTime
	)

	err := s.db.QueryRowContext(ctx,
		"SELECT t.id, t.user_id, t.subject, t.`status`, t.priority, "+
			"COALESCE(u.username, ''), "+
			"(SELECT COUNT(*) FROM `ticket_messages` m WHERE m.ticket_id = t.id), "+
			"t.created_at, t.last_message_at "+
			"FROM `tickets` t LEFT JOIN `users` u ON u.id = t.user_id WHERE t.id = ?", ticketID).
		Scan(&ticket.ID, &ticket.UserID, &ticket.Subject, &ticket.Status, &ticket.Priority,
			&ticket.Account, &ticket.Messages, &opened, &last)
	if errors.Is(err, sql.ErrNoRows) {
		return Ticket{}, ErrNotFound
	}

	if err != nil {
		return Ticket{}, err
	}

	if opened.Valid {
		ticket.Opened = opened.Time.UTC()
	}

	if last.Valid {
		at := last.Time.UTC()
		ticket.LastReply = &at
	}

	allowed, err := s.MayReadTicket(ctx, viewer, ticket)
	if err != nil {
		return Ticket{}, err
	}

	if !allowed {
		return Ticket{}, ErrNotFound
	}

	return ticket, nil
}

// MayReadTicket says whether the viewer may read a ticket: its own account,
// a reseller's accounts, or anybody when the viewer is an administrator.
func (s *Store) MayReadTicket(ctx context.Context, viewer *User, ticket Ticket) (bool, error) {
	if viewer.Role == RoleAdmin || ticket.UserID == viewer.ID {
		return true, nil
	}

	if viewer.Role != RoleReseller {
		return false, nil
	}

	var owned bool

	err := s.db.QueryRowContext(ctx,
		"SELECT EXISTS (SELECT 1 FROM `users` WHERE id = ? AND owner_id = ?)",
		ticket.UserID, viewer.ID).Scan(&owned)

	return owned, err
}

// TicketMessages reads the conversation in the order it happened, with the
// name of whoever wrote each turn.
func (s *Store) TicketMessages(ctx context.Context, ticketID int64) ([]TicketMessage, error) {
	rows, err := s.db.QueryContext(ctx,
		"SELECT m.id, COALESCE(u.username, ''), COALESCE(u.`role`, ''), m.body, m.created_at "+
			"FROM `ticket_messages` m LEFT JOIN `users` u ON u.id = m.author_id "+
			"WHERE m.ticket_id = ? ORDER BY m.id", ticketID)
	if err != nil {
		return nil, err
	}

	defer func() { _ = rows.Close() }()

	messages := []TicketMessage{}

	for rows.Next() {
		var (
			message TicketMessage
			role    string
			created sql.NullTime
		)

		if err := rows.Scan(&message.ID, &message.Author, &role, &message.Body, &created); err != nil {
			return nil, err
		}

		message.Staff = role == string(RoleAdmin) || role == string(RoleReseller)

		if created.Valid {
			message.Created = created.Time.UTC()
		}

		messages = append(messages, message)
	}

	return messages, rows.Err()
}

// OpenTicket starts a conversation on behalf of the viewer's own account.
func (s *Store) OpenTicket(ctx context.Context, viewer *User, subject, body, priority string) (int64, error) {
	if !ValidTicketPriority(priority) {
		priority = "normal"
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}

	defer func() { _ = tx.Rollback() }()

	var reseller any

	if viewer.OwnerID != nil {
		var role Role

		err := tx.QueryRowContext(ctx, "SELECT `role` FROM `users` WHERE id = ?", *viewer.OwnerID).Scan(&role)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return 0, err
		}

		if role == RoleReseller {
			reseller = *viewer.OwnerID
		}
	}

	result, err := tx.ExecContext(ctx,
		"INSERT INTO `tickets` (`user_id`, `reseller_id`, `subject`, `status`, `priority`, `last_message_at`, `created_at`, `updated_at`) "+
			"VALUES (?, ?, ?, 'open', ?, NOW(), NOW(), NOW())",
		viewer.ID, reseller, subject, priority)
	if err != nil {
		return 0, err
	}

	ticketID, err := result.LastInsertId()
	if err != nil {
		return 0, err
	}

	if _, err := tx.ExecContext(ctx,
		"INSERT INTO `ticket_messages` (`ticket_id`, `author_id`, `body`, `created_at`, `updated_at`) VALUES (?, ?, ?, NOW(), NOW())",
		ticketID, viewer.ID, body); err != nil {
		return 0, err
	}

	return ticketID, tx.Commit()
}

// ReplyTicket adds a turn and moves the ticket to the side that has to answer.
// A closed ticket refuses a reply unless reopen is set.
func (s *Store) ReplyTicket(ctx context.Context, viewer *User, ticket Ticket, body string, staff, reopen bool) error {
	if ticket.Status == "closed" && !reopen {
		return ErrTicketClosed
	}

	status := "open"
	if staff {
		status = "answered"
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}

	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx,
		"INSERT INTO `ticket_messages` (`ticket_id`, `author_id`, `body`, `created_at`, `updated_at`) VALUES (?, ?, ?, NOW(), NOW())",
		ticket.ID, viewer.ID, body); err != nil {
		return err
	}

	if _, err := tx.ExecContext(ctx,
		"UPDATE `tickets` SET `status` = ?, `last_message_at` = NOW(), `updated_at` = NOW() WHERE id = ?",
		status, ticket.ID); err != nil {
		return err
	}

	return tx.Commit()
}

// SetTicketStatus closes or reopens a ticket.
func (s *Store) SetTicketStatus(ctx context.Context, ticketID int64, status string) error {
	if !ValidTicketStatus(status) {
		return fmt.Errorf("unknown ticket status %q", status)
	}

	_, err := s.db.ExecContext(ctx,
		"UPDATE `tickets` SET `status` = ?, `updated_at` = NOW() WHERE id = ?", status, ticketID)

	return err
}

// ----------------------------------------------------------- announcements

// Announcement is a message one person sends to many accounts.
type Announcement struct {
	ID             int64     `json:"id"`
	Sender         string    `json:"from"`
	Subject        string    `json:"subject"`
	Body           string    `json:"body"`
	Audience       []string  `json:"audience,omitempty"`
	RecipientCount int       `json:"recipient_count,omitempty"`
	Emailed        bool      `json:"emailed,omitempty"`
	Created        time.Time `json:"created_at"`
}

// AnnouncementsReceived lists what was sent to the viewer's role, and what the
// reseller who owns their account sent them.
func (s *Store) AnnouncementsReceived(ctx context.Context, viewer *User, limit int) ([]Announcement, error) {
	ownerID := int64(0)
	if viewer.OwnerID != nil && viewer.Role == RoleUser {
		ownerID = *viewer.OwnerID
	}

	rows, err := s.db.QueryContext(ctx,
		"SELECT a.id, COALESCE(s.username, ''), a.subject, a.body, a.created_at "+
			"FROM `announcements` a JOIN `users` s ON s.id = a.sender_id "+
			"WHERE (s.`role` = 'admin' AND JSON_CONTAINS(a.audience, JSON_QUOTE(?))) "+
			"OR (? > 0 AND a.sender_id = ?) "+
			"ORDER BY a.id DESC LIMIT ?",
		string(viewer.Role), ownerID, ownerID, limit)
	if err != nil {
		return nil, err
	}

	defer func() { _ = rows.Close() }()

	announcements := []Announcement{}

	for rows.Next() {
		var (
			announcement Announcement
			created      sql.NullTime
		)

		if err := rows.Scan(&announcement.ID, &announcement.Sender, &announcement.Subject,
			&announcement.Body, &created); err != nil {
			return nil, err
		}

		if created.Valid {
			announcement.Created = created.Time.UTC()
		}

		announcements = append(announcements, announcement)
	}

	return announcements, rows.Err()
}

// AnnouncementsSent lists what the viewer sent, with who it reached.
func (s *Store) AnnouncementsSent(ctx context.Context, senderID int64, limit int) ([]Announcement, error) {
	rows, err := s.db.QueryContext(ctx,
		"SELECT a.id, a.subject, a.body, a.audience, a.recipient_count, a.emailed, a.created_at "+
			"FROM `announcements` a WHERE a.sender_id = ? ORDER BY a.id DESC LIMIT ?", senderID, limit)
	if err != nil {
		return nil, err
	}

	defer func() { _ = rows.Close() }()

	announcements := []Announcement{}

	for rows.Next() {
		var (
			announcement Announcement
			audience     []byte
			created      sql.NullTime
		)

		if err := rows.Scan(&announcement.ID, &announcement.Subject, &announcement.Body,
			&audience, &announcement.RecipientCount, &announcement.Emailed, &created); err != nil {
			return nil, err
		}

		_ = json.Unmarshal(audience, &announcement.Audience)

		if created.Valid {
			announcement.Created = created.Time.UTC()
		}

		announcements = append(announcements, announcement)
	}

	return announcements, rows.Err()
}

// AnnouncementAudience counts who each audience would reach, so the composer
// can say "sent to 12 accounts" before it is sent.
func (s *Store) AnnouncementAudience(ctx context.Context, viewer *User) (map[string]int, error) {
	counts := map[string]int{}

	if viewer.Role == RoleReseller {
		var users int

		err := s.db.QueryRowContext(ctx,
			"SELECT COUNT(*) FROM `users` WHERE `status` = 'active' AND `owner_id` = ? AND `role` = 'user'",
			viewer.ID).Scan(&users)

		if err != nil {
			return nil, err
		}

		counts["user"] = users

		return counts, nil
	}

	for _, role := range []Role{RoleReseller, RoleUser} {
		var users int

		err := s.db.QueryRowContext(ctx,
			"SELECT COUNT(*) FROM `users` WHERE `status` = 'active' AND `role` = ?", string(role)).Scan(&users)
		if err != nil {
			return nil, err
		}

		counts[string(role)] = users
	}

	return counts, nil
}

// SendAnnouncement stores the message and returns it with its recipient count.
// A reseller can only address its own hosting accounts, exactly as
// App\Services\AnnouncementService enforces.
func (s *Store) SendAnnouncement(ctx context.Context, sender *User, audience []string, subject, body string, email bool) (Announcement, error) {
	switch sender.Role {
	case RoleReseller:
		audience = []string{"user"}
	case RoleAdmin:
		kept := make([]string, 0, len(audience))

		for _, role := range audience {
			if role == string(RoleReseller) || role == string(RoleUser) {
				kept = append(kept, role)
			}
		}

		audience = kept
	default:
		return Announcement{}, errors.New("only administrators and resellers send messages to many accounts")
	}

	if len(audience) == 0 {
		return Announcement{}, errors.New("choose who receives the message")
	}

	recipients, err := s.announcementRecipients(ctx, sender, audience)
	if err != nil {
		return Announcement{}, err
	}

	encoded, err := json.Marshal(audience)
	if err != nil {
		return Announcement{}, err
	}

	result, err := s.db.ExecContext(ctx,
		"INSERT INTO `announcements` (`sender_id`, `audience`, `subject`, `body`, `emailed`, `recipient_count`, `created_at`, `updated_at`) "+
			"VALUES (?, ?, ?, ?, ?, ?, NOW(), NOW())",
		sender.ID, string(encoded), subject, body, email, recipients)
	if err != nil {
		return Announcement{}, err
	}

	id, err := result.LastInsertId()
	if err != nil {
		return Announcement{}, err
	}

	return Announcement{
		ID:             id,
		Sender:         sender.Username,
		Subject:        subject,
		Body:           body,
		Audience:       audience,
		RecipientCount: recipients,
		Emailed:        email,
		Created:        time.Now().UTC(),
	}, nil
}

func (s *Store) announcementRecipients(ctx context.Context, sender *User, audience []string) (int, error) {
	if sender.Role == RoleReseller {
		var users int

		err := s.db.QueryRowContext(ctx,
			"SELECT COUNT(*) FROM `users` WHERE `status` = 'active' AND `owner_id` = ? AND `role` = 'user'",
			sender.ID).Scan(&users)

		return users, err
	}

	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(audience)), ",")
	args := make([]any, 0, len(audience))

	for _, role := range audience {
		args = append(args, role)
	}

	var users int

	err := s.db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM `users` WHERE `status` = 'active' AND `role` IN ("+placeholders+")",
		args...).Scan(&users)

	return users, err
}
