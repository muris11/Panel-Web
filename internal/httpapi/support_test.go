package httpapi

import (
	"context"
	"net/http"
	"testing"

	"muriscore/panel/internal/auth"
	"muriscore/panel/internal/store"
)

// resellerWithPackage creates a reseller whose package allows or refuses SSH,
// and returns the reseller's id.
func resellerWithPackage(t *testing.T, st *store.Store, name string, ssh bool) int64 {
	t.Helper()

	hash, err := auth.HashPassword("rahasia123")
	if err != nil {
		t.Fatalf("hash: %v", err)
	}

	reseller, err := st.CreatePanelUser(context.Background(), name, "Reseller", name+"@example.test", hash, store.RoleReseller, nil)
	if err != nil {
		t.Fatalf("create the reseller: %v", err)
	}

	result, err := st.DB().ExecContext(context.Background(),
		"INSERT INTO reseller_packages (name, ssh, created_at, updated_at) VALUES (?, ?, NOW(), NOW())", name+"-pack", ssh)
	if err != nil {
		t.Fatalf("create the reseller package: %v", err)
	}

	id, _ := result.LastInsertId()

	if _, err := st.DB().ExecContext(context.Background(),
		"UPDATE users SET reseller_package_id = ? WHERE id = ?", id, reseller.ID); err != nil {
		t.Fatalf("attach the reseller package: %v", err)
	}

	return reseller.ID
}

// accountOwnedBy creates a hosting account under an owner and marks it active.
func accountOwnedBy(t *testing.T, st *store.Store, username string, ownerID int64, packageID *int64) store.User {
	t.Helper()

	hash, err := auth.HashPassword("rahasia123")
	if err != nil {
		t.Fatalf("hash: %v", err)
	}

	account, err := st.CreateAccount(context.Background(), username, username, username+"@example.test", hash, ownerID, packageID)
	if err != nil {
		t.Fatalf("create the account: %v", err)
	}

	if err := st.UpdateAccountStatus(context.Background(), account.ID, store.StatusActive, ""); err != nil {
		t.Fatalf("activate the account: %v", err)
	}

	return account
}

func TestResellerPackageDecidesWhetherItsAccountsMayHaveSsh(t *testing.T) {
	server, st, queue := harness(t)
	adminClient, csrf := signIn(t, server, st)

	resellerID := resellerWithPackage(t, st, "resel", false)
	accountOwnedBy(t, st, "budi", resellerID, nil)

	forbidden := call(t, adminClient, http.MethodPatch, server.URL+"/api/v1/ssh", map[string]any{
		"username": "budi",
		"enabled":  true,
	}, csrf)

	if forbidden.Status != http.StatusForbidden {
		t.Fatalf("a reseller package without SSH answered %d, want 403: %v", forbidden.Status, forbidden.Body)
	}

	if queue.RunOnce(context.Background()) {
		t.Fatal("a refused request still queued a task")
	}

	// The same account, once the reseller's package includes a shell.
	if _, err := st.DB().ExecContext(context.Background(),
		"UPDATE reseller_packages SET ssh = 1 WHERE name = ?", "resel-pack"); err != nil {
		t.Fatalf("allow SSH in the reseller package: %v", err)
	}

	allowed := call(t, adminClient, http.MethodPatch, server.URL+"/api/v1/ssh", map[string]any{
		"username": "budi",
		"enabled":  true,
	}, csrf)

	if allowed.Status != http.StatusAccepted {
		t.Fatalf("a reseller package with SSH answered %d, want 202: %v", allowed.Status, allowed.Body)
	}

	drain(t, queue, 1)

	// The account's own package has the last word.
	result, err := st.DB().ExecContext(context.Background(),
		"INSERT INTO packages (name, ssh, created_at, updated_at) VALUES ('no-shell', 0, NOW(), NOW())")
	if err != nil {
		t.Fatalf("create the package: %v", err)
	}

	packageID, _ := result.LastInsertId()

	budi, err := st.UserByUsername(context.Background(), "budi")
	if err != nil {
		t.Fatalf("read the account: %v", err)
	}

	if _, err := st.DB().ExecContext(context.Background(),
		"UPDATE users SET package_id = ? WHERE id = ?", packageID, budi.ID); err != nil {
		t.Fatalf("attach the package: %v", err)
	}

	refusedAgain := call(t, adminClient, http.MethodPatch, server.URL+"/api/v1/ssh", map[string]any{
		"username": "budi",
		"enabled":  true,
	}, csrf)

	if refusedAgain.Status != http.StatusForbidden {
		t.Fatalf("an account package without SSH answered %d, want 403: %v", refusedAgain.Status, refusedAgain.Body)
	}
}

