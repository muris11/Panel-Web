package httpapi

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"rayxo/panel/internal/rules"
	"rayxo/panel/internal/store"
)

// handleDomains lists every website the caller may see, or adds one.
func (s *Server) handleDomains(w http.ResponseWriter, r *http.Request) {
	caller, ok := s.caller(w, r)
	if !ok {
		return
	}

	switch r.Method {
	case http.MethodGet:
		query := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("q")))

		domains, err := s.store.AllDomains(r.Context(), &caller, query)
		if err != nil {
			s.failStore(w, r, err)

			return
		}

		writeJSON(w, http.StatusOK, map[string]any{
			"domains":    domains,
			"publicIp":   s.cfg.PublicIP,
			"nameserver": s.cfg.Nameservers,
		})

	case http.MethodPost:
		s.createDomain(w, r, caller)
	}
}

func (s *Server) createDomain(w http.ResponseWriter, r *http.Request, caller store.User) {
	var request struct {
		Username string `json:"username"`
		Name     string `json:"name"`
		Type     string `json:"type"`
	}

	if !decodeJSON(w, r, &request) {
		return
	}

	account, ok := s.visibleAccount(w, r, request.Username)
	if !ok {
		return
	}

	name := rules.NormalizeDomain(request.Name)
	if err := rules.Domain(name); err != nil {
		failFields(w, http.StatusUnprocessableEntity, codeInvalid, "That domain cannot be added.", map[string]string{
			"name": err.Error(),
		})

		return
	}

	domainType := request.Type
	if domainType == "" {
		domainType = "domain"
	}

	if domainType != "domain" && domainType != "subdomain" && domainType != "pointer" {
		failFields(w, http.StatusUnprocessableEntity, codeInvalid, "That domain cannot be added.", map[string]string{
			"type": "Type must be domain, subdomain or pointer.",
		})

		return
	}

	domain, err := s.store.CreateDomain(r.Context(), account.ID, name, domainType, "8.4", domainType == "domain")
	if err != nil {
		s.failStore(w, r, err)

		return
	}

	// A subdomain's records live in its parent's zone, so only a domain and a
	// pointer get a zone of their own.
	if domainType != "subdomain" {
		if _, err := s.store.CreateZone(r.Context(), account.ID, domain.ID, name); err != nil {
			s.failStore(w, r, err)

			return
		}

		s.writeDefaultRecords(r, account, name)
	}

	if _, err := s.queueFor(r, caller, account, "web.apply_domain", map[string]any{
		"username":      account.Username,
		"domain":        name,
		"php_version":   "8.4",
		"document_root": "",
	}); err != nil {
		s.failStore(w, r, err)

		return
	}

	if domainType != "subdomain" {
		s.syncZone(r, account, name)
	}

	s.audit(r, caller, "domain.added", "domain", name, map[string]any{"type": domainType})

	writeJSON(w, http.StatusCreated, map[string]any{"domain": domain})
}

// writeDefaultRecords fills a new zone with the four records every site needs:
// the address itself, www, mail, and an SPF line. A deployment that has not set
// an address or nameservers gets the records it can have.
func (s *Server) writeDefaultRecords(r *http.Request, account store.User, zone string) {
	zones, err := s.store.ZonesByAccount(r.Context(), nil, account.Username)
	if err != nil {
		return
	}

	var zoneID int64

	for _, candidate := range zones {
		if candidate.Name == zone {
			zoneID = candidate.ID
		}
	}

	if zoneID == 0 {
		return
	}

	ttl := int64(3600)

	if s.cfg.PublicIP != "" {
		_, _ = s.store.AddRecord(r.Context(), zoneID, "@", "A", s.cfg.PublicIP, ttl, nil)
		_, _ = s.store.AddRecord(r.Context(), zoneID, "www", "CNAME", zone+".", ttl, nil)
	}

	priority := int64(10)
	_, _ = s.store.AddRecord(r.Context(), zoneID, "@", "MX", "mail."+zone+".", ttl, &priority)
	_, _ = s.store.AddRecord(r.Context(), zoneID, "@", "TXT", "v=spf1 a mx ~all", ttl, nil)

	for _, nameserver := range s.cfg.Nameservers {
		_, _ = s.store.AddRecord(r.Context(), zoneID, "@", "NS", strings.TrimSuffix(nameserver, ".")+".", ttl, nil)
	}
}

