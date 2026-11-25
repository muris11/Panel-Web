package httpapi

import (
	"errors"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"muriscore/panel/internal/sshkey"
	"muriscore/panel/internal/store"
)

// keyNamePattern is what a key may be called: something a person can read back.
var keyNamePattern = regexp.MustCompile(`^[\p{L}\p{N} ._-]{1,100}$`)

// handleSSH reports an account's SSH access and lets it be turned on or off.
//
// Every account has SFTP; a shell is a package feature. The panel records the
// request and the agent does the work (account.set_ssh), because whether a
// shell exists is a fact about the server, not about this database.
func (s *Server) handleSSH(w http.ResponseWriter, r *http.Request) {
	caller, ok := s.caller(w, r)
	if !ok {
		return
	}

	switch r.Method {
	case http.MethodGet:
		s.showSSH(w, r, caller)

	case http.MethodPatch:
		var request struct {
			Username string `json:"username"`
			Enabled  *bool  `json:"enabled"`
		}

		if !decodeJSON(w, r, &request) {
			return
		}

		if request.Enabled == nil {
			failFields(w, http.StatusUnprocessableEntity, codeInvalid, "Say whether SSH should be on or off.", map[string]string{
				"enabled": "This is required.",
			})

			return
		}

		account, ok := s.sshAccount(w, r, caller, request.Username)
		if !ok {
			return
		}

		s.setSSHEnabled(w, r, caller, account, *request.Enabled)
	}
}

// showSSH answers the SSH page: access, connection details and keys.
func (s *Server) showSSH(w http.ResponseWriter, r *http.Request, caller store.User) {
	account, ok := s.sshAccount(w, r, caller, r.URL.Query().Get("username"))
	if !ok {
		return
	}

	enabled, err := s.store.SSHEnabled(r.Context(), account.ID)
	if err != nil {
		s.failStore(w, r, err)

		return
	}

	allowed, err := s.store.AllowsSSH(r.Context(), account.ID)
	if err != nil {
		s.failStore(w, r, err)

		return
	}

	keys, err := s.store.SSHKeys(r.Context(), account.ID)
	if err != nil {
		s.failStore(w, r, err)

		return
	}

	pending, failure, err := s.sshTaskState(r, account.ID)
	if err != nil {
		s.failStore(w, r, err)

		return
	}

	host := s.cfg.PublicIP
	if host == "" {
		host = "this server"
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"account": account.Username,
		"access": map[string]any{
			"allowed": allowed,
			"enabled": enabled,
			"active":  enabled && allowed && account.Status == store.StatusActive,
			"pending": pending,
			"error":   failure,
		},
		"connection": map[string]any{
			"host":        host,
			"port":        22,
			"username":    account.Username,
			"home":        "/home/" + account.Username,
			"passwordSet": s.store.SSHPasswordSet(r.Context(), account.ID),
		},
		"keys":    keys,
		"maxKeys": store.MaxSSHKeys,
	})
}

// setSSHEnabled records the request and queues the agent call.
func (s *Server) setSSHEnabled(w http.ResponseWriter, r *http.Request, caller, account store.User, enabled bool) {
	if account.Role != store.RoleUser {
		fail(w, http.StatusUnprocessableEntity, codeInvalid, "SSH belongs to hosting accounts, not to panel logins.")

		return
	}

	if account.Status != store.StatusActive {
		failFields(w, http.StatusConflict, codeConflict, "SSH can only be changed while the account is active.", map[string]string{
			"enabled": "This account is " + string(account.Status) + ".",
		})

		return
	}

	if enabled {
		allowed, err := s.store.AllowsSSH(r.Context(), account.ID)
		if err != nil {
			s.failStore(w, r, err)

			return
		}

		if !allowed {
			failFields(w, http.StatusForbidden, codeForbidden, "SSH is not included in this account's package.", map[string]string{
				"enabled": "Ask for a package that includes SSH.",
			})

			return
		}
	}

	current, err := s.store.SSHEnabled(r.Context(), account.ID)
	if err != nil {
		s.failStore(w, r, err)

		return
	}

	// Asking for what is already true is not a change, so it queues nothing.
	if current == enabled {
		writeJSON(w, http.StatusOK, map[string]any{"enabled": current, "queued": false})

		return
	}

	if err := s.store.SetSSHEnabled(r.Context(), account.ID, enabled); err != nil {
		s.failStore(w, r, err)

		return
	}

	taskID, err := s.queueFor(r, caller, account, "account.set_ssh", map[string]any{
		"username": account.Username,
		"enabled":  enabled,
	})
	if err != nil {
		s.failStore(w, r, err)

		return
	}

	action := "account.ssh_disabled"
	if enabled {
		action = "account.ssh_enabled"
	}

	s.audit(r, caller, action, "account", account.Username, map[string]any{"username": account.Username})

	writeJSON(w, http.StatusAccepted, map[string]any{"enabled": enabled, "queued": true, "task": taskID})
}

