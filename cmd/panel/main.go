// Command panel is MurisCore's control panel: a JSON API and a React
// single-page app in one static binary.
//
// Subcommands:
//
//	panel serve                    run the panel (the default)
//	panel migrate                  apply the schema and exit
//	panel create-admin <name> <email>
//	panel collect                  run the collectors once and exit
//	panel doctor                   report whether this host can run the panel
//	panel version
package main

import (
	"context"
	"crypto/rand"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"muriscore/panel/internal/agent"
	"muriscore/panel/internal/auth"
	"muriscore/panel/internal/collector"
	"muriscore/panel/internal/config"
	"muriscore/panel/internal/db"
	"muriscore/panel/internal/httpapi"
	"muriscore/panel/internal/rules"
	"muriscore/panel/internal/store"
	"muriscore/panel/internal/worker"
)

// version is set at build time:
//
//	go build -ldflags "-X main.version=$(cat VERSION)" ./cmd/panel
var version = "dev"

func main() {
	command := "serve"
	if len(os.Args) > 1 {
		command = os.Args[1]
	}

	logger := slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)

	var err error

	switch command {
	case "serve":
		err = serve(logger, os.Args[2:])
	case "migrate":
		err = migrate(logger)
	case "create-admin":
		err = createAdmin(logger, os.Args[2:])
	case "collect":
		err = collect(logger)
	case "doctor":
		err = doctor()
	case "version", "-version", "--version":
		fmt.Println(version)
	case "help", "-h", "--help":
		usage()
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n", command)
		usage()
		os.Exit(2)
	}

	if err != nil {
		logger.Error("panel stopped", "error", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `MurisCore panel

  muriscore-panel serve                     run the panel (default)
  muriscore-panel migrate                   apply the schema and exit
  muriscore-panel create-admin <user> <email> [--password <password>]
  muriscore-panel collect                   run the collectors once and exit
  muriscore-panel doctor                    report whether this host can run it
  muriscore-panel version

Environment (MURISCORE_* wins, RAYXO_* is still read for an older install):
  MURISCORE_LISTEN          address to serve on (default 127.0.0.1:8080)
  MURISCORE_DB_DSN          MariaDB DSN, required (include parseTime=true&loc=UTC)
  MURISCORE_AGENT_DRIVER    socket | fake (default fake)
  MURISCORE_AGENT_SOCKET    rayxod socket (default /run/rayxo/agent.sock)
  MURISCORE_APP_KEY         the Laravel APP_KEY, to read encrypted 2FA secrets
  MURISCORE_SESSION_TTL     session lifetime (default 12h)
  MURISCORE_SECURE_COOKIES  true behind HTTPS (default false)
  MURISCORE_TRUSTED_PROXIES comma-separated addresses allowed to set X-Forwarded-For
  MURISCORE_COLLECT_INTERVAL time between collector runs (default 15m, "off")
  MURISCORE_PUBLIC_IP       address new A records point at
  MURISCORE_NAMESERVERS     nameservers every new zone names
`)
}

// open connects and migrates, which every command that touches data needs.
func open(ctx context.Context, logger *slog.Logger) (config.Config, *store.Store, func(), error) {
	cfg, err := config.Load()
	if err != nil {
		return config.Config{}, nil, nil, err
	}

	pool, err := db.Open(ctx, cfg.DatabaseDSN)
	if err != nil {
		return cfg, nil, nil, err
	}

	applied, err := db.Migrate(ctx, pool)
	if err != nil {
		_ = pool.Close()

		return cfg, nil, nil, err
	}

	for _, name := range applied {
		logger.Info("applied schema", "file", name)
	}

	return cfg, store.New(pool), func() { _ = pool.Close() }, nil
}

func serve(logger *slog.Logger, args []string) error {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, os.Interrupt)
	defer stop()

	cfg, st, closeDB, err := open(ctx, logger)
	if err != nil {
		return err
	}

	defer closeDB()

	client := clientFor(cfg, logger)

	logger.Info("panel starting", "version", version, "agent", client.Name())

	// The task worker answers queued work. It is a goroutine rather than a
	// second binary: one deployable file was the point.
	go worker.New(st, client, logger, worker.Config{
		Poll:        400 * time.Millisecond,
		TaskTimeout: cfg.AgentTaskTimeout,
	}).Start(ctx)

	// The collectors replace the Laravel scheduler: traffic, resources and
	// certificate expiry are kept current by the panel itself, so a host that
	// runs only this binary needs no queue worker and no timer.
	if cfg.CollectInterval > 0 {
		go runCollectors(ctx, logger, st, client, cfg.CollectInterval)
	}

	server := httpapi.New(cfg, st, client, logger)

	return server.Run(ctx)
}

