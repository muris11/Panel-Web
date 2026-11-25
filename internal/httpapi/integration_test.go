package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"muriscore/panel/internal/agent"
	"muriscore/panel/internal/auth"
	"muriscore/panel/internal/config"
	"muriscore/panel/internal/db"
	"muriscore/panel/internal/store"
	"muriscore/panel/internal/worker"
)

// These tests need a real MySQL/MariaDB, because the store's SQL is written
// for one. Point MURISCORE_TEST_DSN at a
// throwaway database whose name ends in
// _test; the tests empty it. Without it they skip, so `go test ./...` works on
// a machine with no database.
//
//	MURISCORE_TEST_DSN='root@tcp(127.0.0.1:3307)/muriscore_test?parseTime=true&loc=UTC' go test ./...
func harness(t *testing.T) (*httptest.Server, *store.Store, *worker.Worker) {
	t.Helper()

	dsn := os.Getenv("MURISCORE_TEST_DSN")
	if dsn == "" {
		t.Skip("set MURISCORE_TEST_DSN to run the panel's database tests")
	}

	if !strings.Contains(dsn, "_test") {
		t.Fatalf("the test DSN must name a database ending in _test; it is emptied by these tests: %s", dsn)
	}

	ctx := context.Background()

	pool, err := db.Open(ctx, dsn)
	if err != nil {
		t.Fatalf("open the test database: %v", err)
	}

	t.Cleanup(func() { _ = pool.Close() })

	if _, err := db.Migrate(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	// Order matters: children before parents.
	for _, statement := range []string{
		"DELETE FROM `ticket_messages`",
		"DELETE FROM `tickets`",
		"DELETE FROM `announcements`",
		"DELETE FROM `database_database_user`",
		"DELETE FROM `database_remote_hosts`",
		"DELETE FROM `database_users`",
		"DELETE FROM `databases`",
		"DELETE FROM `dns_records`",
		"DELETE FROM `dns_zones`",
		"DELETE FROM `mail_forwarders`",
		"DELETE FROM `mailboxes`",
		"DELETE FROM `mail_domains`",
		"DELETE FROM `ftp_accounts`",
		"DELETE FROM `cron_jobs`",
		"DELETE FROM `api_tokens`",
		"DELETE FROM `backup_runs`",
		"DELETE FROM `tasks`",
		"DELETE FROM `audit_logs`",
		"DELETE FROM `panel_sessions`",
		"DELETE FROM `domains`",
		"DELETE FROM `users`",
		"DELETE FROM `reseller_packages`",
		"DELETE FROM `packages`",
	} {
		if _, err := pool.ExecContext(ctx, statement); err != nil {
			t.Fatalf("%s: %v", statement, err)
		}
	}

	cfg := config.Config{
		Listen:           "127.0.0.1:0",
		DatabaseDSN:      dsn,
		AgentDriver:      "fake",
		AgentTimeout:     5 * time.Second,
		AgentTaskTimeout: 30 * time.Second,
		SessionTTL:       time.Hour,
		// A real deployment has an address and a pair of nameservers; the
		// default records and the DNS page both read them.
		PublicIP:    "203.0.113.10",
		Nameservers: []string{"ns1.example.test", "ns2.example.test"},
	}

	logOut := io.Writer(io.Discard)
	if os.Getenv("MURISCORE_TEST_VERBOSE") != "" {
		logOut = os.Stderr
	}

	logger := slog.New(slog.NewTextHandler(logOut, nil))
	st := store.New(pool)
	fake := agent.NewFake()

	server := httptest.NewServer(New(cfg, st, fake, logger).Handler())
	t.Cleanup(server.Close)

	return server, st, worker.New(st, fake, logger, worker.Config{Poll: time.Millisecond, TaskTimeout: 30 * time.Second})
}

// client returns a cookie-keeping client, the way a browser behaves.
func client(t *testing.T) *http.Client {
	t.Helper()

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookie jar: %v", err)
	}

	return &http.Client{Jar: jar, Timeout: 10 * time.Second}
}

