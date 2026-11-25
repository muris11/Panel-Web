// Package rules validates what an operator types into the panel, before the
// value is stored and long before it reaches the agent.
//
// The agent validates everything again on its own side. That is not
// redundant: this package produces friendly messages for the form, and the
// agent's copy is the one that protects the server.
package rules

import (
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"
)

var (
	usernamePattern = regexp.MustCompile(`^[a-z][a-z0-9]{2,15}$`)
	domainLabel     = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)
	emailPattern    = regexp.MustCompile(`^[^@\s]+@[^@\s]+\.[^@\s]+$`)
)

// ReservedUsernames are system, service and privileged group names.
//
// Kept in sync by hand with agent/internal/validate/validate.go and
// App\Rules\ValidUsername in the Laravel panel; a name missing from one of
// the three is how an account could end up sharing a name with a system user.
var ReservedUsernames = map[string]bool{
	"adm": true, "apache": true, "backup": true, "bin": true, "clamav": true, "daemon": true,
	"dnsmasq": true, "dovecot": true, "dovenull": true, "ftp": true, "games": true, "gnats": true,
	"irc": true, "landscape": true, "list": true, "lp": true, "lxd": true, "mail": true, "man": true,
	"messagebus": true, "mysql": true, "news": true, "nginx": true, "nobody": true, "rayxod": true,
	"rayxo": true, "operator": true, "pdns": true, "polkitd": true, "pollinate": true,
	"postfix": true, "proxy": true, "pureftpd": true, "redis": true, "root": true, "rspamd": true,
	"sshd": true, "staff": true, "sudo": true, "sync": true, "sys": true, "syslog": true,
	"systemd": true, "tcpdump": true, "tss": true, "ubuntu": true, "users": true, "uucp": true,
	"uuidd": true, "vmail": true,
	"information": true, "performance": true,
	"audio": true, "cdrom": true, "crontab": true, "dialout": true, "disk": true, "docker": true,
	"floppy": true, "input": true, "kmem": true, "kvm": true, "libvirt": true, "netdev": true,
	"plugdev": true, "render": true, "shadow": true, "systemd-journal": true, "tape": true,
	"tty": true, "utmp": true, "video": true, "wheel": true, "www": true,
}

// Username checks a hosting account's Linux username.
func Username(name string) error {
	if !usernamePattern.MatchString(name) {
		return fmt.Errorf("Use 3 to 16 lowercase letters or digits, starting with a letter.")
	}

	if ReservedUsernames[name] {
		return fmt.Errorf("That username is reserved by the system.")
	}

	return nil
}

// Domain checks a website name. Internationalized names must already be
// converted to punycode, which is what the browser sends.
func Domain(name string) error {
	name = strings.ToLower(strings.TrimSpace(name))

	if name == "" {
		return fmt.Errorf("Enter a domain name.")
	}

	if len(name) > 253 {
		return fmt.Errorf("That domain name is too long.")
	}

	labels := strings.Split(name, ".")

	if len(labels) < 2 {
		return fmt.Errorf("Enter a full domain, like example.com.")
	}

	for _, label := range labels {
		if !domainLabel.MatchString(label) {
			return fmt.Errorf("Part of that domain name is not valid: %q.", label)
		}
	}

	topLevel := labels[len(labels)-1]

	if !strings.ContainsAny(topLevel, "abcdefghijklmnopqrstuvwxyz") {
		return fmt.Errorf("A domain ends in letters, not numbers.")
	}

	return nil
}

// Email checks an address. It is a shape check, not a delivery check: the
// only way to know an address works is to send to it.
func Email(address string) error {
	address = strings.TrimSpace(address)

	if len(address) > 254 || !emailPattern.MatchString(address) {
		return fmt.Errorf("That email address does not look right.")
	}

	return nil
}

// Name checks that a display name is present and not a novel.
func Name(name string) error {
	name = strings.TrimSpace(name)

	if name == "" {
		return fmt.Errorf("Enter a name.")
	}

	if utf8.RuneCountInString(name) > 255 {
		return fmt.Errorf("That name is too long.")
	}

	return nil
}

// Password checks length only; strength is a policy decision, and a longer
// minimum would lock out accounts created by the Laravel panel.
func Password(password string) error {
	if len(password) < 8 {
		return fmt.Errorf("Use at least 8 characters.")
	}

	if len(password) > 72 {
		return fmt.Errorf("Use at most 72 characters.")
	}

	return nil
}

// NormalizeUsername lowercases and trims, so "Budi" and "budi" are one name.
func NormalizeUsername(name string) string {
	return strings.ToLower(strings.TrimSpace(name))
}

// NormalizeDomain lowercases and trims a website name.
func NormalizeDomain(name string) string {
	return strings.ToLower(strings.TrimSuffix(strings.TrimSpace(name), "."))
}
