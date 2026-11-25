package httpapi

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"rayxo/panel/internal/auth"
	"rayxo/panel/internal/rules"
	"rayxo/panel/internal/store"
)

// newID returns a UUID v4, the shape the tasks table's id column already has
// in the Laravel panel.
func newID() string {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		// A missing random source is not something a panel can paper over: an
		// id that could collide would mean one task overwriting another.
		panic("no randomness for ids: " + err.Error())
	}

	raw[6] = (raw[6] & 0x0f) | 0x40
	raw[8] = (raw[8] & 0x3f) | 0x80

	encoded := hex.EncodeToString(raw)

	return encoded[0:8] + "-" + encoded[8:12] + "-" + encoded[12:16] + "-" + encoded[16:20] + "-" + encoded[20:32]
}

// page reads ?limit= and ?offset= with a sane ceiling.
func page(r *http.Request) (limit, offset int) {
	limit = 50

	if raw := r.URL.Query().Get("limit"); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 {
			limit = parsed
		}
	}

	if limit > 200 {
		limit = 200
	}

	if raw := r.URL.Query().Get("offset"); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 {
			offset = parsed
		}
	}

	return limit, offset
}

// accountPayload is one row of the accounts table as the SPA draws it.
type accountPayload struct {
	ID        int64       `json:"id"`
	Username  string      `json:"username"`
	Name      string      `json:"name"`
	Email     string      `json:"email"`
	Status    string      `json:"status"`
	Reason    string      `json:"reason"`
	Owner     string      `json:"owner"`
	UID       *int32      `json:"uid"`
	Package   *pkgPayload `json:"package"`
	Domains   int         `json:"domains"`
	CreatedAt string      `json:"createdAt"`
	LastLogin *string     `json:"lastLoginAt"`
}

type pkgPayload struct {
	ID            int64  `json:"id"`
	Name          string `json:"name"`
	Description   string `json:"description"`
	DiskMB        *int64 `json:"diskMb"`
	BandwidthMB   *int64 `json:"bandwidthMb"`
	Domains       *int64 `json:"domains"`
	Databases     *int64 `json:"databases"`
	EmailAccounts *int64 `json:"emailAccounts"`
}

func payloadForAccount(account store.Account) accountPayload {
	payload := accountPayload{
		ID:        account.ID,
		Username:  account.Username,
		Name:      account.Name,
		Email:     account.Email,
		Status:    string(account.Status),
		Reason:    account.SuspendReason,
		Owner:     account.OwnerName,
		UID:       account.UID,
		Domains:   account.DomainCount,
		CreatedAt: account.CreatedAt.UTC().Format("2006-01-02T15:04:05Z"),
	}

	if account.Package != nil {
		payload.Package = &pkgPayload{
			ID:            account.Package.ID,
			Name:          account.Package.Name,
			Description:   account.Package.Description,
			DiskMB:        account.Package.DiskMB,
			BandwidthMB:   account.Package.BandwidthMB,
			Domains:       account.Package.Domains,
			Databases:     account.Package.Databases,
			EmailAccounts: account.Package.EmailAccounts,
		}
	}

	if account.LastLoginAt != nil {
		formatted := account.LastLoginAt.UTC().Format("2006-01-02T15:04:05Z")
		payload.LastLogin = &formatted
	}

	return payload
}

func (s *Server) handleAccounts(w http.ResponseWriter, r *http.Request) {
	user, _ := userFrom(r.Context())

	limit, offset := page(r)

	accounts, err := s.store.ListAccounts(r.Context(), &user, limit, offset)
	if err != nil {
		s.failStore(w, r, err)

		return
	}

	total, err := s.store.CountAccounts(r.Context(), &user)
	if err != nil {
		s.failStore(w, r, err)

		return
	}

	payload := make([]accountPayload, 0, len(accounts))
	for _, account := range accounts {
		payload = append(payload, payloadForAccount(account))
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"accounts": payload,
		"total":    total,
		"limit":    limit,
		"offset":   offset,
	})
}

// findAccount loads an account by username and checks the caller may manage
// it. An administrator may manage any account; a reseller only its own.
func (s *Server) findAccount(w http.ResponseWriter, r *http.Request) (store.User, bool) {
	caller, ok := userFrom(r.Context())
	if !ok {
		fail(w, http.StatusUnauthorized, codeUnauthorized, "Sign in to continue.")

		return store.User{}, false
	}

	username := rules.NormalizeUsername(r.PathValue("username"))

	account, err := s.store.UserByUsername(r.Context(), username)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			fail(w, http.StatusNotFound, codeNotFound, "There is no account by that name.")

			return store.User{}, false
		}

		s.failStore(w, r, err)

		return store.User{}, false
	}

	if account.Role != store.RoleUser {
		fail(w, http.StatusNotFound, codeNotFound, "There is no hosting account by that name.")

		return store.User{}, false
	}

	if !caller.IsAdmin() && (account.OwnerID == nil || *account.OwnerID != caller.ID) {
		// Not 403: a reseller must not learn which usernames exist on the
		// rest of the server.
		fail(w, http.StatusNotFound, codeNotFound, "There is no account by that name.")

		return store.User{}, false
	}

	return account, true
}

