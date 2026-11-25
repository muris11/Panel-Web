package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"rayxo/panel/internal/auth"
	"rayxo/panel/internal/store"
)

// userPayload is what the SPA is told about the signed-in account. It never
// carries the password hash, the two-factor secret, or anything else the
// browser has no use for.
type userPayload struct {
	ID          int64      `json:"id"`
	Username    string     `json:"username"`
	Name        string     `json:"name"`
	Email       string     `json:"email"`
	Role        string     `json:"role"`
	Status      string     `json:"status"`
	TwoFactor   bool       `json:"twoFactor"`
	LastLoginAt *time.Time `json:"lastLoginAt"`
}

func payloadFor(user store.User) userPayload {
	return userPayload{
		ID:          user.ID,
		Username:    user.Username,
		Name:        user.Name,
		Email:       user.Email,
		Role:        string(user.Role),
		Status:      string(user.Status),
		TwoFactor:   user.HasTwoFactor(),
		LastLoginAt: user.LastLoginAt,
	}
}

// handleSessionProbe tells the SPA whether it is signed in, and hands out a
// CSRF token for the sign-in form when it is not.
func (s *Server) handleSessionProbe(w http.ResponseWriter, r *http.Request) {
	if cookie, err := r.Cookie(auth.SessionCookieName); err == nil && cookie.Value != "" {
		if session, err := s.store.SessionByToken(r.Context(), auth.HashToken(cookie.Value), store.Now()); err == nil {
			if user, err := s.store.UserByID(r.Context(), session.UserID); err == nil {
				writeJSON(w, http.StatusOK, map[string]any{
					"authenticated": true,
					"user":          payloadFor(user),
					"csrfToken":     session.CSRFToken,
				})

				return
			}
		}

		// A cookie that resolves to nothing is a stale tab; clear it so the
		// browser stops sending it.
		s.clearSessionCookie(w)
	}

	token, err := auth.NewCSRFToken()
	if err != nil {
		fail(w, http.StatusInternalServerError, codeServerError, "The panel could not prepare the sign-in form.")

		return
	}

	s.setPreSessionCSRF(w, token)

	writeJSON(w, http.StatusOK, map[string]any{
		"authenticated": false,
		"csrfToken":     token,
		"agent":         s.agent.Name(),
	})
}

type loginRequest struct {
	Login    string `json:"login"`
	Password string `json:"password"`
	TOTP     string `json:"totp"`
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	if !preSessionCSRFOK(r) {
		fail(w, http.StatusForbidden, codeForbidden, "Reload the page and try again.")

		return
	}

	var request loginRequest
	if !decodeJSON(w, r, &request) {
		return
	}

	ip := s.clientIP(r)
	login := strings.TrimSpace(request.Login)

	if login == "" || request.Password == "" {
		fail(w, http.StatusBadRequest, codeInvalid, "Enter your username and password.")

		return
	}

	now := store.Now()

	// Two windows: one per address (a scan of many usernames) and one per
	// username (an attack from many addresses).
	if ok, wait := s.loginLimiter.Allow(ip, now); !ok {
		s.tooManyAttempts(w, wait)

		return
	}

	if ok, wait := s.usernameLimiter.Allow(strings.ToLower(login), now); !ok {
		s.tooManyAttempts(w, wait)

		return
	}

	user, err := s.store.UserByLogin(r.Context(), login)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			// Spend the same time on a missing account as on a wrong
			// password, so the answer does not say which one it was.
			_ = auth.VerifyPassword("$2a$12$C6UzMDM.H6dfI/f/IKcEeO5YQ9Y9WjvXe4FBv0v0pY6aQ0Vh1yVbG", request.Password)

			fail(w, http.StatusUnauthorized, codeUnauthorized, "That username and password do not match.")

			return
		}

		s.failStore(w, r, err)

		return
	}

	if err := auth.VerifyPassword(user.PasswordHash, request.Password); err != nil {
		s.recordFailedLogin(r, user)
		fail(w, http.StatusUnauthorized, codeUnauthorized, "That username and password do not match.")

		return
	}

	// The password is right, but a suspended account still may not come in.
	if user.Role == store.RoleUser && user.Status != store.StatusActive {
		message := "This account is not active yet."

		if user.Status == store.StatusSuspended {
			message = "This account is suspended."
			if user.SuspendReason != "" {
				message = "This account is suspended: " + user.SuspendReason
			}
		}

		fail(w, http.StatusForbidden, codeForbidden, message)

		return
	}

	if user.HasTwoFactor() {
		code := strings.TrimSpace(request.TOTP)
		if code == "" {
			// 401 with its own code: the form shows the second field instead
			// of an error the account cannot fix.
			writeJSON(w, http.StatusUnauthorized, map[string]any{
				"error": map[string]any{
					"code":    "two_factor_required",
					"message": "Enter the six-digit code from your authenticator app.",
				},
			})

			return
		}

		if !s.twoFactorAccepts(r, user, code) {
			s.recordFailedLogin(r, user)
			fail(w, http.StatusUnauthorized, codeUnauthorized, "That code is not valid.")

			return
		}
	}

	if auth.NeedsRehash(user.PasswordHash) {
		if hash, err := auth.HashPassword(request.Password); err == nil {
			_ = s.store.UpdatePassword(r.Context(), user.ID, hash)
		}
	}

	s.startSession(w, r, user)
}

