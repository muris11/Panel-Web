// Package httpapi is the panel's HTTP layer: a JSON API under /api/v1 and
// the built React single-page app for everything else.
//
// It is deliberately plain net/http. The panel's routes are a few dozen, and
// the standard library's pattern matching (Go 1.22: "GET /api/v1/accounts/{id}")
// covers them without a router dependency.
package httpapi

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"muriscore/panel/internal/agent"
	"muriscore/panel/internal/auth"
	"muriscore/panel/internal/config"
	"muriscore/panel/internal/store"
	"muriscore/panel/internal/web"
)

// Server holds what every handler needs.
type Server struct {
	cfg   config.Config
	store *store.Store
	agent agent.Agent
	log   *slog.Logger

	// loginLimiter slows password guessing by address and by username; it is
	// two limiters because the first stops a scan, the second stops a focused
	// attack from many addresses.
	loginLimiter    *auth.Limiter
	usernameLimiter *auth.Limiter
}

// New builds the server. It does not start listening.
func New(cfg config.Config, st *store.Store, client agent.Agent, log *slog.Logger) *Server {
	return &Server{
		cfg:             cfg,
		store:           st,
		agent:           client,
		log:             log,
		loginLimiter:    auth.NewLimiter(20, 15*time.Minute), // 20 tries per address
		usernameLimiter: auth.NewLimiter(8, 15*time.Minute),  // 8 tries per username
	}
}