// syncZone sends a whole zone to the agent, which is how the panel keeps
// PowerDNS exactly as its own table says.
func (s *Server) syncZone(r *http.Request, account store.User, zone string) {
	zones, err := s.store.ZonesByAccount(r.Context(), nil, account.Username)
	if err != nil {
		return
	}

	for _, candidate := range zones {
		if candidate.Name != zone {
			continue
		}

		records, err := s.store.RecordsByZone(r.Context(), candidate.ID)
		if err != nil {
			return
		}

		caller, ok := userFrom(r.Context())
		if !ok {
			return
		}

		payload := make([]map[string]any, 0, len(records))

		for _, record := range records {
			entry := map[string]any{
				"name":    record.Name,
				"type":    record.Type,
				"content": record.Content,
				"ttl":     record.TTL,
			}

			if record.Priority != nil {
				entry["priority"] = *record.Priority
			}

			payload = append(payload, entry)
		}

		_, _ = s.queueFor(r, caller, account, "dns.sync_zone", map[string]any{
			"username":    account.Username,
			"zone":        zone,
			"nameservers": s.cfg.Nameservers,
			"records":     payload,
		})
	}
}

// handleDomain reads one website, changes its web settings, or removes it.
func (s *Server) handleDomain(w http.ResponseWriter, r *http.Request) {
	domain, account, ok := s.ownedDomain(w, r)
	if !ok {
		return
	}

	caller, _ := s.caller(w, r)

	switch r.Method {
	case http.MethodGet:
		s.writeDomainDetail(w, r, domain, account)

	case http.MethodPatch:
		s.updateDomain(w, r, caller, domain, account)

	case http.MethodDelete:
		if _, err := s.queueFor(r, caller, account, "web.remove_domain", map[string]any{
			"username": account.Username,
			"domain":   domain.Name,
		}); err != nil {
			s.failStore(w, r, err)

			return
		}

		if err := s.store.DeleteZoneForDomain(r.Context(), domain.ID); err != nil {
			s.failStore(w, r, err)

			return
		}

		if err := s.store.DeleteDomain(r.Context(), domain.Name); err != nil {
			s.failStore(w, r, err)

			return
		}

		s.audit(r, caller, "domain.removed", "domain", domain.Name, nil)

		w.WriteHeader(http.StatusNoContent)
	}
}

func (s *Server) writeDomainDetail(w http.ResponseWriter, r *http.Request, domain store.Domain, account store.User) {
	payload := map[string]any{
		"id":           domain.ID,
		"name":         domain.Name,
		"type":         domain.Type,
		"username":     account.Username,
		"phpVersion":   domain.PHPVersion,
		"documentRoot": domain.DocumentRoot,
		"isPrimary":    domain.IsPrimary,
		"status":       domain.Status,
		"sslStatus":    domain.SSLStatus,
		"createdAt":    domain.CreatedAt,
		"publicIp":     s.cfg.PublicIP,
		"nameservers":  s.cfg.Nameservers,
		"zone":         nil,
	}

	zone, err := s.store.ZoneByDomain(r.Context(), domain.ID)
	if err == nil {
		payload["zone"] = zone
	} else if !errors.Is(err, store.ErrNotFound) {
		s.failStore(w, r, err)

		return
	}

	writeJSON(w, http.StatusOK, payload)
}

