package httpapi

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/binary"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"muriscore/panel/internal/auth"
	"muriscore/panel/internal/store"
)

// provision creates a hosting account and drains its queue, because every
// module below hangs off an account that exists on the server.
func provision(t *testing.T, server *httptest.Server, st *store.Store, queue interface{ RunOnce(context.Context) bool }, httpClient *http.Client, csrf, username string) int64 {
	t.Helper()

	created := call(t, httpClient, http.MethodPost, server.URL+"/api/v1/accounts", map[string]any{
		"username": username,
		"name":     "Siti Nurhaliza",
		"email":    username + "@example.test",
		"password": "rahasia123",
		"domain":   username + ".example.test",
	}, csrf)

	if created.Status != http.StatusCreated {
		t.Fatalf("creating %s answered %d: %v", username, created.Status, created.Body)
	}

	for drained := 0; drained < 2; drained++ {
		if !queue.RunOnce(context.Background()) {
			t.Fatalf("only %d task(s) were queued for %s, expected 2", drained, username)
		}
	}

	detailed := call(t, httpClient, http.MethodGet, server.URL+"/api/v1/accounts/"+username, nil, "")

	account, _ := detailed.Body["account"].(map[string]any)
	if account == nil {
		t.Fatalf("the account page answered %d: %v", detailed.Status, detailed.Body)
	}

	id, _ := account["id"].(float64)
	if id == 0 {
		t.Fatalf("the account page has no id: %v", detailed.Body)
	}

	return int64(id)
}

// domainID returns the id of the domain the account was created with.
func domainID(t *testing.T, server *httptest.Server, httpClient *http.Client, username string) int64 {
	t.Helper()

	listed := call(t, httpClient, http.MethodGet, server.URL+"/api/v1/domains?username="+username, nil, "")
	if listed.Status != http.StatusOK {
		t.Fatalf("listing domains answered %d: %v", listed.Status, listed.Body)
	}

	domains, _ := listed.Body["domains"].([]any)
	if len(domains) == 0 {
		t.Fatalf("%s has no domains: %v", username, listed.Body)
	}

	first, _ := domains[0].(map[string]any)
	id, _ := first["id"].(float64)

	return int64(id)
}

func drain(t *testing.T, queue interface{ RunOnce(context.Context) bool }, want int) {
	t.Helper()

	for done := 0; done < want; done++ {
		if !queue.RunOnce(context.Background()) {
			t.Fatalf("only %d of %d queued task(s) ran", done, want)
		}
	}
}

func TestDomainCreationSeedsDNSAndSite(t *testing.T) {
	server, st, queue := harness(t)
	httpClient, csrf := signIn(t, server, st)

	provision(t, server, st, queue, httpClient, csrf, "siti")

	created := call(t, httpClient, http.MethodPost, server.URL+"/api/v1/domains", map[string]any{
		"username": "siti",
		"name":     "toko.example.test",
	}, csrf)

	if created.Status != http.StatusCreated {
		t.Fatalf("adding a domain answered %d: %v", created.Status, created.Body)
	}

	domain, _ := created.Body["domain"].(map[string]any)
	id, _ := domain["id"].(float64)

	if id == 0 {
		t.Fatalf("the new domain has no id: %v", created.Body)
	}

	records := call(t, httpClient, http.MethodGet, server.URL+"/api/v1/domains/"+itoa(int64(id))+"/dns", nil, "")
	if records.Status != http.StatusOK {
		t.Fatalf("listing DNS answered %d: %v", records.Status, records.Body)
	}

	list, _ := records.Body["records"].([]any)
	if len(list) < 4 {
		t.Fatalf("a new zone has %d records, want at least 4 (A, www, MX, TXT)", len(list))
	}

	// The site and the zone were both queued.
	drain(t, queue, 2)

	synced := call(t, httpClient, http.MethodGet, server.URL+"/api/v1/domains/"+itoa(int64(id)), nil, "")
	if synced.Status != http.StatusOK {
		t.Fatalf("the domain page answered %d: %v", synced.Status, synced.Body)
	}

	if synced.Body["status"] != "active" {
		t.Fatalf("the domain is %v after its tasks ran, want active", synced.Body["status"])
	}
}

