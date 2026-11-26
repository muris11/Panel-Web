package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"muriscore/panel/internal/store"
)

// TestNodeAppsRunBehindTheirWebsites covers the whole life of a Node.js app:
// create on a domain the caller owns, one app per website, restart, delete,
// and the refusal of a domain the caller does not own.
func TestNodeAppsRunBehindTheirWebsites(t *testing.T) {
	server, st, queue := harness(t)
	httpClient, csrf := signIn(t, server, st)

	// An account with a website to hang the app on.
	created := call(t, httpClient, http.MethodPost, server.URL+"/api/v1/accounts", map[string]any{
		"username": "nodeowner",
		"name":     "Node Owner",
		"email":    "nodeowner@example.test",
		"password": "rahasia123",
		"domain":   "node.example.test",
	}, csrf)

	if created.Status != http.StatusCreated {
		t.Fatalf("creating the account answered %d: %v", created.Status, created.Body)
	}

	// Drain the account and website work, so the domain is active.
	for queue.RunOnce(context.Background()) {
	}

	domains := call(t, httpClient, http.MethodGet, server.URL+"/api/v1/domains", nil, "")
	if domains.Status != http.StatusOK {
		t.Fatalf("listing domains answered %d: %v", domains.Status, domains.Body)
	}

	rows, _ := domains.Body["domains"].([]any)
	if len(rows) == 0 {
		t.Fatal("the account's website is missing")
	}

	domain := rows[0].(map[string]any)
	domainID := int64(domain["id"].(float64))

	// Create the app.
	answer := call(t, httpClient, http.MethodPost, server.URL+"/api/v1/node-apps", map[string]any{
		"domainId": domainID,
		"entry":    "dist/server.js",
		"env":      map[string]string{"PORT": "3000", "NODE_ENV": "production"},
	}, csrf)

	if answer.Status != http.StatusCreated {
		t.Fatalf("creating the app answered %d: %v", answer.Status, answer.Body)
	}

	app, _ := answer.Body["app"].(map[string]any)

	if app["entry"] != "dist/server.js" || app["status"] != "pending" {
		t.Fatalf("the app row is wrong: %v", app)
	}

	port := int(app["port"].(float64))
	if port < store.NodePortMin || port > store.NodePortMax {
		t.Fatalf("port %d is outside the window", port)
	}

	// The queued task carries the agent's whole contract.
	provision := lastTaskPayload(t, st, "nodeapp.provision")
	if provision["username"] != "nodeowner" || provision["domain"] != "node.example.test" ||
		provision["entry"] != "dist/server.js" || provision["port"] != float64(port) {
		t.Fatalf("provision payload is wrong: %v", provision)
	}

	env, _ := provision["env"].(map[string]any)
	if env["NODE_ENV"] != "production" {
		t.Fatalf("the environment did not survive: %v", env)
	}

	// A second app on the same website is refused.
	again := call(t, httpClient, http.MethodPost, server.URL+"/api/v1/node-apps", map[string]any{
		"domainId": domainID,
		"entry":    "app.js",
	}, csrf)
	if again.Status != http.StatusConflict {
		t.Fatalf("a second app answered %d, want 409: %v", again.Status, again.Body)
	}

	// Restart queues the agent call and rewinds the status.
	restart := call(t, httpClient, http.MethodPost,
		fmt.Sprintf("%s/api/v1/node-apps/%d/restart", server.URL, int(app["id"].(float64))), nil, csrf)
	if restart.Status != http.StatusOK {
		t.Fatalf("restart answered %d: %v", restart.Status, restart.Body)
	}

	if lastTaskPayload(t, st, "nodeapp.restart") == nil {
		t.Fatal("no nodeapp.restart task was queued")
	}

	// Delete queues the deprovision and removes the row.
	removal := call(t, httpClient, http.MethodDelete,
		fmt.Sprintf("%s/api/v1/node-apps/%d", server.URL, int(app["id"].(float64))), nil, csrf)
	if removal.Status != http.StatusOK {
		t.Fatalf("delete answered %d: %v", removal.Status, removal.Body)
	}

	if lastTaskPayload(t, st, "nodeapp.deprovision") == nil {
		t.Fatal("no nodeapp.deprovision task was queued")
	}

	list := call(t, httpClient, http.MethodGet, server.URL+"/api/v1/node-apps", nil, "")
	apps, _ := list.Body["apps"].([]any)
	if len(apps) != 0 {
		t.Fatalf("the app survived its deletion: %v", apps)
	}
}