type envelope struct {
	Status int
	Body   map[string]any
}

func call(t *testing.T, client *http.Client, method, url string, body any, csrf string) envelope {
	t.Helper()

	var payload io.Reader

	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("encode body: %v", err)
		}

		payload = bytes.NewReader(encoded)
	}

	request, err := http.NewRequest(method, url, payload)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}

	request.Header.Set("Accept", "application/json")

	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}

	if csrf != "" {
		request.Header.Set("X-CSRF-Token", csrf)
	}

	response, err := client.Do(request)
	if err != nil {
		t.Fatalf("%s %s: %v", method, url, err)
	}

	defer func() { _ = response.Body.Close() }()

	raw, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}

	decoded := map[string]any{}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &decoded); err != nil {
			t.Fatalf("%s %s returned %d with a body that is not JSON: %s", method, url, response.StatusCode, raw)
		}
	}

	return envelope{Status: response.StatusCode, Body: decoded}
}

// signIn creates an administrator and returns a client signed in as it, plus
// that session's CSRF token.
func signIn(t *testing.T, server *httptest.Server, st *store.Store) (*http.Client, string) {
	t.Helper()

	hash, err := auth.HashPassword("rahasia123")
	if err != nil {
		t.Fatalf("hash: %v", err)
	}

	if _, err := st.CreatePanelUser(context.Background(), "admin", "Administrator", "admin@example.test", hash, store.RoleAdmin, nil); err != nil {
		t.Fatalf("create the administrator: %v", err)
	}

	httpClient := client(t)

	probe := call(t, httpClient, http.MethodGet, server.URL+"/api/v1/auth/session", nil, "")
	if probe.Status != http.StatusOK {
		t.Fatalf("session probe answered %d", probe.Status)
	}

	csrf, _ := probe.Body["csrfToken"].(string)
	if csrf == "" {
		t.Fatal("the session probe did not hand out a CSRF token")
	}

	// The login endpoint also accepts the pre-session cookie, but a browser
	// sends the header, so that is what is tested.
	login := call(t, httpClient, http.MethodPost, server.URL+"/api/v1/auth/login", map[string]any{
		"login":    "admin",
		"password": "rahasia123",
	}, csrf)

	if login.Status != http.StatusOK {
		t.Fatalf("sign in answered %d: %v", login.Status, login.Body)
	}

	token, _ := login.Body["csrfToken"].(string)
	if token == "" {
		t.Fatal("sign in did not return a session CSRF token")
	}

	return httpClient, token
}

func TestHealthIsPublicAndReportsTheAgent(t *testing.T) {
	server, _, _ := harness(t)

	response := call(t, client(t), http.MethodGet, server.URL+"/api/v1/health", nil, "")

	if response.Status != http.StatusOK {
		t.Fatalf("health answered %d: %v", response.Status, response.Body)
	}

	if response.Body["database"] != "ok" {
		t.Fatalf("health says the database is %v", response.Body["database"])
	}
}

func TestAccountEndpointsRequireSignIn(t *testing.T) {
	server, _, _ := harness(t)

	for _, path := range []string{"/api/v1/dashboard", "/api/v1/accounts", "/api/v1/tasks"} {
		response := call(t, client(t), http.MethodGet, server.URL+path, nil, "")

		if response.Status != http.StatusUnauthorized {
			t.Errorf("%s answered %d to a signed-out request", path, response.Status)
		}
	}
}