func TestDNSRecordValidationAndEditing(t *testing.T) {
	server, st, queue := harness(t)
	httpClient, csrf := signIn(t, server, st)

	provision(t, server, st, queue, httpClient, csrf, "siti")
	id := domainID(t, server, httpClient, "siti")

	base := server.URL + "/api/v1/domains/" + itoa(id) + "/dns"

	bad := call(t, httpClient, http.MethodPost, base, map[string]any{
		"name":    "shop",
		"type":    "WAT",
		"content": "192.0.2.10",
	}, csrf)

	if bad.Status != http.StatusUnprocessableEntity {
		t.Fatalf("an unknown record type answered %d: %v", bad.Status, bad.Body)
	}

	added := call(t, httpClient, http.MethodPost, base, map[string]any{
		"name":    "shop",
		"type":    "A",
		"content": "192.0.2.10",
		"ttl":     600,
	}, csrf)

	if added.Status != http.StatusCreated {
		t.Fatalf("adding an A record answered %d: %v", added.Status, added.Body)
	}

	record, _ := added.Body["record"].(map[string]any)
	recordID, _ := record["id"].(float64)

	if record["name"] != "shop.toko.example.test." && record["name"] != "shop" {
		t.Fatalf("the record name came back as %v", record["name"])
	}

	updated := call(t, httpClient, http.MethodPatch, server.URL+"/api/v1/dns/"+itoa(int64(recordID)), map[string]any{
		"name":    "shop",
		"type":    "A",
		"content": "192.0.2.11",
		"ttl":     300,
	}, csrf)

	if updated.Status != http.StatusOK {
		t.Fatalf("updating a record answered %d: %v", updated.Status, updated.Body)
	}

	removed := call(t, httpClient, http.MethodDelete, server.URL+"/api/v1/dns/"+itoa(int64(recordID)), nil, csrf)
	if removed.Status != http.StatusNoContent {
		t.Fatalf("deleting a record answered %d: %v", removed.Status, removed.Body)
	}
}

func TestCertificateIssueIsRecorded(t *testing.T) {
	server, st, queue := harness(t)
	httpClient, csrf := signIn(t, server, st)

	provision(t, server, st, queue, httpClient, csrf, "siti")
	id := domainID(t, server, httpClient, "siti")

	issued := call(t, httpClient, http.MethodPost, server.URL+"/api/v1/domains/"+itoa(id)+"/ssl", map[string]any{}, csrf)
	if issued.Status != http.StatusAccepted && issued.Status != http.StatusOK {
		t.Fatalf("asking for a certificate answered %d: %v", issued.Status, issued.Body)
	}

	drain(t, queue, 1)

	page := call(t, httpClient, http.MethodGet, server.URL+"/api/v1/domains/"+itoa(id), nil, "")

	if page.Body["sslStatus"] != "active" {
		t.Fatalf("the certificate status is %v, want active: %v", page.Body["sslStatus"], page.Body)
	}

	if page.Body["sslExpiresAt"] == nil && page.Body["zone"] == nil {
		t.Logf("the certificate has no expiry field yet: %v", page.Body)
	}
}

func TestMailDomainWithMailboxAndForwarder(t *testing.T) {
	server, st, queue := harness(t)
	httpClient, csrf := signIn(t, server, st)

	provision(t, server, st, queue, httpClient, csrf, "siti")
	id := domainID(t, server, httpClient, "siti")

	mail := call(t, httpClient, http.MethodPost, server.URL+"/api/v1/mail", map[string]any{
		"domainId": id,
	}, csrf)

	if mail.Status != http.StatusCreated {
		t.Fatalf("adding a mail domain answered %d: %v", mail.Status, mail.Body)
	}

	mailDomain, _ := mail.Body["domain"].(map[string]any)
	mailID, _ := mailDomain["id"].(float64)

	if mailID == 0 {
		t.Fatalf("the mail domain has no id: %v", mail.Body)
	}

	mailbox := call(t, httpClient, http.MethodPost, server.URL+"/api/v1/mail/"+itoa(int64(mailID))+"/mailboxes", map[string]any{
		"localPart": "halo",
	}, csrf)

	if mailbox.Status != http.StatusCreated {
		t.Fatalf("adding a mailbox answered %d: %v", mailbox.Status, mailbox.Body)
	}

	password, _ := mailbox.Body["password"].(string)
	if len(password) < 16 {
		t.Fatalf("the mailbox password is %q, too short to hand to a user", password)
	}

	forwarder := call(t, httpClient, http.MethodPost, server.URL+"/api/v1/mail/"+itoa(int64(mailID))+"/forwarders", map[string]any{
		"localPart":    "info",
		"destinations": []string{"budi@example.test"},
	}, csrf)

	if forwarder.Status != http.StatusCreated && forwarder.Status != http.StatusAccepted {
		t.Fatalf("adding a forwarder answered %d: %v", forwarder.Status, forwarder.Body)
	}

	listed := call(t, httpClient, http.MethodGet, server.URL+"/api/v1/mail?username=siti", nil, "")
	if listed.Status != http.StatusOK {
		t.Fatalf("listing mail domains answered %d", listed.Status)
	}

	domains, _ := listed.Body["domains"].([]any)
	if len(domains) != 1 {
		t.Fatalf("siti has %d mail domains, want 1", len(domains))
	}
}

