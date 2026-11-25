package httpapi

import (
	"net/http"
	"path"
	"strings"

	"rayxo/panel/internal/auth"
	"rayxo/panel/internal/rules"
	"rayxo/panel/internal/store"
)

// --- FTP ------------------------------------------------------------------

// handleFTP lists FTP logins or adds one.
func (s *Server) handleFTP(w http.ResponseWriter, r *http.Request) {
	caller, ok := s.caller(w, r)
	if !ok {
		return
	}

	switch r.Method {
	case http.MethodGet:
		accounts, err := s.store.FTPAccounts(r.Context(), &caller)
		if err != nil {
			s.failStore(w, r, err)

			return
		}

		writeJSON(w, http.StatusOK, map[string]any{"accounts": accounts})

	case http.MethodPost:
		var request struct {
			Username  string `json:"username"`
			Name      string `json:"name"`
			DomainID  int64  `json:"domainId"`
			Directory string `json:"directory"`
			Password  string `json:"password"`
			QuotaMB   *int64 `json:"quotaMb"`
		}

		if !decodeJSON(w, r, &request) {
			return
		}

		account, ok := s.visibleAccount(w, r, request.Username)
		if !ok {
			return
		}

		domain, _, ok := s.ownedDomainByID(w, r, request.DomainID)
		if !ok {
			return
		}

		name := strings.ToLower(strings.TrimSpace(request.Name))
		directory := strings.Trim(strings.TrimSpace(request.Directory), "/")

		fields := map[string]string{}

		if !localPartPattern.MatchString(name) || len(name) > 32 {
			fields["name"] = "Use letters, digits, dots, dashes or underscores, up to 32 characters."
		}

		if !validRelativeDirectory(directory) {
			fields["directory"] = "A folder inside the account, with no .. anywhere."
		}

		password := request.Password
		if password == "" {
			password = generatePassword(18)
		} else if err := rules.Password(password); err != nil {
			fields["password"] = err.Error()
		}

		if request.QuotaMB != nil && (*request.QuotaMB < 0 || *request.QuotaMB > 1024*1024) {
			fields["quotaMb"] = "A quota is between 0 and 1048576 MB."
		}

		if len(fields) > 0 {
			failFields(w, http.StatusUnprocessableEntity, codeInvalid, "That FTP login cannot be created.", fields)

			return
		}

		hash, err := auth.HashCryptPassword(password)
		if err != nil {
			s.failStore(w, r, err)

			return
		}

		login := name + "@" + domain.Name

		created, err := s.store.CreateFTPAccount(r.Context(), account.ID, domain.ID, login, hash, directory, request.QuotaMB)
		if err != nil {
			s.failStore(w, r, err)

			return
		}

		// The view vsftpd reads only lists active logins, so the row is what
		// makes it work; the agent call only prepares the folder.
		if err := s.store.SetFTPStatus(r.Context(), created.ID, "active"); err != nil {
			s.failStore(w, r, err)

			return
		}

		if _, err := s.queueFor(r, caller, account, "ftp.ensure_dir", map[string]any{
			"username":  account.Username,
			"directory": directory,
			"quota":     request.QuotaMB != nil,
		}); err != nil {
			s.failStore(w, r, err)

			return
		}

		created.Status = "active"
		s.audit(r, caller, "ftp.created", "ftp", login, map[string]any{"directory": directory})

		writeJSON(w, http.StatusCreated, map[string]any{
			"account":  created,
			"password": password,
			"login":    login,
			"host":     s.cfg.PublicIP,
		})
	}
}

// validRelativeDirectory accepts "" or a path of plain segments.
func validRelativeDirectory(directory string) bool {
	if directory == "" {
		return true
	}

	if len(directory) > 200 {
		return false
	}

	for _, segment := range strings.Split(directory, "/") {
		if segment == "" || segment == "." || segment == ".." {
			return false
		}

		if strings.ContainsAny(segment, "\\:*?\"<>|") {
			return false
		}
	}

	return true
}

// handleFTPAccount changes one login's password or quota, or deletes it.
func (s *Server) handleFTPAccount(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(w, r, "id")
	if !ok {
		return
	}

	ftp, err := s.store.FTPAccountByID(r.Context(), id)
	if err != nil {
		fail(w, http.StatusNotFound, codeNotFound, "There is no FTP login by that id.")

		return
	}

	account, ok := s.visibleAccount(w, r, ftp.Username)
	if !ok {
		return
	}

	caller, _ := s.caller(w, r)

	switch r.Method {
	case http.MethodPatch:
		var request struct {
			Password string `json:"password"`
			QuotaMB  *int64 `json:"quotaMb"`
			Status   string `json:"status"`
		}

		if !decodeJSON(w, r, &request) {
			return
		}

		password := request.Password
		if password == "" {
			password = generatePassword(18)
		} else if err := rules.Password(password); err != nil {
			failFields(w, http.StatusUnprocessableEntity, codeInvalid, "That password cannot be used.", map[string]string{
				"password": err.Error(),
			})

			return
		}

		hash, err := auth.HashCryptPassword(password)
		if err != nil {
			s.failStore(w, r, err)

			return
		}

		status := ftp.Status
		if request.Status == "suspended" {
			status = "suspended"
		} else if request.Status == "active" {
			status = "active"
		}

		if err := s.store.UpdateFTPAccount(r.Context(), id, hash, request.QuotaMB, status); err != nil {
			s.failStore(w, r, err)

			return
		}

		s.audit(r, caller, "ftp.updated", "ftp", ftp.Login, nil)

		writeJSON(w, http.StatusOK, map[string]any{"password": password})

	case http.MethodDelete:
		if err := s.store.DeleteFTPAccount(r.Context(), id); err != nil {
			s.failStore(w, r, err)

			return
		}

		s.audit(r, caller, "ftp.deleted", "ftp", ftp.Login, map[string]any{"username": account.Username})

		w.WriteHeader(http.StatusNoContent)
	}
}