func (s *Server) updateDomain(w http.ResponseWriter, r *http.Request, caller store.User, domain store.Domain, account store.User) {
	var request struct {
		PHPVersion   string `json:"phpVersion"`
		PHPEnabled   *bool  `json:"phpEnabled"`
		ForceHTTPS   *bool  `json:"forceHttps"`
		WWWMode      string `json:"wwwMode"`
		DocumentRoot string `json:"documentRoot"`
		SSL          *bool  `json:"ssl"`
	}

	if !decodeJSON(w, r, &request) {
		return
	}

	phpVersion := domain.PHPVersion
	if request.PHPVersion != "" {
		phpVersion = request.PHPVersion
	}

	phpEnabled := true
	if request.PHPEnabled != nil {
		phpEnabled = *request.PHPEnabled
	}

	forceHTTPS := false
	if request.ForceHTTPS != nil {
		forceHTTPS = *request.ForceHTTPS
	}

	wwwMode := request.WWWMode
	if wwwMode == "" {
		wwwMode = "both"
	}

	if wwwMode != "both" && wwwMode != "www" && wwwMode != "bare" {
		failFields(w, http.StatusUnprocessableEntity, codeInvalid, "That setting cannot be saved.", map[string]string{
			"wwwMode": "Choose both, www or bare.",
		})

		return
	}

	documentRoot := strings.Trim(strings.TrimSpace(request.DocumentRoot), "/")
	if len(documentRoot) > 200 {
		failFields(w, http.StatusUnprocessableEntity, codeInvalid, "That setting cannot be saved.", map[string]string{
			"documentRoot": "Keep the folder under 200 characters.",
		})

		return
	}

	ssl := forceHTTPS || domain.SSLStatus == "active"
	if request.SSL != nil {
		ssl = *request.SSL
	}

	if err := s.store.UpdateDomainSettings(r.Context(), domain.ID, phpVersion, phpEnabled, forceHTTPS, wwwMode, documentRoot); err != nil {
		s.failStore(w, r, err)

		return
	}

	if _, err := s.queueFor(r, caller, account, "web.apply_domain", map[string]any{
		"username":      account.Username,
		"domain":        domain.Name,
		"php_version":   phpVersion,
		"document_root": documentRoot,
		"ssl":           ssl,
		"force_https":   forceHTTPS,
		"www":           wwwMode,
		"no_php":        !phpEnabled,
	}); err != nil {
		s.failStore(w, r, err)

		return
	}

	s.audit(r, caller, "domain.updated", "domain", domain.Name, map[string]any{
		"phpVersion": phpVersion,
		"forceHttps": forceHTTPS,
	})

	queued(w, "web.apply_domain", nil)
}

// handleDomainSSL issues or removes a certificate.
func (s *Server) handleDomainSSL(w http.ResponseWriter, r *http.Request) {
	domain, account, ok := s.ownedDomain(w, r)
	if !ok {
		return
	}

	caller, _ := s.caller(w, r)

	switch r.Method {
	case http.MethodPost:
		if _, err := s.queueFor(r, caller, account, "ssl.issue", map[string]any{
			"username": account.Username,
			"domain":   domain.Name,
		}); err != nil {
			s.failStore(w, r, err)

			return
		}

		if err := s.store.SetDomainSSL(r.Context(), domain.ID, "pending", "letsencrypt", nil); err != nil {
			s.failStore(w, r, err)

			return
		}

		s.audit(r, caller, "ssl.requested", "domain", domain.Name, nil)

		queued(w, "ssl.issue", nil)

	case http.MethodDelete:
		if _, err := s.queueFor(r, caller, account, "ssl.delete", map[string]any{
			"username": account.Username,
			"domain":   domain.Name,
		}); err != nil {
			s.failStore(w, r, err)

			return
		}

		if err := s.store.SetDomainSSL(r.Context(), domain.ID, "", "", nil); err != nil {
			s.failStore(w, r, err)

			return
		}

		s.audit(r, caller, "ssl.removed", "domain", domain.Name, nil)

		w.WriteHeader(http.StatusNoContent)
	}
}

// handleDNSRecords adds a record to a domain's zone and republishes the zone.
func (s *Server) handleDNSRecords(w http.ResponseWriter, r *http.Request) {
	domain, account, ok := s.ownedDomain(w, r)
	if !ok {
		return
	}

	zone, err := s.store.ZoneByDomain(r.Context(), domain.ID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			zone, err = s.store.CreateZone(r.Context(), account.ID, domain.ID, domain.Name)
		}

		if err != nil {
			s.failStore(w, r, err)

			return
		}
	}

	if r.Method == http.MethodGet {
		records, err := s.store.RecordsByZone(r.Context(), zone.ID)
		if err != nil {
			s.failStore(w, r, err)

			return
		}

		writeJSON(w, http.StatusOK, map[string]any{
			"zone":        zone,
			"records":     records,
			"nameservers": s.cfg.Nameservers,
			"publicIp":    s.cfg.PublicIP,
		})

		return
	}

	var request struct {
		Name     string `json:"name"`
		Type     string `json:"type"`
		Content  string `json:"content"`
		TTL      int64  `json:"ttl"`
		Priority *int64 `json:"priority"`
	}

	if !decodeJSON(w, r, &request) {
		return
	}

	name, recordType, content, ttl, priority, ok := s.validRecord(w, r, domain.Name, request.Name,
		request.Type, request.Content, request.TTL, request.Priority)
	if !ok {
		return
	}

	record, err := s.store.AddRecord(r.Context(), zone.ID, name, recordType, content, ttl, priority)
	if err != nil {
		s.failStore(w, r, err)

		return
	}

	s.syncZone(r, account, domain.Name)

	caller, _ := s.caller(w, r)
	s.audit(r, caller, "dns.record_added", "domain", domain.Name, map[string]any{
		"name": name, "type": recordType,
	})

	writeJSON(w, http.StatusCreated, map[string]any{"record": record})
}