func TestLoginNeedsCSRF(t *testing.T) {
	server, st, _ := harness(t)

	hash, err := auth.HashPassword("rahasia123")
	if err != nil {
		t.Fatalf("hash: %v", err)
	}

	if _, err := st.CreatePanelUser(context.Background(), "admin", "Administrator", "admin@example.test", hash, store.RoleAdmin, nil); err != nil {
		t.Fatalf("create the administrator: %v", err)
	}

	httpClient := client(t)

	// No token at all.
	response := call(t, httpClient, http.MethodPost, server.URL+"/api/v1/auth/login", map[string]any{
		"login":    "admin",
		"password": "rahasia123",
	}, "")

	if response.Status != http.StatusForbidden {
		t.Fatalf("a login without a CSRF token answered %d", response.Status)
	}

	// A token the panel never issued.
	response = call(t, httpClient, http.MethodPost, server.URL+"/api/v1/auth/login", map[string]any{
		"login":    "admin",
		"password": "rahasia123",
	}, strings.Repeat("0", 64))

	if response.Status != http.StatusForbidden {
		t.Fatalf("a login with a made-up CSRF token answered %d", response.Status)
	}
}

func TestWrongPasswordIsRefused(t *testing.T) {
	server, st, _ := harness(t)

	hash, err := auth.HashPassword("rahasia123")
	if err != nil {
		t.Fatalf("hash: %v", err)
	}

	if _, err := st.CreatePanelUser(context.Background(), "admin", "Administrator", "admin@example.test", hash, store.RoleAdmin, nil); err != nil {
		t.Fatalf("create the administrator: %v", err)
	}

	httpClient := client(t)

	probe := call(t, httpClient, http.MethodGet, server.URL+"/api/v1/auth/session", nil, "")
	csrf, _ := probe.Body["csrfToken"].(string)

	response := call(t, httpClient, http.MethodPost, server.URL+"/api/v1/auth/login", map[string]any{
		"login":    "admin",
		"password": "wrong password",
	}, csrf)

	if response.Status != http.StatusUnauthorized {
		t.Fatalf("a wrong password answered %d", response.Status)
	}

	// The message must not say whether the account exists.
	message, _ := response.Body["error"].(map[string]any)["message"].(string)
	if strings.Contains(strings.ToLower(message), "password") && strings.Contains(strings.ToLower(message), "wrong") {
		t.Fatalf("the error message leaks which part was wrong: %q", message)
	}
}

// The whole path a hosting account takes: created by the panel, provisioned
// by the worker through the agent, then visible as active.
func TestCreateAccountThenProvisioned(t *testing.T) {
	server, st, queue := harness(t)
	httpClient, csrf := signIn(t, server, st)

	created := call(t, httpClient, http.MethodPost, server.URL+"/api/v1/accounts", map[string]any{
		"username": "budi",
		"name":     "Budi Santoso",
		"email":    "budi@example.test",
		"password": "rahasia123",
		"domain":   "budi.example.test",
	}, csrf)

	if created.Status != http.StatusCreated {
		t.Fatalf("creating an account answered %d: %v", created.Status, created.Body)
	}

	account, _ := created.Body["account"].(map[string]any)

	if account["status"] != "pending" {
		t.Fatalf("a new account is %v, want pending until the agent provisions it", account["status"])
	}

	// Two pieces of work: the Linux user and the website.
	for drained := 0; drained < 2; drained++ {
		if !queue.RunOnce(context.Background()) {
			t.Fatalf("only %d task(s) were queued, expected 2", drained)
		}
	}

	detailed := call(t, httpClient, http.MethodGet, server.URL+"/api/v1/accounts/budi", nil, "")

	if detailed.Status != http.StatusOK {
		t.Fatalf("the account page answered %d: %v", detailed.Status, detailed.Body)
	}

	provisioned, _ := detailed.Body["account"].(map[string]any)

	if provisioned["status"] != "active" {
		t.Fatalf("the account is %v after provisioning, want active", provisioned["status"])
	}

	if provisioned["uid"] == nil {
		t.Fatal("the account has no uid after provisioning")
	}

	domains, _ := detailed.Body["domains"].([]any)
	if len(domains) != 1 {
		t.Fatalf("the account has %d domains, want 1", len(domains))
	}
}

