package httpapi

import (
	"encoding/json"
	"net/http"
	"time"

	"muriscore/panel/internal/store"
)

// formatTimePtr formats an optional timestamp the way every other payload
// does, so the SPA parses one date format and no others.
func formatTimePtr(value *time.Time) *string {
	if value == nil {
		return nil
	}

	formatted := value.UTC().Format("2006-01-02T15:04:05Z")

	return &formatted
}

// handleDashboard answers the one request the landing page makes: live
// counts, the server's own numbers, and the work in flight.
func (s *Server) handleDashboard(w http.ResponseWriter, r *http.Request) {
	user, ok := userFrom(r.Context())
	if !ok {
		fail(w, http.StatusUnauthorized, codeUnauthorized, "Sign in to continue.")

		return
	}

	stats, err := s.store.Stats(r.Context(), &user)
	if err != nil {
		s.failStore(w, r, err)

		return
	}

	tasks, err := s.store.Tasks(r.Context(), store.TaskFilter{Limit: 8})
	if err != nil {
		s.failStore(w, r, err)

		return
	}

	taskPayload := make([]map[string]any, 0, len(tasks))
	for _, task := range tasks {
		taskPayload = append(taskPayload, map[string]any{
			"id":         task.ID,
			"action":     task.Action,
			"username":   task.Username,
			"status":     string(task.Status),
			"error":      task.Error,
			"createdAt":  task.CreatedAt.UTC().Format("2006-01-02T15:04:05Z"),
			"finishedAt": formatTimePtr(task.FinishedAt),
		})
	}

	// The agent's numbers are worth showing but not worth failing the page
	// for: a panel that cannot reach rayxod should still say how many
	// accounts it has, and say plainly that the server is unreachable.
	system, agentErr := s.systemInfo(r)

	body := map[string]any{
		"stats": map[string]any{
			"accounts":    stats.Accounts,
			"active":      stats.ActiveAccounts,
			"suspended":   stats.Suspended,
			"pending":     stats.Pending,
			"domains":     stats.Domains,
			"resellers":   stats.Resellers,
			"tasksFailed": stats.TasksFailed,
			"tasksQueued": stats.TasksQueued,
		},
		"tasks": taskPayload,
		"agent": s.agent.Name(),
	}

	if agentErr != nil {
		body["agentError"] = agentErr.Error()
		body["system"] = nil
	} else {
		body["system"] = system
	}

	writeJSON(w, http.StatusOK, body)
}

// systemInfo asks the agent for the host's numbers.
func (s *Server) systemInfo(r *http.Request) (json.RawMessage, error) {
	return s.agent.Call(r.Context(), "system.info", nil)
}

func (s *Server) handleSystemInfo(w http.ResponseWriter, r *http.Request) {
	system, err := s.systemInfo(r)
	if err != nil {
		fail(w, http.StatusBadGateway, codeServerError, "The server agent did not answer: "+err.Error())

		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"system": system})
}

// handleTasks lists the activity log. A hosting account sees its own work; an
// administrator or reseller sees what it manages.
func (s *Server) handleTasks(w http.ResponseWriter, r *http.Request) {
	user, ok := userFrom(r.Context())
	if !ok {
		fail(w, http.StatusUnauthorized, codeUnauthorized, "Sign in to continue.")

		return
	}

	limit, offset := page(r)

	filter := store.TaskFilter{Limit: limit, Offset: offset}

	if !user.IsReseller() {
		filter.UserID = &user.ID
	}

	if status := r.URL.Query().Get("status"); status != "" {
		filter.Status = store.TaskStatus(status)
	}

	tasks, err := s.store.Tasks(r.Context(), filter)
	if err != nil {
		s.failStore(w, r, err)

		return
	}

	payload := make([]map[string]any, 0, len(tasks))
	for _, task := range tasks {
		payload = append(payload, map[string]any{
			"id":         task.ID,
			"action":     task.Action,
			"username":   task.Username,
			"status":     string(task.Status),
			"error":      task.Error,
			"output":     task.Output,
			"attempts":   task.Attempts,
			"createdAt":  task.CreatedAt.UTC().Format("2006-01-02T15:04:05Z"),
			"startedAt":  formatTimePtr(task.StartedAt),
			"finishedAt": formatTimePtr(task.FinishedAt),
		})
	}

	writeJSON(w, http.StatusOK, map[string]any{"tasks": payload, "limit": limit, "offset": offset})
}

// handlePackages lists the plans the caller may hand out.
func (s *Server) handlePackages(w http.ResponseWriter, r *http.Request) {
	user, ok := userFrom(r.Context())
	if !ok {
		fail(w, http.StatusUnauthorized, codeUnauthorized, "Sign in to continue.")

		return
	}

	packages, err := s.store.Packages(r.Context(), &user)
	if err != nil {
		s.failStore(w, r, err)

		return
	}

	payload := make([]map[string]any, 0, len(packages))
	for _, pkg := range packages {
		payload = append(payload, map[string]any{
			"id":            pkg.ID,
			"name":          pkg.Name,
			"description":   pkg.Description,
			"diskMb":        pkg.DiskMB,
			"bandwidthMb":   pkg.BandwidthMB,
			"domains":       pkg.Domains,
			"databases":     pkg.Databases,
			"emailAccounts": pkg.EmailAccounts,
			"ftpAccounts":   pkg.FTPAccounts,
			"cronJobs":      pkg.CronJobs,
			"subdomains":    pkg.Subdomains,
		})
	}

	writeJSON(w, http.StatusOK, map[string]any{"packages": payload})
}

// handleHealth is what a load balancer or a backup script pings: it says the
// panel is up and whether it can reach the database and the agent.
func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := timeLimited(r, 2*time.Second)
	defer cancel()

	database := "ok"
	if err := s.store.Pool().PingContext(ctx); err != nil {
		database = "unreachable"
	}

	agentStatus := "fake"
	if s.agent.Name() == "socket" {
		agentStatus = "unreachable"

		if _, err := s.agent.Call(ctx, "agent.ping", nil); err == nil {
			agentStatus = "ok"
		}
	}

	status := http.StatusOK
	if database != "ok" || agentStatus == "unreachable" {
		status = http.StatusServiceUnavailable
	}

	writeJSON(w, status, map[string]any{
		"panel":    "ok",
		"database": database,
		"agent":    agentStatus,
	})
}