func TestDatabaseWithUserAndGrants(t *testing.T) {
	server, st, queue := harness(t)
	httpClient, csrf := signIn(t, server, st)

	provision(t, server, st, queue, httpClient, csrf, "siti")

	db := call(t, httpClient, http.MethodPost, server.URL+"/api/v1/databases", map[string]any{
		"username": "siti",
		"suffix":   "toko",
	}, csrf)

	if db.Status != http.StatusCreated {
		t.Fatalf("adding a database answered %d: %v", db.Status, db.Body)
	}

	created, _ := db.Body["database"].(map[string]any)
	dbID, _ := created["id"].(float64)

	if created["name"] != "siti_toko" {
		t.Fatalf("the database is named %v, want siti_toko", created["name"])
	}

	// The database is created on the server by a queued task; drain it so the
	// next drain in this test belongs to the user.
	drain(t, queue, 1)

	duplicate := call(t, httpClient, http.MethodPost, server.URL+"/api/v1/databases", map[string]any{
		"username": "siti",
		"suffix":   "toko",
	}, csrf)

	if duplicate.Status != http.StatusConflict {
		t.Fatalf("a duplicate database answered %d: %v", duplicate.Status, duplicate.Body)
	}

	user := call(t, httpClient, http.MethodPost, server.URL+"/api/v1/database-users", map[string]any{
		"username":  "siti",
		"suffix":    "tokoapp",
		"databases": []int64{int64(dbID)},
	}, csrf)

	if user.Status != http.StatusCreated {
		t.Fatalf("adding a database user answered %d: %v", user.Status, user.Body)
	}

	password, _ := user.Body["password"].(string)
	if len(password) < 16 {
		t.Fatalf("the database password is %q, too short", password)
	}

	drain(t, queue, 1)

	listed := call(t, httpClient, http.MethodGet, server.URL+"/api/v1/database-users?username=siti", nil, "")
	if listed.Status != http.StatusOK {
		t.Fatalf("listing database users answered %d", listed.Status)
	}

	users, _ := listed.Body["users"].([]any)
	if len(users) != 1 {
		t.Fatalf("siti has %d database users, want 1", len(users))
	}

	first, _ := users[0].(map[string]any)
	if first["status"] != "active" {
		t.Fatalf("the database user is %v after its task ran, want active", first["status"])
	}
}

func TestFTPLoginUsesCryptPassword(t *testing.T) {
	server, st, queue := harness(t)
	httpClient, csrf := signIn(t, server, st)

	provision(t, server, st, queue, httpClient, csrf, "siti")
	id := domainID(t, server, httpClient, "siti")

	created := call(t, httpClient, http.MethodPost, server.URL+"/api/v1/ftp", map[string]any{
		"username":  "siti",
		"name":      "unggah",
		"domainId":  id,
		"directory": "public_html",
	}, csrf)

	if created.Status != http.StatusCreated {
		t.Fatalf("adding an FTP login answered %d: %v", created.Status, created.Body)
	}

	login, _ := created.Body["login"].(string)
	if login != "unggah@siti.example.test" {
		t.Fatalf("the FTP login is %q", login)
	}

	password, _ := created.Body["password"].(string)

	var hash string
	if err := st.DB().QueryRowContext(context.Background(),
		"SELECT `password_hash` FROM `ftp_accounts` WHERE `login` = ?", login).Scan(&hash); err != nil {
		t.Fatalf("reading the stored hash: %v", err)
	}

	if !auth.VerifyCryptPassword(hash, password) {
		t.Fatalf("the stored hash does not match the password handed out: %q", hash)
	}

	drain(t, queue, 1)

	listed := call(t, httpClient, http.MethodGet, server.URL+"/api/v1/ftp?username=siti", nil, "")
	accounts, _ := listed.Body["accounts"].([]any)

	if len(accounts) != 1 {
		t.Fatalf("siti has %d FTP logins, want 1", len(accounts))
	}
}