func TestDuplicateUsernameIsRefused(t *testing.T) {
	server, st, _ := harness(t)
	httpClient, csrf := signIn(t, server, st)

	body := map[string]any{
		"username": "siti",
		"name":     "Siti",
		"email":    "siti@example.test",
		"password": "rahasia123",
	}

	first := call(t, httpClient, http.MethodPost, server.URL+"/api/v1/accounts", body, csrf)
	if first.Status != http.StatusCreated {
		t.Fatalf("the first account answered %d: %v", first.Status, first.Body)
	}

	second := call(t, httpClient, http.MethodPost, server.URL+"/api/v1/accounts", body, csrf)

	if second.Status != http.StatusConflict {
		t.Fatalf("a duplicate username answered %d, want 409", second.Status)
	}

	fields, _ := second.Body["error"].(map[string]any)["fields"].(map[string]any)
	if fields["username"] == nil {
		t.Fatalf("the conflict did not name the username field: %v", second.Body)
	}
}

func TestValidationIsReportedPerField(t *testing.T) {
	server, st, _ := harness(t)
	httpClient, csrf := signIn(t, server, st)

	response := call(t, httpClient, http.MethodPost, server.URL+"/api/v1/accounts", map[string]any{
		"username": "root",
		"name":     "",
		"email":    "not-an-email",
		"password": "short",
		"domain":   "localhost",
	}, csrf)

	if response.Status != http.StatusUnprocessableEntity {
		t.Fatalf("a bad form answered %d: %v", response.Status, response.Body)
	}

	fields, _ := response.Body["error"].(map[string]any)["fields"].(map[string]any)

	for _, name := range []string{"username", "name", "email", "password", "domain"} {
		if fields[name] == nil {
			t.Errorf("the answer did not mention the %s field", name)
		}
	}
}

func TestSuspendQueuesWorkAndAuditsIt(t *testing.T) {
	server, st, queue := harness(t)
	httpClient, csrf := signIn(t, server, st)

	call(t, httpClient, http.MethodPost, server.URL+"/api/v1/accounts", map[string]any{
		"username": "budi",
		"name":     "Budi",
		"email":    "budi@example.test",
		"password": "rahasia123",
	}, csrf)

	queue.RunOnce(context.Background())

	suspended := call(t, httpClient, http.MethodPost, server.URL+"/api/v1/accounts/budi/suspend", map[string]any{
		"reason": "unpaid invoice",
	}, csrf)

	if suspended.Status != http.StatusAccepted {
		t.Fatalf("suspending answered %d: %v", suspended.Status, suspended.Body)
	}

	queue.RunOnce(context.Background())

	detailed := call(t, httpClient, http.MethodGet, server.URL+"/api/v1/accounts/budi", nil, "")
	account, _ := detailed.Body["account"].(map[string]any)

	if account["status"] != "suspended" {
		t.Fatalf("the account is %v after suspension", account["status"])
	}

	if account["reason"] != "unpaid invoice" {
		t.Fatalf("the suspension reason is %q", account["reason"])
	}

	// A suspended hosting account may not sign in.
	other := client(t)
	probe := call(t, other, http.MethodGet, server.URL+"/api/v1/auth/session", nil, "")
	otherCSRF, _ := probe.Body["csrfToken"].(string)

	login := call(t, other, http.MethodPost, server.URL+"/api/v1/auth/login", map[string]any{
		"login":    "budi",
		"password": "rahasia123",
	}, otherCSRF)

	if login.Status != http.StatusForbidden {
		t.Fatalf("a suspended account signed in with status %d", login.Status)
	}
}

func TestDeleteRequiresTheUsernameAsConfirmation(t *testing.T) {
	server, st, queue := harness(t)
	httpClient, csrf := signIn(t, server, st)

	call(t, httpClient, http.MethodPost, server.URL+"/api/v1/accounts", map[string]any{
		"username": "budi",
		"name":     "Budi",
		"email":    "budi@example.test",
		"password": "rahasia123",
	}, csrf)

	queue.RunOnce(context.Background())

	// Without the confirmation the panel must refuse.
	if response := call(t, httpClient, http.MethodDelete, server.URL+"/api/v1/accounts/budi", nil, csrf); response.Status != http.StatusBadRequest {
		t.Fatalf("a delete without confirmation answered %d", response.Status)
	}

	if response := call(t, httpClient, http.MethodDelete, server.URL+"/api/v1/accounts/budi?confirm=budi", nil, csrf); response.Status != http.StatusAccepted {
		t.Fatalf("a confirmed delete answered %d: %v", response.Status, response.Body)
	}
}

