package httpapi

import (
	"net/http"
	"strings"

	"muriscore/panel/internal/rules"
	"muriscore/panel/internal/store"
)

// postgresSuffixPattern is what may follow the account's name in a database or
// user name. PostgreSQL truncates identifiers at 63 bytes, so "<user>_<suffix>"
// has to fit.
var postgresSuffixPattern = databaseSuffixPattern

// handlePostgres lists an operator's PostgreSQL databases and logins, or adds a
// database.
func (s *Server) handlePostgres(w http.ResponseWriter, r *http.Request) {
	caller, ok := s.caller(w, r)
	if !ok {
		return
	}

	switch r.Method {
	case http.MethodGet:
		databases, err := s.store.PostgresDatabases(r.Context(), &caller)
		if err != nil {
			s.failStore(w, r, err)

			return
		}

		users, err := s.store.PostgresUsers(r.Context(), &caller)
		if err != nil {
			s.failStore(w, r, err)

			return
		}

		writeJSON(w, http.StatusOK, map[string]any{"databases": databases, "users": users})

	case http.MethodPost:
		var request struct {
			Username string `json:"username"`
			Suffix   string `json:"suffix"`
		}

		if !decodeJSON(w, r, &request) {
			return
		}

		account, ok := s.visibleAccount(w, r, request.Username)
		if !ok {
			return
		}

		suffix := strings.ToLower(strings.TrimSpace(request.Suffix))
		suffix = strings.TrimPrefix(suffix, account.Username+"_")

		if !postgresSuffixPattern.MatchString(suffix) {
			failFields(w, http.StatusUnprocessableEntity, codeInvalid, "That name cannot be used.", map[string]string{
				"suffix": "Use lowercase letters, digits or underscores, up to 27 characters.",
			})

			return
		}

		name := account.Username + "_" + suffix

		database, err := s.store.CreatePostgresDatabase(r.Context(), account.ID, name)
		if err != nil {
			if store.IsDuplicate(err) {
				failFields(w, http.StatusConflict, codeConflict, "That database already exists.", map[string]string{
					"suffix": name + " is already taken.",
				})

				return
			}

			s.failStore(w, r, err)

			return
		}

		if _, err := s.queueFor(r, caller, account, "pg.create_database", map[string]any{
			"username": account.Username,
			"database": name,
		}); err != nil {
			s.failStore(w, r, err)

			return
		}

		s.audit(r, caller, "postgres.created", "postgres_database", name, map[string]any{"username": account.Username})

		writeJSON(w, http.StatusCreated, map[string]any{"database": database})
	}
}

// handlePostgresDatabase removes one PostgreSQL database.
func (s *Server) handlePostgresDatabase(w http.ResponseWriter, r *http.Request) {
	caller, ok := s.caller(w, r)
	if !ok {
		return
	}

	if r.Method != http.MethodDelete {
		fail(w, http.StatusMethodNotAllowed, codeInvalid, "That database can only be deleted.")

		return
	}

	id, ok := parseID(w, r, "id")
	if !ok {
		return
	}

	database, err := s.store.PostgresDatabaseByID(r.Context(), id)
	if err != nil {
		s.failStore(w, r, err)

		return
	}

	account, ok := s.visibleAccountByID(w, r, database.UserID)
	if !ok {
		return
	}

	if _, err := s.queueFor(r, caller, account, "pg.delete_database", map[string]any{
		"username": account.Username,
		"database": database.Name,
	}); err != nil {
		s.failStore(w, r, err)

		return
	}

	if err := s.store.DeletePostgresDatabase(r.Context(), id); err != nil {
		s.failStore(w, r, err)

		return
	}

	s.audit(r, caller, "postgres.deleted", "postgres_database", database.Name, map[string]any{"username": account.Username})

	w.WriteHeader(http.StatusNoContent)
}

// handlePostgresUsers creates or updates a PostgreSQL login.
func (s *Server) handlePostgresUsers(w http.ResponseWriter, r *http.Request) {
	caller, ok := s.caller(w, r)
	if !ok {
		return
	}

	var request struct {
		Username  string  `json:"username"`
		Suffix    string  `json:"suffix"`
		Password  string  `json:"password"`
		Databases []int64 `json:"databases"`
	}

	if !decodeJSON(w, r, &request) {
		return
	}

	account, ok := s.visibleAccount(w, r, request.Username)
	if !ok {
		return
	}

	suffix := strings.ToLower(strings.TrimSpace(request.Suffix))
	suffix = strings.TrimPrefix(suffix, account.Username+"_")

	if !postgresSuffixPattern.MatchString(suffix) {
		failFields(w, http.StatusUnprocessableEntity, codeInvalid, "That login cannot be used.", map[string]string{
			"suffix": "Use lowercase letters, digits or underscores, up to 27 characters.",
		})

		return
	}

	if request.Password != "" {
		if err := rules.Password(request.Password); err != nil {
			failFields(w, http.StatusUnprocessableEntity, codeInvalid, "That password cannot be used.", map[string]string{
				"password": err.Error(),
			})

			return
		}
	}

	grants, ok := s.postgresGrants(w, r, account, request.Databases)
	if !ok {
		return
	}

	name := account.Username + "_" + suffix

	password := request.Password
	if password == "" {
		// Keeping the old password is what a grants-only edit means, so read it
		// back rather than refusing.
		existing, err := s.store.PostgresUsers(r.Context(), &account)
		if err != nil {
			s.failStore(w, r, err)

			return
		}

		found := false
		for _, user := range existing {
			if user.Name != name {
				continue
			}

			stored, err := s.store.PostgresPassword(r.Context(), user.ID)
			if err != nil {
				s.failStore(w, r, err)

				return
			}

			password, err = s.decodeSecret(stored)
			if err != nil {
				s.failStore(w, r, err)

				return
			}

			found = true

			break
		}

		if !found {
			// A new login with no password typed gets one, shown once, the same
			// way the MySQL and FTP drawers behave.
			password = generatePassword(20)
		}
	}

	stored, err := s.encodeSecret(password)
	if err != nil {
		s.failStore(w, r, err)

		return
	}

	user, err := s.store.SavePostgresUser(r.Context(), account.ID, name, stored, grants)
	if err != nil {
		s.failStore(w, r, err)

		return
	}

	if _, err := s.queueFor(r, caller, account, "pg.apply_user", map[string]any{
		"username":  account.Username,
		"user":      name,
		"password":  password,
		"databases": user.Databases,
	}); err != nil {
		s.failStore(w, r, err)

		return
	}

	s.audit(r, caller, "postgres.user_saved", "postgres_user", name, map[string]any{
		"username":  account.Username,
		"databases": user.Databases,
	})

	writeJSON(w, http.StatusCreated, map[string]any{"user": user, "password": password})
}

