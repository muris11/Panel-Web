package httpapi

import (
	"context"
	"net/http"
	"testing"

	"muriscore/panel/internal/auth"
	"muriscore/panel/internal/store"
)

func TestPackageEditingReachesTheAccountsOnIt(t *testing.T) {
	server, st, queue := harness(t)
	adminClient, csrf := signIn(t, server, st)

	created := call(t, adminClient, http.MethodPost, server.URL+"/api/v1/packages", map[string]any{
		"name":            "Blog",
		"description":     "One small website",
		"diskMb":          1024,
		"inodes":          50000,
		"bandwidthMb":     nil,
		"domains":         1,
		"subdomains":      nil,
		"pointers":        nil,
		"emailAccounts":   5,
		"emailForwarders": nil,
		"databases":       2,
		"ftpAccounts":     1,
		"cronJobs":        3,
		"ssl":             true,
		"cgi":             false,
		"ssh":             false,
	}, csrf)

	if created.Status != http.StatusCreated {
		t.Fatalf("creating a package answered %d: %v", created.Status, created.Body)
	}

	pkg, _ := created.Body["package"].(map[string]any)
	id, _ := pkg["id"].(float64)

	if pkg["name"] != "Blog" || pkg["diskMb"] != float64(1024) {
		t.Fatalf("the package came back as %v", pkg)
	}

	if pkg["bandwidthMb"] != nil {
		t.Fatalf("an empty limit should stay unlimited, got %v", pkg["bandwidthMb"])
	}

	duplicate := call(t, adminClient, http.MethodPost, server.URL+"/api/v1/packages", map[string]any{
		"name":   "Blog",
		"ssl":    true,
		"ssh":    false,
		"cgi":    false,
		"diskMb": 10,
		"inodes": 10,
	}, csrf)

	if duplicate.Status != http.StatusUnprocessableEntity {
		t.Fatalf("a duplicate name answered %d, want 422: %v", duplicate.Status, duplicate.Body)
	}

	// An account on the package, so the shells and quotas have somewhere to go.
	admin, err := st.UserByUsername(context.Background(), "admin")
	if err != nil {
		t.Fatalf("read the administrator: %v", err)
	}

	account := accountOwnedBy(t, st, "siti", admin.ID, nil)

	if _, err := st.DB().ExecContext(context.Background(),
		"UPDATE users SET package_id = ?, ssh_enabled = 1 WHERE id = ?", int64(id), account.ID); err != nil {
		t.Fatalf("put the account on the package: %v", err)
	}

	updated := call(t, adminClient, http.MethodPatch, server.URL+"/api/v1/packages/"+itoa(int64(id)), map[string]any{
		"name":            "Blog",
		"description":     "One small website",
		"diskMb":          2048,
		"inodes":          50000,
		"bandwidthMb":     nil,
		"domains":         1,
		"subdomains":      nil,
		"pointers":        nil,
		"emailAccounts":   5,
		"emailForwarders": nil,
		"databases":       2,
		"ftpAccounts":     1,
		"cronJobs":        3,
		"ssl":             true,
		"cgi":             false,
		"ssh":             true,
	}, csrf)

	if updated.Status != http.StatusOK {
		t.Fatalf("editing the package answered %d: %v", updated.Status, updated.Body)
	}

	// The disk limit changed, so the account's quota is queued again, and SSH
	// was turned on, so the shell it already had is re-applied.
	drain(t, queue, 2)

	tasks := call(t, adminClient, http.MethodGet, server.URL+"/api/v1/tasks", nil, "")
	list, _ := tasks.Body["tasks"].([]any)

	actions := map[string]bool{}
	for _, raw := range list {
		task, _ := raw.(map[string]any)
		action, _ := task["action"].(string)
		actions[action] = true
	}

	if !actions["account.set_quota"] {
		t.Fatalf("no quota task was queued: %v", tasks.Body)
	}

	if !actions["account.set_ssh"] {
		t.Fatalf("no shell task was queued: %v", tasks.Body)
	}

	inUse := call(t, adminClient, http.MethodDelete, server.URL+"/api/v1/packages/"+itoa(int64(id)), nil, csrf)
	if inUse.Status != http.StatusConflict {
		t.Fatalf("deleting a used package answered %d, want 409: %v", inUse.Status, inUse.Body)
	}

	if _, err := st.DB().ExecContext(context.Background(),
		"UPDATE users SET package_id = NULL WHERE id = ?", account.ID); err != nil {
		t.Fatalf("take the account off the package: %v", err)
	}

	removed := call(t, adminClient, http.MethodDelete, server.URL+"/api/v1/packages/"+itoa(int64(id)), nil, csrf)
	if removed.Status != http.StatusNoContent {
		t.Fatalf("deleting an unused package answered %d: %v", removed.Status, removed.Body)
	}
}

