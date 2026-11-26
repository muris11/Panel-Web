package httpapi

import (
	"errors"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"muriscore/panel/internal/store"
)

// nodeEntryPattern keeps the entry a relative path inside the document root:
// the same shape the agent enforces, checked here so the drawer can say so
// before a task is queued.
var nodeEntryPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_./-]*$`)

// nodeEnvKeyPattern matches an environment variable name.
var nodeEnvKeyPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// handleNodeApps lists, creates, restarts and removes the Node.js apps an
// operator may see. Every change is a queued agent call; the row only records
// intent, the agent owns the process.
func (s *Server) handleNodeApps(w http.ResponseWriter, r *http.Request) {
	caller, ok := s.caller(w, r)
	if !ok {
		return
	}

	switch r.Method {
	case http.MethodGet:
		query := strings.TrimSpace(r.URL.Query().Get("q"))

		apps, err := s.store.NodeApps(r.Context(), &caller, query)
		if err != nil {
			s.failStore(w, r, err)

			return
		}

		writeJSON(w, http.StatusOK, map[string]any{"apps": apps})

	case http.MethodPost:
		var request struct {
			DomainID int64             `json:"domainId"`
			Entry    string            `json:"entry"`
			Env      map[string]string `json:"env"`
		}

		if !decodeJSON(w, r, &request) {
			return
		}

		domain, account, ok := s.ownedDomainByID(w, r, request.DomainID)
		if !ok {
			return
		}

		entry := strings.TrimSpace(request.Entry)
		if entry == "" || !nodeEntryPattern.MatchString(entry) || strings.Contains(entry, "..") {
			fail(w, http.StatusUnprocessableEntity, codeInvalid,
				"The entry must be a relative path from the document root, like app.js or dist/server.js.")

			return
		}

		env, ok := nodeEnv(w, request.Env)
		if !ok {
			return
		}

		app, err := s.store.CreateNodeApp(r.Context(), domain.ID, entry, env)
		if err != nil {
			switch {
			case errors.Is(err, store.ErrNodeAppExists):
				fail(w, http.StatusConflict, codeConflict, "This website already runs a Node.js app; one app per website.")
			case errors.Is(err, store.ErrNoFreePort):
				fail(w, http.StatusConflict, codeConflict, "Every port in the Node.js window is taken.")
			default:
				s.failStore(w, r, err)
			}

			return
		}

		if _, err := s.queueFor(r, caller, account, "nodeapp.provision", map[string]any{
			"username": account.Username,
			"domain":   domain.Name,
			"entry":    entry,
			"env":      env,
			"port":     app.Port,
		}); err != nil {
			s.failStore(w, r, err)

			return
		}

		_ = s.store.Audit(r.Context(), &caller.ID, "nodeapp.created", "domain", domain.Name,
			s.clientIP(r), userAgent(r), map[string]any{"entry": entry, "port": app.Port})

		writeJSON(w, http.StatusCreated, map[string]any{"app": app, "taskId": ""})

	case http.MethodDelete:
		id, ok := nodeAppID(w, r)
		if !ok {
			return
		}

		app, err := s.store.NodeAppByID(r.Context(), id)
		if err != nil {
			s.failStore(w, r, err)

			return
		}

		if !caller.IsAdmin() && app.UserID != caller.ID {
			// Another operator's app is not announced, it simply is not here.
			fail(w, http.StatusNotFound, codeNotFound, "No such Node.js app.")

			return
		}

		if _, err := s.store.DeleteNodeApp(r.Context(), id); err != nil {
			s.failStore(w, r, err)

			return
		}

		if _, err := s.queueFor(r, caller, accountOf(app), "nodeapp.deprovision", map[string]any{
			"username": app.Username,
			"domain":   app.Domain,
		}); err != nil {
			s.failStore(w, r, err)

			return
		}

		_ = s.store.Audit(r.Context(), &caller.ID, "nodeapp.deleted", "domain", app.Domain,
			s.clientIP(r), userAgent(r), nil)

		writeJSON(w, http.StatusOK, map[string]any{"ok": true})

	default:
		fail(w, http.StatusMethodNotAllowed, codeInvalid, "Use GET, POST or DELETE.")
	}
}

// handleNodeAppAction restarts one app.
func (s *Server) handleNodeAppAction(w http.ResponseWriter, r *http.Request) {
	caller, ok := s.caller(w, r)
	if !ok {
		return
	}

	id, ok := nodeAppID(w, r)
	if !ok {
		return
	}

	app, err := s.store.NodeAppByID(r.Context(), id)
	if err != nil {
		s.failStore(w, r, err)

		return
	}

	if !caller.IsAdmin() && app.UserID != caller.ID {
		fail(w, http.StatusNotFound, codeNotFound, "No such Node.js app.")

		return
	}

	if _, err := s.queueFor(r, caller, accountOf(app), "nodeapp.restart", map[string]any{
		"username": app.Username,
		"domain":   app.Domain,
	}); err != nil {
		s.failStore(w, r, err)

		return
	}

	if err := s.store.SetNodeAppStatus(r.Context(), id, "pending"); err != nil {
		s.failStore(w, r, err)

		return
	}

	_ = s.store.Audit(r.Context(), &caller.ID, "nodeapp.restarted", "domain", app.Domain,
		s.clientIP(r), userAgent(r), nil)

	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handlePHPVersions answers with the PHP versions the agent has installed, so
// the drawers offer real choices instead of a free-text field.
func (s *Server) handlePHPVersions(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.caller(w, r); !ok {
		return
	}

	raw, err := s.agentRaw(r, "php.versions", map[string]any{})
	if err != nil {
		// An unreachable agent must not blank the drawer; it falls back to
		// free text there.
		writeJSON(w, http.StatusOK, map[string]any{"versions": []string{}, "agentError": err.Error()})

		return
	}

	writeJSON(w, http.StatusOK, raw)
}

// nodeEnv validates the environment map: names like shell variables, values
// one line each, at most forty of them.
func nodeEnv(w http.ResponseWriter, env map[string]string) (map[string]string, bool) {
	clean := map[string]string{}

	for key, value := range env {
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)

		if key == "" && value == "" {
			continue
		}

		if !nodeEnvKeyPattern.MatchString(key) {
			fail(w, http.StatusUnprocessableEntity, codeInvalid,
				"Environment names use letters, digits and underscores, and do not start with a digit.")

			return nil, false
		}

		if strings.ContainsAny(value, "\n\r") || len(value) > 4000 {
			fail(w, http.StatusUnprocessableEntity, codeInvalid,
				"Environment values are one line each, at most 4000 characters.")

			return nil, false
		}

		clean[key] = value
	}

	if len(clean) > 40 {
		fail(w, http.StatusUnprocessableEntity, codeInvalid, "At most 40 environment variables.")

		return nil, false
	}

	return clean, true
}

// nodeAppID reads the {id} path segment.
func nodeAppID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		fail(w, http.StatusNotFound, codeNotFound, "No such Node.js app.")

		return 0, false
	}

	return id, true
}

// accountOf turns a stored app back into the account a task is queued for.
func accountOf(app store.NodeApp) store.User {
	return store.User{ID: app.UserID, Username: app.Username}
}
