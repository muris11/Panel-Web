package httpapi

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"muriscore/panel/internal/auth"
	"muriscore/panel/internal/store"
)

// handleTokens lists and creates API tokens. A token is shown once, at
// creation: the panel stores only its SHA-256, so a stolen database does not
// hand anyone a working key.
func (s *Server) handleTokens(w http.ResponseWriter, r *http.Request) {
	caller, ok := s.caller(w, r)
	if !ok {
		return
	}

	switch r.Method {
	case http.MethodGet:
		tokens, err := s.store.Tokens(r.Context(), caller.ID)
		if err != nil {
			s.failStore(w, r, err)

			return
		}

		writeJSON(w, http.StatusOK, map[string]any{"tokens": tokens})

	case http.MethodPost:
		var request struct {
			Name  string `json:"name"`
			Days  int    `json:"days"`
			Scope string `json:"scope"`
		}

		if !decodeJSON(w, r, &request) {
			return
		}

		name := strings.TrimSpace(request.Name)
		if name == "" || len(name) > 60 {
			failFields(w, http.StatusUnprocessableEntity, codeInvalid, "That token cannot be created.", map[string]string{
				"name": "Give the token a name of up to 60 characters.",
			})

			return
		}

		token, hash, err := auth.NewSessionToken()
		if err != nil {
			s.failStore(w, r, err)

			return
		}

		var expires *time.Time
		if request.Days > 0 {
			when := time.Now().AddDate(0, 0, request.Days)
			expires = &when
		}

		created, err := s.store.CreateToken(r.Context(), caller.ID, name, hash, expires)
		if err != nil {
			s.failStore(w, r, err)

			return
		}

		s.audit(r, caller, "token.created", "token", created.Name, nil)

		writeJSON(w, http.StatusCreated, map[string]any{"token": created, "secret": token})
	}
}

// handleToken revokes one token.
func (s *Server) handleToken(w http.ResponseWriter, r *http.Request) {
	caller, ok := s.caller(w, r)
	if !ok {
		return
	}

	id, ok := parseID(w, r, "id")
	if !ok {
		return
	}

	if err := s.store.DeleteToken(r.Context(), caller.ID, id); err != nil {
		s.failStore(w, r, err)

		return
	}

	s.audit(r, caller, "token.revoked", "token", strconv.FormatInt(id, 10), nil)

	w.WriteHeader(http.StatusNoContent)
}

// authenticateToken accepts "Authorization: Bearer <token>" in place of a
// session cookie. It reports whether it handled the request.
func (s *Server) authenticateToken(w http.ResponseWriter, r *http.Request) bool {
	header := r.Header.Get("Authorization")

	if !strings.HasPrefix(header, "Bearer ") {
		return false
	}

	secret := strings.TrimSpace(strings.TrimPrefix(header, "Bearer "))
	if secret == "" {
		fail(w, http.StatusUnauthorized, codeUnauthorized, "That token is not valid.")

		return true
	}

	user, err := s.store.UserByTokenHash(r.Context(), auth.HashToken(secret))
	if err != nil {
		fail(w, http.StatusUnauthorized, codeUnauthorized, "That token is not valid.")

		return true
	}

	if user.Role == store.RoleUser && user.Status != store.StatusActive {
		fail(w, http.StatusForbidden, codeForbidden, "This account is not active.")

		return true
	}

	_ = s.store.TouchToken(r.Context(), auth.HashToken(secret))

	s.applyUser(r, user)

	return true
}