// Handler returns the panel's handler tree.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	// Public: what the sign-in page needs before it is signed in.
	mux.HandleFunc("GET /api/v1/auth/session", s.handleSessionProbe)
	mux.HandleFunc("POST /api/v1/auth/login", s.handleLogin)
	mux.HandleFunc("GET /api/v1/health", s.handleHealth)

	// Signed in.
	mux.Handle("POST /api/v1/auth/logout", s.requireAuth(http.HandlerFunc(s.handleLogout)))
	mux.Handle("GET /api/v1/me", s.requireAuth(http.HandlerFunc(s.handleMe)))
	mux.Handle("POST /api/v1/me/two-factor", s.requireAuth(http.HandlerFunc(s.handleTwoFactor)))
	mux.Handle("DELETE /api/v1/me/two-factor", s.requireAuth(http.HandlerFunc(s.handleTwoFactor)))
	mux.Handle("POST /api/v1/me/two-factor/confirm", s.requireAuth(http.HandlerFunc(s.handleTwoFactorConfirm)))
	mux.Handle("PATCH /api/v1/me", s.requireAuth(http.HandlerFunc(s.handleProfile)))
	mux.Handle("POST /api/v1/me/password", s.requireAuth(http.HandlerFunc(s.handlePassword)))
	mux.Handle("GET /api/v1/dashboard", s.requireAuth(http.HandlerFunc(s.handleDashboard)))
	mux.Handle("GET /api/v1/system", s.requireAuth(http.HandlerFunc(s.handleSystemInfo)))
	mux.Handle("GET /api/v1/sessions", s.requireAuth(http.HandlerFunc(s.handleSessions)))
	mux.Handle("DELETE /api/v1/sessions/{id}", s.requireAuth(http.HandlerFunc(s.handleRevokeSession)))
	mux.Handle("GET /api/v1/tasks", s.requireAuth(http.HandlerFunc(s.handleTasks)))
	mux.Handle("GET /api/v1/packages", s.requireAuth(http.HandlerFunc(s.handlePackages)))

	mux.Handle("GET /api/v1/usage", s.requireAuth(http.HandlerFunc(s.handleUsage)))
	mux.Handle("GET /api/v1/tokens", s.requireAuth(http.HandlerFunc(s.handleTokens)))
	mux.Handle("POST /api/v1/tokens", s.requireAuth(http.HandlerFunc(s.handleTokens)))
	mux.Handle("DELETE /api/v1/tokens/{id}", s.requireAuth(http.HandlerFunc(s.handleToken)))

	// Server-wide settings, administrators only.
	admin := s.requireRole(store.RoleAdmin)
	mux.Handle("GET /api/v1/server", admin(http.HandlerFunc(s.handleServer)))
	mux.Handle("POST /api/v1/server/services", admin(http.HandlerFunc(s.handleServiceAction)))
	mux.Handle("PATCH /api/v1/server", admin(http.HandlerFunc(s.handleServerSettings)))

	// Accounts: administrators and resellers only.
	accounts := s.requireRole(store.RoleAdmin, store.RoleReseller)
	mux.Handle("GET /api/v1/accounts", accounts(http.HandlerFunc(s.handleAccounts)))
	mux.Handle("POST /api/v1/accounts", accounts(http.HandlerFunc(s.handleCreateAccount)))
	mux.Handle("GET /api/v1/accounts/{username}", accounts(http.HandlerFunc(s.handleAccount)))
	mux.Handle("POST /api/v1/accounts/{username}/suspend", accounts(http.HandlerFunc(s.handleSuspendAccount)))
	mux.Handle("POST /api/v1/accounts/{username}/unsuspend", accounts(http.HandlerFunc(s.handleUnsuspendAccount)))
	mux.Handle("DELETE /api/v1/accounts/{username}", accounts(http.HandlerFunc(s.handleDeleteAccount)))

	// Domains, DNS and TLS.
	mux.Handle("GET /api/v1/domains", accounts(http.HandlerFunc(s.handleDomains)))
	mux.Handle("POST /api/v1/domains", accounts(http.HandlerFunc(s.handleDomains)))
	mux.Handle("GET /api/v1/domains/{id}", accounts(http.HandlerFunc(s.handleDomain)))
	mux.Handle("PATCH /api/v1/domains/{id}", accounts(http.HandlerFunc(s.handleDomain)))
	mux.Handle("DELETE /api/v1/domains/{id}", accounts(http.HandlerFunc(s.handleDomain)))
	mux.Handle("POST /api/v1/domains/{id}/ssl", accounts(http.HandlerFunc(s.handleDomainSSL)))
	mux.Handle("GET /api/v1/domains/{id}/dns", accounts(http.HandlerFunc(s.handleDNSRecords)))
	mux.Handle("POST /api/v1/domains/{id}/dns", accounts(http.HandlerFunc(s.handleDNSRecords)))
	mux.Handle("PATCH /api/v1/dns/{id}", accounts(http.HandlerFunc(s.handleDNSRecord)))
	mux.Handle("DELETE /api/v1/dns/{id}", accounts(http.HandlerFunc(s.handleDNSRecord)))

	// Mail.
	mux.Handle("GET /api/v1/mail", accounts(http.HandlerFunc(s.handleMail)))
	mux.Handle("POST /api/v1/mail", accounts(http.HandlerFunc(s.handleMail)))
	mux.Handle("GET /api/v1/mail/{id}", accounts(http.HandlerFunc(s.handleMailDomain)))
	mux.Handle("PATCH /api/v1/mail/{id}", accounts(http.HandlerFunc(s.handleMailDomain)))
	mux.Handle("DELETE /api/v1/mail/{id}", accounts(http.HandlerFunc(s.handleMailDomain)))
	mux.Handle("GET /api/v1/mail/{id}/mailboxes", accounts(http.HandlerFunc(s.handleMailboxes)))
	mux.Handle("POST /api/v1/mail/{id}/mailboxes", accounts(http.HandlerFunc(s.handleMailboxes)))
	mux.Handle("PATCH /api/v1/mailboxes/{id}", accounts(http.HandlerFunc(s.handleMailbox)))
	mux.Handle("DELETE /api/v1/mailboxes/{id}", accounts(http.HandlerFunc(s.handleMailbox)))
	mux.Handle("GET /api/v1/mail/{id}/forwarders", accounts(http.HandlerFunc(s.handleForwarders)))
	mux.Handle("POST /api/v1/mail/{id}/forwarders", accounts(http.HandlerFunc(s.handleForwarders)))
	mux.Handle("DELETE /api/v1/forwarders/{id}", accounts(http.HandlerFunc(s.handleForwarder)))

	// MySQL.
	mux.Handle("GET /api/v1/databases", accounts(http.HandlerFunc(s.handleDatabases)))
	mux.Handle("POST /api/v1/databases", accounts(http.HandlerFunc(s.handleDatabases)))
	mux.Handle("DELETE /api/v1/databases/{id}", accounts(http.HandlerFunc(s.handleDatabase)))
	mux.Handle("GET /api/v1/database-users", accounts(http.HandlerFunc(s.handleDatabaseUsers)))
	mux.Handle("POST /api/v1/database-users", accounts(http.HandlerFunc(s.handleDatabaseUsers)))
	mux.Handle("PATCH /api/v1/database-users/{id}", accounts(http.HandlerFunc(s.handleDatabaseUser)))
	mux.Handle("DELETE /api/v1/database-users/{id}", accounts(http.HandlerFunc(s.handleDatabaseUser)))

	// PostgreSQL databases and their logins.
	mux.Handle("GET /api/v1/postgres", accounts(http.HandlerFunc(s.handlePostgres)))
	mux.Handle("POST /api/v1/postgres", accounts(http.HandlerFunc(s.handlePostgres)))
	mux.Handle("DELETE /api/v1/postgres/{id}", accounts(http.HandlerFunc(s.handlePostgresDatabase)))
	mux.Handle("POST /api/v1/postgres-users", accounts(http.HandlerFunc(s.handlePostgresUsers)))
	mux.Handle("PATCH /api/v1/postgres-users/{id}", accounts(http.HandlerFunc(s.handlePostgresUser)))
	mux.Handle("DELETE /api/v1/postgres-users/{id}", accounts(http.HandlerFunc(s.handlePostgresUser)))

	// The account's own Redis cache.
	mux.Handle("GET /api/v1/redis", s.requireAuth(http.HandlerFunc(s.handleRedis)))
	mux.Handle("POST /api/v1/redis", accounts(http.HandlerFunc(s.handleRedis)))
	mux.Handle("POST /api/v1/redis/{id}", accounts(http.HandlerFunc(s.handleRedisAction)))
	mux.Handle("PATCH /api/v1/redis/{id}", accounts(http.HandlerFunc(s.handleRedisAction)))
	mux.Handle("DELETE /api/v1/redis/{id}", accounts(http.HandlerFunc(s.handleRedisAction)))

	// FTP logins.
	mux.Handle("GET /api/v1/ssh", accounts(http.HandlerFunc(s.handleSSH)))
	mux.Handle("PATCH /api/v1/ssh", accounts(http.HandlerFunc(s.handleSSH)))
	mux.Handle("POST /api/v1/ssh/keys", accounts(http.HandlerFunc(s.handleSSHKeys)))
	mux.Handle("DELETE /api/v1/ssh/keys/{id}", accounts(http.HandlerFunc(s.handleSSHKey)))

	// Tickets: every account may open one; an administrator or reseller sees the
	// inbox of the accounts it answers.
	mux.Handle("GET /api/v1/tickets", s.requireAuth(http.HandlerFunc(s.handleTickets)))
	mux.Handle("POST /api/v1/tickets", s.requireAuth(http.HandlerFunc(s.handleTickets)))
	mux.Handle("GET /api/v1/tickets/{id}", s.requireAuth(http.HandlerFunc(s.handleTicket)))
	mux.Handle("POST /api/v1/tickets/{id}/reply", s.requireAuth(http.HandlerFunc(s.handleTicket)))
	mux.Handle("POST /api/v1/tickets/{id}/status", s.requireAuth(http.HandlerFunc(s.handleTicketStatus)))

	// Announcements: received by everybody, sent by administrators and
	// resellers.
	mux.Handle("GET /api/v1/announcements", s.requireAuth(http.HandlerFunc(s.handleAnnouncements)))
	mux.Handle("POST /api/v1/announcements", s.requireAuth(http.HandlerFunc(s.handleAnnouncements)))

	mux.Handle("GET /api/v1/ftp", accounts(http.HandlerFunc(s.handleFTP)))
	mux.Handle("POST /api/v1/ftp", accounts(http.HandlerFunc(s.handleFTP)))
	mux.Handle("PATCH /api/v1/ftp/{id}", accounts(http.HandlerFunc(s.handleFTPAccount)))
	mux.Handle("DELETE /api/v1/ftp/{id}", accounts(http.HandlerFunc(s.handleFTPAccount)))

	// Cron jobs, files and backups belong to one account.
	mux.Handle("GET /api/v1/accounts/{username}/cron", accounts(http.HandlerFunc(s.handleCron)))
	mux.Handle("POST /api/v1/accounts/{username}/cron", accounts(http.HandlerFunc(s.handleCron)))
	mux.Handle("PATCH /api/v1/cron/{id}", accounts(http.HandlerFunc(s.handleCronJob)))
	mux.Handle("DELETE /api/v1/cron/{id}", accounts(http.HandlerFunc(s.handleCronJob)))
	mux.Handle("GET /api/v1/accounts/{username}/backups", accounts(http.HandlerFunc(s.handleBackups)))
	mux.Handle("POST /api/v1/accounts/{username}/backups", accounts(http.HandlerFunc(s.handleBackups)))
	mux.Handle("DELETE /api/v1/accounts/{username}/backups", accounts(http.HandlerFunc(s.handleBackups)))
	mux.Handle("POST /api/v1/accounts/{username}/backups/restore", accounts(http.HandlerFunc(s.handleBackupRestore)))

	// Files: the file manager needs an immediate answer, so it waits on the
	// agent rather than queueing, and it is open to account owners too.
	files := s.requireAuth
	mux.Handle("GET /api/v1/files", files(http.HandlerFunc(s.handleFiles)))
	mux.Handle("GET /api/v1/files/search", files(http.HandlerFunc(s.handleFileSearch)))
	mux.Handle("POST /api/v1/files/{action}", files(http.HandlerFunc(s.handleFileAction)))

	// The single-page app, for every other path.
	mux.Handle("/", web.Handler())

	// Outermost wrappers: recover, log, and the headers that apply to every
	// answer, including the ones the API never sees.
	return withRecovery(s.log,
		withRequestLog(s.log,
			withSecurityHeaders(mux),
		),
	)
}

// Run serves until ctx is cancelled.
func (s *Server) Run(ctx context.Context) error {
	server := &http.Server{
		Addr:              s.cfg.Listen,
		Handler:           s.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    1 << 20,
	}

	shutdownDone := make(chan struct{})

	go func() {
		defer close(shutdownDone)

		<-ctx.Done()

		stopCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()

		if err := server.Shutdown(stopCtx); err != nil {
			s.log.Error("could not stop the HTTP server cleanly", "error", err)
		}
	}()

	s.log.Info("panel listening", "address", s.cfg.Listen, "agent", s.agent.Name())

	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return err
	}

	<-shutdownDone

	return nil
}