func (s *Server) handleAccount(w http.ResponseWriter, r *http.Request) {
	account, ok := s.findAccount(w, r)
	if !ok {
		return
	}

	domains, err := s.store.DomainsByUser(r.Context(), account.ID)
	if err != nil {
		s.failStore(w, r, err)

		return
	}

	domainPayload := make([]map[string]any, 0, len(domains))
	for _, domain := range domains {
		domainPayload = append(domainPayload, map[string]any{
			"id":           domain.ID,
			"name":         domain.Name,
			"type":         domain.Type,
			"phpVersion":   domain.PHPVersion,
			"isPrimary":    domain.IsPrimary,
			"documentRoot": domain.DocumentRoot,
			"status":       domain.Status,
			"sslStatus":    domain.SSLStatus,
			"createdAt":    domain.CreatedAt.UTC().Format("2006-01-02T15:04:05Z"),
		})
	}

	accountID := account.ID

	tasks, err := s.store.Tasks(r.Context(), store.TaskFilter{UserID: &accountID, Limit: 10})
	if err != nil {
		s.failStore(w, r, err)

		return
	}

	taskPayload := make([]map[string]any, 0, len(tasks))
	for _, task := range tasks {
		taskPayload = append(taskPayload, map[string]any{
			"id":         task.ID,
			"action":     task.Action,
			"status":     string(task.Status),
			"error":      task.Error,
			"createdAt":  task.CreatedAt.UTC().Format("2006-01-02T15:04:05Z"),
			"finishedAt": formatTimePtr(task.FinishedAt),
		})
	}

	accountPayloadValue := payloadForAccount(store.Account{User: account, DomainCount: len(domains)})

	writeJSON(w, http.StatusOK, map[string]any{
		"account": accountPayloadValue,
		"domains": domainPayload,
		"tasks":   taskPayload,
	})
}

type createAccountRequest struct {
	Username  string `json:"username"`
	Name      string `json:"name"`
	Email     string `json:"email"`
	Password  string `json:"password"`
	PackageID *int64 `json:"packageId"`
	Domain    string `json:"domain"`
}

func (s *Server) handleCreateAccount(w http.ResponseWriter, r *http.Request) {
	caller, ok := userFrom(r.Context())
	if !ok {
		fail(w, http.StatusUnauthorized, codeUnauthorized, "Sign in to continue.")

		return
	}

	var request createAccountRequest
	if !decodeJSON(w, r, &request) {
		return
	}

	username := rules.NormalizeUsername(request.Username)
	domain := rules.NormalizeDomain(request.Domain)
	email := strings.TrimSpace(request.Email)
	name := strings.TrimSpace(request.Name)

	fields := map[string]string{}

	if err := rules.Username(username); err != nil {
		fields["username"] = err.Error()
	}

	if err := rules.Name(name); err != nil {
		fields["name"] = err.Error()
	}

	if err := rules.Email(email); err != nil {
		fields["email"] = err.Error()
	}

	if err := rules.Password(request.Password); err != nil {
		fields["password"] = err.Error()
	}

	if domain != "" {
		if err := rules.Domain(domain); err != nil {
			fields["domain"] = err.Error()
		}
	}

	if len(fields) > 0 {
		failFields(w, http.StatusUnprocessableEntity, codeInvalid, "Some fields need fixing.", fields)

		return
	}

	taken, err := s.store.UsernameTaken(r.Context(), username)
	if err != nil {
		s.failStore(w, r, err)

		return
	}

	if taken {
		failFields(w, http.StatusConflict, codeConflict, "That username is taken.", map[string]string{
			"username": "That username is taken.",
		})

		return
	}

	if domain != "" {
		exists, err := s.store.DomainExists(r.Context(), domain)
		if err != nil {
			s.failStore(w, r, err)

			return
		}

		if exists {
			failFields(w, http.StatusConflict, codeConflict, "That domain is already on this server.", map[string]string{
				"domain": "That domain is already on this server.",
			})

			return
		}
	}

	// A reseller may only hand out packages it owns, and only if its own
	// reseller package allows this one.
	packageID := request.PackageID
	if packageID != nil {
		packages, err := s.store.Packages(r.Context(), &caller)
		if err != nil {
			s.failStore(w, r, err)

			return
		}

		found := false

		for _, pkg := range packages {
			if pkg.ID == *packageID {
				found = true

				break
			}
		}

		if !found {
			failFields(w, http.StatusForbidden, codeForbidden, "That package is not yours to hand out.", map[string]string{
				"packageId": "That package is not yours to hand out.",
			})

			return
		}
	}

	hash, err := auth.HashPassword(request.Password)
	if err != nil {
		failFields(w, http.StatusUnprocessableEntity, codeInvalid, err.Error(), map[string]string{"password": err.Error()})

		return
	}

	account, err := s.store.CreateAccount(r.Context(), username, name, email, hash, caller.ID, packageID)
	if err != nil {
		s.failStore(w, r, err)

		return
	}

	actorID := caller.ID
	accountID := account.ID

	if err := s.dispatch(r, "account.create", map[string]any{"username": username}, &accountID, &actorID); err != nil {
		s.failStore(w, r, err)

		return
	}

	if domain != "" {
		phpVersion := "8.4"

		website, err := s.store.CreateDomain(r.Context(), account.ID, domain, "domain", phpVersion, true)
		if err != nil {
			s.failStore(w, r, err)

			return
		}

		if err := s.dispatch(r, "web.apply_domain", map[string]any{
			"username":      username,
			"domain":        website.Name,
			"php_version":   phpVersion,
			"document_root": "",
		}, &accountID, &actorID); err != nil {
			s.failStore(w, r, err)

			return
		}
	}

	_ = s.store.Audit(r.Context(), &actorID, "account.created", "user",
		strconv.FormatInt(account.ID, 10), s.clientIP(r), userAgent(r), map[string]any{
			"username": username,
			"domain":   domain,
		})

	writeJSON(w, http.StatusCreated, map[string]any{"account": payloadForAccount(store.Account{User: account})})
}

