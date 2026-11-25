package httpapi

import (
	"net/http"
	"strings"

	"muriscore/panel/internal/auth"
	"muriscore/panel/internal/rules"
)

// handleProfile changes the signed-in user's own name and address. Nothing
// here needs the agent: it is panel data.
func (s *Server) handleProfile(w http.ResponseWriter, r *http.Request) {
	caller, ok := s.caller(w, r)
	if !ok {
		return
	}

	if r.Method != http.MethodPatch {
		fail(w, http.StatusMethodNotAllowed, codeInvalid, "Use PATCH to change a profile.")

		return
	}

	var request struct {
		Name  string `json:"name"`
		Email string `json:"email"`
	}

	if !decodeJSON(w, r, &request) {
		return
	}

	request.Name = strings.TrimSpace(request.Name)
	request.Email = strings.TrimSpace(strings.ToLower(request.Email))

	fields := map[string]string{}

	if request.Name != "" && rules.Name(request.Name) != nil {
		fields["name"] = "Use 1 to 100 characters."
	}

	if request.Email != "" && rules.Email(request.Email) != nil {
		fields["email"] = "That does not look like an email address."
	}

	if len(fields) > 0 {
		failFields(w, http.StatusUnprocessableEntity, codeInvalid, "Some of those details cannot be used.", fields)

		return
	}

	if request.Name != "" && request.Name != caller.Name {
		if err := s.store.SetUserName(r.Context(), caller.ID, request.Name); err != nil {
			s.failStore(w, r, err)

			return
		}

		s.audit(r, caller, "profile.name_changed", "user", caller.Username, map[string]any{"name": request.Name})
	}

	if request.Email != "" && !strings.EqualFold(request.Email, caller.Email) {
		taken, err := s.store.EmailTaken(r.Context(), request.Email, caller.ID)
		if err != nil {
			s.failStore(w, r, err)

			return
		}

		if taken {
			failFields(w, http.StatusConflict, codeConflict, "That address is already in use.", map[string]string{
				"email": "Another account signs in with it.",
			})

			return
		}

		if err := s.store.SetUserEmail(r.Context(), caller.ID, request.Email); err != nil {
			s.failStore(w, r, err)

			return
		}

		s.audit(r, caller, "profile.email_changed", "user", caller.Username, map[string]any{"email": request.Email})
	}

	fresh, err := s.store.UserByID(r.Context(), caller.ID)
	if err != nil {
		s.failStore(w, r, err)

		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"user": payloadFor(fresh)})
}

// handlePassword changes the signed-in user's own password. The current one is
// required, all other sessions are dropped, and this browser is signed in again
// so the page does not bounce to the form.
func (s *Server) handlePassword(w http.ResponseWriter, r *http.Request) {
	caller, ok := s.caller(w, r)
	if !ok {
		return
	}

	var request struct {
		Current string `json:"currentPassword"`
		New     string `json:"newPassword"`
	}

	if !decodeJSON(w, r, &request) {
		return
	}

	if err := auth.VerifyPassword(caller.PasswordHash, request.Current); err != nil {
		s.recordFailedLogin(r, caller)
		failFields(w, http.StatusUnauthorized, codeUnauthorized, "That password is not right.", map[string]string{
			"currentPassword": "That is not the password you signed in with.",
		})

		return
	}

	if request.New == request.Current {
		failFields(w, http.StatusUnprocessableEntity, codeInvalid, "Pick a different password.", map[string]string{
			"newPassword": "The new password is the one you already have.",
		})

		return
	}

	hash, err := auth.HashPassword(request.New)
	if err != nil {
		failFields(w, http.StatusUnprocessableEntity, codeInvalid, "That password cannot be used.", map[string]string{
			"newPassword": err.Error(),
		})

		return
	}

	// UpdatePassword also deletes every session, this browser's included.
	if err := s.store.UpdatePassword(r.Context(), caller.ID, hash); err != nil {
		s.failStore(w, r, err)

		return
	}

	s.audit(r, caller, "profile.password_changed", "user", caller.Username, nil)

	s.startSession(w, r, caller)
}
