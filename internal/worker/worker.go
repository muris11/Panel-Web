// Package worker runs the tasks the panel queues for the agent.
//
// It is the Go equivalent of the Laravel panel's queue worker plus its
// Apply*TaskResult listeners: claim a task, call the agent, and then write
// the agent's answer back into the panel's own tables.
package worker

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"time"

	"muriscore/panel/internal/agent"
	"muriscore/panel/internal/store"
)

// Config tunes the worker.
type Config struct {
	// Poll is how often an idle worker looks for work.
	Poll time.Duration
	// TaskTimeout bounds one agent call; a task still running after this is
	// failed, not retried, so an account is never blocked forever.
	TaskTimeout time.Duration
}

// DefaultConfig keeps the panel responsive without polling in a tight loop.
func DefaultConfig() Config {
	return Config{Poll: 400 * time.Millisecond, TaskTimeout: 10 * time.Minute}
}

// Worker turns queued tasks into agent calls.
type Worker struct {
	store *store.Store
	agent agent.Agent
	log   *slog.Logger
	cfg   Config
}

// New returns a worker. Nothing runs until Start is called.
func New(st *store.Store, client agent.Agent, log *slog.Logger, cfg Config) *Worker {
	if cfg.Poll <= 0 {
		cfg.Poll = DefaultConfig().Poll
	}

	if cfg.TaskTimeout <= 0 {
		cfg.TaskTimeout = DefaultConfig().TaskTimeout
	}

	return &Worker{store: st, agent: client, log: log, cfg: cfg}
}

// Start runs until ctx is cancelled.
func (w *Worker) Start(ctx context.Context) {
	ticker := time.NewTicker(w.cfg.Poll)
	defer ticker.Stop()

	housekeeping := time.NewTicker(30 * time.Second)
	defer housekeeping.Stop()

	w.sweep(ctx)

	for {
		select {
		case <-ctx.Done():
			w.log.Info("task worker stopped")

			return
		case <-housekeeping.C:
			w.sweep(ctx)
		case <-ticker.C:
			// Drain everything that is ready before waiting again, so a burst
			// of work does not wait one poll per task.
			for w.once(ctx) {
				if ctx.Err() != nil {
					return
				}
			}
		}
	}
}

// once runs at most one task and reports whether there was work.
// RunOnce processes at most one queued task and reports whether it found any.
// Start calls it in a loop; it is exported so a test can drain the queue
// without waiting on a ticker.
func (w *Worker) RunOnce(ctx context.Context) bool {
	return w.once(ctx)
}

func (w *Worker) once(ctx context.Context) bool {
	task, ok, err := w.store.ClaimNext(ctx, store.Now())
	if err != nil {
		w.log.Error("could not claim a task", "error", err)

		return false
	}

	if !ok {
		return false
	}

	callCtx, cancel := context.WithTimeout(ctx, w.cfg.TaskTimeout)
	defer cancel()

	var params map[string]any
	if task.Payload != "" {
		if err := json.Unmarshal([]byte(task.Payload), &params); err != nil {
			w.fail(ctx, task, "the stored task payload is not valid JSON")

			return true
		}
	}

	output, callErr := w.agent.Call(callCtx, task.Action, params)
	if callErr != nil {
		w.fail(ctx, task, callErr.Error())

		return true
	}

	// The change on the server is done; only the panel's own record can still
	// fail here. Report that as a failed task with the reason, because an
	// account stuck in "pending" with a server behind it is worse than a
	// visible error.
	if err := w.apply(ctx, task, output); err != nil {
		w.fail(ctx, task, "the agent finished but the panel could not record it: "+err.Error())

		return true
	}

	if err := w.store.FinishTask(ctx, task.ID, store.TaskSuccess, string(output), ""); err != nil {
		w.log.Error("could not record a finished task", "task", task.ID, "error", err)
	}

	w.log.Info("task finished", "task", task.ID, "action", task.Action)

	return true
}

func (w *Worker) fail(ctx context.Context, task store.Task, reason string) {
	if err := w.store.FinishTask(ctx, task.ID, store.TaskFailed, "", reason); err != nil {
		w.log.Error("could not record a failed task", "task", task.ID, "error", err)
	}

	w.log.Warn("task failed", "task", task.ID, "action", task.Action, "reason", reason)
}

