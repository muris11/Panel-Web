package httpapi

import (
	"errors"
	"net/http"
	"strings"

	"muriscore/panel/internal/auth"
	"muriscore/panel/internal/rules"
	"muriscore/panel/internal/store"
)

// handleMail lists mail domains, or turns one on for a website.
func (s *Server) handleMail(w http.ResponseWriter, r *http.Request) {
	caller, ok := s.caller(w, r)
	if !ok {
		return
	}

	switch r.Method {
	case http.MethodGet:
		domains, err := s.store.MailDomains(r.Context(), &caller)
		if err != nil {
			s.failStore(w, r, err)

			return
		}

		writeJSON(w, http.StatusOK, map[string]any{"domains": domains})

	case http.MethodPost:
		var request struct {
			DomainID int64  `json:"domainId"`
			Selector string `json:"selector"`
		}

		if !decodeJSON(w, r, &request) {
			return
		}

		domain, account, ok := s.ownedDomainByID(w, r, request.DomainID)
		if !ok {
			return
		}

		selector := strings.ToLower(strings.TrimSpace(request.Selector))
		if selector == "" {
			selector = "rayxo"
		}

		if !localPartPattern.MatchString(selector) {
			failFields(w, http.StatusUnprocessableEntity, codeInvalid, "That DKIM selector cannot be used.", map[string]string{
				"selector": "Use letters, digits, dots, dashes or underscores.",
			})

			return
		}

		mailDomain, err := s.store.CreateMailDomain(r.Context(), domain.ID, selector)
		if err != nil {
			s.failStore(w, r, err)

			return
		}

		if _, err := s.queueFor(r, caller, account, "mail.domain_apply", map[string]any{
			"username": account.Username,
			"domain":   domain.Name,
			"selector": selector,
		}); err != nil {
			s.failStore(w, r, err)

			return
		}

		s.audit(r, caller, "mail.domain_added", "domain", domain.Name, nil)

		writeJSON(w, http.StatusCreated, map[string]any{"domain": mailDomain})
	}
}

// ownedDomainByID is ownedDomain for an id that arrived in the body.
func (s *Server) ownedDomainByID(w http.ResponseWriter, r *http.Request, id int64) (store.Domain, store.User, bool) {
	domain, err := s.store.DomainByID(r.Context(), id)
	if err != nil {
		fail(w, http.StatusNotFound, codeNotFound, "There is no website by that id.")

		return store.Domain{}, store.User{}, false
	}

	account, ok := s.domainedAccount(w, r, domain)
	if !ok {
		return store.Domain{}, store.User{}, false
	}

	return domain, account, true
}

// visibleMailDomain loads a mail domain and checks the caller may manage it.
func (s *Server) visibleMailDomain(w http.ResponseWriter, r *http.Request) (store.MailDomain, store.User, bool) {
	id, ok := parseID(w, r, "id")
	if !ok {
		return store.MailDomain{}, store.User{}, false
	}

	domain, err := s.store.MailDomainByID(r.Context(), id)
	if err != nil {
		fail(w, http.StatusNotFound, codeNotFound, "There is no mail domain by that id.")

		return store.MailDomain{}, store.User{}, false
	}

	account, ok := s.visibleAccount(w, r, domain.Username)
	if !ok {
		return store.MailDomain{}, store.User{}, false
	}

	return domain, account, true
}

// handleMailDomain reads one domain's addresses, changes its catch-all, or
// turns mail off for it.
func (s *Server) handleMailDomain(w http.ResponseWriter, r *http.Request) {
	domain, account, ok := s.visibleMailDomain(w, r)
	if !ok {
		return
	}

	caller, _ := s.caller(w, r)

	switch r.Method {
	case http.MethodGet:
		boxes, err := s.store.Mailboxes(r.Context(), domain.ID)
		if err != nil {
			s.failStore(w, r, err)

			return
		}

		forwards, err := s.store.Forwarders(r.Context(), domain.ID)
		if err != nil {
			s.failStore(w, r, err)

			return
		}

		writeJSON(w, http.StatusOK, map[string]any{
			"domain":     domain,
			"mailboxes":  boxes,
			"forwarders": forwards,
		})

	case http.MethodPatch:
		var request struct {
			CatchAll *string `json:"catchAll"`
		}

		if !decodeJSON(w, r, &request) {
			return
		}

		if request.CatchAll == nil {
			fail(w, http.StatusUnprocessableEntity, codeInvalid, "There is nothing to change.")

			return
		}

		target := strings.ToLower(strings.TrimSpace(*request.CatchAll))

		if target != "" {
			if err := rules.Email(target); err != nil {
				failFields(w, http.StatusUnprocessableEntity, codeInvalid, "That catch-all address cannot be used.", map[string]string{
					"catchAll": err.Error(),
				})

				return
			}
		}

		if err := s.store.SetCatchAll(r.Context(), domain.ID, target); err != nil {
			s.failStore(w, r, err)

			return
		}

		s.audit(r, caller, "mail.catchall_changed", "domain", domain.DomainName, map[string]any{"target": target})

		queued(w, "mail.domain_apply", nil)

	case http.MethodDelete:
		if _, err := s.queueFor(r, caller, account, "mail.domain_remove", map[string]any{
			"username": account.Username,
			"domain":   domain.DomainName,
		}); err != nil {
			s.failStore(w, r, err)

			return
		}

		if err := s.store.DeleteMailDomain(r.Context(), domain.ID); err != nil {
			s.failStore(w, r, err)

			return
		}

		s.audit(r, caller, "mail.domain_removed", "domain", domain.DomainName, nil)

		w.WriteHeader(http.StatusNoContent)
	}
}