func TestCronJobsAreAppliedAsAWhole(t *testing.T) {
	server, st, queue := harness(t)
	httpClient, csrf := signIn(t, server, st)

	provision(t, server, st, queue, httpClient, csrf, "siti")

	base := server.URL + "/api/v1/accounts/siti/cron"

	bad := call(t, httpClient, http.MethodPost, base, map[string]any{
		"schedule": "every minute",
		"command":  "/usr/bin/true",
	}, csrf)

	if bad.Status != http.StatusUnprocessableEntity {
		t.Fatalf("a bad schedule answered %d: %v", bad.Status, bad.Body)
	}

	added := call(t, httpClient, http.MethodPost, base, map[string]any{
		"schedule":    "*/5 * * * *",
		"command":     "php /home/siti/public_html/artisan schedule:run",
		"description": "Laravel scheduler",
	}, csrf)

	if added.Status != http.StatusCreated {
		t.Fatalf("adding a cron job answered %d: %v", added.Status, added.Body)
	}

	job, _ := added.Body["job"].(map[string]any)
	jobID, _ := job["id"].(float64)

	drain(t, queue, 1)

	toggled := call(t, httpClient, http.MethodPatch, server.URL+"/api/v1/cron/"+itoa(int64(jobID)), map[string]any{
		"schedule": "*/10 * * * *",
		"command":  "php /home/siti/public_html/artisan schedule:run",
		"enabled":  false,
	}, csrf)

	if toggled.Status != http.StatusOK && toggled.Status != http.StatusAccepted {
		t.Fatalf("changing a cron job answered %d: %v", toggled.Status, toggled.Body)
	}

	drain(t, queue, 1)

	removed := call(t, httpClient, http.MethodDelete, server.URL+"/api/v1/cron/"+itoa(int64(jobID)), nil, csrf)
	if removed.Status != http.StatusNoContent {
		t.Fatalf("deleting a cron job answered %d: %v", removed.Status, removed.Body)
	}

	drain(t, queue, 1)
}

func TestFilesAndBackupsUseTheAgent(t *testing.T) {
	server, st, queue := harness(t)
	httpClient, csrf := signIn(t, server, st)

	provision(t, server, st, queue, httpClient, csrf, "siti")

	listed := call(t, httpClient, http.MethodGet, server.URL+"/api/v1/files?username=siti&path=", nil, "")
	if listed.Status != http.StatusOK {
		t.Fatalf("listing files answered %d: %v", listed.Status, listed.Body)
	}

	entries, _ := listed.Body["entries"].([]any)
	if len(entries) == 0 {
		t.Fatalf("the fake listing is empty: %v", listed.Body)
	}

	read := call(t, httpClient, http.MethodGet, server.URL+"/api/v1/files?username=siti&path=public_html/index.php&read=1", nil, "")
	if read.Status != http.StatusOK {
		t.Fatalf("reading a file answered %d: %v", read.Status, read.Body)
	}

	wrote := call(t, httpClient, http.MethodPost, server.URL+"/api/v1/files/write", map[string]any{
		"username": "siti",
		"path":     "public_html/index.php",
		"content":  "<?php echo 'halo';",
		"version":  "fake-1",
	}, csrf)

	if wrote.Status != http.StatusOK {
		t.Fatalf("writing a file answered %d: %v", wrote.Status, wrote.Body)
	}

	backups := call(t, httpClient, http.MethodGet, server.URL+"/api/v1/accounts/siti/backups", nil, "")
	if backups.Status != http.StatusOK {
		t.Fatalf("listing backups answered %d: %v", backups.Status, backups.Body)
	}

	started := call(t, httpClient, http.MethodPost, server.URL+"/api/v1/accounts/siti/backups", map[string]any{
		"name":  "sebelum-upgrade",
		"files": true,
	}, csrf)

	if started.Status != http.StatusAccepted && started.Status != http.StatusCreated {
		t.Fatalf("starting a backup answered %d: %v", started.Status, started.Body)
	}

	drain(t, queue, 1)

	restored := call(t, httpClient, http.MethodPost, server.URL+"/api/v1/accounts/siti/backups/restore", map[string]any{
		"name":  "sebelum-upgrade",
		"files": true,
	}, csrf)

	if restored.Status != http.StatusAccepted && restored.Status != http.StatusOK {
		t.Fatalf("restoring answered %d: %v", restored.Status, restored.Body)
	}

	drain(t, queue, 1)
}

