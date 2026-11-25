package httpapi

import (
	"errors"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"muriscore/panel/internal/store"
)

// packageNamePattern is what a package may be called: something a person can
// read back on an invoice.
var packageNamePattern = regexp.MustCompile(`^[\p{L}\p{N} ._+&()/-]{2,64}$`)

// packageField maps one submitted key to the column it writes.
type packageField struct {
	key     string
	label   string
	min     int64
	max     int64
	limitOf func(*store.PackageInput) **int64
}

// packageFields are every optional limit the editor sends. A missing key keeps
// what the package has, an explicit null means unlimited.
var packageFields = []packageField{
	{"diskMb", "Disk space", 1, 1_000_000_000, func(in *store.PackageInput) **int64 { return &in.DiskMB }},
	{"inodes", "Inodes", 1, 1_000_000_000, func(in *store.PackageInput) **int64 { return &in.Inodes }},
	{"bandwidthMb", "Bandwidth", 0, 1_000_000_000, func(in *store.PackageInput) **int64 { return &in.BandwidthMB }},
	{"domains", "Domains", 0, 1_000_000_000, func(in *store.PackageInput) **int64 { return &in.Domains }},
	{"subdomains", "Subdomains", 0, 1_000_000_000, func(in *store.PackageInput) **int64 { return &in.Subdomains }},
	{"pointers", "Pointers", 0, 1_000_000_000, func(in *store.PackageInput) **int64 { return &in.Pointers }},
	{"emailAccounts", "Mailboxes", 0, 1_000_000_000, func(in *store.PackageInput) **int64 { return &in.Mailboxes }},
	{"emailForwarders", "Forwarders", 0, 1_000_000_000, func(in *store.PackageInput) **int64 { return &in.Forwarders }},
	{"databases", "Databases", 0, 1_000_000_000, func(in *store.PackageInput) **int64 { return &in.Databases }},
	{"ftpAccounts", "FTP logins", 0, 1_000_000_000, func(in *store.PackageInput) **int64 { return &in.FTPAccounts }},
	{"cronJobs", "Cron jobs", 0, 1_000_000_000, func(in *store.PackageInput) **int64 { return &in.CronJobs }},
}

// handlePackages lists the packages the caller may hand out, and creates one.
func (s *Server) handlePackages(w http.ResponseWriter, r *http.Request) {
	caller, ok := s.caller(w, r)
	if !ok {
		return
	}

	switch r.Method {
	case http.MethodGet:
		s.listPackages(w, r, caller)

	case http.MethodPost:
		s.createPackage(w, r, caller)
	}
}

// handlePackage changes or deletes one package.
func (s *Server) handlePackage(w http.ResponseWriter, r *http.Request) {
	caller, ok := s.caller(w, r)
	if !ok {
		return
	}

	id, err := strconv.ParseInt(strings.TrimPrefix(r.URL.Path, "/api/v1/packages/"), 10, 64)
	if err != nil {
		fail(w, http.StatusNotFound, codeNotFound, "No such package.")

		return
	}

	pkg, err := s.store.PackageByID(r.Context(), id)
	if errors.Is(err, store.ErrPackageMissing) {
		fail(w, http.StatusNotFound, codeNotFound, "No such package.")

		return
	}
	if err != nil {
		s.failStore(w, r, err)

		return
	}

	// A reseller edits its own packages only; a package with no owner belongs
	// to the administrators.
	if !caller.IsAdmin() && (pkg.OwnerID == nil || *pkg.OwnerID != caller.ID) {
		fail(w, http.StatusNotFound, codeNotFound, "No such package.")

		return
	}

	switch r.Method {
	case http.MethodPatch, http.MethodPut:
		s.updatePackage(w, r, caller, pkg)

	case http.MethodDelete:
		s.deletePackage(w, r, caller, pkg)
	}
}