// --- cron -----------------------------------------------------------------

// handleCron lists an account's jobs or adds one.
func (s *Server) handleCron(w http.ResponseWriter, r *http.Request) {
	account, ok := s.accountInPath(w, r)
	if !ok {
		return
	}

	caller, _ := s.caller(w, r)

	switch r.Method {
	case http.MethodGet:
		jobs, err := s.store.CronJobs(r.Context(), account.ID)
		if err != nil {
			s.failStore(w, r, err)

			return
		}

		writeJSON(w, http.StatusOK, map[string]any{"jobs": jobs, "mailTo": account.Email})

	case http.MethodPost:
		var request struct {
			Schedule    string `json:"schedule"`
			Command     string `json:"command"`
			Description string `json:"description"`
			Enabled     *bool  `json:"enabled"`
		}

		if !decodeJSON(w, r, &request) {
			return
		}

		schedule := strings.Join(strings.Fields(request.Schedule), " ")
		command := strings.TrimSpace(request.Command)
		enabled := true

		if request.Enabled != nil {
			enabled = *request.Enabled
		}

		fields := map[string]string{}

		if !cronSchedulePattern.MatchString(schedule) {
			fields["schedule"] = "Five fields (minute hour day month weekday) or @daily, @hourly, @weekly, @monthly, @yearly."
		}

		if command == "" || len(command) > 1024 {
			fields["command"] = "A command is required and may not exceed 1024 characters."
		}

		if len(request.Description) > 100 {
			fields["description"] = "Keep the description under 100 characters."
		}

		if len(fields) > 0 {
			failFields(w, http.StatusUnprocessableEntity, codeInvalid, "That job cannot be saved.", fields)

			return
		}

		job, err := s.store.CreateCronJob(r.Context(), account.ID, schedule, command, request.Description, enabled)
		if err != nil {
			s.failStore(w, r, err)

			return
		}

		if err := s.applyCron(r, caller, account); err != nil {
			s.failStore(w, r, err)

			return
		}

		s.audit(r, caller, "cron.created", "cron", schedule, map[string]any{"command": command})

		writeJSON(w, http.StatusCreated, map[string]any{"job": job})
	}
}

// applyCron sends the account's whole crontab to the agent, which is how the
// crontab stays exactly what the panel lists.
func (s *Server) applyCron(r *http.Request, caller, account store.User) error {
	jobs, err := s.store.CronJobs(r.Context(), account.ID)
	if err != nil {
		return err
	}

	payload := make([]map[string]any, 0, len(jobs))

	for _, job := range jobs {
		if !job.Enabled {
			continue
		}

		payload = append(payload, map[string]any{
			"schedule": job.Schedule,
			"command":  job.Command,
		})
	}

	_, err = s.queueFor(r, caller, account, "cron.apply", map[string]any{
		"username": account.Username,
		"jobs":     payload,
		"mail_to":  account.Email,
	})

	return err
}

// handleCronJob changes or removes one job.
func (s *Server) handleCronJob(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(w, r, "id")
	if !ok {
		return
	}

	job, account, err := s.store.CronJobByID(r.Context(), id)
	if err != nil {
		fail(w, http.StatusNotFound, codeNotFound, "There is no cron job by that id.")

		return
	}

	caller, ok := s.caller(w, r)
	if !ok {
		return
	}

	if !mayManage(caller, account) {
		fail(w, http.StatusNotFound, codeNotFound, "There is no cron job by that id.")

		return
	}

	switch r.Method {
	case http.MethodPatch:
		var request struct {
			Schedule    string `json:"schedule"`
			Command     string `json:"command"`
			Description string `json:"description"`
			Enabled     *bool  `json:"enabled"`
		}

		if !decodeJSON(w, r, &request) {
			return
		}

		schedule := strings.Join(strings.Fields(request.Schedule), " ")
		command := strings.TrimSpace(request.Command)
		enabled := job.Enabled

		if request.Enabled != nil {
			enabled = *request.Enabled
		}

		if !cronSchedulePattern.MatchString(schedule) || command == "" {
			failFields(w, http.StatusUnprocessableEntity, codeInvalid, "That job cannot be saved.", map[string]string{
				"schedule": "Use five fields or @daily.",
				"command":  "A command is required.",
			})

			return
		}

		if err := s.store.UpdateCronJob(r.Context(), id, schedule, command, request.Description, enabled); err != nil {
			s.failStore(w, r, err)

			return
		}

		if err := s.applyCron(r, caller, account); err != nil {
			s.failStore(w, r, err)

			return
		}

		s.audit(r, caller, "cron.updated", "cron", schedule, nil)

		queued(w, "cron.apply", nil)

	case http.MethodDelete:
		if err := s.store.DeleteCronJob(r.Context(), id); err != nil {
			s.failStore(w, r, err)

			return
		}

		if err := s.applyCron(r, caller, account); err != nil {
			s.failStore(w, r, err)

			return
		}

		s.audit(r, caller, "cron.deleted", "cron", job.Schedule, nil)

		w.WriteHeader(http.StatusNoContent)
	}
}

