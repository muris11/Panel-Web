package config

import (
	"os"
	"testing"
	"time"
)

// The rename must not strand an installation whose env file still says RAYXO_.
func TestLegacyPrefixIsRead(t *testing.T) {
	t.Setenv("RAYXO_DB_DSN", "legacy@tcp(127.0.0.1:3306)/rayxo?parseTime=true")
	t.Setenv("RAYXO_AGENT_DRIVER", "socket")
	t.Setenv("RAYXO_LISTEN", "127.0.0.1:9999")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.DatabaseDSN != "legacy@tcp(127.0.0.1:3306)/rayxo?parseTime=true" {
		t.Errorf("DatabaseDSN = %q, want the legacy value", cfg.DatabaseDSN)
	}

	if cfg.Listen != "127.0.0.1:9999" {
		t.Errorf("Listen = %q, want 127.0.0.1:9999", cfg.Listen)
	}
}

// A host that sets both must get the new name, so an upgrade can be rolled out
// without deleting the old file first.
func TestNewPrefixWins(t *testing.T) {
	t.Setenv("RAYXO_DB_DSN", "old@tcp(127.0.0.1:3306)/rayxo")
	t.Setenv("MURISCORE_DB_DSN", "new@tcp(127.0.0.1:3306)/rayxo")
	t.Setenv("MURISCORE_SESSION_TTL", "2h")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.DatabaseDSN != "new@tcp(127.0.0.1:3306)/rayxo" {
		t.Errorf("DatabaseDSN = %q, want the MURISCORE_ value", cfg.DatabaseDSN)
	}

	if cfg.SessionTTL != 2*time.Hour {
		t.Errorf("SessionTTL = %v, want 2h", cfg.SessionTTL)
	}
}

func TestNameserversAreSplit(t *testing.T) {
	t.Setenv("MURISCORE_DB_DSN", "x@tcp(127.0.0.1:3306)/rayxo")
	t.Setenv("MURISCORE_NAMESERVERS", "ns1.example.test, ns2.example.test ,")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if len(cfg.Nameservers) != 2 || cfg.Nameservers[0] != "ns1.example.test" || cfg.Nameservers[1] != "ns2.example.test" {
		t.Errorf("Nameservers = %v, want two trimmed names", cfg.Nameservers)
	}
}

func TestMissingDSNIsAnError(t *testing.T) {
	// A panel that started without a database would answer every page with
	// 500s, so this has to be refused at the door.
	os.Unsetenv("RAYXO_DB_DSN")
	os.Unsetenv("MURISCORE_DB_DSN")

	if _, err := Load(); err == nil {
		t.Fatal("Load accepted a configuration with no database")
	}
}