// twoFactorAccepts checks a TOTP code, then a recovery code. A used recovery
// code is removed, because it is single use.
func (s *Server) twoFactorAccepts(r *http.Request, user store.User, code string) bool {
	if secret, err := s.twoFactorSecret(user); err == nil && auth.VerifyTOTP(secret, code, time.Now()) {
		return true
	}

	codes, err := s.recoveryCodes(r, user)
	if err != nil || len(codes) == 0 {
		return false
	}

	remaining, ok := auth.ConsumeRecoveryCode(codes, code)
	if !ok {
		return false
	}

	raw, err := json.Marshal(remaining)
	if err != nil {
		return false
	}

	stored := string(raw)
	if s.cfg.AppKey != "" {
		if encrypted, err := auth.EncryptLaravel(s.cfg.AppKey, stored); err == nil {
			stored = encrypted
		}
	}

	_ = s.store.SetRecoveryCodes(r.Context(), user.ID, stored)

	return true
}

// twoFactorSecret returns the raw TOTP secret, decrypting it when the Laravel
// panel wrote it with the model's "encrypted" cast.
func (s *Server) twoFactorSecret(user store.User) ([]byte, error) {
	if secret, err := auth.DecodeTOTPSecret(user.TwoFactorSecret); err == nil {
		return secret, nil
	}

	if s.cfg.AppKey == "" {
		return nil, errors.New("the two-factor secret is encrypted and RAYXO_APP_KEY is not set")
	}

	plain, err := auth.DecryptLaravel(s.cfg.AppKey, user.TwoFactorSecret)
	if err != nil {
		return nil, err
	}

	return auth.DecodeTOTPSecret(plain)
}

// startSession creates the signed-in browser and answers with the account.
func (s *Server) startSession(w http.ResponseWriter, r *http.Request, user store.User) {
	token, hash, err := auth.NewSessionToken()
	if err != nil {
		fail(w, http.StatusInternalServerError, codeServerError, "The panel could not start a session.")

		return
	}

	csrf, err := auth.NewCSRFToken()
	if err != nil {
		fail(w, http.StatusInternalServerError, codeServerError, "The panel could not start a session.")

		return
	}

	now := store.Now()

	session := store.Session{
		ID:        newID(),
		UserID:    user.ID,
		TokenHash: hash,
		CSRFToken: csrf,
		IP:        s.clientIP(r),
		UserAgent: userAgent(r),
		CreatedAt: now,
		ExpiresAt: now.Add(s.cfg.SessionTTL),
	}

	if err := s.store.CreateSession(r.Context(), session); err != nil {
		s.failStore(w, r, err)

		return
	}

	http.SetCookie(w, s.sessionCookie(token, session.ExpiresAt))
	s.clearPreSessionCSRF(w)

	_ = s.store.TouchLogin(r.Context(), user.ID)

	actorID := user.ID
	_ = s.store.Audit(r.Context(), &actorID, "panel.signed_in", "user", strconv.FormatInt(user.ID, 10), s.clientIP(r), userAgent(r), nil)

	// Re-read so lastLoginAt in the answer is the one just written.
	if fresh, err := s.store.UserByID(r.Context(), user.ID); err == nil {
		user = fresh
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"authenticated": true,
		"user":          payloadFor(user),
		"csrfToken":     csrf,
	})
}