func TestPackagesBelongToTheirOwner(t *testing.T) {
	server, st, _ := harness(t)
	adminClient, adminCSRF := signIn(t, server, st)

	hash, err := auth.HashPassword("rahasia123")
	if err != nil {
		t.Fatalf("hash: %v", err)
	}

	if _, err := st.CreatePanelUser(context.Background(), "resel", "Reseller", "resel@example.test", hash, store.RoleReseller, nil); err != nil {
		t.Fatalf("create the reseller: %v", err)
	}

	if _, err := st.CreatePanelUser(context.Background(), "lain", "Other", "lain@example.test", hash, store.RoleReseller, nil); err != nil {
		t.Fatalf("create the other reseller: %v", err)
	}

	resellerClient := signInAs(t, server, st, "resel", "rahasia123")
	otherClient := signInAs(t, server, st, "lain", "rahasia123")

	if resellerClient == nil || otherClient == nil {
		t.Fatal("a reseller could not sign in")
	}

	probe := call(t, resellerClient, http.MethodGet, server.URL+"/api/v1/auth/session", nil, "")
	resellerCSRF, _ := probe.Body["csrfToken"].(string)

	made := call(t, resellerClient, http.MethodPost, server.URL+"/api/v1/packages", map[string]any{
		"name":   "Starter",
		"diskMb": 512,
		"inodes": 10000,
		"ssl":    true,
		"cgi":    false,
		"ssh":    false,
	}, resellerCSRF)

	if made.Status != http.StatusCreated {
		t.Fatalf("a reseller creating a package answered %d: %v", made.Status, made.Body)
	}

	pkg, _ := made.Body["package"].(map[string]any)
	id, _ := pkg["id"].(float64)

	// The other reseller cannot see it in a listing or edit it.
	theirs := call(t, otherClient, http.MethodGet, server.URL+"/api/v1/packages", nil, "")
	pkgs, _ := theirs.Body["packages"].([]any)
	if len(pkgs) != 0 {
		t.Fatalf("another reseller sees %d package(s): %v", len(pkgs), theirs.Body)
	}

	otherProbe := call(t, otherClient, http.MethodGet, server.URL+"/api/v1/auth/session", nil, "")
	otherCSRF, _ := otherProbe.Body["csrfToken"].(string)

	refused := call(t, otherClient, http.MethodPatch, server.URL+"/api/v1/packages/"+itoa(int64(id)), map[string]any{
		"name":   "Starter",
		"diskMb": 4096,
		"inodes": 10000,
		"ssl":    true,
		"cgi":    false,
		"ssh":    false,
	}, otherCSRF)

	if refused.Status != http.StatusNotFound {
		t.Fatalf("another reseller editing it answered %d, want 404: %v", refused.Status, refused.Body)
	}

	// The administrator owns the whole namespace and may edit it.
	approved := call(t, adminClient, http.MethodPatch, server.URL+"/api/v1/packages/"+itoa(int64(id)), map[string]any{
		"name":   "Starter",
		"diskMb": 4096,
		"inodes": 10000,
		"ssl":    true,
		"cgi":    false,
		"ssh":    false,
	}, adminCSRF)

	if approved.Status != http.StatusOK {
		t.Fatalf("an administrator editing a reseller package answered %d: %v", approved.Status, approved.Body)
	}
}

func TestPackageLimitsMustBeWholeNumbers(t *testing.T) {
	server, st, _ := harness(t)
	adminClient, csrf := signIn(t, server, st)

	bad := call(t, adminClient, http.MethodPost, server.URL+"/api/v1/packages", map[string]any{
		"name":   "Half",
		"diskMb": 10.5,
		"inodes": 0,
		"ssl":    true,
		"cgi":    false,
		"ssh":    false,
	}, csrf)

	if bad.Status != http.StatusUnprocessableEntity {
		t.Fatalf("a fractional disk limit answered %d, want 422: %v", bad.Status, bad.Body)
	}

	fields, _ := bad.Body["error"].(map[string]any)
	named, _ := fields["fields"].(map[string]any)

	if named["diskMb"] == nil {
		t.Fatalf("the answer does not name diskMb: %v", bad.Body)
	}

	if named["inodes"] == nil {
		t.Fatalf("a quota of 0 must be refused, since the server reads it as unlimited: %v", bad.Body)
	}
}
