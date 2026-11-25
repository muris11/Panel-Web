package store

import "context"

// RedisInstance is one account's cache instance. There is at most one per
// account, so a row is replaced rather than added next to another.
type RedisInstance struct {
	ID          int64  `json:"id"`
	UserID      int64  `json:"userId"`
	MaxMemoryMB int    `json:"maxMemoryMb"`
	Enabled     bool   `json:"enabled"`
	Username    string `json:"username"`
	Host        string `json:"host"`
	Port        int    `json:"port"`
}

// RedisInstanceFor returns an account's instance, or ErrNotFound.
func (s *Store) RedisInstanceFor(ctx context.Context, userID int64) (RedisInstance, error) {
	var (
		instance RedisInstance
		enabled  int
	)

	err := s.db.QueryRowContext(ctx, `
		SELECT r.id, r.user_id, r.max_memory_mb, r.enabled, u.username
		FROM redis_instances r
		JOIN users u ON u.id = r.user_id
		WHERE r.user_id = ?`, userID).
		Scan(&instance.ID, &instance.UserID, &instance.MaxMemoryMB, &enabled, &instance.Username)
	if err != nil {
		return RedisInstance{}, wrap("read the Redis instance", translate(err))
	}

	instance.Enabled = enabled == 1

	return instance, nil
}

// RedisInstanceByID loads one instance by row id.
func (s *Store) RedisInstanceByID(ctx context.Context, id int64) (RedisInstance, error) {
	var (
		instance RedisInstance
		enabled  int
	)

	err := s.db.QueryRowContext(ctx, `
		SELECT r.id, r.user_id, r.max_memory_mb, r.enabled, u.username
		FROM redis_instances r
		JOIN users u ON u.id = r.user_id
		WHERE r.id = ?`, id).
		Scan(&instance.ID, &instance.UserID, &instance.MaxMemoryMB, &enabled, &instance.Username)
	if err != nil {
		return RedisInstance{}, wrap("read the Redis instance", translate(err))
	}

	instance.Enabled = enabled == 1

	return instance, nil
}

// RedisPassword returns the stored password, decrypting it is the caller's job.
func (s *Store) RedisPassword(ctx context.Context, id int64) (string, error) {
	var password string

	if err := s.db.QueryRowContext(ctx, `SELECT password FROM redis_instances WHERE id = ?`, id).Scan(&password); err != nil {
		return "", wrap("read the Redis password", translate(err))
	}

	return password, nil
}

// SaveRedisInstance writes the instance for an account, replacing any earlier
// row. password is stored as given: encrypt it before calling when the panel has
// an APP_KEY.
func (s *Store) SaveRedisInstance(ctx context.Context, userID int64, password string, maxMemoryMB int) (RedisInstance, error) {
	now := Now()

	if _, err := s.db.ExecContext(ctx, `
		INSERT INTO redis_instances (user_id, password, max_memory_mb, enabled, created_at, updated_at)
		VALUES (?, ?, ?, 1, ?, ?)
		ON DUPLICATE KEY UPDATE password = VALUES(password), max_memory_mb = VALUES(max_memory_mb),
			enabled = 1, updated_at = VALUES(updated_at)`,
		userID, password, maxMemoryMB, now, now); err != nil {
		return RedisInstance{}, wrap("save the Redis instance", err)
	}

	return s.RedisInstanceFor(ctx, userID)
}

// SetRedisEnabled turns an instance on or off without forgetting its password.
func (s *Store) SetRedisEnabled(ctx context.Context, id int64, enabled bool) error {
	flag := 0
	if enabled {
		flag = 1
	}

	_, err := s.db.ExecContext(ctx,
		`UPDATE redis_instances SET enabled = ?, updated_at = ? WHERE id = ?`, flag, Now(), id)

	return wrap("update the Redis instance", err)
}

// DeleteRedisInstance forgets an account's instance.
func (s *Store) DeleteRedisInstance(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM redis_instances WHERE id = ?`, id)

	return wrap("delete the Redis instance", err)
}