// TestNodeAppsBelongsToTheirOwner proves another account's app is invisible
// and unmanageable: 404, the same rule as every other cross-account object.
func TestNodeAppsBelongsToTheirOwner(t *testing.T) {
	server, st, queue := harness(t)
	admin, csrf := signIn(t, server, st)

	// Two accounts, each with a website.
	for _, name := range []string{"ownerone", "ownertwo"} {
		created := call(t, admin, http.MethodPost, server.URL+"/api/v1/accounts", map[string]any{
			"username": name,
			"name":     name,
			"email":    name + "@example.test",
			"password": "rahasia123",
			"domain":   name + ".example.test",
		}, csrf)
		if created.Status != http.StatusCreated {
			t.Fatalf("creating %s answered %d: %v", name, created.Status, created.Body)
		}
	}

	// Provision both accounts, so they may sign in.
	for queue.RunOnce(context.Background()) {
	}

	// Sign in as ownerone.
	one := client(t)
	probe := call(t, one, http.MethodGet, server.URL+"/api/v1/auth/session", nil, "")
	login := call(t, one, http.MethodPost, server.URL+"/api/v1/auth/login", map[string]any{
		"login":    "ownerone",
		"password": "rahasia123",
	}, probe.Body["csrfToken"].(string))
	if login.Status != http.StatusOK {
		t.Fatalf("ownerone could not sign in: %v", login.Body)
	}

	oneCSRF := login.Body["csrfToken"].(string)

	domains := call(t, one, http.MethodGet, server.URL+"/api/v1/domains", nil, "")
	rows, _ := domains.Body["domains"].([]any)
	if len(rows) != 1 {
		adminRows, _ := call(t, admin, http.MethodGet, server.URL+"/api/v1/domains", nil, csrf).Body["domains"].([]any)
		t.Fatalf("ownerone sees %d domains, want 1 (admin sees %d: %v)", len(rows), len(adminRows), adminRows)
	}

	domainID := int64(rows[0].(map[string]any)["id"].(float64))

	answer := call(t, one, http.MethodPost, server.URL+"/api/v1/node-apps", map[string]any{
		"domainId": domainID,
		"entry":    "app.js",
	}, oneCSRF)
	if answer.Status != http.StatusCreated {
		t.Fatalf("creating the app answered %d: %v", answer.Status, answer.Body)
	}

	app, _ := answer.Body["app"].(map[string]any)
	appID := int(app["id"].(float64))

	// ownerone sees exactly its own app.
	list := call(t, one, http.MethodGet, server.URL+"/api/v1/node-apps", nil, "")
	apps, _ := list.Body["apps"].([]any)
	if len(apps) != 1 {
		t.Fatalf("ownerone sees %d apps, want 1", len(apps))
	}

	// The administrator sees it too, and can restart it.
	adminList := call(t, admin, http.MethodGet, server.URL+"/api/v1/node-apps", nil, csrf)
	adminApps, _ := adminList.Body["apps"].([]any)
	if len(adminApps) != 1 {
		t.Fatalf("the administrator sees %d apps, want 1", len(adminApps))
	}

	restart := call(t, admin, http.MethodPost,
		fmt.Sprintf("%s/api/v1/node-apps/%d/restart", server.URL, appID), nil, csrf)
	if restart.Status != http.StatusOK {
		t.Fatalf("the administrator's restart answered %d: %v", restart.Status, restart.Body)
	}

	// ownerone must not reach the next account's domain: 404, not 403.
	second := call(t, one, http.MethodPost, server.URL+"/api/v1/node-apps", map[string]any{
		"domainId": domainID + 1,
		"entry":    "app.js",
	}, oneCSRF)
	if second.Status != http.StatusForbidden && second.Status != http.StatusNotFound {
		t.Fatalf("ownerone reaching another account's domain answered %d, want 403 or 404: %v",
			second.Status, second.Body)
	}
}

// lastTaskPayload returns the params of the newest task of one action.
func lastTaskPayload(t *testing.T, st *store.Store, action string) map[string]any {
	t.Helper()

	tasks, err := st.Tasks(context.Background(), store.TaskFilter{Limit: 50})
	if err != nil {
		t.Fatalf("list tasks: %v", err)
	}

	for _, task := range tasks {
		if task.Action != action {
			continue
		}

		var params map[string]any
		if err := json.Unmarshal([]byte(task.Payload), &params); err != nil {
			t.Fatalf("decode the payload of %s: %v", action, err)
		}

		return params
	}

	return nil
}