// handleDNSRecord changes or deletes one record.
func (s *Server) handleDNSRecord(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(w, r, "id")
	if !ok {
		return
	}

	record, zone, err := s.store.RecordByID(r.Context(), id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			fail(w, http.StatusNotFound, codeNotFound, "There is no record by that id.")
		} else {
			s.failStore(w, r, err)
		}

		return
	}

	domain, err := s.store.DomainByID(r.Context(), deref(zone.DomainID))
	if err != nil {
		s.failStore(w, r, err)

		return
	}

	account, ok := s.domainedAccount(w, r, domain)
	if !ok {
		return
	}

	caller, _ := s.caller(w, r)

	switch r.Method {
	case http.MethodDelete:
		if err := s.store.DeleteRecord(r.Context(), id); err != nil {
			s.failStore(w, r, err)

			return
		}

		s.syncZone(r, account, zone.Name)
		s.audit(r, caller, "dns.record_deleted", "domain", zone.Name, map[string]any{"name": record.Name})

		w.WriteHeader(http.StatusNoContent)

	case http.MethodPatch:
		var request struct {
			Name     string `json:"name"`
			Type     string `json:"type"`
			Content  string `json:"content"`
			TTL      int64  `json:"ttl"`
			Priority *int64 `json:"priority"`
		}

		if !decodeJSON(w, r, &request) {
			return
		}

		name, recordType, content, ttl, priority, ok := s.validRecord(w, r, zone.Name, request.Name,
			request.Type, request.Content, request.TTL, request.Priority)
		if !ok {
			return
		}

		if err := s.store.UpdateRecord(r.Context(), id, name, recordType, content, ttl, priority); err != nil {
			s.failStore(w, r, err)

			return
		}

		s.syncZone(r, account, zone.Name)
		s.audit(r, caller, "dns.record_updated", "domain", zone.Name, map[string]any{"name": name})

		writeJSON(w, http.StatusOK, map[string]any{"record": store.Record{
			ID: id, ZoneID: record.ZoneID, Name: name, Type: recordType,
			Content: content, TTL: ttl, Priority: priority,
		}})
	}
}

// validRecord checks one record and normalises its fields.
func (s *Server) validRecord(
	w http.ResponseWriter, r *http.Request, zone, name, recordType, content string,
	ttl int64, priority *int64,
) (string, string, string, int64, *int64, bool) {
	fields := map[string]string{}

	name = store.TrimRecordName(name, zone)
	recordType = strings.ToUpper(strings.TrimSpace(recordType))
	content = strings.TrimSpace(content)

	if !validRecordName(name) {
		fields["name"] = "Use a name inside the zone, @ for the domain itself, or * for a wildcard."
	}

	if !dnsTypes[recordType] {
		fields["type"] = "Choose A, AAAA, CNAME, MX, TXT, NS, SRV or CAA."
	}

	if content == "" || len(content) > 2000 {
		fields["content"] = "A record needs a value under 2000 characters."
	}

	switch recordType {
	case "A":
		if !validIPv4(content) {
			fields["content"] = "An A record needs an IPv4 address, e.g. 203.0.113.10."
		}
	case "AAAA":
		if !validIPv6(content) {
			fields["content"] = "An AAAA record needs an IPv6 address."
		}
	case "MX", "SRV":
		if priority == nil {
			priority = ptrInt64(10)
		}
	}

	if ttl <= 0 {
		ttl = 3600
	}

	if ttl < 60 || ttl > 86400 {
		fields["ttl"] = "Keep the TTL between 60 and 86400 seconds."
	}

	if len(fields) > 0 {
		failFields(w, http.StatusUnprocessableEntity, codeInvalid, "That record cannot be saved.", fields)

		return "", "", "", 0, nil, false
	}

	return name, recordType, content, ttl, priority, true
}

func ptrInt64(value int64) *int64 { return &value }

func deref(value *int64) int64 {
	if value == nil {
		return 0
	}

	return *value
}

// sslExpiry is the moment a certificate stops working.
func sslExpiry(days int) *time.Time {
	when := time.Now().UTC().AddDate(0, 0, days)

	return &when
}