// collect runs the collectors once, for a host that prefers cron to a
// goroutine. It exits non-zero when a job failed, so cron mails someone.
func collect(logger *slog.Logger) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	cfg, st, closeDB, err := open(ctx, logger)
	if err != nil {
		return err
	}

	defer closeDB()

	client := clientFor(cfg, logger)

	return collector.New(st, client, logger).Run(ctx)
}

// clientFor returns the agent the rest of the process uses.
func clientFor(cfg config.Config, logger *slog.Logger) agent.Agent {
	if cfg.AgentDriver == "socket" {
		return agent.NewSocketAgent(cfg.AgentSocket, cfg.AgentTimeout)
	}

	logger.Warn("the agent is simulated: no change reaches a server (MURISCORE_AGENT_DRIVER=fake)")

	return agent.NewFake()
}

// runCollectors runs once at start and then on a timer. It waits for the first
// tick rather than running immediately, so a panel that starts during a
// database restore does not read a half-written schema.
func runCollectors(ctx context.Context, logger *slog.Logger, st *store.Store, client agent.Agent, every time.Duration) {
	ticker := time.NewTicker(every)
	defer ticker.Stop()

	work := collector.New(st, client, logger)

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			runCtx, cancel := context.WithTimeout(ctx, every)

			if err := work.Run(runCtx); err != nil {
				logger.Warn("a collector run had failures", "error", err)
			}

			cancel()
		}
	}
}

func migrate(logger *slog.Logger) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	_, _, closeDB, err := open(ctx, logger)
	if err != nil {
		return err
	}

	defer closeDB()

	logger.Info("schema is up to date")

	return nil
}

// createAdmin makes the first administrator, and prints the password once.
// It is what the installer calls on a fresh server.
func createAdmin(logger *slog.Logger, args []string) error {
	if len(args) < 2 {
		usage()

		return fmt.Errorf("create-admin needs a username and an email")
	}

	username := rules.NormalizeUsername(args[0])
	email := args[1]

	password := ""
	generated := true

	for i := 2; i < len(args); i++ {
		if args[i] == "--password" && i+1 < len(args) {
			password = args[i+1]
			generated = false
			i++
		}
	}

	if err := rules.Username(username); err != nil {
		return err
	}

	if err := rules.Email(email); err != nil {
		return err
	}

	if generated {
		var err error

		if password, err = generatePassword(24); err != nil {
			return err
		}
	}

	hash, err := auth.HashPassword(password)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	_, st, closeDB, err := open(ctx, logger)
	if err != nil {
		return err
	}

	defer closeDB()

	taken, err := st.UsernameTaken(ctx, username)
	if err != nil {
		return err
	}

	if taken {
		return fmt.Errorf("the username %q is already taken", username)
	}

	user, err := st.CreatePanelUser(ctx, username, "Administrator", email, hash, store.RoleAdmin, nil)
	if err != nil {
		return err
	}

	logger.Info("administrator created", "username", user.Username, "id", user.ID)

	if generated {
		// The password is printed to stdout, not to the log: a log file
		// outlives the terminal and is copied to backups.
		fmt.Printf("\n  username: %s\n  password: %s\n\n  This password is shown once. Store it now.\n\n", user.Username, password)
	}

	return nil
}

func generatePassword(length int) (string, error) {
	// 32 symbols, and 256/32 = 8, so mapping a byte onto the alphabet with a
	// modulo introduces no bias. l, o, 0 and 1 are left out so a password read
	// off a screen cannot be mistyped.
	const alphabet = "abcdefghijkmnpqrstuvwxyz23456789"

	if 256%len(alphabet) != 0 {
		return "", fmt.Errorf("the password alphabet must divide 256")
	}

	raw := make([]byte, length)

	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("generate a password: %w", err)
	}

	out := make([]byte, length)
	for i, b := range raw {
		out[i] = alphabet[int(b)%len(alphabet)]
	}

	return string(out), nil
}
