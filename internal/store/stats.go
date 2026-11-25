package store

import (
	"context"
	"fmt"
)

// Stats counts what the dashboard shows. Every figure is a live count from
// the database; nothing here is a placeholder or an estimate.
//
// A reseller sees the same page limited to the accounts it owns, so every
// subquery takes the same scope.
func (s *Store) Stats(ctx context.Context, owner *User) (Stats, error) {
	scope := ""
	args := []any{}

	if owner != nil && !owner.IsAdmin() {
		scope = ` AND u.owner_id = ?`
		// Every subquery below repeats the scope, so the owner id is bound
		// once per occurrence, in order.
		args = append(args, owner.ID, owner.ID, owner.ID, owner.ID, owner.ID, owner.ID, owner.ID, owner.ID)
	}

	query := fmt.Sprintf(`
		SELECT
			(SELECT COUNT(*) FROM users u WHERE u.role = 'user'%[1]s),
			(SELECT COUNT(*) FROM users u WHERE u.role = 'user' AND u.status = 'active'%[1]s),
			(SELECT COUNT(*) FROM users u WHERE u.role = 'user' AND u.status = 'suspended'%[1]s),
			(SELECT COUNT(*) FROM users u WHERE u.role = 'user' AND u.status = 'pending'%[1]s),
			(SELECT COUNT(*) FROM domains d JOIN users u ON u.id = d.user_id WHERE 1 = 1%[1]s),
			(SELECT COUNT(*) FROM users u WHERE u.role = 'reseller'%[1]s),
			(SELECT COUNT(*) FROM tasks t JOIN users u ON u.id = t.user_id WHERE t.status = 'failed'%[1]s),
			(SELECT COUNT(*) FROM tasks t JOIN users u ON u.id = t.user_id WHERE t.status IN ('queued', 'running')%[1]s)`,
		scope)

	var stats Stats

	err := s.db.QueryRowContext(ctx, query, args...).Scan(
		&stats.Accounts, &stats.ActiveAccounts, &stats.Suspended, &stats.Pending,
		&stats.Domains, &stats.Resellers, &stats.TasksFailed, &stats.TasksQueued,
	)

	return stats, wrap("count the dashboard figures", err)
}
