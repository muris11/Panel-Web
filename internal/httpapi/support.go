package httpapi

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"

	"muriscore/panel/internal/store"
)

// The limits App\Http\Controllers\TicketController and
// App\Http\Controllers\AnnouncementController enforce.
const (
	maxTicketSubject    = 150
	maxTicketBody       = 10000
	maxAnnouncementBody = 10000
)

// handleTickets lists tickets or opens a new one.
func (s *Server) handleTickets(w http.ResponseWriter, r *http.Request) {
	caller, ok := s.caller(w, r)
	if !ok {
		return
	}

	switch r.Method {
	case http.MethodGet:
		s.listTickets(w, r, caller)

	case http.MethodPost:
		var request struct {
			Subject  string `json:"subject"`
			Body     string `json:"body"`
			Priority string `json:"priority"`
		}

		if !decodeJSON(w, r, &request) {
			return
		}

		subject := strings.TrimSpace(request.Subject)
		body := strings.TrimSpace(request.Body)

		fields := map[string]string{}

		switch {
		case subject == "":
			fields["subject"] = "Write a subject."
		case utf8.RuneCountInString(subject) > maxTicketSubject:
			fields["subject"] = "Keep it under " + strconv.Itoa(maxTicketSubject) + " characters."
		case body == "":
			fields["body"] = "Write what happened."
		case utf8.RuneCountInString(body) > maxTicketBody:
			fields["body"] = "Keep it under " + strconv.Itoa(maxTicketBody) + " characters."
		}

		if len(fields) > 0 {
			failFields(w, http.StatusUnprocessableEntity, codeInvalid, "Please check the ticket.", fields)

			return
		}

		id, err := s.store.OpenTicket(r.Context(), &caller, subject, body, request.Priority)
		if err != nil {
			s.failStore(w, r, err)

			return
		}

		s.audit(r, caller, "ticket.open", "ticket", strconv.FormatInt(id, 10), map[string]any{"subject": subject})

		writeJSON(w, http.StatusCreated, map[string]any{"id": id})
	}
}

// listTickets answers the tickets page: the caller's own box, and for an
// administrator or a reseller the inbox they answer.
func (s *Server) listTickets(w http.ResponseWriter, r *http.Request, caller store.User) {
	box := r.URL.Query().Get("box")
	if box == "" {
		if caller.Role == store.RoleUser {
			box = "mine"
		} else {
			box = "inbox"
		}
	}

	status := r.URL.Query().Get("status")

	tickets, err := s.store.Tickets(r.Context(), &caller, store.TicketFilter{Box: box, Status: status})
	if err != nil {
		s.failStore(w, r, err)

		return
	}

	answers := caller.Role == store.RoleAdmin || caller.Role == store.RoleReseller

	writeJSON(w, http.StatusOK, map[string]any{
		"box":       box,
		"status":    status,
		"tickets":   tickets,
		"canAnswer": answers,
		"statuses":  []string{"open", "answered", "closed"},
	})
}

// ticket loads the ticket named in the path, or answers 404.
func (s *Server) ticket(w http.ResponseWriter, r *http.Request, caller store.User) (store.Ticket, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		fail(w, http.StatusNotFound, codeNotFound, "Not found.")

		return store.Ticket{}, false
	}

	ticket, err := s.store.TicketReader(r.Context(), &caller, id)
	if err != nil {
		s.failStore(w, r, err)

		return store.Ticket{}, false
	}

	return ticket, true
}

// handleTicket reads one conversation or adds a turn to it.
func (s *Server) handleTicket(w http.ResponseWriter, r *http.Request) {
	caller, ok := s.caller(w, r)
	if !ok {
		return
	}

	ticket, ok := s.ticket(w, r, caller)
	if !ok {
		return
	}

	if r.Method == http.MethodGet {
		messages, err := s.store.TicketMessages(r.Context(), ticket.ID)
		if err != nil {
			s.failStore(w, r, err)

			return
		}

		writeJSON(w, http.StatusOK, map[string]any{
			"ticket":    ticket,
			"messages":  messages,
			"canAnswer": ticket.UserID == caller.ID || caller.Role != store.RoleUser,
		})

		return
	}

	// A reply, and optionally a new state for the ticket.
	var request struct {
		Body   string `json:"body"`
		Status string `json:"status"`
		Reopen bool   `json:"reopen"`
	}

	if !decodeJSON(w, r, &request) {
		return
	}

	body := strings.TrimSpace(request.Body)

	if body == "" {
		failFields(w, http.StatusUnprocessableEntity, codeInvalid, "Write a reply.", map[string]string{
			"body": "This is required.",
		})

		return
	}

	if utf8.RuneCountInString(body) > maxTicketBody {
		failFields(w, http.StatusUnprocessableEntity, codeInvalid, "That reply is too long.", map[string]string{
			"body": "Keep it under " + strconv.Itoa(maxTicketBody) + " characters.",
		})

		return
	}

	staff := ticket.UserID != caller.ID
	reopen := request.Reopen || (request.Status != "" && request.Status != "closed")

	if err := s.store.ReplyTicket(r.Context(), &caller, ticket, body, staff, reopen); err != nil {
		if errors.Is(err, store.ErrTicketClosed) {
			fail(w, http.StatusConflict, codeConflict, "This ticket is closed. Reopen it to reply.")

			return
		}

		s.failStore(w, r, err)

		return
	}

	s.audit(r, caller, "ticket.reply", "ticket", strconv.FormatInt(ticket.ID, 10), nil)

	writeJSON(w, http.StatusOK, map[string]any{"id": ticket.ID})
}

