package httpapi

import (
	"encoding/json"
	"net/http"
	"strings"

	"rayxo/panel/internal/rules"
	"rayxo/panel/internal/store"
)

// handleServer reports what the machine is doing: the services the panel
// manages, the firewall, and the settings the panel is allowed to change.
func (s *Server) handleServer(w http.ResponseWriter, r *http.Request) {
	caller, ok := s.caller(w, r)
	if !ok {
		return
	}

	if caller.Role != store.RoleAdmin {
		fail(w, http.StatusForbidden, codeForbidden, "Only an administrator can see the server.")

		return
	}

	// Each answer is optional: a fake driver or an older agent may not have
	// every method, and one missing section must not blank the page.
	services, servicesErr := s.agentRaw(r, "service.status", map[string]any{})
	firewall, firewallErr := s.agentRaw(r, "firewall.status", map[string]any{})
	settings, settingsErr := s.agentRaw(r, "system.settings", map[string]any{})
	bfm, _ := s.agentRaw(r, "bfm.status", map[string]any{})

	problems := map[string]string{}

	if servicesErr != nil {
		problems["services"] = servicesErr.Error()
	}

	if firewallErr != nil {
		problems["firewall"] = firewallErr.Error()
	}

	if settingsErr != nil {
		problems["settings"] = settingsErr.Error()
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"services": services,
		"firewall": firewall,
		"settings": settings,
		"bfm":      bfm,
		"problems": problems,
	})
}

// agentRaw calls the agent and returns the parsed answer, or the error.
func (s *Server) agentRaw(r *http.Request, method string, params map[string]any) (any, error) {
	answer, err := s.agent.Call(r.Context(), method, params)
	if err != nil {
		return nil, err
	}

	if len(answer) == 0 {
		return nil, nil
	}

	var parsed any
	if err := json.Unmarshal(answer, &parsed); err != nil {
		return nil, err
	}

	return parsed, nil
}

// handleServiceAction starts, stops or restarts one managed service.
func (s *Server) handleServiceAction(w http.ResponseWriter, r *http.Request) {
	caller, ok := s.caller(w, r)
	if !ok {
		return
	}

	if caller.Role != store.RoleAdmin {
		fail(w, http.StatusForbidden, codeForbidden, "Only an administrator can change a service.")

		return
	}

	var request struct {
		Unit   string `json:"unit"`
		Action string `json:"action"`
	}

	if !decodeJSON(w, r, &request) {
		return
	}

	unit := strings.TrimSpace(request.Unit)

	switch request.Action {
	case "start", "stop", "restart", "reload":
	default:
		failFields(w, http.StatusUnprocessableEntity, codeInvalid, "That service action is not allowed.", map[string]string{
			"action": "Use start, stop, restart or reload.",
		})

		return
	}

	if unit == "" {
		fail(w, http.StatusUnprocessableEntity, codeInvalid, "Name the service.")

		return
	}

	if _, err := s.agent.Call(r.Context(), "service.action", map[string]any{
		"unit":   unit,
		"action": request.Action,
	}); err != nil {
		s.failAgent(w, r, err)

		return
	}

	s.audit(r, caller, "service."+request.Action, "service", unit, nil)

	writeJSON(w, http.StatusOK, map[string]any{"unit": unit, "action": request.Action})
}

// handleServerSettings renames the machine or moves its clock.
func (s *Server) handleServerSettings(w http.ResponseWriter, r *http.Request) {
	caller, ok := s.caller(w, r)
	if !ok {
		return
	}

	if caller.Role != store.RoleAdmin {
		fail(w, http.StatusForbidden, codeForbidden, "Only an administrator can change the server.")

		return
	}

	var request struct {
		Hostname string `json:"hostname"`
		Timezone string `json:"timezone"`
	}

	if !decodeJSON(w, r, &request) {
		return
	}

	if request.Hostname == "" && request.Timezone == "" {
		fail(w, http.StatusUnprocessableEntity, codeInvalid, "There is nothing to change.")

		return
	}

	if request.Hostname != "" {
		// The agent validates a hostname with the same rule it uses for a
		// domain, because a hostname is one.
		if err := rules.Domain(request.Hostname); err != nil {
			failFields(w, http.StatusUnprocessableEntity, codeInvalid, "That hostname cannot be used.", map[string]string{
				"hostname": err.Error(),
			})

			return
		}

		if _, err := s.agent.Call(r.Context(), "system.change_hostname", map[string]any{
			"hostname": request.Hostname,
		}); err != nil {
			s.failAgent(w, r, err)

			return
		}

		s.audit(r, caller, "server.hostname", "server", request.Hostname, nil)
	}

	if request.Timezone != "" {
		if _, err := s.agent.Call(r.Context(), "system.set_timezone", map[string]any{
			"timezone": request.Timezone,
		}); err != nil {
			s.failAgent(w, r, err)

			return
		}

		s.audit(r, caller, "server.timezone", "server", request.Timezone, nil)
	}

	writeJSON(w, http.StatusOK, map[string]any{"hostname": request.Hostname, "timezone": request.Timezone})
}

// handleUsage reports bandwidth and resource usage for the accounts a caller
// may see.
func (s *Server) handleUsage(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.caller(w, r); !ok {
		return
	}

	bandwidth, bandwidthErr := s.agentRaw(r, "usage.bandwidth", map[string]any{})
	resources, _ := s.agentRaw(r, "usage.resources", map[string]any{})
	quota, _ := s.agentRaw(r, "quota.report", map[string]any{})

	problems := map[string]string{}

	if bandwidthErr != nil {
		problems["bandwidth"] = bandwidthErr.Error()
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"bandwidth": bandwidth,
		"resources": resources,
		"quota":     quota,
		"problems":  problems,
	})
}
