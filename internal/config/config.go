// Package config reads the panel's settings from the environment.
//
// Everything has a development default so "go run ./cmd/panel" starts, and
// every production value is explicit (see README): a panel that silently
// guessed a database or an agent socket is a panel that breaks quietly.
//
// Every setting is read as MURISCORE_<NAME>.
package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// Prefix is the name every setting of this panel uses.
const Prefix = "MURISCORE_"

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

	// AgentSocket is muriscored's Unix socket.
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

	// CollectInterval is how often the in-process collectors run (traffic,
	// resources, certificate expiry). Zero turns them off, for a host that
	// drives "muriscore-panel collect" from cron instead.
	CollectInterval time.Duration
}

// Load reads the configuration and refuses one that cannot work.
func Load() (Config, error) {
	cfg := Config{
		Listen:           setting("LISTEN", "127.0.0.1:8080"),
		DatabaseDSN:      setting("DB_DSN", ""),
		AppKey:           setting("APP_KEY", ""),
		AgentDriver:      setting("AGENT_DRIVER", "fake"),
		AgentSocket:      setting("AGENT_SOCKET", "/run/muriscore/agent.sock"),
		AgentTimeout:     duration("AGENT_TIMEOUT", 30*time.Second),
		AgentTaskTimeout: duration("AGENT_TASK_TIMEOUT", 5*time.Minute),
		SessionTTL:       duration("SESSION_TTL", 12*time.Hour),
		SecureCookies:    boolSetting("SECURE_COOKIES", false),
		PublicIP:         setting("PUBLIC_IP", ""),
		CollectInterval:  interval("COLLECT_INTERVAL", 15*time.Minute),
	}

	for _, nameserver := range strings.Split(setting("NAMESERVERS", ""), ",") {
		if nameserver = strings.TrimSpace(nameserver); nameserver != "" {
			cfg.Nameservers = append(cfg.Nameservers, nameserver)
		}
	}

	if cfg.DatabaseDSN == "" {
		return Config{}, fmt.Errorf("%sDB_DSN is not set, e.g. \"muriscore:secret@tcp(127.0.0.1:3306)/muriscore?parseTime=true&loc=UTC\"", Prefix)
	}

	switch cfg.AgentDriver {
	case "socket", "fake":
	default:
		return Config{}, fmt.Errorf("%sAGENT_DRIVER must be \"socket\" or \"fake\", not %q", Prefix, cfg.AgentDriver)
	}

	if cfg.AgentDriver == "socket" && cfg.AgentSocket == "" {
		return Config{}, fmt.Errorf("%sAGENT_SOCKET is empty while %sAGENT_DRIVER is \"socket\"", Prefix, Prefix)
	}

	if cfg.SessionTTL < time.Minute {
		return Config{}, fmt.Errorf("%sSESSION_TTL must be at least a minute", Prefix)
	}

	for _, proxy := range strings.Split(setting("TRUSTED_PROXIES", ""), ",") {
		if proxy = strings.TrimSpace(proxy); proxy != "" {
			cfg.TrustedProxies = append(cfg.TrustedProxies, proxy)
		}
	}

	return cfg, nil
}

// setting reads MURISCORE_<name>, or the fallback when it is not set.
func setting(name, fallback string) string {
	if value := os.Getenv(Prefix + name); value != "" {
		return value
	}

	return fallback
}

func duration(name string, fallback time.Duration) time.Duration {
	value := setting(name, "")
	if value == "" {
		return fallback
	}

	parsed, err := time.ParseDuration(value)
	if err != nil {
		// A duration that does not parse would otherwise silently become
		// zero and make every task time out at once.
		seconds, secondsErr := strconv.Atoi(value)
		if secondsErr != nil {
			panic(fmt.Sprintf("%s%s: %q is not a duration", Prefix, name, value))
		}

		return time.Duration(seconds) * time.Second
	}

	return parsed
}

// interval reads a duration where "off", "none" and "0" mean "do not run".
func interval(name string, fallback time.Duration) time.Duration {
	value := strings.ToLower(strings.TrimSpace(setting(name, "")))

	switch value {
	case "":
		return fallback
	case "off", "none", "false", "0":
		return 0
	}

	return duration(name, fallback)
}

func boolSetting(name string, fallback bool) bool {
	value := setting(name, "")
	if value == "" {
		return fallback
	}

	switch strings.ToLower(value) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}