// dispatch queues one agent call. The task row is written first, so work that
// never reaches the agent is still visible in the activity list.
func (s *Server) dispatch(r *http.Request, action string, params map[string]any, userID, actorID *int64) error {
	payload, err := json.Marshal(params)
	if err != nil {
		return err
	}

	return s.store.CreateTask(r.Context(), newID(), userID, actorID, action, string(payload))
}

func (s *Server) handleSuspendAccount(w http.ResponseWriter, r *http.Request) {
	account, ok := s.findAccount(w, r)
	if !ok {
		return
	}

	// The reason is optional, but it is what the account is told when it
	// tries to sign in, so it is worth asking for.
	var request struct {
		Reason string `json:"reason"`
	}

	if r.ContentLength > 0 && !decodeJSON(w, r, &request) {
		return
	}

	reason := strings.TrimSpace(request.Reason)
	if len(reason) > 255 {
		reason = reason[:255]
	}

	actor, _ := userFrom(r.Context())

	accountID := account.ID
	actorID := actor.ID

	if err := s.dispatch(r, "account.suspend", map[string]any{
		"username": account.Username,
		"reason":   reason,
	}, &accountID, &actorID); err != nil {
		s.failStore(w, r, err)

		return
	}

	_ = s.store.Audit(r.Context(), &actorID, "account.suspended", "user",
		strconv.FormatInt(account.ID, 10), s.clientIP(r), userAgent(r), map[string]any{"reason": reason})

	writeJSON(w, http.StatusAccepted, map[string]any{"queued": true})
}

func (s *Server) handleUnsuspendAccount(w http.ResponseWriter, r *http.Request) {
	account, ok := s.findAccount(w, r)
	if !ok {
		return
	}

	actor, _ := userFrom(r.Context())

	accountID := account.ID
	actorID := actor.ID

	if err := s.dispatch(r, "account.unsuspend", map[string]any{
		"username": account.Username,
	}, &accountID, &actorID); err != nil {
		s.failStore(w, r, err)

		return
	}

	_ = s.store.Audit(r.Context(), &actorID, "account.unsuspended", "user",
		strconv.FormatInt(account.ID, 10), s.clientIP(r), userAgent(r), nil)

	writeJSON(w, http.StatusAccepted, map[string]any{"queued": true})
}

func (s *Server) handleDeleteAccount(w http.ResponseWriter, r *http.Request) {
	account, ok := s.findAccount(w, r)
	if !ok {
		return
	}

	// Deleting a hosting account removes its files, databases and mail on the
	// server. Typing the username back is the difference between a misclick
	// and an incident.
	if r.URL.Query().Get("confirm") != account.Username {
		fail(w, http.StatusBadRequest, codeInvalid, "Type the account's username to confirm the deletion.")

		return
	}

	actor, _ := userFrom(r.Context())

	accountID := account.ID
	actorID := actor.ID

	if err := s.dispatch(r, "account.delete", map[string]any{
		"username": account.Username,
	}, &accountID, &actorID); err != nil {
		s.failStore(w, r, err)

		return
	}

	_ = s.store.Audit(r.Context(), &actorID, "account.deleted", "user",
		strconv.FormatInt(account.ID, 10), s.clientIP(r), userAgent(r), nil)

	writeJSON(w, http.StatusAccepted, map[string]any{"queued": true})
}
