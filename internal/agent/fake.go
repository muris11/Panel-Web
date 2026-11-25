package agent

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"sync"
	"time"
)

// Fake answers the agent methods the panel uses without touching a server,
// so the panel runs on a laptop that has no rayxod. Every answer carries
// "simulated": true, which the panel shows in the interface.
//
// It is also what the HTTP tests run against: the point of the fake is that
// a test can drive a whole task to its end without root.
type Fake struct {
	mu sync.Mutex

	// Calls records every method the panel asked for, in order, so a test can
	// check that the right work was dispatched.
	Calls []string

	// FailNext makes the next call for the method fail, to exercise the
	// failure path.
	FailNext map[string]string
}

// NewFake returns an agent that simulates the server.
func NewFake() *Fake {
	return &Fake{FailNext: map[string]string{}}
}

func (f *Fake) Name() string { return "fake" }

// Call answers one method.
func (f *Fake) Call(_ context.Context, method string, params any) (json.RawMessage, error) {
	f.mu.Lock()
	f.Calls = append(f.Calls, method)

	reason, shouldFail := f.FailNext[method]
	if shouldFail {
		delete(f.FailNext, method)
	}

	f.mu.Unlock()

	if shouldFail {
		return nil, &Error{Code: -32000, Message: reason}
	}

	values, _ := params.(map[string]any)

	var answer any

	switch method {
	case "agent.ping":
		answer = map[string]any{"pong": true, "version": "fake"}

	case "system.info":
		// The shape the real agent returns from sysinfo.Info: the SPA renders
		// this, so the fake must not invent its own field names.
		answer = map[string]any{
			"hostname": "panel-go.test",
			"os": map[string]any{
				"name":    "Ubuntu",
				"version": "24.04.3 LTS",
				"kernel":  "6.8.0-51-generic",
			},
			"cpu":            map[string]any{"model": "Fake Xeon", "cores": 4},
			"load":           []float64{0.42, 0.31, 0.28},
			"memory":         map[string]any{"total_mb": 8192, "used_mb": 3072},
			"disk":           map[string]any{"mount": "/", "total_gb": 80.0, "used_gb": 21.4},
			"uptime_seconds": 86400 * 3,
			"agent_version":  "fake",
			"simulated":      true,
		}

	case "account.create":
		username, _ := values["username"].(string)

		answer = map[string]any{
			"username":  username,
			"uid":       2000 + stableNumber(username)%500,
			"gid":       2000 + stableNumber(username)%500,
			"home":      "/home/" + username,
			"simulated": true,
		}

	case "account.suspend", "account.unsuspend", "account.delete":
		username, _ := values["username"].(string)

		answer = map[string]any{"username": username, "done": true, "simulated": true}

	case "web.apply_domain":
		domain, _ := values["domain"].(string)

		answer = map[string]any{"domain": domain, "applied": true, "simulated": true}

	case "web.remove_domain":
		domain, _ := values["domain"].(string)

		answer = map[string]any{"domain": domain, "removed": true, "simulated": true}

	case "service.status":
		answer = map[string]any{
			"services": []any{
				map[string]any{"unit": "nginx.service", "label": "Web server", "state": "active", "enabled": true},
				map[string]any{"unit": "mariadb.service", "label": "Database", "state": "active", "enabled": true},
				map[string]any{"unit": "postfix.service", "label": "Mail", "state": "active", "enabled": true},
				map[string]any{"unit": "ssh.service", "label": "SSH", "state": "active", "enabled": true},
			},
			"simulated": true,
		}

	case "firewall.status":
		answer = map[string]any{
			"enabled": true,
			"rules": []any{
				map[string]any{"port": 22, "protocol": "tcp", "action": "allow"},
				map[string]any{"port": 80, "protocol": "tcp", "action": "allow"},
				map[string]any{"port": 443, "protocol": "tcp", "action": "allow"},
			},
			"simulated": true,
		}

	case "system.settings":
		answer = map[string]any{
			"hostname":         "panel-go.test",
			"timezone":         "Asia/Jakarta",
			"panel_port":       2222,
			"nameservers":      []string{"ns1.panel-go.test", "ns2.panel-go.test"},
			"agent_version":    "fake",
			"panel_version":    "0.9.0",
			"operating_system": "Ubuntu 24.04",
			"simulated":        true,
		}

	case "bfm.status":
		answer = map[string]any{"enabled": true, "blocked": []any{}, "simulated": true}

	case "usage.bandwidth":
		answer = map[string]any{
			"month":     time.Now().Format("2006-01"),
			"usage":     map[string]any{"total_gb": 12.5, "in_gb": 8.1, "out_gb": 4.4},
			"simulated": true,
		}

	case "usage.resources":
		answer = map[string]any{"cpu_percent": 12.0, "memory_mb": 3072, "simulated": true}

	case "quota.report":
		answer = map[string]any{"accounts": []any{}, "simulated": true}

	case "dns.sync_zone":
		zone, _ := values["zone"].(string)

		answer = map[string]any{"zone": zone, "records": 0, "synced": true, "simulated": true}

	case "ssl.issue":
		domain, _ := values["domain"].(string)

		answer = map[string]any{
			"names":      []string{domain, "www." + domain},
			"expires_at": time.Now().AddDate(0, 0, 90).UTC().Format(time.RFC3339),
			"simulated":  true,
		}

	case "ssl.delete":
		domain, _ := values["domain"].(string)

		answer = map[string]any{"domain": domain, "removed": true, "simulated": true}

	case "mail.domain_apply":
		domain, _ := values["domain"].(string)

		answer = map[string]any{
			"domain":          domain,
			"dkim_public_key": "v=DKIM1; k=rsa; p=FAKE",
			"simulated":       true,
		}

	case "db.create_database", "db.drop_database":
		database, _ := values["database"].(string)

		answer = map[string]any{"database": database, "done": true, "simulated": true}

	case "db.apply_user":
		user, _ := values["user"].(string)

		answer = map[string]any{"user": user, "applied": true, "simulated": true}

	case "pg.create_database", "pg.delete_database":
		database, _ := values["database"].(string)

		answer = map[string]any{"database": database, "done": true, "simulated": true}

	case "pg.apply_user":
		user, _ := values["user"].(string)

		answer = map[string]any{"user": user, "applied": true, "simulated": true}

	case "pg.delete_user":
		user, _ := values["user"].(string)

		answer = map[string]any{"user": user, "done": true, "simulated": true}

	case "redis.provision":
		answer = map[string]any{
			"username":      values["username"],
			"password":      values["password"],
			"max_memory_mb": values["max_memory_mb"],
			"done":          true,
			"simulated":     true,
		}

	case "redis.deprovision", "redis.check", "redis.service", "redis.status", "redis.conf":
		answer = map[string]any{"done": true, "simulated": true}

	case "cron.apply", "ftp.ensure_dir", "backup.create", "backup.restore":
		answer = map[string]any{"done": true, "simulated": true}

	case "fs.list":
		path, _ := values["path"].(string)

		answer = map[string]any{
			"path": path,
			"entries": []any{
				map[string]any{"name": "public_html", "type": "dir", "size": 0, "mode": "0755", "modified": 1767225600},
				map[string]any{"name": "backups", "type": "dir", "size": 0, "mode": "0750", "modified": 1767225600},
				map[string]any{"name": "logs", "type": "dir", "size": 0, "mode": "0750", "modified": 1767225600},
				map[string]any{"name": "index.html", "type": "file", "size": 1024, "mode": "0644", "modified": 1767225600},
			},
			"truncated": false,
			"simulated": true,
		}

	case "fs.read":
		path, _ := values["path"].(string)

		answer = map[string]any{
			"path":      path,
			"content":   "<!-- simulated file -->\n<p>This is the fake agent.</p>\n",
			"size":      54,
			"version":   "fake-1",
			"simulated": true,
		}

	case "fs.search":
		answer = map[string]any{"path": values["path"], "entries": []any{}, "simulated": true}

	case "fs.write", "fs.mkdir", "fs.delete", "fs.rename", "fs.chmod", "fs.compress", "fs.extract":
		answer = map[string]any{"path": values["path"], "done": true, "simulated": true}

	case "service.action":
		answer = map[string]any{
			"unit":      values["unit"],
			"action":    values["action"],
			"simulated": true,
		}

	case "system.change_hostname":
		answer = map[string]any{"hostname": values["hostname"], "simulated": true}

	case "system.set_timezone":
		answer = map[string]any{"timezone": values["timezone"], "simulated": true}

	default:
		answer = map[string]any{"simulated": true, "method": method}
	}

	encoded, err := json.Marshal(answer)
	if err != nil {
		return nil, fmt.Errorf("encode the simulated answer: %w", err)
	}

	// A little latency, so the panel's own progress handling is exercised
	// rather than hidden by an instant answer.
	time.Sleep(20 * time.Millisecond)

	return encoded, nil
}

func stableNumber(value string) int {
	sum := 0
	for _, r := range value {
		sum = (sum*31 + int(r)) % 100000
	}

	return sum
}

// RandomToken is a small helper the tests use to build unique names.
func RandomToken() string {
	raw := make([]byte, 4)
	_, _ = rand.Read(raw)

	return fmt.Sprintf("%x", raw)
}
