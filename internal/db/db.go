package db

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"sort"
	"strings"
	"time"

	_ "github.com/go-sql-driver/mysql"
)

//go:embed schema/*.sql
var schemaFS embed.FS

// Timeout for one migration file; a big ALTER on a large table can take a
// while, so this is generous compared with a normal query.
const migrationTimeout = 10 * time.Minute

// Open connects to the panel's database and checks it is reachable.
//
// The DSN must carry parseTime=true so TIMESTAMP columns scan into time.Time,
// and loc=UTC so every timestamp the panel writes is UTC, like the Laravel
// panel's.
func Open(ctx context.Context, dsn string) (*sql.DB, error) {
	if !strings.Contains(dsn, "parseTime=true") {
		return nil, fmt.Errorf("database DSN must include parseTime=true")
	}

	pool, err := sql.Open("mysql", dsn)
	if err != nil {
		return nil, fmt.Errorf("open the database: %w", err)
	}

	// A panel serves a handful of operators, not a crowd. Keeping the pool
	// small leaves connections for the customers' own sites, which share the
	// same MariaDB.
	pool.SetMaxOpenConns(16)
	pool.SetMaxIdleConns(8)
	pool.SetConnMaxLifetime(30 * time.Minute)
	pool.SetConnMaxIdleTime(5 * time.Minute)

	pingCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	if err := pool.PingContext(pingCtx); err != nil {
		_ = pool.Close()

		return nil, fmt.Errorf("reach the database: %w", err)
	}

	return pool, nil
}

// Migrate applies every embedded schema file that has not run yet, in file
// name order, each one in its own transaction. An interrupted migration is
// rolled back and runs again on the next start.
func Migrate(ctx context.Context, pool *sql.DB) (applied []string, err error) {
	if _, err := pool.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			name VARCHAR(255) NOT NULL,
			applied_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
			PRIMARY KEY (name)
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci`); err != nil {
		return nil, fmt.Errorf("create schema_migrations: %w", err)
	}

	done := map[string]bool{}

	rows, err := pool.QueryContext(ctx, `SELECT name FROM schema_migrations`)
	if err != nil {
		return nil, fmt.Errorf("read schema_migrations: %w", err)
	}

	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			rows.Close()

			return nil, fmt.Errorf("read schema_migrations: %w", err)
		}

		done[name] = true
	}

	if err := rows.Err(); err != nil {
		rows.Close()

		return nil, fmt.Errorf("read schema_migrations: %w", err)
	}

	rows.Close()

	entries, err := schemaFS.ReadDir("schema")
	if err != nil {
		return nil, fmt.Errorf("list embedded schema files: %w", err)
	}

	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".sql") {
			names = append(names, entry.Name())
		}
	}

	sort.Strings(names)

	for _, name := range names {
		if done[name] {
			continue
		}

		body, err := schemaFS.ReadFile("schema/" + name)
		if err != nil {
			return applied, fmt.Errorf("read %s: %w", name, err)
		}

		if err := applyFile(ctx, pool, name, string(body)); err != nil {
			return applied, err
		}

		applied = append(applied, name)
	}

	return applied, nil
}

func applyFile(ctx context.Context, pool *sql.DB, name, body string) error {
	fileCtx, cancel := context.WithTimeout(ctx, migrationTimeout)
	defer cancel()

	tx, err := pool.BeginTx(fileCtx, nil)
	if err != nil {
		return fmt.Errorf("begin %s: %w", name, err)
	}

	defer func() { _ = tx.Rollback() }()

	for _, statement := range SplitStatements(body) {
		if _, err := tx.ExecContext(fileCtx, statement); err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
	}

	if _, err := tx.ExecContext(fileCtx, `INSERT INTO schema_migrations (name) VALUES (?)`, name); err != nil {
		return fmt.Errorf("%s: record the migration: %w", name, err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("%s: commit: %w", name, err)
	}

	return nil
}

// SplitStatements cuts a SQL file into single statements, dropping "--" line
// comments and "/* */" blocks and ignoring semicolons inside quoted strings.
//
// MySQL's driver does not run several statements in one Exec unless
// multiStatements is turned on, and turning that on would also let one
// injected value end a statement and start another.
func SplitStatements(body string) []string {
	var (
		statements []string
		current    strings.Builder

		inLineComment  bool
		inBlockComment bool
		escaped        bool

		// quote is the character that opened the value we are inside of: a
		// single quote, a double quote or a backtick. Zero means we are in SQL.
		quote rune
	)

	runes := []rune(body)

	for i := 0; i < len(runes); i++ {
		ch := runes[i]

		switch {
		case inLineComment:
			if ch == '\n' {
				inLineComment = false
				current.WriteRune(ch)
			}

			continue

		case inBlockComment:
			if ch == '*' && i+1 < len(runes) && runes[i+1] == '/' {
				inBlockComment = false
				i++
			}

			continue

		case quote != 0:
			current.WriteRune(ch)

			if quote == '`' {
				// A backtick inside a backticked name is written twice.
				if ch == '`' && i+1 < len(runes) && runes[i+1] == '`' {
					current.WriteRune(runes[i+1])
					i++

					continue
				}

				if ch == '`' {
					quote = 0
				}

				continue
			}

			switch {
			case escaped:
				escaped = false
			case ch == '\\':
				escaped = true
			case ch == quote:
				quote = 0
			}

			continue
		}

		switch {
		case ch == '-' && i+1 < len(runes) && runes[i+1] == '-':
			inLineComment = true
			i++
		case ch == '/' && i+1 < len(runes) && runes[i+1] == '*':
			inBlockComment = true
			i++
		case ch == '\'' || ch == '"' || ch == '`':
			quote = ch
			current.WriteRune(ch)
		case ch == ';':
			if statement := strings.TrimSpace(current.String()); statement != "" {
				statements = append(statements, statement)
			}

			current.Reset()
		default:
			current.WriteRune(ch)
		}
	}

	if statement := strings.TrimSpace(current.String()); statement != "" {
		statements = append(statements, statement)
	}

	return statements
}