// apply writes the agent's answer into the panel's tables.
func (w *Worker) apply(ctx context.Context, task store.Task, output json.RawMessage) error {
	switch task.Action {
	case "account.create":
		var answer struct {
			UID *int32 `json:"uid"`
			GID *int32 `json:"gid"`
		}

		if err := json.Unmarshal(output, &answer); err != nil {
			return errors.New("the agent's answer has no account ids")
		}

		if task.UserID == nil || answer.UID == nil || answer.GID == nil {
			return errors.New("the agent did not report the account's uid and gid")
		}

		return w.store.ActivateAccount(ctx, *task.UserID, *answer.UID, *answer.GID)

	case "account.suspend":
		if task.UserID == nil {
			return errors.New("a suspend task has no account")
		}

		return w.store.UpdateAccountStatus(ctx, *task.UserID, store.StatusSuspended, w.reason(task))

	case "account.unsuspend":
		if task.UserID == nil {
			return errors.New("an unsuspend task has no account")
		}

		return w.store.UpdateAccountStatus(ctx, *task.UserID, store.StatusActive, "")

	case "account.delete":
		if task.UserID == nil {
			return errors.New("a delete task has no account")
		}

		return w.store.DeleteAccount(ctx, *task.UserID)

	case "web.apply_domain":
		var answer struct {
			Domain string `json:"domain"`
		}

		if err := json.Unmarshal(output, &answer); err != nil || answer.Domain == "" {
			return errors.New("the agent's answer has no domain")
		}

		return w.store.SetDomainStatus(ctx, answer.Domain, "active")

	case "ssl.issue", "ssl.install":
		// The panel stores the certificate's own expiry so the domains page can
		// warn before it lapses, instead of asking the agent on every page load.
		var answer struct {
			Names     []string `json:"names"`
			Domain    string   `json:"domain"`
			ExpiresAt string   `json:"expires_at"`
		}

		if err := json.Unmarshal(output, &answer); err != nil {
			return errors.New("the agent's answer is not a certificate result")
		}

		name := answer.Domain

		if name == "" && len(answer.Names) > 0 {
			name = answer.Names[0]
		}

		if name == "" {
			return errors.New("the agent did not say which domain the certificate is for")
		}

		var expires *time.Time

		if answer.ExpiresAt != "" {
			if when, err := time.Parse(time.RFC3339, answer.ExpiresAt); err == nil {
				expires = &when
			}
		}

		return w.store.SetDomainSSLByName(ctx, name, "active", "letsencrypt", expires)

	case "dns.sync_zone":
		var answer struct {
			Zone string `json:"zone"`
		}

		if err := json.Unmarshal(output, &answer); err != nil || answer.Zone == "" {
			return nil
		}

		return w.store.MarkZoneSynced(ctx, answer.Zone)

	case "mail.domain_apply":
		var answer struct {
			Domain    string `json:"domain"`
			PublicKey string `json:"dkim_public_key"`
		}

		if err := json.Unmarshal(output, &answer); err != nil || answer.Domain == "" {
			return nil
		}

		return w.store.SetMailDomainActiveByName(ctx, answer.Domain, "active", answer.PublicKey)

	case "db.create_database":
		var answer struct {
			Database string `json:"database"`
		}

		if err := json.Unmarshal(output, &answer); err != nil || answer.Database == "" {
			return nil
		}

		return w.store.SetDatabaseStatusByName(ctx, answer.Database, "active")

	case "db.apply_user":
		var answer struct {
			User string `json:"user"`
		}

		if err := json.Unmarshal(output, &answer); err != nil || answer.User == "" {
			return nil
		}

		return w.store.SetDatabaseUserStatusByName(ctx, answer.User, "active")

	case "pg.create_database":
		var answer struct {
			Database string `json:"database"`
		}

		if err := json.Unmarshal(output, &answer); err != nil || answer.Database == "" {
			return nil
		}

		return w.store.SetPostgresDatabaseStatusByName(ctx, answer.Database, "active")

	case "pg.apply_user":
		var answer struct {
			User string `json:"user"`
		}

		if err := json.Unmarshal(output, &answer); err != nil || answer.User == "" {
			return nil
		}

		return w.store.SetPostgresUserStatusByName(ctx, answer.User, "active")

	case "web.remove_domain":
		var answer struct {
			Domain string `json:"domain"`
		}

		if err := json.Unmarshal(output, &answer); err != nil || answer.Domain == "" {
			return errors.New("the agent's answer has no domain")
		}

		return w.store.DeleteDomain(ctx, answer.Domain)
	}

	// A task with no panel-side effect is still a success: the agent did the
	// work and reported it.
	return nil
}

// reason reads the payload's "reason" so a suspended account says why.
func (w *Worker) reason(task store.Task) string {
	var params struct {
		Reason string `json:"reason"`
	}

	if task.Payload == "" || json.Unmarshal([]byte(task.Payload), &params) != nil {
		return ""
	}

	return params.Reason
}

// sweep fails tasks a dead worker left behind and clears expired sessions.
func (w *Worker) sweep(ctx context.Context) {
	if count, err := w.store.FailStaleTasks(ctx, store.Now(), w.cfg.TaskTimeout); err != nil {
		w.log.Error("could not sweep stale tasks", "error", err)
	} else if count > 0 {
		w.log.Warn("failed tasks left behind by an interrupted worker", "count", count)
	}

	if _, err := w.store.DeleteExpiredSessions(ctx, store.Now()); err != nil {
		w.log.Error("could not clear expired sessions", "error", err)
	}
}