// handleTicketStatus closes or reopens a ticket.
func (s *Server) handleTicketStatus(w http.ResponseWriter, r *http.Request) {
	caller, ok := s.caller(w, r)
	if !ok {
		return
	}

	ticket, ok := s.ticket(w, r, caller)
	if !ok {
		return
	}

	var request struct {
		Status string `json:"status"`
	}

	if !decodeJSON(w, r, &request) {
		return
	}

	if request.Status != "closed" && request.Status != "open" {
		failFields(w, http.StatusUnprocessableEntity, codeInvalid, "Use open or closed.", map[string]string{
			"status": "open or closed.",
		})

		return
	}

	if err := s.store.SetTicketStatus(r.Context(), ticket.ID, request.Status); err != nil {
		s.failStore(w, r, err)

		return
	}

	s.audit(r, caller, "ticket."+request.Status, "ticket", strconv.FormatInt(ticket.ID, 10), nil)

	writeJSON(w, http.StatusOK, map[string]any{"id": ticket.ID, "status": request.Status})
}

// handleAnnouncements lists what the caller received and, for the people who
// may send, what they sent.
func (s *Server) handleAnnouncements(w http.ResponseWriter, r *http.Request) {
	caller, ok := s.caller(w, r)
	if !ok {
		return
	}

	if r.Method == http.MethodPost {
		s.sendAnnouncement(w, r, caller)

		return
	}

	received, err := s.store.AnnouncementsReceived(r.Context(), &caller, 50)
	if err != nil {
		s.failStore(w, r, err)

		return
	}

	canSend := caller.Role == store.RoleAdmin || caller.Role == store.RoleReseller

	answer := map[string]any{
		"received":  received,
		"can_send":  canSend,
		"sent":      []store.Announcement{},
		"audiences": map[string]int{},
		"max_body":  maxAnnouncementBody,
	}

	if canSend {
		sent, err := s.store.AnnouncementsSent(r.Context(), caller.ID, 50)
		if err != nil {
			s.failStore(w, r, err)

			return
		}

		audiences, err := s.store.AnnouncementAudience(r.Context(), &caller)
		if err != nil {
			s.failStore(w, r, err)

			return
		}

		answer["sent"] = sent
		answer["audiences"] = audiences
	}

	writeJSON(w, http.StatusOK, answer)
}

func (s *Server) sendAnnouncement(w http.ResponseWriter, r *http.Request, caller store.User) {
	if caller.Role != store.RoleAdmin && caller.Role != store.RoleReseller {
		fail(w, http.StatusForbidden, codeForbidden, "Only administrators and resellers send messages to many accounts.")

		return
	}

	var request struct {
		Subject  string   `json:"subject"`
		Body     string   `json:"body"`
		Audience []string `json:"audience"`
		Email    bool     `json:"email"`
	}

	if !decodeJSON(w, r, &request) {
		return
	}

	subject := strings.TrimSpace(request.Subject)
	body := strings.TrimSpace(request.Body)

	fields := map[string]string{}

	switch {
	case subject == "":
		fields["subject"] = "Write a subject."
	case utf8.RuneCountInString(subject) > maxTicketSubject:
		fields["subject"] = "Keep it under " + strconv.Itoa(maxTicketSubject) + " characters."
	case body == "":
		fields["body"] = "Write the message."
	case utf8.RuneCountInString(body) > maxAnnouncementBody:
		fields["body"] = "Keep it under " + strconv.Itoa(maxAnnouncementBody) + " characters."
	}

	if len(fields) > 0 {
		failFields(w, http.StatusUnprocessableEntity, codeInvalid, "Please check the message.", fields)

		return
	}

	announcement, err := s.store.SendAnnouncement(r.Context(), &caller, request.Audience, subject, body, request.Email)
	if err != nil {
		fail(w, http.StatusUnprocessableEntity, codeInvalid, err.Error())

		return
	}

	s.audit(r, caller, "announcement.sent", "announcement", strconv.FormatInt(announcement.ID, 10), map[string]any{
		"subject":    subject,
		"audience":   announcement.Audience,
		"recipients": announcement.RecipientCount,
		"emailed":    request.Email,
	})

	writeJSON(w, http.StatusCreated, announcement)
}