// handleMailboxes adds an address to a mail domain.
func (s *Server) handleMailboxes(w http.ResponseWriter, r *http.Request) {
	domain, account, ok := s.visibleMailDomain(w, r)
	if !ok {
		return
	}

	var request struct {
		LocalPart string `json:"localPart"`
		Password  string `json:"password"`
		QuotaMB   *int64 `json:"quotaMb"`
	}

	if !decodeJSON(w, r, &request) {
		return
	}

	localPart := strings.ToLower(strings.TrimSpace(request.LocalPart))
	fields := map[string]string{}

	if !localPartPattern.MatchString(localPart) {
		fields["localPart"] = "Use letters, digits, dots, dashes or underscores."
	}

	password := request.Password
	if password == "" {
		password = generatePassword(16)
	} else if err := rules.Password(password); err != nil {
		fields["password"] = err.Error()
	}

	if request.QuotaMB != nil && (*request.QuotaMB < 0 || *request.QuotaMB > 1024*1024) {
		fields["quotaMb"] = "A mailbox quota is between 0 and 1048576 MB."
	}

	if len(fields) > 0 {
		failFields(w, http.StatusUnprocessableEntity, codeInvalid, "That mailbox cannot be created.", fields)

		return
	}

	hash, err := auth.HashPassword(password)
	if err != nil {
		s.failStore(w, r, err)

		return
	}

	box, err := s.store.CreateMailbox(r.Context(), domain.ID, localPart, hash, request.QuotaMB)
	if err != nil {
		s.failStore(w, r, err)

		return
	}

	// Rewriting the mail domain is what makes Dovecot see the new address: the
	// views read the tables, and the agent only has to fix the folder owner.
	if _, err := s.queueFor(r, callerUser(r), account, "mail.domain_apply", map[string]any{
		"username": account.Username,
		"domain":   domain.DomainName,
		"selector": domain.DKIMSelector,
	}); err != nil {
		s.failStore(w, r, err)

		return
	}

	s.audit(r, callerUser(r), "mail.mailbox_created", "domain", domain.DomainName, map[string]any{
		"address": localPart + "@" + domain.DomainName,
	})

	writeJSON(w, http.StatusCreated, map[string]any{
		"mailbox":  box,
		"password": password,
		"address":  localPart + "@" + domain.DomainName,
	})
}

// handleMailbox changes or removes one address.
func (s *Server) handleMailbox(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(w, r, "id")
	if !ok {
		return
	}

	box, domain, err := s.store.MailboxByID(r.Context(), id)
	if err != nil {
		fail(w, http.StatusNotFound, codeNotFound, "There is no mailbox by that id.")

		return
	}

	account, ok := s.visibleAccount(w, r, domain.Username)
	if !ok {
		return
	}

	caller, _ := s.caller(w, r)

	switch r.Method {
	case http.MethodPatch:
		var request struct {
			Password *string `json:"password"`
			QuotaMB  *int64  `json:"quotaMb"`
			Status   string  `json:"status"`
		}

		if !decodeJSON(w, r, &request) {
			return
		}

		status := box.Status
		if status == "" {
			status = "active"
		}

		if request.Status != "" {
			if request.Status != "active" && request.Status != "suspended" {
				fail(w, http.StatusUnprocessableEntity, codeInvalid, "A mailbox is either active or suspended.")

				return
			}

			status = request.Status
		}

		hash := ""

		if request.Password != nil && *request.Password != "" {
			if err := rules.Password(*request.Password); err != nil {
				failFields(w, http.StatusUnprocessableEntity, codeInvalid, "That password cannot be used.", map[string]string{
					"password": err.Error(),
				})

				return
			}

			hash, err = auth.HashPassword(*request.Password)
			if err != nil {
				s.failStore(w, r, err)

				return
			}
		}

		quota := box.QuotaMB
		if request.QuotaMB != nil {
			quota = request.QuotaMB
		}

		if err := s.store.UpdateMailbox(r.Context(), id, hash, quota, status); err != nil {
			s.failStore(w, r, err)

			return
		}

		s.audit(r, caller, "mail.mailbox_updated", "domain", domain.DomainName, map[string]any{
			"address": box.LocalPart + "@" + domain.DomainName,
		})

		queued(w, "mail.domain_apply", nil)

	case http.MethodDelete:
		if _, err := s.queueFor(r, caller, account, "mail.mailbox_remove", map[string]any{
			"username":   account.Username,
			"domain":     domain.DomainName,
			"local_part": box.LocalPart,
		}); err != nil {
			s.failStore(w, r, err)

			return
		}

		if err := s.store.DeleteMailbox(r.Context(), id); err != nil {
			s.failStore(w, r, err)

			return
		}

		s.audit(r, caller, "mail.mailbox_deleted", "domain", domain.DomainName, map[string]any{
			"address": box.LocalPart + "@" + domain.DomainName,
		})

		w.WriteHeader(http.StatusNoContent)
	}
}