// handleSSHKeys adds a key to an account and queues writing authorized_keys.
func (s *Server) handleSSHKeys(w http.ResponseWriter, r *http.Request) {
	caller, ok := s.caller(w, r)
	if !ok {
		return
	}

	var request struct {
		Username  string `json:"username"`
		Name      string `json:"name"`
		PublicKey string `json:"publicKey"`
	}

	if !decodeJSON(w, r, &request) {
		return
	}

	account, ok := s.sshAccount(w, r, caller, request.Username)
	if !ok {
		return
	}

	if account.Role != store.RoleUser || account.Status != store.StatusActive {
		fail(w, http.StatusConflict, codeConflict, "SSH keys can only be added while the account is active.")

		return
	}

	name := strings.TrimSpace(request.Name)
	if name == "" || len([]rune(name)) > 100 {
		failFields(w, http.StatusUnprocessableEntity, codeInvalid, "Give the key a name of at most 100 characters.", map[string]string{
			"name": "A name is required, at most 100 characters.",
		})

		return
	}

	if !keyNamePattern.MatchString(name) {
		failFields(w, http.StatusUnprocessableEntity, codeInvalid, "That name has characters the panel does not allow.", map[string]string{
			"name": "Letters, digits, spaces, dots, dashes and underscores only.",
		})

		return
	}

	key, err := sshkey.Parse(request.PublicKey)
	if err != nil {
		field := "publicKey"

		if errors.Is(err, sshkey.ErrPrivateKey) {
			field = "publicKey"
		}

		failFields(w, http.StatusUnprocessableEntity, codeInvalid, err.Error(), map[string]string{field: err.Error()})

		return
	}

	added, err := s.store.AddSSHKey(r.Context(), account.ID, name, key.Line(), key.Fingerprint())
	if err != nil {
		switch {
		case errors.Is(err, store.ErrKeyExists):
			failFields(w, http.StatusConflict, codeConflict, "That key is already added.", map[string]string{
				"publicKey": "This key is on the account already.",
			})
		case errors.Is(err, store.ErrTooManyKeys):
			failFields(w, http.StatusConflict, codeConflict, "An account may have at most twenty keys.", map[string]string{
				"publicKey": "Remove a key before adding another.",
			})
		default:
			s.failStore(w, r, err)
		}

		return
	}

	if err := s.queueSSHKeys(r, caller, account); err != nil {
		s.failStore(w, r, err)

		return
	}

	s.audit(r, caller, "ssh_key.added", "ssh_key", key.Fingerprint(), map[string]any{
		"username":    account.Username,
		"name":        name,
		"fingerprint": key.Fingerprint(),
	})

	writeJSON(w, http.StatusCreated, map[string]any{"key": added})
}

// handleSSHKey removes one key and queues writing authorized_keys again.
func (s *Server) handleSSHKey(w http.ResponseWriter, r *http.Request) {
	caller, ok := s.caller(w, r)
	if !ok {
		return
	}

	id, ok := parseID(w, r, "id")
	if !ok {
		return
	}

	ownerID, err := s.store.SSHKeyOwner(r.Context(), id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			fail(w, http.StatusNotFound, codeNotFound, "There is no SSH key by that id.")
		} else {
			s.failStore(w, r, err)
		}

		return
	}

	account, ok := s.visibleAccountByID(w, r, ownerID)
	if !ok {
		return
	}

	if account.Status != store.StatusActive {
		fail(w, http.StatusConflict, codeConflict, "SSH keys can only be removed while the account is active.")

		return
	}

	if err := s.store.DeleteSSHKey(r.Context(), account.ID, id); err != nil {
		s.failStore(w, r, err)

		return
	}

	if err := s.queueSSHKeys(r, caller, account); err != nil {
		s.failStore(w, r, err)

		return
	}

	s.audit(r, caller, "ssh_key.removed", "ssh_key", strconv.FormatInt(id, 10), map[string]any{"username": account.Username})

	w.WriteHeader(http.StatusNoContent)
}

// queueSSHKeys sends every key of the account to the agent, because
// authorized_keys is written whole.
func (s *Server) queueSSHKeys(r *http.Request, caller, account store.User) error {
	keys, err := s.store.SSHKeys(r.Context(), account.ID)
	if err != nil {
		return err
	}

	lines := make([]string, len(keys))
	for i, key := range keys {
		lines[i] = key.PublicKey
	}

	_, err = s.queueFor(r, caller, account, "account.ssh_keys", map[string]any{
		"username": account.Username,
		"keys":     lines,
	})

	return err
}

// sshTaskState reports whether the last SSH change is still running, and why it
// failed when it did.
func (s *Server) sshTaskState(r *http.Request, userID int64) (bool, string, error) {
	task, found, err := s.store.LatestTaskFor(r.Context(), userID, "account.set_ssh", "account.ssh_keys")
	if err != nil || !found {
		return false, "", err
	}

	switch task.Status {
	case store.TaskQueued, store.TaskRunning:
		return true, "", nil
	case store.TaskFailed:
		return false, task.Error, nil
	}

	return false, "", nil
}

// sshAccount resolves which account the page is about. A hosting account is
// its own default; an operator or a reseller has to name one, because SSH is a
// property of a hosting account and not of a panel login.
func (s *Server) sshAccount(w http.ResponseWriter, r *http.Request, caller store.User, username string) (store.User, bool) {
	name := firstNonEmpty(username)

	if name == "" && caller.Role == store.RoleUser {
		name = caller.Username
	}

	if name == "" {
		fail(w, http.StatusUnprocessableEntity, codeInvalid, "Name the account whose SSH you mean.")

		return store.User{}, false
	}

	return s.visibleAccount(w, r, name)
}

// firstNonEmpty picks the first value that has something in it.
func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}

	return ""
}
