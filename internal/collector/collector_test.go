package collector

import (
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"

	"muriscore/panel/internal/agent"
	"muriscore/panel/internal/auth"
	"muriscore/panel/internal/db"
	"muriscore/panel/internal/store"
)

// These tests need a real database, because the collector's SQL is written for
// one. Point MURISCORE_TEST_DSN at a throwaway database whose name ends in
// _test; it is emptied. Without it they skip.
func harness(t *testing.T) (*store.Store, *sql.DB) {
	t.Helper()

	dsn := os.Getenv("MURISCORE_TEST_DSN")
	if dsn == "" {
		dsn = os.Getenv("RAYXO_TEST_DSN")
	}

	if dsn == "" {
		t.Skip("set MURISCORE_TEST_DSN to run the collector's database tests")
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

	for _, statement := range []string{
		"DELETE FROM collector_runs",
		"DELETE FROM resource_counters",
		"DELETE FROM resource_usages",
		"DELETE FROM domain_bandwidths",
		"DELETE FROM bandwidth_usages",
		"DELETE FROM settings",
		"DELETE FROM ftp_accounts",
		"DELETE FROM domains",
		"DELETE FROM users",
	} {
		if _, err := pool.ExecContext(ctx, statement); err != nil {
			t.Fatalf("%s: %v", statement, err)
		}
	}

	return store.New(pool), pool
}

// stubAgent answers a scripted method and records what it was asked.
type stubAgent struct {
	answers map[string]string
	calls   []string
	params  map[string]json.RawMessage
}

func (s *stubAgent) Call(_ context.Context, method string, params any) (json.RawMessage, error) {
	s.calls = append(s.calls, method)

	if s.params == nil {
		s.params = map[string]json.RawMessage{}
	}

	encoded, _ := json.Marshal(params)
	s.params[method] = encoded

	answer, ok := s.answers[method]
	if !ok {
		return json.RawMessage(`{}`), nil
	}

	return json.RawMessage(answer), nil
}

func (s *stubAgent) Name() string { return "stub" }

func quiet() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// account creates a hosting account and returns its id.
func account(t *testing.T, st *store.Store, username string) int64 {
	t.Helper()

	hash, err := auth.HashPassword("rahasia123")
	if err != nil {
		t.Fatalf("hash: %v", err)
	}

	ctx := context.Background()

	// Accounts need an owner; a reseller is the cheapest row that satisfies
	// the foreign key.
	reseller, err := st.CreatePanelUser(ctx, "resel", "Reseller", "resel@example.test", hash, store.RoleReseller, nil)
	if err != nil {
		t.Fatalf("create the reseller: %v", err)
	}

	user, err := st.CreateAccount(ctx, username, "Siti", username+"@example.test", hash, reseller.ID, nil)
	if err != nil {
		t.Fatalf("create %s: %v", username, err)
	}

	return user.ID
}

func TestBandwidthIsChargedToTheOwnerAndTheWebsite(t *testing.T) {
	st, pool := harness(t)
	ctx := context.Background()

	userID := account(t, st, "siti")

	if _, err := pool.ExecContext(ctx, "INSERT INTO domains (user_id, name, type, status) VALUES (?, 'siti.test', 'domain', 'active')", userID); err != nil {
		t.Fatalf("domain: %v", err)
	}

	if _, err := pool.ExecContext(ctx, "INSERT INTO ftp_accounts (user_id, domain_id, login, password_hash) VALUES (?, (SELECT id FROM domains WHERE name = 'siti.test'), 'siti_ftp', '*x')", userID); err != nil {
		t.Fatalf("ftp: %v", err)
	}

	agentStub := &stubAgent{answers: map[string]string{
		"usage.bandwidth": `{
			"web":       [{"domain": "siti.test", "date": "2026-02-10", "bytes": 1000}],
			"mail":      [{"domain": "siti.test", "date": "2026-02-10", "bytes": 500}],
			"mail_user": [{"user": "siti", "date": "2026-02-10", "bytes": 100}],
			"ftp":       [{"user": "siti_ftp", "date": "2026-02-10", "bytes": 250}],
			"cursor":    {"web:/var/log/rayxo/domains/siti.test.access.log": {"inode": 7, "offset": 900}},
			"complete":  true
		}`,
	}}

	c := New(st, agentStub, quiet())

	if _, err := c.Bandwidth(ctx); err != nil {
		t.Fatalf("Bandwidth: %v", err)
	}

	var web, mail, ftp uint64

	if err := pool.QueryRowContext(ctx,
		"SELECT web_bytes, mail_bytes, ftp_bytes FROM bandwidth_usages WHERE user_id = ?", userID).Scan(&web, &mail, &ftp); err != nil {
		t.Fatalf("read the usage row: %v", err)
	}

	if web != 1000 || mail != 600 || ftp != 250 {
		t.Errorf("counted web=%d mail=%d ftp=%d, want 1000/600/250", web, mail, ftp)
	}

	var siteBytes uint64

	if err := pool.QueryRowContext(ctx,
		"SELECT bytes FROM domain_bandwidths WHERE date = '2026-02-10'").Scan(&siteBytes); err != nil {
		t.Fatalf("read the website row: %v", err)
	}

	if siteBytes != 1500 {
		t.Errorf("website bytes = %d, want 1500", siteBytes)
	}

	// The cursor must be stored, or the next count reads the same log again.
	var cursor []byte

	if err := pool.QueryRowContext(ctx, "SELECT `value` FROM settings WHERE `key` = 'bandwidth.cursor'").Scan(&cursor); err != nil {
		t.Fatalf("the cursor was not stored: %v", err)
	}

	if !strings.Contains(string(cursor), "inode") {
		t.Errorf("cursor = %s, want the agent's cursor", cursor)
	}

	// A second identical count must add to the same row, not insert a new one.
	if _, err := c.Bandwidth(ctx); err != nil {
		t.Fatalf("second Bandwidth: %v", err)
	}

	if err := pool.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM bandwidth_usages WHERE user_id = ?", userID).Scan(new(int)); err != nil {
		t.Fatalf("count rows: %v", err)
	}

	var rows int

	if err := pool.QueryRowContext(ctx, "SELECT COUNT(*) FROM bandwidth_usages").Scan(&rows); err != nil {
		t.Fatalf("count rows: %v", err)
	}

	if rows != 1 {
		t.Errorf("bandwidth_usages has %d rows, want 1 day per account", rows)
	}
}

