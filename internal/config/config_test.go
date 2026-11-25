package config

import (
	"os"
	"testing"
	"time"
)

// A setting that is not in the environment falls back to the documented
// default, so a fresh install starts without an env file.
func TestDefaultsApply(t *testing.T) {
	t.Setenv("MURISCORE_DB_DSN", "panel@tcp(127.0.0.1:3306)/muriscore?parseTime=true")
	t.Setenv("MURISCORE_SESSION_TTL", "")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.SessionTTL != 12*time.Hour {
		t.Errorf("SessionTTL = %v, want the 12h default", cfg.SessionTTL)
	}

	if cfg.AgentDriver != "fake" {
		t.Errorf("AgentDriver = %q, want the fake development default", cfg.AgentDriver)
	}
}

func TestSettingIsReadFromTheSinglePrefix(t *testing.T) {
	t.Setenv("MURISCORE_DB_DSN", "panel@tcp(127.0.0.1:3306)/muriscore?parseTime=true")
	t.Setenv("MURISCORE_SESSION_TTL", "2h")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.DatabaseDSN != "panel@tcp(127.0.0.1:3306)/muriscore?parseTime=true" {
		t.Errorf("DatabaseDSN = %q", cfg.DatabaseDSN)
	}

	if cfg.SessionTTL != 2*time.Hour {
		t.Errorf("SessionTTL = %v, want 2h", cfg.SessionTTL)
	}
}

func TestNameserversAreSplit(t *testing.T) {
	t.Setenv("MURISCORE_DB_DSN", "x@tcp(127.0.0.1:3306)/muriscore")
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
	os.Unsetenv("MURISCORE_DB_DSN")
	os.Unsetenv("MURISCORE_DB_DSN")

	if _, err := Load(); err == nil {
		t.Fatal("Load accepted a configuration with no database")
	}
}