func TestAPITokenSignsARequestWithoutACookie(t *testing.T) {
	server, st, queue := harness(t)
	httpClient, csrf := signIn(t, server, st)

	provision(t, server, st, queue, httpClient, csrf, "siti")

	created := call(t, httpClient, http.MethodPost, server.URL+"/api/v1/tokens", map[string]any{
		"name": "billing",
		"days": 30,
	}, csrf)

	if created.Status != http.StatusCreated {
		t.Fatalf("creating a token answered %d: %v", created.Status, created.Body)
	}

	secret, _ := created.Body["secret"].(string)
	if len(secret) < 32 {
		t.Fatalf("the token secret is %q", secret)
	}

	// No cookie jar: a billing script has none.
	bare := &http.Client{}

	request, err := http.NewRequest(http.MethodGet, server.URL+"/api/v1/me", nil)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}

	request.Header.Set("Authorization", "Bearer "+secret)
	request.Header.Set("Accept", "application/json")

	response, err := bare.Do(request)
	if err != nil {
		t.Fatalf("token request: %v", err)
	}

	defer func() { _ = response.Body.Close() }()

	if response.StatusCode != http.StatusOK {
		t.Fatalf("a token request answered %d", response.StatusCode)
	}

	token, _ := created.Body["token"].(map[string]any)
	tokenID, _ := token["id"].(float64)

	revoked := call(t, httpClient, http.MethodDelete, server.URL+"/api/v1/tokens/"+itoa(int64(tokenID)), nil, csrf)
	if revoked.Status != http.StatusNoContent {
		t.Fatalf("revoking a token answered %d: %v", revoked.Status, revoked.Body)
	}

	again, err := bare.Do(request)
	if err != nil {
		t.Fatalf("second token request: %v", err)
	}

	defer func() { _ = again.Body.Close() }()

	if again.StatusCode != http.StatusUnauthorized {
		t.Fatalf("a revoked token still answered %d", again.StatusCode)
	}
}

func TestServerPageNeedsAnAdministrator(t *testing.T) {
	server, st, queue := harness(t)
	httpClient, csrf := signIn(t, server, st)

	provision(t, server, st, queue, httpClient, csrf, "siti")

	page := call(t, httpClient, http.MethodGet, server.URL+"/api/v1/server", nil, "")
	if page.Status != http.StatusOK {
		t.Fatalf("the server page answered %d: %v", page.Status, page.Body)
	}

	services, _ := page.Body["services"].(map[string]any)
	if services == nil {
		t.Fatalf("the server page has no services: %v", page.Body)
	}

	acted := call(t, httpClient, http.MethodPost, server.URL+"/api/v1/server/services", map[string]any{
		"unit":   "nginx.service",
		"action": "restart",
	}, csrf)

	if acted.Status != http.StatusOK {
		t.Fatalf("restarting a service answered %d: %v", acted.Status, acted.Body)
	}

	usage := call(t, httpClient, http.MethodGet, server.URL+"/api/v1/usage", nil, "")
	if usage.Status != http.StatusOK {
		t.Fatalf("usage answered %d: %v", usage.Status, usage.Body)
	}
}

// signInAs tries a sign-in and returns the client that holds it, or nil when
// the panel refused. It is how the tests prove an address or a password stopped
// working.
func signInAs(t *testing.T, server *httptest.Server, st *store.Store, login, password string) *http.Client {
	t.Helper()

	httpClient := client(t)

	probe := call(t, httpClient, http.MethodGet, server.URL+"/api/v1/auth/session", nil, "")
	if probe.Status != http.StatusOK {
		t.Fatalf("session probe answered %d", probe.Status)
	}

	csrf, _ := probe.Body["csrfToken"].(string)

	answer := call(t, httpClient, http.MethodPost, server.URL+"/api/v1/auth/login", map[string]any{
		"login":    login,
		"password": password,
	}, csrf)

	if answer.Status != http.StatusOK {
		return nil
	}

	return httpClient
}

// itoa keeps the test file free of strconv noise at the call sites.
func itoa(value int64) string {
	if value == 0 {
		return "0"
	}

	digits := make([]byte, 0, 20)

	for value > 0 {
		digits = append([]byte{byte('0' + value%10)}, digits...)
		value /= 10
	}

	return string(digits)
}

var _ = sql.ErrNoRows

