package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// Node app port range, the same window the Laravel panel allocates from.
const (
	NodePortMin = 40000
	NodePortMax = 49999
)

var (
	// ErrNodeAppExists means the domain already runs an app; one per domain.
	ErrNodeAppExists = errors.New("the domain already has a Node.js app")
	// ErrNodeAppMissing means the id does not exist or belongs to somebody else.
	ErrNodeAppMissing = errors.New("no such Node.js app")
	// ErrNoFreePort means every port in the window is taken.
	ErrNoFreePort = errors.New("no free port left for another Node.js app")
)

// NodeApp is one long-running Node.js process behind a website.
type NodeApp struct {
	ID        int64             `json:"id"`
	DomainID  int64             `json:"domainId"`
	Domain    string            `json:"domain"`
	UserID    int64             `json:"userId"`
	Username  string            `json:"username"`
	Entry     string            `json:"entry"`
	Env       map[string]string `json:"env"`
	Port      int               `json:"port"`
	Enabled   bool              `json:"enabled"`
	Status    string            `json:"status"`
	CreatedAt time.Time         `json:"createdAt"`
}

// NodeApps lists the apps an operator may see: an administrator every app, a
// reseller the ones on its accounts, an account its own. Newest first.
func (s *Store) NodeApps(ctx context.Context, viewer *User, query string) ([]NodeApp, error) {
	filter := ""
	args := []any{}

	if viewer != nil && !viewer.IsAdmin() {
		filter = " WHERE d.user_id = ?"
		args = append(args, viewer.ID)
	}

	if query != "" {
		like := "%" + query + "%"
		if filter == "" {
			filter = " WHERE"
		} else {
			filter += " AND"
		}
		filter += " (d.name LIKE ? OR u.username LIKE ? OR n.entry LIKE ?)"
		args = append(args, like, like, like)
	}

	rows, err := s.db.QueryContext(ctx, `
		SELECT n.id, n.domain_id, d.name, d.user_id, u.username, n.entry,
			COALESCE(n.env, ''), n.port, n.enabled, n.status, n.created_at
		FROM node_apps n
		JOIN domains d ON d.id = n.domain_id
		JOIN users u ON u.id = d.user_id`+filter+`
		ORDER BY n.id DESC`, args...)
	if err != nil {
		return nil, wrap("list the Node.js apps", err)
	}
	defer rows.Close()

	apps := []NodeApp{}

	for rows.Next() {
		app, scanErr := scanNodeApp(rows)
		if scanErr != nil {
			return nil, wrap("list the Node.js apps", scanErr)
		}

		apps = append(apps, app)
	}

	return apps, wrap("list the Node.js apps", rows.Err())
}

// NodeAppByID reads one app, or ErrNodeAppMissing.
func (s *Store) NodeAppByID(ctx context.Context, id int64) (NodeApp, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT n.id, n.domain_id, d.name, d.user_id, u.username, n.entry,
			COALESCE(n.env, ''), n.port, n.enabled, n.status, n.created_at
		FROM node_apps n
		JOIN domains d ON d.id = n.domain_id
		JOIN users u ON u.id = d.user_id
		WHERE n.id = ?`, id)

	app, err := scanNodeApp(row)
	if errors.Is(err, sql.ErrNoRows) {
		return NodeApp{}, ErrNodeAppMissing
	}

	if err != nil {
		return NodeApp{}, wrap("read the Node.js app", err)
	}

	return app, nil
}

// CreateNodeApp reserves a port and records the app as pending. The agent
// call itself is queued by the HTTP layer, which knows the actor.
func (s *Store) CreateNodeApp(ctx context.Context, domainID int64, entry string, env map[string]string) (NodeApp, error) {
	if _, err := s.DomainByID(ctx, domainID); err != nil {
		return NodeApp{}, err
	}

	var taken int
	if err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM node_apps WHERE domain_id = ?`, domainID).Scan(&taken); err != nil {
		return NodeApp{}, wrap("check the domain", err)
	}

	if taken > 0 {
		return NodeApp{}, ErrNodeAppExists
	}

	port, err := s.freeNodePort(ctx)
	if err != nil {
		return NodeApp{}, err
	}

	encoded := []byte{}
	if len(env) > 0 {
		encoded, err = json.Marshal(env)
		if err != nil {
			return NodeApp{}, wrap("encode the environment", err)
		}
	}

	answer, err := s.db.ExecContext(ctx, `
		INSERT INTO node_apps (domain_id, entry, env, port, enabled, status, created_at, updated_at)
		VALUES (?, ?, ?, ?, 1, 'pending', ?, ?)`,
		domainID, entry, string(encoded), port, Now(), Now())
	if err != nil {
		return NodeApp{}, wrap("create the Node.js app", err)
	}

	id, err := answer.LastInsertId()
	if err != nil {
		return NodeApp{}, wrap("create the Node.js app", err)
	}

	app, err := s.NodeAppByID(ctx, id)
	if err != nil {
		return NodeApp{}, err
	}

	return app, nil
}

// DeleteNodeApp removes the row and reports who owned the domain, so the
// caller can queue the agent's deprovision afterwards.
func (s *Store) DeleteNodeApp(ctx context.Context, id int64) (NodeApp, error) {
	app, err := s.NodeAppByID(ctx, id)
	if err != nil {
		return NodeApp{}, err
	}

	if _, err := s.db.ExecContext(ctx, `DELETE FROM node_apps WHERE id = ?`, id); err != nil {
		return NodeApp{}, wrap("delete the Node.js app", err)
	}

	return app, nil
}

// SetNodeAppStatus records what the agent last reported, without touching the
// rest of the row.
func (s *Store) SetNodeAppStatus(ctx context.Context, id int64, status string) error {
	if _, err := s.db.ExecContext(ctx,
		`UPDATE node_apps SET status = ?, updated_at = ? WHERE id = ?`, status, Now(), id); err != nil {
		return wrap("update the Node.js app", err)
	}

	return nil
}

// freeNodePort picks the lowest free port in the window. Sequential keeps the
// allocation explainable; the window is wide enough that it never matters.
func (s *Store) freeNodePort(ctx context.Context) (int, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT port FROM node_apps`)
	if err != nil {
		return 0, wrap("look for a free port", err)
	}

	taken := map[int]bool{}

	for rows.Next() {
		var port int
		if err := rows.Scan(&port); err != nil {
			rows.Close()
			return 0, wrap("look for a free port", err)
		}

		taken[port] = true
	}

	if err := rows.Err(); err != nil {
		rows.Close()
		return 0, wrap("look for a free port", err)
	}

	rows.Close()

	for port := NodePortMin; port <= NodePortMax; port++ {
		if !taken[port] {
			return port, nil
		}
	}

	return 0, ErrNoFreePort
}

// nodeAppScanner reads one row of the list query.
type nodeAppScanner interface {
	Scan(dest ...any) error
}

func scanNodeApp(row nodeAppScanner) (NodeApp, error) {
	var (
		app     NodeApp
		env     string
		enabled bool
		created sql.NullTime
	)

	if err := row.Scan(&app.ID, &app.DomainID, &app.Domain, &app.UserID, &app.Username,
		&app.Entry, &env, &app.Port, &enabled, &app.Status, &created); err != nil {
		return NodeApp{}, err
	}

	app.Enabled = enabled

	if created.Valid {
		app.CreatedAt = created.Time
	}

	app.Env = map[string]string{}
	if env != "" {
		if err := json.Unmarshal([]byte(env), &app.Env); err != nil {
			return NodeApp{}, fmt.Errorf("decode the environment: %w", err)
		}
	}

	return app, nil
}