// listPackages answers the packages page.
func (s *Server) listPackages(w http.ResponseWriter, r *http.Request, caller store.User) {
	packages, err := s.store.PackagesFor(r.Context(), &caller)
	if err != nil {
		s.failStore(w, r, err)

		return
	}

	payload := make([]map[string]any, 0, len(packages))
	for _, pkg := range packages {
		payload = append(payload, packageJSON(pkg))
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"packages":       payload,
		"canEdit":        caller.IsAdmin() || caller.IsReseller(),
		"whereItApplies": "Changing a limit here reaches every account on the package the next time its quota is applied.",
	})
}

// packageJSON is one package as the interface reads it.
func packageJSON(pkg store.Package) map[string]any {
	return map[string]any{
		"id":              pkg.ID,
		"name":            pkg.Name,
		"description":     pkg.Description,
		"diskMb":          pkg.DiskMB,
		"inodes":          pkg.Inodes,
		"bandwidthMb":     pkg.BandwidthMB,
		"domains":         pkg.Domains,
		"subdomains":      pkg.Subdomains,
		"pointers":        pkg.Pointers,
		"emailAccounts":   pkg.EmailAccounts,
		"emailForwarders": pkg.EmailForwarder,
		"databases":       pkg.Databases,
		"ftpAccounts":     pkg.FTPAccounts,
		"cronJobs":        pkg.CronJobs,
		"ssl":             pkg.SSL,
		"cgi":             pkg.CGI,
		"ssh":             pkg.SSH,
		"accounts":        pkg.Accounts,
	}
}

// createPackage stores a package owned by the caller.
func (s *Server) createPackage(w http.ResponseWriter, r *http.Request, caller store.User) {
	input, ok := s.readPackage(w, r, nil)
	if !ok {
		return
	}

	pkg, err := s.store.CreatePackage(r.Context(), input, &caller)
	if errors.Is(err, store.ErrPackageNameTaken) {
		failFields(w, http.StatusUnprocessableEntity, codeInvalid, "That name is already used by one of your packages.", map[string]string{
			"name": "This name is taken.",
		})

		return
	}
	if err != nil {
		s.failStore(w, r, err)

		return
	}

	s.audit(r, caller, "package.created", "package", strconv.FormatInt(pkg.ID, 10), map[string]any{
		"name": pkg.Name,
	})

	writeJSON(w, http.StatusCreated, map[string]any{"package": packageJSON(pkg)})
}

// updatePackage writes the submitted limits and queues the work they imply.
func (s *Server) updatePackage(w http.ResponseWriter, r *http.Request, caller store.User, before store.Package) {
	input, ok := s.readPackage(w, r, &before)
	if !ok {
		return
	}

	after, changed, err := s.store.UpdatePackage(r.Context(), before.ID, input)
	if errors.Is(err, store.ErrPackageNameTaken) {
		failFields(w, http.StatusUnprocessableEntity, codeInvalid, "That name is already used by one of your packages.", map[string]string{
			"name": "This name is taken.",
		})

		return
	}
	if err != nil {
		s.failStore(w, r, err)

		return
	}

	extra := map[string]any{"name": after.Name, "changed": changed}

	if includes(changed, "disk_mb") || includes(changed, "inodes") {
		queued, err := s.store.QueueQuota(r.Context(), after, &caller)
		if err != nil {
			s.failStore(w, r, err)

			return
		}

		extra["quotas_queued"] = queued
	}

	if includes(changed, "ssh") {
		queued, err := s.store.QueueSSH(r.Context(), after, &caller)
		if err != nil {
			s.failStore(w, r, err)

			return
		}

		extra["shells_queued"] = queued
	}

	s.audit(r, caller, "package.updated", "package", strconv.FormatInt(after.ID, 10), extra)

	writeJSON(w, http.StatusOK, map[string]any{"package": packageJSON(after), "changed": changed})
}

// deletePackage removes a package no account uses.
func (s *Server) deletePackage(w http.ResponseWriter, r *http.Request, caller store.User, pkg store.Package) {
	err := s.store.DeletePackage(r.Context(), pkg.ID)
	if errors.Is(err, store.ErrPackageInUse) {
		fail(w, http.StatusConflict, codeConflict,
			"This package is still used by "+strconv.FormatInt(pkg.Accounts, 10)+" account(s). Move them to another package first.")

		return
	}
	if err != nil {
		s.failStore(w, r, err)

		return
	}

	s.audit(r, caller, "package.deleted", "package", strconv.FormatInt(pkg.ID, 10), map[string]any{
		"name": pkg.Name,
	})

	w.WriteHeader(http.StatusNoContent)
}