func TestResourcesUseTheDifferenceAndKeepThePeak(t *testing.T) {
	st, pool := harness(t)
	ctx := context.Background()

	userID := account(t, st, "budi")

	if _, err := pool.ExecContext(ctx, `
		INSERT INTO resource_counters (user_id, cgroup_id, cpu_usec) VALUES (?, 42, 1000000)`, userID); err != nil {
		t.Fatalf("counter: %v", err)
	}

	agentStub := &stubAgent{answers: map[string]string{
		"usage.resources": `{"accounts": [
			{"username": "budi", "memory_bytes": 268435456, "cpu": [{"id": 42, "usec": 1500000}]}
		]}`,
	}}

	c := New(st, agentStub, quiet())
	c.Now = func() time.Time { return time.Date(2026, 2, 10, 12, 0, 0, 0, time.UTC) }

	if _, err := c.Resources(ctx); err != nil {
		t.Fatalf("Resources: %v", err)
	}

	var cpuMs uint64
	var peak int64

	if err := pool.QueryRowContext(ctx,
		"SELECT cpu_ms, memory_peak_mb FROM resource_usages WHERE user_id = ? AND `date` = '2026-02-10'",
		userID).Scan(&cpuMs, &peak); err != nil {
		t.Fatalf("read the usage row: %v", err)
	}

	if cpuMs != 500 {
		t.Errorf("cpu_ms = %d, want 500 (1.5s minus 1s, in milliseconds)", cpuMs)
	}

	if peak != 256 {
		t.Errorf("memory_peak_mb = %d, want 256", peak)
	}

	// Add another 250ms: the second run stores the difference and keeps the
	// larger peak rather than replacing it with a lower one.
	agentStub.answers["usage.resources"] = `{"accounts": [
		{"username": "budi", "memory_bytes": 134217728, "cpu": [{"id": 42, "usec": 1750000}]}
	]}`

	if _, err := c.Resources(ctx); err != nil {
		t.Fatalf("second Resources: %v", err)
	}

	if err := pool.QueryRowContext(ctx,
		"SELECT cpu_ms, memory_peak_mb FROM resource_usages WHERE user_id = ? AND `date` = '2026-02-10'",
		userID).Scan(&cpuMs, &peak); err != nil {
		t.Fatalf("read the usage row again: %v", err)
	}

	if cpuMs != 750 {
		t.Errorf("cpu_ms = %d, want 750", cpuMs)
	}

	if peak != 256 {
		t.Errorf("memory_peak_mb = %d, want the larger 256 to be kept", peak)
	}
}

func TestSearchlessSSLExpiryWritesAndClearsDates(t *testing.T) {
	st, pool := harness(t)
	ctx := context.Background()

	userID := account(t, st, "siti")

	if _, err := pool.ExecContext(ctx, `
		INSERT INTO domains (user_id, name, type, status) VALUES
			(?, 'siti.test', 'domain', 'active'),
			(?, 'gone.test', 'domain', 'active')`, userID, userID); err != nil {
		t.Fatalf("domains: %v", err)
	}

	agentStub := &stubAgent{answers: map[string]string{
		"ssl.expiry": `{"expires_at": {"siti.test": "2026-06-01T00:00:00Z"}}`,
	}}

	c := New(st, agentStub, quiet())

	if _, err := c.SSLExpiry(ctx); err != nil {
		t.Fatalf("SSLExpiry: %v", err)
	}

	var expires time.Time

	if err := pool.QueryRowContext(ctx,
		"SELECT ssl_expires_at FROM domains WHERE name = 'siti.test'").Scan(&expires); err != nil {
		t.Fatalf("read the date: %v", err)
	}

	if expires.UTC().Format("2006-01-02") != "2026-06-01" {
		t.Errorf("expires = %s, want 2026-06-01", expires.UTC())
	}

	// A domain with no certificate on disk must not keep a stale date.
	var stale sql.NullTime

	if err := pool.QueryRowContext(ctx,
		"SELECT ssl_expires_at FROM domains WHERE name = 'gone.test'").Scan(&stale); err != nil {
		t.Fatalf("read the empty date: %v", err)
	}

	if stale.Valid {
		t.Errorf("gone.test kept a date: %s", stale.Time)
	}
}

// Every job must leave a row behind, so the server page can say whether the
// numbers are current instead of guessing.
func TestRunRecordsEveryJob(t *testing.T) {
	st, pool := harness(t)

	c := New(st, &stubAgent{answers: map[string]string{}}, quiet())

	if err := c.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}

	var runs int

	if err := pool.QueryRowContext(context.Background(), "SELECT COUNT(*) FROM collector_runs").Scan(&runs); err != nil {
		t.Fatalf("count runs: %v", err)
	}

	if runs != len(c.Jobs()) {
		t.Errorf("collector_runs has %d rows, want %d", runs, len(c.Jobs()))
	}
}

var _ agent.Agent = (*stubAgent)(nil)
