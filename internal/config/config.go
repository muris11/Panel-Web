// Package config reads the panel's settings from the environment.
//
// Everything has a development default so "go run ./cmd/panel" starts, and
// every production value is explicit (see README): a panel that silently
// guessed a database or an agent socket is a panel that breaks quietly.
package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	// Listen is the address the panel serves on, e.g. "127.0.0.1:8080".
	// In production Nginx talks to it over a Unix socket or localhost.
	Listen string

	// DatabaseDSN is the MariaDB connection string, with parseTime=true.
	DatabaseDSN string

	// AppKey is the Laravel APP_KEY of the panel this one replaces. It is
	// only needed to read a two-factor secret the Laravel panel encrypted;
	// without it those accounts sign in with their password only.
	AppKey string

	// AgentDriver is "socket" (a real server) or "fake" (development).
	AgentDriver string

	// AgentSocket is rayxod's Unix socket.
	AgentSocket string

	// AgentTimeout bounds one agent call a page waits on.
	AgentTimeout time.Duration

	// AgentTaskTimeout bounds one queued task.
	AgentTaskTimeout time.Duration

	// SessionTTL is how long a signed-in browser stays signed in.
	SessionTTL time.Duration

	// SecureCookies marks the session cookie Secure; on in production.
	SecureCookies bool

	// TrustedProxies are the addresses allowed to set X-Forwarded-For. Empty
	// means the panel believes the socket's own address, which is right when
	// Nginx sits in front on the same host.
	TrustedProxies []string

	// PublicIP is the address new A records point at. Empty means the panel
	// writes no address records and says so on the DNS screen, rather than
	// guessing an address that would be wrong.
	PublicIP string

	// Nameservers are the servers every new zone names. Empty means the same
	// thing: the zone gets no NS rows and the panel asks for the setting.
	Nameservers []string
}

// Load reads the configuration and refuses one that cannot work.
func Load() (Config, error) {
	cfg := Config{
		Listen:           env("RAYXO_LISTEN", "127.0.0.1:8080"),
		DatabaseDSN:      env("RAYXO_DB_DSN", ""),
		AppKey:           env("RAYXO_APP_KEY", ""),
		AgentDriver:      env("RAYXO_AGENT_DRIVER", "fake"),
		AgentSocket:      env("RAYXO_AGENT_SOCKET", "/run/rayxo/agent.sock"),
		AgentTimeout:     duration("RAYXO_AGENT_TIMEOUT", 30*time.Second),
		AgentTaskTimeout: duration("RAYXO_AGENT_TASK_TIMEOUT", 5*time.Minute),
		SessionTTL:       duration("RAYXO_SESSION_TTL", 12*time.Hour),
		SecureCookies:    boolEnv("RAYXO_SECURE_COOKIES", false),
		PublicIP:         env("RAYXO_PUBLIC_IP", ""),
	}

	for _, nameserver := range strings.Split(env("RAYXO_NAMESERVERS", ""), ",") {
		if nameserver = strings.TrimSpace(nameserver); nameserver != "" {
			cfg.Nameservers = append(cfg.Nameservers, nameserver)
		}
	}

	if cfg.DatabaseDSN == "" {
		return Config{}, fmt.Errorf("RAYXO_DB_DSN is not set, e.g. \"rayxo:secret@tcp(127.0.0.1:3306)/rayxo?parseTime=true&loc=UTC\"")
	}

	switch cfg.AgentDriver {
	case "socket", "fake":
	default:
		return Config{}, fmt.Errorf("RAYXO_AGENT_DRIVER must be \"socket\" or \"fake\", not %q", cfg.AgentDriver)
	}

	if cfg.AgentDriver == "socket" && cfg.AgentSocket == "" {
		return Config{}, fmt.Errorf("RAYXO_AGENT_SOCKET is empty while RAYXO_AGENT_DRIVER is \"socket\"")
	}

	if cfg.SessionTTL < time.Minute {
		return Config{}, fmt.Errorf("RAYXO_SESSION_TTL must be at least a minute")
	}

	for _, proxy := range strings.Split(env("RAYXO_TRUSTED_PROXIES", ""), ",") {
		if proxy = strings.TrimSpace(proxy); proxy != "" {
			cfg.TrustedProxies = append(cfg.TrustedProxies, proxy)
		}
	}

	return cfg, nil
}

func env(name, fallback string) string {
	if value, ok := os.LookupEnv(name); ok && value != "" {
		return value
	}

	return fallback
}

func duration(name string, fallback time.Duration) time.Duration {
	value, ok := os.LookupEnv(name)
	if !ok || value == "" {
		return fallback
	}

	parsed, err := time.ParseDuration(value)
	if err != nil {
		// A duration that does not parse would otherwise silently become
		// zero and make every task time out at once.
		seconds, secondsErr := strconv.Atoi(value)
		if secondsErr != nil {
			panic(fmt.Sprintf("%s: %q is not a duration", name, value))
		}

		return time.Duration(seconds) * time.Second
	}

	return parsed
}

func boolEnv(name string, fallback bool) bool {
	value, ok := os.LookupEnv(name)
	if !ok || value == "" {
		return fallback
	}

	switch strings.ToLower(value) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}