func TestTicketsTravelBetweenAccountAndInbox(t *testing.T) {
	server, st, _ := harness(t)
	adminClient, adminCSRF := signIn(t, server, st)

	hash, err := auth.HashPassword("rahasia123")
	if err != nil {
		t.Fatalf("hash: %v", err)
	}

	admin, err := st.UserByUsername(context.Background(), "admin")
	if err != nil {
		t.Fatalf("read the administrator: %v", err)
	}

	if _, err := st.CreateAccount(context.Background(), "siti", "Siti", "siti@example.test", hash, admin.ID, nil); err != nil {
		t.Fatalf("create the account: %v", err)
	}

	if _, err := st.DB().ExecContext(context.Background(),
		"UPDATE users SET status = 'active' WHERE username = 'siti'"); err != nil {
		t.Fatalf("activate the account: %v", err)
	}

	sitiClient := signInAs(t, server, st, "siti", "rahasia123")
	if sitiClient == nil {
		t.Fatal("the hosting account could not sign in")
	}

	probe := call(t, sitiClient, http.MethodGet, server.URL+"/api/v1/auth/session", nil, "")
	sitiCSRF, _ := probe.Body["csrfToken"].(string)

	opened := call(t, sitiClient, http.MethodPost, server.URL+"/api/v1/tickets", map[string]any{
		"subject":  "Email is not arriving",
		"body":     "Since this morning nothing arrives.",
		"priority": "high",
	}, sitiCSRF)

	if opened.Status != http.StatusCreated {
		t.Fatalf("opening a ticket answered %d: %v", opened.Status, opened.Body)
	}

	id, _ := opened.Body["id"].(float64)

	// The administrator sees it in the inbox.
	inbox := call(t, adminClient, http.MethodGet, server.URL+"/api/v1/tickets?box=inbox", nil, "")
	if inbox.Status != http.StatusOK {
		t.Fatalf("the inbox answered %d: %v", inbox.Status, inbox.Body)
	}

	tickets, _ := inbox.Body["tickets"].([]any)
	if len(tickets) != 1 {
		t.Fatalf("the inbox holds %d ticket(s), want 1: %v", len(tickets), inbox.Body)
	}

	detail := call(t, adminClient, http.MethodGet, server.URL+"/api/v1/tickets/"+itoa(int64(id)), nil, "")
	if detail.Status != http.StatusOK {
		t.Fatalf("reading the ticket answered %d: %v", detail.Status, detail.Body)
	}

	messages, _ := detail.Body["messages"].([]any)
	if len(messages) != 1 {
		t.Fatalf("the ticket has %d message(s), want 1", len(messages))
	}

	answered := call(t, adminClient, http.MethodPost, server.URL+"/api/v1/tickets/"+itoa(int64(id))+"/reply", map[string]any{
		"body": "Looking at it now.",
	}, adminCSRF)

	if answered.Status != http.StatusOK {
		t.Fatalf("a staff reply answered %d: %v", answered.Status, answered.Body)
	}

	after := call(t, adminClient, http.MethodGet, server.URL+"/api/v1/tickets/"+itoa(int64(id)), nil, "")
	ticket, _ := after.Body["ticket"].(map[string]any)
	if ticket["status"] != "answered" {
		t.Fatalf("after a staff reply the ticket is %v, want answered", ticket["status"])
	}

	// The account answers back, which moves it to open again.
	sitiReply := call(t, sitiClient, http.MethodPost, server.URL+"/api/v1/tickets/"+itoa(int64(id))+"/reply", map[string]any{
		"body": "Still nothing.",
	}, sitiCSRF)

	if sitiReply.Status != http.StatusOK {
		t.Fatalf("an account reply answered %d: %v", sitiReply.Status, sitiReply.Body)
	}

	again := call(t, adminClient, http.MethodGet, server.URL+"/api/v1/tickets/"+itoa(int64(id)), nil, "")
	ticket, _ = again.Body["ticket"].(map[string]any)
	if ticket["status"] != "open" {
		t.Fatalf("after an account reply the ticket is %v, want open", ticket["status"])
	}

	closed := call(t, adminClient, http.MethodPost, server.URL+"/api/v1/tickets/"+itoa(int64(id))+"/status", map[string]any{
		"status": "closed",
	}, adminCSRF)

	if closed.Status != http.StatusOK {
		t.Fatalf("closing answered %d: %v", closed.Status, closed.Body)
	}

	refused := call(t, sitiClient, http.MethodPost, server.URL+"/api/v1/tickets/"+itoa(int64(id))+"/reply", map[string]any{
		"body": "One more thing.",
	}, sitiCSRF)

	if refused.Status != http.StatusConflict {
		t.Fatalf("a reply to a closed ticket answered %d, want 409: %v", refused.Status, refused.Body)
	}

	reopened := call(t, sitiClient, http.MethodPost, server.URL+"/api/v1/tickets/"+itoa(int64(id))+"/reply", map[string]any{
		"body":   "One more thing.",
		"reopen": true,
	}, sitiCSRF)

	if reopened.Status != http.StatusOK {
		t.Fatalf("reopening with a reply answered %d: %v", reopened.Status, reopened.Body)
	}
}

