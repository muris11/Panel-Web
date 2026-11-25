package main

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"os/user"
	"runtime"
	"strings"
	"time"

	"muriscore/panel/internal/config"
	"muriscore/panel/internal/db"
)

// check is one line of the doctor's report.
type check struct {
	name   string
	state  string // ok, warn, fail
	detail string
}

// doctor reports what this machine is and whether the panel can run here. It is
// read-only: it opens the database, looks at the agent socket and asks for the
// tools the panel and its deployment need. `muriscore-panel doctor` is the first
// thing to run on a distribution nobody tried before.
func doctor() error {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	checks := []check{
		operatingSystem(),
		initSystem(),
		binary("nginx", "serves the panel on 2222"),
		binary("systemctl", "runs the panel as a service"),
		binary("openssl", "makes the self-signed certificate"),
		binaryAny([]string{"mariadb", "mysql"}, "administers the panel database"),
	}

	cfg, cfgErr := config.Load()
	if cfgErr != nil {
		checks = append(checks, check{"configuration", "fail", cfgErr.Error()})
	} else {
		checks = append(checks, check{"configuration", "ok", "settings read from the environment"})
		checks = append(checks, agentSocket(cfg.AgentSocket))
		checks = append(checks, database(ctx, cfg.DatabaseDSN))
		checks = append(checks, listenAddress(cfg.Listen))
	}

	checks = append(checks, configDirectory(), panelUser())

	width := 0

	for _, item := range checks {
		if len(item.name) > width {
			width = len(item.name)
		}
	}

	failed := 0

	fmt.Printf("MurisCore doctor — %s/%s, %s\n\n", runtime.GOOS, runtime.GOARCH, version)

	for _, item := range checks {
		if item.state == "fail" {
			failed++
		}

		fmt.Printf("  %-*s  %-4s  %s\n", width, item.name, strings.ToUpper(item.state), item.detail)
	}

	fmt.Println()

	if failed > 0 {
		return fmt.Errorf("%d check(s) failed", failed)
	}

	fmt.Println("Everything the panel needs is present.")

	return nil
}

// operatingSystem names the distribution, which is what decides package names.
func operatingSystem() check {
	name, id, version, like := osRelease()

	if id == "" {
		return check{"operating system", "warn", runtime.GOOS + " — /etc/os-release is missing, so packages cannot be chosen"}
	}

	family := id
	if like != "" {
		family += " (" + like + "-like)"
	}

	return check{"operating system", "ok", fmt.Sprintf("%s, family %s", name+" "+version, family)}
}

// initSystem reports whether systemd is the running init, because the unit file
// assumes it.
func initSystem() check {
	if _, err := os.Stat("/run/systemd/system"); err == nil {
		return check{"init system", "ok", "systemd is running"}
	}

	return check{"init system", "warn", "systemd not detected; start the panel another way (the unit will not load)"}
}

// binary checks that a command exists.
func binary(name, why string) check {
	path, err := exec.LookPath(name)
	if err != nil {
		return check{name, "warn", "not installed — " + why}
	}

	return check{name, "ok", path}
}

// binaryAny reports the first of several names that exists, since Debian and
// RHEL disagree on whether the MariaDB client is called mariadb or mysql.
func binaryAny(names []string, why string) check {
	for _, name := range names {
		if path, err := exec.LookPath(name); err == nil {
			return check{name, "ok", path}
		}
	}

	return check{names[0], "warn", "not installed — " + why}
}

// agentSocket checks the socket muriscored listens on and whether this user may
// open it.
func agentSocket(path string) check {
	info, err := os.Stat(path)
	if err != nil {
		return check{"agent socket", "fail", path + " does not exist; is muriscored running?"}
	}

	if info.Mode()&os.ModeSocket == 0 {
		return check{"agent socket", "warn", path + " is not a socket"}
	}

	return check{"agent socket", "ok", fmt.Sprintf("%s, mode %04o", path, info.Mode().Perm())}
}

// database opens the panel database and reports the server version.
func database(ctx context.Context, dsn string) check {
	pool, err := db.Open(ctx, dsn)
	if err != nil {
		return check{"database", "fail", "cannot connect: " + err.Error()}
	}

	defer func() { _ = pool.Close() }()

	var version string
	if err := pool.QueryRowContext(ctx, "SELECT VERSION()").Scan(&version); err != nil {
		return check{"database", "fail", "connected but cannot read the version: " + err.Error()}
	}

	var tables int
	if err := pool.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM information_schema.tables
		WHERE table_schema = DATABASE()`).Scan(&tables); err != nil {
		return check{"database", "warn", version + " — cannot count tables: " + err.Error()}
	}

	return check{"database", "ok", fmt.Sprintf("%s, %d table(s)", version, tables)}
}

// listenAddress reports whether the configured address can be bound, which also
// catches a panel that is already running.
func listenAddress(address string) check {
	listener, err := net.Listen("tcp", address)
	if err != nil {
		return check{"listen address", "warn", address + " is busy; the panel may already be running"}
	}

	_ = listener.Close()

	return check{"listen address", "ok", address + " is free"}
}

// configDirectory looks at where the installer puts settings and certificates.
func configDirectory() check {
	const dir = "/etc/muriscore"

	info, err := os.Stat(dir)
	if err != nil {
		return check{"config directory", "warn", dir + " does not exist yet; deploy/install.sh creates it"}
	}

	return check{"config directory", "ok", fmt.Sprintf("%s, mode %04o", dir, info.Mode().Perm())}
}

// panelUser reports who will run the service, which is what the unit file names.
func panelUser() check {
	name := "muriscore"
	if current, err := user.Lookup("muriscore"); err == nil && current != nil {
		// An upgraded host keeps the old account, and that is fine.
		name = "muriscore"
	}

	found, err := user.Lookup(name)
	if err != nil {
		return check{"service user", "warn", name + " does not exist; deploy/install.sh creates it"}
	}

	groups, _ := found.GroupIds()

	return check{"service user", "ok", fmt.Sprintf("%s, uid %s, %d group(s)", found.Username, found.Uid, len(groups))}
}

// osRelease reads /etc/os-release without pulling in a dependency.
func osRelease() (name, id, version, like string) {
	raw, err := os.ReadFile("/etc/os-release")
	if err != nil {
		return "", "", "", ""
	}

	values := map[string]string{}

	for _, line := range strings.Split(string(raw), "\n") {
		key, value, ok := strings.Cut(strings.TrimSpace(line), "=")
		if !ok {
			continue
		}

		values[key] = strings.Trim(value, `"'`)
	}

	name = values["PRETTY_NAME"]
	if name == "" {
		name = values["NAME"]
	}

	return name, values["ID"], values["VERSION_ID"], values["ID_LIKE"]
}