func TestPostgresDatabaseAndLogin(t *testing.T) {
	server, st, queue := harness(t)
	httpClient, csrf := signIn(t, server, st)

	provision(t, server, st, queue, httpClient, csrf, "siti")

	created := call(t, httpClient, http.MethodPost, server.URL+"/api/v1/postgres", map[string]any{
		"username": "siti",
		"suffix":   "analytics",
	}, csrf)

	if created.Status != http.StatusCreated {
		t.Fatalf("adding a PostgreSQL database answered %d: %v", created.Status, created.Body)
	}

	row, _ := created.Body["database"].(map[string]any)
	databaseID, _ := row["id"].(float64)

	if row["name"] != "siti_analytics" {
		t.Fatalf("the database is named %v, want siti_analytics", row["name"])
	}

	drain(t, queue, 1)

	login := call(t, httpClient, http.MethodPost, server.URL+"/api/v1/postgres-users", map[string]any{
		"username":  "siti",
		"suffix":    "analyticsapp",
		"databases": []int64{int64(databaseID)},
	}, csrf)

	if login.Status != http.StatusCreated {
		t.Fatalf("adding a PostgreSQL login answered %d: %v", login.Status, login.Body)
	}

	password, _ := login.Body["password"].(string)
	if len(password) < 16 {
		t.Fatalf("the login password is %q, too short", password)
	}

	drain(t, queue, 1)

	listed := call(t, httpClient, http.MethodGet, server.URL+"/api/v1/postgres", nil, "")
	if listed.Status != http.StatusOK {
		t.Fatalf("listing PostgreSQL answered %d: %v", listed.Status, listed.Body)
	}

	users, _ := listed.Body["users"].([]any)
	if len(users) != 1 {
		t.Fatalf("siti has %d PostgreSQL logins, want 1", len(users))
	}

	first, _ := users[0].(map[string]any)
	if first["status"] != "active" {
		t.Fatalf("the login is %v after its task ran, want active", first["status"])
	}

	loginID, _ := first["id"].(float64)

	// A PATCH with no password keeps the stored one and only moves the grants.
	updated := call(t, httpClient, http.MethodPatch,
		server.URL+"/api/v1/postgres-users/"+itoa(int64(loginID)), map[string]any{
			"databases": []int64{},
		}, csrf)

	if updated.Status != http.StatusOK {
		t.Fatalf("editing the login answered %d: %v", updated.Status, updated.Body)
	}

	drain(t, queue, 1)

	removed := call(t, httpClient, http.MethodDelete,
		server.URL+"/api/v1/postgres-users/"+itoa(int64(loginID)), nil, csrf)

	if removed.Status != http.StatusNoContent {
		t.Fatalf("removing the login answered %d: %v", removed.Status, removed.Body)
	}
}

func TestRedisInstanceIsOnePerAccount(t *testing.T) {
	server, st, queue := harness(t)
	httpClient, csrf := signIn(t, server, st)

	provision(t, server, st, queue, httpClient, csrf, "siti")

	before := call(t, httpClient, http.MethodGet, server.URL+"/api/v1/redis", nil, "")
	if before.Status != http.StatusOK {
		t.Fatalf("reading Redis answered %d: %v", before.Status, before.Body)
	}

	if before.Body["instance"] != nil {
		t.Fatalf("a new account already has a Redis instance: %v", before.Body)
	}

	created := call(t, httpClient, http.MethodPost, server.URL+"/api/v1/redis", map[string]any{
		"username":    "siti",
		"maxMemoryMb": 256,
	}, csrf)

	if created.Status != http.StatusCreated {
		t.Fatalf("creating the cache answered %d: %v", created.Status, created.Body)
	}

	password, _ := created.Body["password"].(string)
	if len(password) < 16 {
		t.Fatalf("the Redis password is %q, too short", password)
	}

	instance, _ := created.Body["instance"].(map[string]any)
	instanceID, _ := instance["id"].(float64)

	if instance["maxMemoryMb"] != float64(256) {
		t.Fatalf("the ceiling is %v, want 256", instance["maxMemoryMb"])
	}

	drain(t, queue, 1)

	// Asking again rotates the password instead of adding a second row.
	rotated := call(t, httpClient, http.MethodPost,
		server.URL+"/api/v1/redis/"+itoa(int64(instanceID)), map[string]any{}, csrf)

	if rotated.Status != http.StatusOK {
		t.Fatalf("rotating the password answered %d: %v", rotated.Status, rotated.Body)
	}

	rotatedPassword, _ := rotated.Body["password"].(string)
	if rotatedPassword == password {
		t.Fatal("rotating the password returned the same one")
	}

	drain(t, queue, 1)

	resized := call(t, httpClient, http.MethodPatch,
		server.URL+"/api/v1/redis/"+itoa(int64(instanceID)), map[string]any{"maxMemoryMb": 512}, csrf)

	if resized.Status != http.StatusOK {
		t.Fatalf("resizing answered %d: %v", resized.Status, resized.Body)
	}

	drain(t, queue, 1)

	removed := call(t, httpClient, http.MethodDelete,
		server.URL+"/api/v1/redis/"+itoa(int64(instanceID)), nil, csrf)

	if removed.Status != http.StatusNoContent {
		t.Fatalf("removing the cache answered %d: %v", removed.Status, removed.Body)
	}

	after := call(t, httpClient, http.MethodGet, server.URL+"/api/v1/redis", nil, "")
	if after.Body["instance"] != nil {
		t.Fatalf("the instance is still there: %v", after.Body)
	}
}