// handlePostgresUser deletes one login.
func (s *Server) handlePostgresUser(w http.ResponseWriter, r *http.Request) {
	caller, ok := s.caller(w, r)
	if !ok {
		return
	}

	id, ok := parseID(w, r, "id")
	if !ok {
		return
	}

	user, err := s.store.PostgresUserByID(r.Context(), id)
	if err != nil {
		s.failStore(w, r, err)

		return
	}

	account, ok := s.visibleAccountByID(w, r, user.UserID)
	if !ok {
		return
	}

	if r.Method == http.MethodPatch {
		s.updatePostgresUser(w, r, caller, account, user)

		return
	}

	if r.Method != http.MethodDelete {
		fail(w, http.StatusMethodNotAllowed, codeInvalid, "That login can be changed or deleted.")

		return
	}

	if _, err := s.queueFor(r, caller, account, "pg.delete_user", map[string]any{
		"username": account.Username,
		"user":     user.Name,
	}); err != nil {
		s.failStore(w, r, err)

		return
	}

	if err := s.store.DeletePostgresUser(r.Context(), id); err != nil {
		s.failStore(w, r, err)

		return
	}

	s.audit(r, caller, "postgres.user_deleted", "postgres_user", user.Name, map[string]any{"username": account.Username})

	w.WriteHeader(http.StatusNoContent)
}

// updatePostgresUser changes a login's grants, its password, or both. An empty
// password keeps the one already stored, so the drawer can grant access without
// invalidating a password somebody is using.
func (s *Server) updatePostgresUser(w http.ResponseWriter, r *http.Request, caller, account store.User, user store.PostgresUser) {
	var request struct {
		Password  string  `json:"password"`
		Databases []int64 `json:"databases"`
	}

	if !decodeJSON(w, r, &request) {
		return
	}

	grants, ok := s.postgresGrants(w, r, account, request.Databases)
	if !ok {
		return
	}

	password := request.Password

	if password == "" {
		stored, err := s.store.PostgresPassword(r.Context(), user.ID)
		if err != nil {
			s.failStore(w, r, err)

			return
		}

		password, err = s.decodeSecret(stored)
		if err != nil {
			s.failStore(w, r, err)

			return
		}
	} else if err := rules.Password(password); err != nil {
		failFields(w, http.StatusUnprocessableEntity, codeInvalid, "That password cannot be used.", map[string]string{
			"password": err.Error(),
		})

		return
	}

	stored, err := s.encodeSecret(password)
	if err != nil {
		s.failStore(w, r, err)

		return
	}

	updated, err := s.store.SavePostgresUser(r.Context(), account.ID, user.Name, stored, grants)
	if err != nil {
		s.failStore(w, r, err)

		return
	}

	if _, err := s.queueFor(r, caller, account, "pg.apply_user", map[string]any{
		"username":  account.Username,
		"user":      user.Name,
		"password":  password,
		"databases": updated.Databases,
	}); err != nil {
		s.failStore(w, r, err)

		return
	}

	s.audit(r, caller, "postgres.user_updated", "postgres_user", user.Name, map[string]any{
		"username":  account.Username,
		"databases": updated.Databases,
		"password":  request.Password != "",
	})

	answer := map[string]any{"user": updated}
	if request.Password != "" {
		answer["password"] = request.Password
	}

	writeJSON(w, http.StatusOK, answer)
}

// postgresGrants checks that every referenced database belongs to the account.
func (s *Server) postgresGrants(w http.ResponseWriter, r *http.Request, account store.User, ids []int64) ([]int64, bool) {
	if len(ids) == 0 {
		return []int64{}, true
	}

	owned, err := s.store.PostgresDatabases(r.Context(), &account)
	if err != nil {
		s.failStore(w, r, err)

		return nil, false
	}

	allowed := make(map[int64]bool, len(owned))
	for _, database := range owned {
		allowed[database.ID] = true
	}

	grants := make([]int64, 0, len(ids))

	for _, id := range ids {
		if !allowed[id] {
			failFields(w, http.StatusUnprocessableEntity, codeInvalid, "That database is not yours.", map[string]string{
				"databases": "Only databases of " + account.Username + " can be granted.",
			})

			return nil, false
		}

		grants = append(grants, id)
	}

	return grants, true
}