// handleForwarders adds or replaces a forward on a mail domain.
func (s *Server) handleForwarders(w http.ResponseWriter, r *http.Request) {
	domain, account, ok := s.visibleMailDomain(w, r)
	if !ok {
		return
	}

	caller, _ := s.caller(w, r)

	var request struct {
		LocalPart    string   `json:"localPart"`
		Destinations []string `json:"destinations"`
	}

	if !decodeJSON(w, r, &request) {
		return
	}

	localPart := strings.ToLower(strings.TrimSpace(request.LocalPart))
	fields := map[string]string{}

	if localPart != "*" && !localPartPattern.MatchString(localPart) {
		fields["localPart"] = "Use letters, digits, dots, dashes or underscores."
	}

	destinations := make([]string, 0, len(request.Destinations))

	for _, destination := range request.Destinations {
		destination = strings.ToLower(strings.TrimSpace(destination))

		if destination == "" {
			continue
		}

		if err := rules.Email(destination); err != nil {
			fields["destinations"] = "Every destination must be an email address."

			break
		}

		destinations = append(destinations, destination)
	}

	if len(destinations) == 0 {
		fields["destinations"] = "Give at least one address to forward to."
	}

	if len(fields) > 0 {
		failFields(w, http.StatusUnprocessableEntity, codeInvalid, "That forward cannot be saved.", fields)

		return
	}

	if err := s.store.SaveForwarder(r.Context(), domain.ID, localPart, destinations); err != nil {
		s.failStore(w, r, err)

		return
	}

	if localPart == "*" {
		if err := s.store.SetCatchAll(r.Context(), domain.ID, destinations[0]); err != nil {
			s.failStore(w, r, err)

			return
		}
	}

	if _, err := s.queueFor(r, caller, account, "mail.domain_apply", map[string]any{
		"username": account.Username,
		"domain":   domain.DomainName,
		"selector": domain.DKIMSelector,
	}); err != nil {
		s.failStore(w, r, err)

		return
	}

	s.audit(r, caller, "mail.forwarder_saved", "domain", domain.DomainName, map[string]any{
		"address": localPart + "@" + domain.DomainName,
	})

	queued(w, "mail.domain_apply", nil)
}

// handleForwarder removes one forward.
func (s *Server) handleForwarder(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(w, r, "id")
	if !ok {
		return
	}

	forwarder, domain, err := s.store.ForwarderByID(r.Context(), id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			fail(w, http.StatusNotFound, codeNotFound, "There is no forward by that id.")
		} else {
			s.failStore(w, r, err)
		}

		return
	}

	account, ok := s.visibleAccount(w, r, domain.Username)
	if !ok {
		return
	}

	if err := s.store.DeleteForwarder(r.Context(), id); err != nil {
		s.failStore(w, r, err)

		return
	}

	caller, _ := s.caller(w, r)

	if _, err := s.queueFor(r, caller, account, "mail.domain_apply", map[string]any{
		"username": account.Username,
		"domain":   domain.DomainName,
		"selector": domain.DKIMSelector,
	}); err != nil {
		s.failStore(w, r, err)

		return
	}

	s.audit(r, caller, "mail.forwarder_deleted", "domain", domain.DomainName, map[string]any{
		"address": forwarder.LocalPart + "@" + domain.DomainName,
	})

	w.WriteHeader(http.StatusNoContent)
}

// callerUser reads the signed-in user; handlers below have already checked it.
func callerUser(r *http.Request) store.User {
	user, _ := userFrom(r.Context())

	return user
}
