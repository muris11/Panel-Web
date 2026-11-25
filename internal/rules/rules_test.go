package rules

import (
	"strings"
	"testing"
)

func TestUsername(t *testing.T) {
	valid := []string{"budi", "a12", "siti9", "rayxocustomer"}
	for _, name := range valid {
		if err := Username(name); err != nil {
			t.Errorf("Username(%q) rejected a valid name: %v", name, err)
		}
	}

	invalid := []string{
		"",                     // empty
		"ab",                   // too short
		"1budi",                // starts with a digit
		"budi!",                // punctuation
		"Budi",                 // upper case
		"budi.s",               // dot
		"thisnameiswaytoolong", // over 16
		"root",                 // reserved
		"nginx",                // reserved service account
		"information",          // would claim information_schema's prefix
		"docker",               // privileged group
	}

	for _, name := range invalid {
		if err := Username(name); err == nil {
			t.Errorf("Username(%q) accepted a name it should refuse", name)
		}
	}
}

func TestDomain(t *testing.T) {
	valid := []string{"example.com", "budi.example.com", "a-b.example.co.uk", "xn--80ak6aa92e.com"}
	for _, name := range valid {
		if err := Domain(name); err != nil {
			t.Errorf("Domain(%q) rejected a valid domain: %v", name, err)
		}
	}

	invalid := []string{
		"",
		"localhost",                      // one label
		"example",                        // one label
		"example.123",                    // numeric top level
		"-example.com",                   // label starts with a dash
		"example-.com",                   // label ends with a dash
		"exa mple.com",                   // space
		"example..com",                   // empty label
		strings.Repeat("a", 64) + ".com", // label over 63
	}

	for _, name := range invalid {
		if err := Domain(name); err == nil {
			t.Errorf("Domain(%q) accepted a domain it should refuse", name)
		}
	}
}

func TestDomainNormalization(t *testing.T) {
	if got := NormalizeDomain("  Budi.Example.COM. "); got != "budi.example.com" {
		t.Fatalf("NormalizeDomain returned %q", got)
	}

	if err := Domain(NormalizeDomain("BUDI.EXAMPLE.COM")); err != nil {
		t.Fatalf("a normalized upper-case domain was refused: %v", err)
	}
}

func TestEmail(t *testing.T) {
	for _, address := range []string{"budi@example.com", "a.b+c@sub.example.co.id"} {
		if err := Email(address); err != nil {
			t.Errorf("Email(%q) rejected a valid address: %v", address, err)
		}
	}

	for _, address := range []string{"", "budi", "budi@", "@example.com", "budi@example", "bu di@example.com"} {
		if err := Email(address); err == nil {
			t.Errorf("Email(%q) accepted an address it should refuse", address)
		}
	}
}

func TestPassword(t *testing.T) {
	if err := Password("rahasia123"); err != nil {
		t.Fatalf("a valid password was refused: %v", err)
	}

	if err := Password("short"); err == nil {
		t.Fatal("a 5-character password was accepted")
	}

	if err := Password(strings.Repeat("a", 73)); err == nil {
		t.Fatal("a 73-byte password was accepted; bcrypt would truncate it")
	}
}
