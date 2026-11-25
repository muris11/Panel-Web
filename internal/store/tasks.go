package store

import (
	"context"
	"database/sql"
	"time"
)

// CreateTask records one piece of agent work and returns its id.
//
// The row is written before the agent is called, so a panel or worker crash
// leaves a visible task in the list instead of work that silently never ran.
func (s *Store) CreateTask(ctx context.Context, id string, userID, actorID *int64, action, payload string) error {
	now := Now()

	_, err := s.db.ExecContext(ctx, `
		INSERT INTO tasks (id, user_id, actor_id, action, payload, status, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, 'queued', ?, ?)`,
		id, NullInt64(userID), NullInt64(actorID), action, payload, now, now)

	return wrap("record the task", err)
}

// ClaimNext takes the oldest queued task whose account is not already busy,
// and returns false when there is nothing to do.
//
// The claim is optimistic: a second worker that raced us updates zero rows
// and tries again, so a task is never handed out twice.
func (s *Store) ClaimNext(ctx context.Context, now time.Time) (Task, bool, error) {
	var (
		id     string
		userID sql.NullInt64
	)

	err := s.db.QueryRowContext(ctx, `
		SELECT t.id, t.user_id
		FROM tasks t
		WHERE t.status = 'queued'
			AND NOT EXISTS (
				SELECT 1 FROM tasks running
				WHERE running.user_id <=> t.user_id AND running.status = 'running'
			)
		ORDER BY t.created_at, t.id
		LIMIT 1`).Scan(&id, &userID)
	if err != nil {
		if translate(err) == ErrNotFound {
			return Task{}, false, nil
		}

		return Task{}, false, wrap("find the next task", err)
	}

	result, err := s.db.ExecContext(ctx, `
		UPDATE tasks SET status = 'running', started_at = ?, attempts = attempts + 1, updated_at = ?
		WHERE id = ? AND status = 'queued'`, now, now, id)
	if err != nil {
		return Task{}, false, wrap("claim the task", err)
	}

	claimed, err := result.RowsAffected()
	if err != nil {
		return Task{}, false, wrap("claim the task", err)
	}

	if claimed == 0 {
		return Task{}, false, nil
	}

	task, err := s.TaskByID(ctx, id)
	if err != nil {
		return Task{}, false, err
	}

	return task, true, nil
}

// TaskByID reads one task.
func (s *Store) TaskByID(ctx context.Context, id string) (Task, error) {
	var (
		task     Task
		userID   sql.NullInt64
		actorID  sql.NullInt64
		payload  sql.NullString
		output   sql.NullString
		errText  sql.NullString
		status   string
		created  sql.NullTime
		started  sql.NullTime
		finished sql.NullTime
		username sql.NullString
	)

	err := s.db.QueryRowContext(ctx, `
		SELECT t.id, t.user_id, t.actor_id, t.action, t.payload, t.status, t.attempts,
			t.output, t.error, t.created_at, t.started_at, t.finished_at,
			COALESCE(u.username, '')
		FROM tasks t
		LEFT JOIN users u ON u.id = t.user_id
		WHERE t.id = ?`, id).
		Scan(&task.ID, &userID, &actorID, &task.Action, &payload, &status, &task.Attempts,
			&output, &errText, &created, &started, &finished, &username)
	if err != nil {
		return Task{}, wrap("read the task", translate(err))
	}

	if userID.Valid {
		task.UserID = &userID.Int64
	}

	if actorID.Valid {
		task.ActorID = &actorID.Int64
	}

	task.Payload = payload.String
	task.Output = output.String
	task.Error = errText.String
	task.Status = TaskStatus(status)
	task.Username = username.String

	if created.Valid {
		task.CreatedAt = created.Time
	}

	if started.Valid {
		task.StartedAt = &started.Time
	}

	if finished.Valid {
		task.FinishedAt = &finished.Time
	}

	return task, nil
}

// FinishTask stores the agent's answer.
func (s *Store) FinishTask(ctx context.Context, id string, status TaskStatus, output string, errText string) error {
	now := Now()

	_, err := s.db.ExecContext(ctx, `
		UPDATE tasks SET status = ?, output = ?, error = NULLIF(?, ''), finished_at = ?, updated_at = ?
		WHERE id = ?`, string(status), nullJSON(output), errText, now, now, id)

	return wrap("finish the task", err)
}

// nullJSON keeps an empty answer out of a JSON column, which MySQL rejects.
func nullJSON(value string) any {
	if value == "" {
		return nil
	}

	return value
}

// FailStaleTasks fails tasks that a killed worker left running. The agent
// bounds a call itself, so a task still running long after that can only be
// one whose worker died; leaving it behind would block its account forever.
func (s *Store) FailStaleTasks(ctx context.Context, now time.Time, timeout time.Duration) (int64, error) {
	result, err := s.db.ExecContext(ctx, `
		UPDATE tasks
		SET status = 'failed',
			error = 'The task stopped unexpectedly before it finished.',
			finished_at = ?, updated_at = ?
		WHERE status = 'running' AND started_at < ?`,
		now, now, now.Add(-timeout))
	if err != nil {
		return 0, wrap("fail stale tasks", err)
	}

	return result.RowsAffected()
}

// TaskFilter narrows the task list.
type TaskFilter struct {
	// UserID limits the list to one account's tasks; nil means all of them.
	UserID *int64
	Status TaskStatus
	Limit  int
	Offset int
}

// Tasks lists tasks newest first.
func (s *Store) Tasks(ctx context.Context, filter TaskFilter) ([]Task, error) {
	query := `
		SELECT t.id, t.user_id, t.actor_id, t.action, t.payload, t.status, t.attempts,
			t.output, t.error, t.created_at, t.started_at, t.finished_at,
			COALESCE(u.username, '')
		FROM tasks t
		LEFT JOIN users u ON u.id = t.user_id
		WHERE 1 = 1`
	args := []any{}

	if filter.UserID != nil {
		query += ` AND t.user_id = ?`
		args = append(args, *filter.UserID)
	}

	if filter.Status != "" {
		query += ` AND t.status = ?`
		args = append(args, string(filter.Status))
	}

	query += ` ORDER BY t.created_at DESC, t.id DESC LIMIT ? OFFSET ?`
	args = append(args, filter.Limit, filter.Offset)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, wrap("list the tasks", err)
	}

	defer rows.Close()

	tasks := []Task{}

	for rows.Next() {
		var (
			task     Task
			userID   sql.NullInt64
			actorID  sql.NullInt64
			payload  sql.NullString
			output   sql.NullString
			errText  sql.NullString
			status   string
			created  sql.NullTime
			started  sql.NullTime
			finished sql.NullTime
			username sql.NullString
		)

		if err := rows.Scan(&task.ID, &userID, &actorID, &task.Action, &payload, &status, &task.Attempts,
			&output, &errText, &created, &started, &finished, &username); err != nil {
			return nil, wrap("list the tasks", err)
		}

		if userID.Valid {
			task.UserID = &userID.Int64
		}

		if actorID.Valid {
			task.ActorID = &actorID.Int64
		}

		task.Payload = payload.String
		task.Output = output.String
		task.Error = errText.String
		task.Status = TaskStatus(status)
		task.Username = username.String

		if created.Valid {
			task.CreatedAt = created.Time
		}

		if started.Valid {
			task.StartedAt = &started.Time
		}

		if finished.Valid {
			task.FinishedAt = &finished.Time
		}

		tasks = append(tasks, task)
	}

	return tasks, wrap("list the tasks", rows.Err())
}