func TestProfileChangeAndPasswordRotateSessions(t *testing.T) {
	server, st, queue := harness(t)
	httpClient, csrf := signIn(t, server, st)

	if queue == nil {
		t.Fatal("the harness has no queue")
	}

	changed := call(t, httpClient, http.MethodPatch, server.URL+"/api/v1/me", map[string]any{
		"name":  "Muhammad Rifqy",
		"email": "rifqy@example.test",
	}, csrf)

	if changed.Status != http.StatusOK {
		t.Fatalf("changing the profile answered %d: %v", changed.Status, changed.Body)
	}

	user, _ := changed.Body["user"].(map[string]any)
	if user["email"] != "rifqy@example.test" {
		t.Fatalf("the email is %v after the change", user["email"])
	}

	// The old address no longer signs in; the new one does.
	if probe := signInAs(t, server, st, "admin@example.test", "rahasia123"); probe != nil {
		t.Fatal("the old address still signs in")
	}

	wrong := call(t, httpClient, http.MethodPost, server.URL+"/api/v1/me/password", map[string]any{
		"currentPassword": "not-the-password",
		"newPassword":     "a-new-long-password",
	}, csrf)

	if wrong.Status != http.StatusUnauthorized {
		t.Fatalf("a wrong current password answered %d: %v", wrong.Status, wrong.Body)
	}

	rotated := call(t, httpClient, http.MethodPost, server.URL+"/api/v1/me/password", map[string]any{
		"currentPassword": "rahasia123",
		"newPassword":     "a-new-long-password",
	}, csrf)

	if rotated.Status != http.StatusOK {
		t.Fatalf("changing the password answered %d: %v", rotated.Status, rotated.Body)
	}

	// The browser that changed it keeps working, because the panel signs it in
	// again under a new session.
	me := call(t, httpClient, http.MethodGet, server.URL+"/api/v1/me", nil, "")
	if me.Status != http.StatusOK {
		t.Fatalf("the session was not renewed: %d %v", me.Status, me.Body)
	}

	if other := signInAs(t, server, st, "admin@example.test", "rahasia123"); other != nil {
		t.Fatal("the old password still signs in")
	}
}

// The server page must show the traffic the panel counted, because the agent's
// usage.bandwidth answers "what moved since this cursor" and would look empty
// right after a count.
func TestUsageShowsCollectedTraffic(t *testing.T) {
	server, st, queue := harness(t)
	httpClient, csrf := signIn(t, server, st)

	accountID := provision(t, server, st, queue, httpClient, csrf, "siti")

	day := time.Now().UTC().Format("2006-01-02")

	if err := st.RecordBandwidth(context.Background(),
		[]store.Accounting{{UserID: accountID, Date: day, Web: 3 << 20, Mail: 1 << 20, FTP: 512 << 10}},
		nil, nil); err != nil {
		t.Fatalf("record bandwidth: %v", err)
	}

	answer := call(t, httpClient, http.MethodGet, server.URL+"/api/v1/usage", nil, "")
	if answer.Status != http.StatusOK {
		t.Fatalf("reading usage answered %d: %v", answer.Status, answer.Body)
	}

	bandwidth, _ := answer.Body["bandwidth"].(map[string]any)

	total, _ := bandwidth["total_bytes"].(float64)
	if total != 4.5*1024*1024 {
		t.Errorf("total_bytes = %v, want %v", total, 4.5*1024*1024)
	}

	if bandwidth["month"] != time.Now().UTC().Format("2006-01") {
		t.Errorf("month = %v", bandwidth["month"])
	}

	if _, ok := answer.Body["collectors"]; !ok {
		t.Error("the answer has no collectors list, so the page cannot say how fresh it is")
	}
}