// readPackage reads the editor's submission. Every limit is present as a key,
// either a number or null for unlimited; anything else is refused with the
// field named, because a silently ignored limit is a package that lies.
func (s *Server) readPackage(w http.ResponseWriter, r *http.Request, before *store.Package) (store.PackageInput, bool) {
	var body map[string]any

	if !decodeJSON(w, r, &body) {
		return store.PackageInput{}, false
	}

	problems := map[string]string{}

	name, _ := body["name"].(string)
	name = strings.TrimSpace(name)

	switch {
	case name == "":
		problems["name"] = "This is required."
	case !packageNamePattern.MatchString(name):
		problems["name"] = "Use 2 to 64 letters, numbers, spaces or . _ + & ( ) / -"
	}

	input := store.PackageInput{Name: name}

	if description, ok := body["description"].(string); ok {
		description = strings.TrimSpace(description)
		if len([]rune(description)) > 255 {
			problems["description"] = "Keep this under 255 characters."
		}

		input.Description = description
	}

	for _, field := range packageFields {
		raw, present := body[field.key]

		if present {
			value, bad := packageLimit(raw, field.min, field.max)
			if bad != "" {
				problems[field.key] = bad

				continue
			}

			*field.limitOf(&input) = value

			continue
		}

		// A key the editor left out keeps the stored value when it is an
		// edit, and means unlimited when the package is new.
		if before != nil {
			*field.limitOf(&input) = storedLimit(before, field.key)
		}
	}

	for _, flag := range []struct {
		key  string
		into *bool
		kept bool
	}{
		{"ssl", &input.SSL, before != nil && before.SSL},
		{"cgi", &input.CGI, before != nil && before.CGI},
		{"ssh", &input.SSH, before != nil && before.SSH},
	} {
		raw, present := body[flag.key]

		if !present {
			*flag.into = flag.kept

			continue
		}

		value, ok := raw.(bool)
		if !ok {
			problems[flag.key] = "Say whether this is allowed."

			continue
		}

		*flag.into = value
	}

	if len(problems) > 0 {
		failFields(w, http.StatusUnprocessableEntity, codeInvalid, "Some of those package values cannot be used.", problems)

		return store.PackageInput{}, false
	}

	return input, true
}

// storedLimit reads the package's current value for a field key.
func storedLimit(pkg *store.Package, key string) *int64 {
	switch key {
	case "diskMb":
		return pkg.DiskMB
	case "inodes":
		return pkg.Inodes
	case "bandwidthMb":
		return pkg.BandwidthMB
	case "domains":
		return pkg.Domains
	case "subdomains":
		return pkg.Subdomains
	case "pointers":
		return pkg.Pointers
	case "emailAccounts":
		return pkg.EmailAccounts
	case "emailForwarders":
		return pkg.EmailForwarder
	case "databases":
		return pkg.Databases
	case "ftpAccounts":
		return pkg.FTPAccounts
	case "cronJobs":
		return pkg.CronJobs
	}

	return nil
}

// packageLimit turns one submitted limit into a number, or explains why not.
// A JS number arrives as a float, so a fractional value is refused rather than
// rounded: half a gigabyte is not a limit the server understands.
func packageLimit(raw any, min, max int64) (*int64, string) {
	if raw == nil {
		return nil, ""
	}

	number, ok := raw.(float64)
	if !ok {
		return nil, "Use a whole number, or leave it empty for no limit."
	}

	if number != float64(int64(number)) {
		return nil, "Use a whole number of units."
	}

	value := int64(number)

	if value < min || value > max {
		return nil, "Use a number between " + strconv.FormatInt(min, 10) + " and " + strconv.FormatInt(max, 10) + "."
	}

	return &value, ""
}

// includes reports whether a slice holds a value.
func includes(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}

	return false
}