// --- backups --------------------------------------------------------------

// handleBackups lists the archives in an account's backups folder, starts a
// new one, restores one, or deletes one.
func (s *Server) handleBackups(w http.ResponseWriter, r *http.Request) {
	account, ok := s.accountInPath(w, r)
	if !ok {
		return
	}

	caller, _ := s.caller(w, r)

	switch r.Method {
	case http.MethodGet:
		answer, err := s.agent.Call(r.Context(), "fs.list", map[string]any{
			"username": account.Username,
			"path":     "backups",
		})
		if err != nil {
			// A folder that does not exist yet is not an error worth a banner:
			// it means the account has no backups, which is a normal state.
			writeJSON(w, http.StatusOK, map[string]any{"entries": []any{}})

			return
		}

		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_, _ = w.Write(answer)

	case http.MethodPost:
		var request struct {
			Name      string   `json:"name"`
			Files     bool     `json:"files"`
			Email     bool     `json:"email"`
			Databases []string `json:"databases"`
		}

		if !decodeJSON(w, r, &request) {
			return
		}

		name := strings.TrimSpace(request.Name)
		if name == "" {
			name = "backup-" + store.Now().Format("20060102-150405")
		}

		if strings.ContainsAny(name, "/\\ ") || len(name) > 64 {
			failFields(w, http.StatusUnprocessableEntity, codeInvalid, "That backup name cannot be used.", map[string]string{
				"name": "Use up to 64 characters, no slashes or spaces.",
			})

			return
		}

		if !request.Files && !request.Email && len(request.Databases) == 0 {
			request.Files = true
		}

		params := map[string]any{
			"username":  account.Username,
			"name":      name,
			"files":     request.Files,
			"email":     request.Email,
			"databases": request.Databases,
		}

		if _, err := s.queueFor(r, caller, account, "backup.create", params); err != nil {
			s.failStore(w, r, err)

			return
		}

		s.audit(r, caller, "backup.started", "account", account.Username, map[string]any{"name": name})

		queued(w, "backup.create", map[string]any{"name": name})

	case http.MethodDelete:
		name := path.Base(strings.TrimSpace(r.URL.Query().Get("name")))

		if name == "" || name == "." || name == "/" {
			fail(w, http.StatusUnprocessableEntity, codeInvalid, "Name the backup to delete.")

			return
		}

		_, err := s.agent.Call(r.Context(), "fs.delete", map[string]any{
			"username": account.Username,
			"paths":    []string{"backups/" + name},
		})
		if err != nil {
			s.failAgent(w, r, err)

			return
		}

		s.audit(r, caller, "backup.deleted", "account", account.Username, map[string]any{"name": name})

		w.WriteHeader(http.StatusNoContent)
	}
}

// handleBackupRestore puts an archive back, which the agent does through a
// queued task because it can take minutes.
func (s *Server) handleBackupRestore(w http.ResponseWriter, r *http.Request) {
	account, ok := s.accountInPath(w, r)
	if !ok {
		return
	}

	var request struct {
		Name      string   `json:"name"`
		Files     bool     `json:"files"`
		Email     bool     `json:"email"`
		Databases []string `json:"databases"`
	}

	if !decodeJSON(w, r, &request) {
		return
	}

	name := path.Base(strings.TrimSpace(request.Name))
	if name == "" || name == "." || name == "/" {
		fail(w, http.StatusUnprocessableEntity, codeInvalid, "Name the backup to restore.")

		return
	}

	if !request.Files && !request.Email && len(request.Databases) == 0 {
		request.Files = true
	}

	caller, _ := s.caller(w, r)

	if _, err := s.queueFor(r, caller, account, "backup.restore", map[string]any{
		"username":  account.Username,
		"name":      name,
		"files":     request.Files,
		"email":     request.Email,
		"databases": request.Databases,
	}); err != nil {
		s.failStore(w, r, err)

		return
	}

	s.audit(r, caller, "backup.restore_started", "account", account.Username, map[string]any{"name": name})

	queued(w, "backup.restore", map[string]any{"name": name})
}
