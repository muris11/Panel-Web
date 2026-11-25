package httpapi

import (
	"crypto/sha1"
	"encoding/hex"
	"net/http"
	"regexp"
	"strings"

	"muriscore/panel/internal/rules"
	"muriscore/panel/internal/store"
)

// databaseSuffixPattern is what may follow the account's name in a database or
// user name.
var databaseSuffixPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_]{0,26}$`)

// mysqlNativeHash is the hash MySQL stores for a password in
// mysql_native_password: SHA1(SHA1(password)) in upper-case hex after a star.
// The agent hands it to CREATE USER IDENTIFIED WITH … AS, so the panel never
// sends a plain password to the database server.
func mysqlNativeHash(password string) string {
	first := sha1.Sum([]byte(password))
	second := sha1.Sum(first[:])

	return "*" + strings.ToUpper(hex.EncodeToString(second[:]))
}

// handleDatabases lists an operator's databases, or adds one.
func (s *Server) handleDatabases(w http.ResponseWriter, r *http.Request) {
	caller, ok := s.caller(w, r)
	if !ok {
		return
	}

	switch r.Method {
	case http.MethodGet:
		databases, err := s.store.Databases(r.Context(), &caller)
		if err != nil {
			s.failStore(w, r, err)

			return
		}

		users, err := s.store.DatabaseUsers(r.Context(), &caller)
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

		name, ok := s.databaseName(w, account, request.Suffix)
		if !ok {
			return
		}

		database, err := s.store.CreateDatabase(r.Context(), account.ID, name)
		if err != nil {
			if store.IsDuplicate(err) {
				failFields(w, http.StatusConflict, codeConflict, "That database already exists.", map[string]string{
					"suffix": name + " is taken by another account or another database.",
				})

				return
			}

			s.failStore(w, r, err)

			return
		}

		if _, err := s.queueFor(r, caller, account, "db.create_database", map[string]any{
			"username": account.Username,
			"database": name,
		}); err != nil {
			s.failStore(w, r, err)

			return
		}

		s.audit(r, caller, "database.created", "database", name, map[string]any{"username": account.Username})

		writeJSON(w, http.StatusCreated, map[string]any{"database": database})
	}
}

// databaseName builds and checks "<account>_<suffix>".
func (s *Server) databaseName(w http.ResponseWriter, account store.User, suffix string) (string, bool) {
	suffix = strings.ToLower(strings.TrimSpace(suffix))
	suffix = strings.TrimPrefix(suffix, account.Username+"_")

	if !databaseSuffixPattern.MatchString(suffix) {
		failFields(w, http.StatusUnprocessableEntity, codeInvalid, "That name cannot be used.", map[string]string{
			"suffix": "Use lowercase letters, digits or underscores, up to 27 characters.",
		})

		return "", false
	}

	return account.Username + "_" + suffix, true
}

// handleDatabase removes one database.
func (s *Server) handleDatabase(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(w, r, "id")
	if !ok {
		return
	}

	database, err := s.store.DatabaseByID(r.Context(), id)
	if err != nil {
		fail(w, http.StatusNotFound, codeNotFound, "There is no database by that id.")

		return
	}

	account, ok := s.visibleAccount(w, r, database.Username)
	if !ok {
		return
	}

	caller, _ := s.caller(w, r)

	if r.Method != http.MethodDelete {
		fail(w, http.StatusMethodNotAllowed, codeInvalid, "That method is not allowed here.")

		return
	}

	if len(r.URL.Query().Get("confirm")) == 0 {
		failFields(w, http.StatusUnprocessableEntity, codeInvalid, "Type the database name to delete it.", map[string]string{
			"confirm": database.Name,
		})

		return
	}

	if _, err := s.queueFor(r, caller, account, "db.delete_database", map[string]any{
		"username": account.Username,
		"database": database.Name,
	}); err != nil {
		s.failStore(w, r, err)

		return
	}

	if err := s.store.DeleteDatabase(r.Context(), id); err != nil {
		s.failStore(w, r, err)

		return
	}

	s.audit(r, caller, "database.deleted", "database", database.Name, nil)

	w.WriteHeader(http.StatusNoContent)
}

// handleDatabaseUsers adds a MySQL login and the databases it may use.
func (s *Server) handleDatabaseUsers(w http.ResponseWriter, r *http.Request) {
	caller, ok := s.caller(w, r)
	if !ok {
		return
	}

	if r.Method == http.MethodGet {
		users, err := s.store.DatabaseUsers(r.Context(), &caller)
		if err != nil {
			s.failStore(w, r, err)

			return
		}

		writeJSON(w, http.StatusOK, map[string]any{"users": users})

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

	name, ok := s.databaseName(w, account, request.Suffix)
	if !ok {
		return
	}

	password := request.Password
	if password == "" {
		password = generatePassword(20)
	} else if err := rules.Password(password); err != nil {
		failFields(w, http.StatusUnprocessableEntity, codeInvalid, "That password cannot be used.", map[string]string{
			"password": err.Error(),
		})

		return
	}

	grants, ok := s.grantableDatabases(w, r, account, request.Databases)
	if !ok {
		return
	}

	user, err := s.store.SaveDatabaseUser(r.Context(), account.ID, name, mysqlNativeHash(password), grants)
	if err != nil {
		s.failStore(w, r, err)

		return
	}

	if _, err := s.queueFor(r, caller, account, "db.apply_user", map[string]any{
		"username":      account.Username,
		"user":          name,
		"password_hash": mysqlNativeHash(password),
		"databases":     user.Databases,
	}); err != nil {
		s.failStore(w, r, err)

		return
	}

	s.audit(r, caller, "database.user_created", "database_user", name, map[string]any{
		"databases": user.Databases,
	})

	writeJSON(w, http.StatusCreated, map[string]any{
		"user":     user,
		"password": password,
	})
}

// grantableDatabases checks that every id belongs to the account.
func (s *Server) grantableDatabases(w http.ResponseWriter, r *http.Request, account store.User, ids []int64) ([]int64, bool) {
	allowed := []int64{}

	for _, id := range ids {
		database, err := s.store.DatabaseByID(r.Context(), id)

		if err != nil || database.UserID != account.ID {
			fail(w, http.StatusUnprocessableEntity, codeInvalid, "One of those databases is not this account's.")

			return nil, false
		}

		allowed = append(allowed, id)
	}

	return allowed, true
}

// handleDatabaseUser changes a login's password or deletes it.
func (s *Server) handleDatabaseUser(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(w, r, "id")
	if !ok {
		return
	}

	user, err := s.store.DatabaseUserByID(r.Context(), id)
	if err != nil {
		fail(w, http.StatusNotFound, codeNotFound, "There is no database user by that id.")

		return
	}

	account, ok := s.visibleAccount(w, r, user.Username)
	if !ok {
		return
	}

	caller, _ := s.caller(w, r)

	switch r.Method {
	case http.MethodPatch:
		var request struct {
			Password  string  `json:"password"`
			Databases []int64 `json:"databases"`
		}

		if !decodeJSON(w, r, &request) {
			return
		}

		grants, ok := s.grantableDatabases(w, r, account, request.Databases)
		if !ok {
			return
		}

		password := request.Password
		if password == "" {
			password = generatePassword(20)
		} else if err := rules.Password(password); err != nil {
			failFields(w, http.StatusUnprocessableEntity, codeInvalid, "That password cannot be used.", map[string]string{
				"password": err.Error(),
			})

			return
		}

		hash := mysqlNativeHash(password)

		updated, err := s.store.SaveDatabaseUser(r.Context(), account.ID, user.Name, hash, grants)
		if err != nil {
			s.failStore(w, r, err)

			return
		}

		if _, err := s.queueFor(r, caller, account, "db.apply_user", map[string]any{
			"username":      account.Username,
			"user":          user.Name,
			"password_hash": hash,
			"databases":     updated.Databases,
		}); err != nil {
			s.failStore(w, r, err)

			return
		}

		s.audit(r, caller, "database.user_updated", "database_user", user.Name, nil)

		writeJSON(w, http.StatusOK, map[string]any{"user": updated, "password": password})

	case http.MethodDelete:
		if _, err := s.queueFor(r, caller, account, "db.delete_user", map[string]any{
			"username": account.Username,
			"user":     user.Name,
		}); err != nil {
			s.failStore(w, r, err)

			return
		}

		if err := s.store.DeleteDatabaseUser(r.Context(), id); err != nil {
			s.failStore(w, r, err)

			return
		}

		s.audit(r, caller, "database.user_deleted", "database_user", user.Name, nil)

		w.WriteHeader(http.StatusNoContent)
	}
}