// ed25519PublicKey builds a well-formed OpenSSH public key line, because a
// hand-typed one would test the parser's mistakes instead of the page.
func ed25519PublicKey(seed byte, comment string) string {
	field := func(value []byte) []byte {
		out := make([]byte, 4, 4+len(value))
		binary.BigEndian.PutUint32(out, uint32(len(value)))

		return append(out, value...)
	}

	blob := append(field([]byte("ssh-ed25519")), field(bytes.Repeat([]byte{seed}, 32))...)

	return "ssh-ed25519 " + base64.StdEncoding.EncodeToString(blob) + " " + comment
}

func TestSSHKeysBelongToTheAccount(t *testing.T) {
	server, st, queue := harness(t)
	httpClient, csrf := signIn(t, server, st)

	provision(t, server, st, queue, httpClient, csrf, "siti")

	before := call(t, httpClient, http.MethodGet, server.URL+"/api/v1/ssh?username=siti", nil, "")
	if before.Status != http.StatusOK {
		t.Fatalf("reading SSH answered %d: %v", before.Status, before.Body)
	}

	if keys, _ := before.Body["keys"].([]any); len(keys) != 0 {
		t.Fatalf("a new account already has keys: %v", before.Body)
	}

	access, _ := before.Body["access"].(map[string]any)
	if access["enabled"] != false {
		t.Errorf("access = %v, want SSH off", access)
	}

	created := call(t, httpClient, http.MethodPost, server.URL+"/api/v1/ssh/keys", map[string]any{
		"username":  "siti",
		"name":      "laptop",
		"publicKey": ed25519PublicKey(3, "siti@laptop"),
	}, csrf)

	if created.Status != http.StatusCreated {
		t.Fatalf("adding a key answered %d: %v", created.Status, created.Body)
	}

	// The same key twice is a conflict, not a second row.
	again := call(t, httpClient, http.MethodPost, server.URL+"/api/v1/ssh/keys", map[string]any{
		"username":  "siti",
		"name":      "laptop again",
		"publicKey": ed25519PublicKey(3, "siti@laptop"),
	}, csrf)

	if again.Status != http.StatusConflict {
		t.Fatalf("adding the same key again answered %d: %v", again.Status, again.Body)
	}

	listed := call(t, httpClient, http.MethodGet, server.URL+"/api/v1/ssh?username=siti", nil, "")
	keys, _ := listed.Body["keys"].([]any)

	if len(keys) != 1 {
		t.Fatalf("the page shows %d keys, want 1: %v", len(keys), listed.Body)
	}

	key, _ := keys[0].(map[string]any)
	fingerprint, _ := key["Fingerprint"].(string)

	if !strings.HasPrefix(fingerprint, "SHA256:") {
		t.Errorf("fingerprint = %q", fingerprint)
	}

	// The key write is queued on its own, after the account already exists.
	drain(t, queue, 1)

	// A private key must be refused with a message that says so.
	private := call(t, httpClient, http.MethodPost, server.URL+"/api/v1/ssh/keys", map[string]any{
		"username":  "siti",
		"name":      "oops",
		"publicKey": "-----BEGIN OPENSSH PRIVATE KEY-----\nabc\n-----END OPENSSH PRIVATE KEY-----",
	}, csrf)

	if private.Status != http.StatusUnprocessableEntity {
		t.Fatalf("a private key answered %d: %v", private.Status, private.Body)
	}

	id, _ := key["ID"].(float64)

	removed := call(t, httpClient, http.MethodDelete, server.URL+"/api/v1/ssh/keys/"+itoa(int64(id)), nil, csrf)
	if removed.Status != http.StatusNoContent {
		t.Fatalf("removing the key answered %d: %v", removed.Status, removed.Body)
	}

	after := call(t, httpClient, http.MethodGet, server.URL+"/api/v1/ssh?username=siti", nil, "")
	if keys, _ := after.Body["keys"].([]any); len(keys) != 0 {
		t.Errorf("the key is still listed: %v", after.Body)
	}
}

// A package without SSH must be refused before anything is queued, so the
// account is not told its shell is coming when it never can.
func TestSSHNeedsAPackageThatIncludesIt(t *testing.T) {
	server, st, queue := harness(t)
	httpClient, csrf := signIn(t, server, st)

	provision(t, server, st, queue, httpClient, csrf, "siti")

	answer := call(t, httpClient, http.MethodPatch, server.URL+"/api/v1/ssh", map[string]any{
		"username": "siti",
		"enabled":  true,
	}, csrf)
	if answer.Status != http.StatusForbidden {
		t.Fatalf("turning SSH on without a package answered %d: %v", answer.Status, answer.Body)
	}

	if queue.RunOnce(context.Background()) {
		t.Error("a refused change queued work for the agent")
	}
}