func TestSignOutEndsTheSession(t *testing.T) {
	server, st, _ := harness(t)
	httpClient, csrf := signIn(t, server, st)

	if response := call(t, httpClient, http.MethodPost, server.URL+"/api/v1/auth/logout", nil, csrf); response.Status != http.StatusOK {
		t.Fatalf("signing out answered %d", response.Status)
	}

	if response := call(t, httpClient, http.MethodGet, server.URL+"/api/v1/dashboard", nil, ""); response.Status != http.StatusUnauthorized {
		t.Fatalf("the dashboard answered %d after signing out", response.Status)
	}
}

// A reseller must not see or touch an account that belongs to another
// reseller, and must not learn whether it exists.
func TestResellerCannotTouchAnotherResellersAccount(t *testing.T) {
	server, st, queue := harness(t)
	adminClient, adminCSRF := signIn(t, server, st)

	hash, err := auth.HashPassword("rahasia123")
	if err != nil {
		t.Fatalf("hash: %v", err)
	}

	if _, err := st.CreatePanelUser(context.Background(), "resel", "Reseller", "resel@example.test", hash, store.RoleReseller, nil); err != nil {
		t.Fatalf("create the reseller: %v", err)
	}

	call(t, adminClient, http.MethodPost, server.URL+"/api/v1/accounts", map[string]any{
		"username": "budi",
		"name":     "Budi",
		"email":    "budi@example.test",
		"password": "rahasia123",
	}, adminCSRF)

	queue.RunOnce(context.Background())

	resellerClient := client(t)

	probe := call(t, resellerClient, http.MethodGet, server.URL+"/api/v1/auth/session", nil, "")
	csrf, _ := probe.Body["csrfToken"].(string)

	login := call(t, resellerClient, http.MethodPost, server.URL+"/api/v1/auth/login", map[string]any{
		"login":    "resel",
		"password": "rahasia123",
	}, csrf)

	if login.Status != http.StatusOK {
		t.Fatalf("the reseller could not sign in: %d %v", login.Status, login.Body)
	}

	resellerCSRF, _ := login.Body["csrfToken"].(string)

	response := call(t, resellerClient, http.MethodGet, server.URL+"/api/v1/accounts/budi", nil, "")

	if response.Status == http.StatusOK {
		t.Fatal("a reseller read an account it does not own")
	}

	if response.Status != http.StatusNotFound {
		t.Fatalf("a reseller got %d for somebody else's account; 404 keeps the name private", response.Status)
	}

	attempt := call(t, resellerClient, http.MethodPost, server.URL+"/api/v1/accounts/budi/suspend", map[string]any{
		"reason": "not mine to touch",
	}, resellerCSRF)

	if attempt.Status != http.StatusNotFound {
		t.Fatalf("a reseller tried to suspend somebody else's account and got %d", attempt.Status)
	}
}

func TestSessionListMarksTheCurrentOne(t *testing.T) {
	server, st, _ := harness(t)
	httpClient, _ := signIn(t, server, st)

	response := call(t, httpClient, http.MethodGet, server.URL+"/api/v1/sessions", nil, "")

	if response.Status != http.StatusOK {
		t.Fatalf("the session list answered %d", response.Status)
	}

	sessions, _ := response.Body["sessions"].([]any)
	if len(sessions) == 0 {
		t.Fatal("the session list is empty while signed in")
	}

	current, _ := sessions[0].(map[string]any)
	if current["current"] != true {
		t.Fatalf("no session in the list is marked current: %v", sessions[0])
	}

	if current["token"] != nil {
		t.Fatal("the session list leaks a token")
	}
}