func TestAnnouncementsReachTheChosenAudience(t *testing.T) {
	server, st, _ := harness(t)
	adminClient, adminCSRF := signIn(t, server, st)

	hash, err := auth.HashPassword("rahasia123")
	if err != nil {
		t.Fatalf("hash: %v", err)
	}

	reseller, err := st.CreatePanelUser(context.Background(), "resel", "Reseller", "resel@example.test", hash, store.RoleReseller, nil)
	if err != nil {
		t.Fatalf("create the reseller: %v", err)
	}

	accountOwnedBy(t, st, "siti", reseller.ID, nil)

	sent := call(t, adminClient, http.MethodPost, server.URL+"/api/v1/announcements", map[string]any{
		"subject":  "Maintenance tonight",
		"body":     "The server reboots at 23:00 UTC.",
		"audience": []string{"user"},
		"email":    true,
	}, adminCSRF)

	if sent.Status != http.StatusCreated {
		t.Fatalf("sending an announcement answered %d: %v", sent.Status, sent.Body)
	}

	recipients, _ := sent.Body["recipient_count"].(float64)
	if recipients != 1 {
		t.Fatalf("the announcement reached %v accounts, want 1: %v", recipients, sent.Body)
	}

	sitiClient := signInAs(t, server, st, "siti", "rahasia123")
	if sitiClient == nil {
		t.Fatal("the hosting account could not sign in")
	}

	received := call(t, sitiClient, http.MethodGet, server.URL+"/api/v1/announcements", nil, "")
	if received.Status != http.StatusOK {
		t.Fatalf("reading announcements answered %d: %v", received.Status, received.Body)
	}

	list, _ := received.Body["received"].([]any)
	if len(list) != 1 {
		t.Fatalf("the account received %d message(s), want 1: %v", len(list), received.Body)
	}

	first, _ := list[0].(map[string]any)
	if first["subject"] != "Maintenance tonight" {
		t.Fatalf("the message has subject %v", first["subject"])
	}

	if received.Body["can_send"] != false {
		t.Fatalf("a hosting account may not send announcements: %v", received.Body["can_send"])
	}

	// A reseller addresses its own accounts only.
	resellerClient := signInAs(t, server, st, "resel", "rahasia123")
	if resellerClient == nil {
		t.Fatal("the reseller could not sign in")
	}

	probe := call(t, resellerClient, http.MethodGet, server.URL+"/api/v1/auth/session", nil, "")
	resellerCSRF, _ := probe.Body["csrfToken"].(string)

	fromReseller := call(t, resellerClient, http.MethodPost, server.URL+"/api/v1/announcements", map[string]any{
		"subject":  "Your bill",
		"body":     "It is due on Friday.",
		"audience": []string{"reseller"},
		"email":    false,
	}, resellerCSRF)

	if fromReseller.Status != http.StatusCreated {
		t.Fatalf("a reseller sending answered %d: %v", fromReseller.Status, fromReseller.Body)
	}

	audience, _ := fromReseller.Body["audience"].([]any)
	if len(audience) != 1 || audience[0] != "user" {
		t.Fatalf("a reseller addressed %v, want [user]", audience)
	}

	// The account sees both: one from the administrator, one from its reseller.
	both := call(t, sitiClient, http.MethodGet, server.URL+"/api/v1/announcements", nil, "")
	list, _ = both.Body["received"].([]any)
	if len(list) != 2 {
		t.Fatalf("the account received %d message(s), want 2: %v", len(list), both.Body)
	}
}
