// Package store is the panel's data access: plain SQL, explicit columns, and
// no hidden query builder. Every method takes a context so a slow query
// dies with the request that asked for it.
package store

import (
	"database/sql"
	"errors"
	"fmt"
	"time"

	mysql "github.com/go-sql-driver/mysql"
)

// ErrNotFound is returned when a row the caller named does not exist.
var ErrNotFound = errors.New("not found")

// Role is a panel account's level.
type Role string

const (
	RoleAdmin    Role = "admin"
	RoleReseller Role = "reseller"
	RoleUser     Role = "user"
)

// ValidRole reports whether r is one of the three levels.
func ValidRole(r Role) bool {
	return r == RoleAdmin || r == RoleReseller || r == RoleUser
}

// AccountStatus is where a hosting account is in its life.
type AccountStatus string

const (
	StatusPending   AccountStatus = "pending"
	StatusActive    AccountStatus = "active"
	StatusSuspended AccountStatus = "suspended"
	StatusDeleting  AccountStatus = "deleting"
	StatusFailed    AccountStatus = "failed"
)

// TaskStatus is where a piece of agent work is.
type TaskStatus string

// The values match App\Enums\TaskStatus in the Laravel panel, so rows written
// by either panel are read the same way.
const (
	TaskQueued    TaskStatus = "queued"
	TaskRunning   TaskStatus = "running"
	TaskSuccess   TaskStatus = "success"
	TaskFailed    TaskStatus = "failed"
	TaskCancelled TaskStatus = "cancelled"
)

// Store queries the panel database.
type Store struct {
	db *sql.DB
}

// New wraps a pool. The caller keeps ownership of the pool and closes it.
func New(pool *sql.DB) *Store {
	return &Store{db: pool}
}

// Pool exposes the underlying pool for health checks.
func (s *Store) Pool() *sql.DB { return s.db }

// User is one row of the accounts table: an administrator, a reseller, or a
// hosting account (which is also a Linux user on the server).
type User struct {
	ID       int64
	Username string
	Name     string
	Email    string
	Role     Role
	Status   AccountStatus

	// PasswordHash is a bcrypt hash; the plain password is never stored.
	PasswordHash string

	// TwoFactorSecret is already decrypted, or empty when the account has no
	// second factor.
	TwoFactorSecret      string
	TwoFactorConfirmedAt *time.Time

	OwnerID   *int64
	PackageID *int64
	UID       *int32
	GID       *int32

	SuspendReason string
	LastLoginAt   *time.Time
	CreatedAt     time.Time
}

// IsAdmin reports whether the account manages the whole server.
func (u User) IsAdmin() bool { return u.Role == RoleAdmin }

// IsReseller reports whether the account may own other accounts.
func (u User) IsReseller() bool { return u.Role == RoleAdmin || u.Role == RoleReseller }

// IsPanelRole reports whether the role is one that lives only in the panel.
func (r Role) IsPanelRole() bool { return r == RoleAdmin || r == RoleReseller }

// HasTwoFactor reports whether a confirmed second factor is set.
func (u User) HasTwoFactor() bool {
	return u.TwoFactorSecret != "" && u.TwoFactorConfirmedAt != nil
}

// Package is a hosting plan's limits. A nil limit means unlimited.
type Package struct {
	ID          int64
	OwnerID     *int64
	Name        string
	Description string

	DiskMB         *int64
	BandwidthMB    *int64
	Domains        *int64
	Databases      *int64
	EmailAccounts  *int64
	FTPAccounts    *int64
	CronJobs       *int64
	Subdomains     *int64
	EmailForwarder *int64
	Inodes         *int64
	Pointers       *int64

	SSL bool
	CGI bool
	SSH bool

	// Accounts is how many hosting accounts sit on the package; it is filled
	// by the listing, not by the row itself.
	Accounts int64
}

// Account is a hosting account as the accounts page shows it: the user row
// plus what it is allowed and what it already has.
type Account struct {
	User
	Package     *Package
	DomainCount int
	OwnerName   string
}

// Domain is one website of an account.
type Domain struct {
	ID           int64     `json:"id"`
	UserID       int64     `json:"userId"`
	Name         string    `json:"name"`
	Type         string    `json:"type"`
	PHPVersion   string    `json:"phpVersion"`
	IsPrimary    bool      `json:"isPrimary"`
	DocumentRoot string    `json:"documentRoot"`
	Status       string    `json:"status"`
	SSLStatus    string    `json:"sslStatus"`
	CreatedAt    time.Time `json:"createdAt"`
}

// Task is one agent call, queued or done.
type Task struct {
	ID         string
	UserID     *int64
	ActorID    *int64
	Action     string
	Payload    string
	Status     TaskStatus
	Output     string
	Error      string
	Attempts   int
	CreatedAt  time.Time
	StartedAt  *time.Time
	FinishedAt *time.Time
	Username   string
}

// AuditEntry is one line of the audit log.
type AuditEntry struct {
	ID        int64
	ActorID   *int64
	Action    string
	SubjectID string
	IP        string
	CreatedAt time.Time
	ActorName string
}

// Stats is what the dashboard shows. Every number comes from the database,
// never from a placeholder.
type Stats struct {
	Accounts       int
	ActiveAccounts int
	Suspended      int
	Pending        int
	Domains        int
	Resellers      int
	TasksFailed    int
	TasksQueued    int
}

// userColumns is the one list every user query selects, in scanUser's order.
const userColumns = `u.id, u.username, u.name, u.email, u.password, u.role, u.status,
	u.owner_id, u.package_id, u.uid, u.gid, u.suspend_reason,
	u.two_factor_secret, u.two_factor_confirmed_at, u.last_login_at, u.created_at`

// translate turns a driver error into the errors callers switch on.
// IsDuplicate reports whether the database refused a row because one of its
// unique keys already exists. A handler turns that into 409, not 500: the
// request was understood and nothing broke.
func IsDuplicate(err error) bool {
	var mysqlErr *mysql.MySQLError

	return errors.As(err, &mysqlErr) && mysqlErr.Number == 1062
}

func translate(err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}

	return err
}

// wrap adds the query's name to an error, so a log line says where it broke.
func wrap(what string, err error) error {
	if err == nil {
		return nil
	}

	if errors.Is(err, ErrNotFound) {
		return ErrNotFound
	}

	return fmt.Errorf("%s: %w", what, err)
}

// NullInt64 makes a pointer for a SQL parameter.
func NullInt64(value *int64) any {
	if value == nil {
		return nil
	}

	return *value
}

// Now returns the panel's clock in UTC, the timezone every row is written in.
func Now() time.Time {
	return time.Now().UTC().Truncate(time.Second)
}

// DomainsRow is one line of the panel-wide website list: the domain plus the
// account that owns it.
type DomainsRow struct {
	ID         int64     `json:"id"`
	Name       string    `json:"name"`
	Type       string    `json:"type"`
	PHPVersion string    `json:"phpVersion"`
	Status     string    `json:"status"`
	SSLStatus  string    `json:"sslStatus"`
	ForceHTTPS bool      `json:"forceHttps"`
	Username   string    `json:"username"`
	CreatedAt  time.Time `json:"createdAt"`
}

// DB exposes the pool. Handlers use typed methods; a test or a one-off repair
// query needs the pool itself.
func (s *Store) DB() *sql.DB { return s.db }
