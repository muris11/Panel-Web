// Command panel is RAYXO's control panel: a JSON API and a React single-page
// app in one static binary.
//
// Subcommands:
//
//	panel serve                    run the panel (the default)
//	panel migrate                  apply the schema and exit
//	panel create-admin <name> <email>
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

	"rayxo/panel/internal/agent"
	"rayxo/panel/internal/auth"
	"rayxo/panel/internal/config"
	"rayxo/panel/internal/db"
	"rayxo/panel/internal/httpapi"
	"rayxo/panel/internal/rules"
	"rayxo/panel/internal/store"
	"rayxo/panel/internal/worker"
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
	fmt.Fprint(os.Stderr, `RAYXO panel

  panel serve                     run the panel (default)
  panel migrate                   apply the schema and exit
  panel create-admin <user> <email> [--password <password>]
  panel version

Environment:
  RAYXO_LISTEN         address to serve on (default 127.0.0.1:8080)
  RAYXO_DB_DSN         MariaDB DSN, required (include parseTime=true&loc=UTC)
  RAYXO_AGENT_DRIVER   socket | fake (default fake)
  RAYXO_AGENT_SOCKET   rayxod socket (default /run/rayxo/agent.sock)
  RAYXO_APP_KEY        the Laravel APP_KEY, to read encrypted 2FA secrets
  RAYXO_SESSION_TTL    session lifetime (default 12h)
  RAYXO_SECURE_COOKIES true behind HTTPS (default false)
  RAYXO_TRUSTED_PROXIES comma-separated addresses allowed to set X-Forwarded-For
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

	var client agent.Agent

	switch cfg.AgentDriver {
	case "socket":
		client = agent.NewSocketAgent(cfg.AgentSocket, cfg.AgentTimeout)
	default:
		client = agent.NewFake()
		logger.Warn("the agent is simulated: no change reaches a server (RAYXO_AGENT_DRIVER=fake)")
	}

	logger.Info("panel starting", "version", version, "agent", client.Name())

	// The task worker answers queued work. It is a goroutine rather than a
	// second binary: one deployable file was the point.
	go worker.New(st, client, logger, worker.Config{
		Poll:        400 * time.Millisecond,
		TaskTimeout: cfg.AgentTaskTimeout,
	}).Start(ctx)

	server := httpapi.New(cfg, st, client, logger)

	return server.Run(ctx)
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