// recordFailedLogin leaves a trail for whoever investigates one account.
func (s *Server) recordFailedLogin(r *http.Request, user store.User) {
	actorID := user.ID

	_ = s.store.Audit(r.Context(), &actorID, "panel.sign_in_failed", "user",
		strconv.FormatInt(user.ID, 10), s.clientIP(r), userAgent(r), nil)
}

func (s *Server) tooManyAttempts(w http.ResponseWriter, wait time.Duration) {
	if wait < time.Minute {
		wait = time.Minute
	}

	writeJSON(w, http.StatusTooManyRequests, map[string]any{
		"error": map[string]any{
			"code":    codeRateLimited,
			"message": "Too many sign-in attempts. Try again in " + wait.Round(time.Minute).String() + ".",
		},
	})
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	session, ok := sessionFrom(r.Context())
	if ok {
		_ = s.store.DeleteSession(r.Context(), session.TokenHash)

		if user, ok := userFrom(r.Context()); ok {
			actorID := user.ID
			_ = s.store.Audit(r.Context(), &actorID, "panel.signed_out", "user",
				strconv.FormatInt(user.ID, 10), s.clientIP(r), userAgent(r), nil)
		}
	}

	s.clearSessionCookie(w)

	writeJSON(w, http.StatusOK, map[string]any{"signedOut": true})
}

func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	user, ok := userFrom(r.Context())
	if !ok {
		fail(w, http.StatusUnauthorized, codeUnauthorized, "Sign in to continue.")

		return
	}

	session, _ := sessionFrom(r.Context())

	writeJSON(w, http.StatusOK, map[string]any{
		"user":      payloadFor(user),
		"csrfToken": session.CSRFToken,
		"agent":     s.agent.Name(),
	})
}

func (s *Server) handleSessions(w http.ResponseWriter, r *http.Request) {
	user, ok := userFrom(r.Context())
	if !ok {
		fail(w, http.StatusUnauthorized, codeUnauthorized, "Sign in to continue.")

		return
	}

	session, _ := sessionFrom(r.Context())

	sessions, err := s.store.Sessions(r.Context(), user.ID, session.ID, store.Now())
	if err != nil {
		s.failStore(w, r, err)

		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"sessions": sessions})
}

func (s *Server) handleRevokeSession(w http.ResponseWriter, r *http.Request) {
	user, ok := userFrom(r.Context())
	if !ok {
		fail(w, http.StatusUnauthorized, codeUnauthorized, "Sign in to continue.")

		return
	}

	current, _ := sessionFrom(r.Context())
	id := r.PathValue("id")

	if id == "" {
		fail(w, http.StatusBadRequest, codeInvalid, "Say which session to end.")

		return
	}

	if id == current.ID {
		fail(w, http.StatusBadRequest, codeInvalid, "Use Sign out to end this session.")

		return
	}

	if err := s.store.DeleteSessionByID(r.Context(), user.ID, id); err != nil {
		s.failStore(w, r, err)

		return
	}

	actorID := user.ID
	_ = s.store.Audit(r.Context(), &actorID, "panel.session_revoked", "session", id, s.clientIP(r), userAgent(r), nil)

	writeJSON(w, http.StatusOK, map[string]any{"ended": id})
}
