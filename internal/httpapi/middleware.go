package httpapi

import (
	"context"
	"crypto/subtle"
	"net/http"
	"time"

	"muriscore/panel/internal/auth"
	"muriscore/panel/internal/store"
)

type ctxKey int

const (
	sessionKey ctxKey = iota
	userKey
)

// preSessionCookie carries the CSRF token for the sign-in form, before there
// is a session to attach one to.
const preSessionCookie = "muriscore_csrf"

// sessionFrom returns the signed-in browser's session.
func sessionFrom(ctx context.Context) (store.Session, bool) {
	session, ok := ctx.Value(sessionKey).(store.Session)

	return session, ok
}

// userFrom returns the signed-in account.
func userFrom(ctx context.Context) (store.User, bool) {
	user, ok := ctx.Value(userKey).(store.User)

	return user, ok
}

// sessionCookie writes the session cookie.
func (s *Server) sessionCookie(token string, expires time.Time) *http.Cookie {
	return &http.Cookie{
		Name:     auth.SessionCookieName,
		Value:    token,
		Path:     "/",
		Expires:  expires,
		HttpOnly: true,
		// Lax and not Strict: a link from an email or a bookmark should land
		// signed in. The CSRF token covers the state-changing requests that
		// Lax alone would not.
		SameSite: http.SameSiteLaxMode,
		Secure:   s.cfg.SecureCookies,
	}
}

func (s *Server) clearSessionCookie(w http.ResponseWriter) {
	cookie := s.sessionCookie("", time.Unix(0, 0))
	cookie.MaxAge = -1

	http.SetCookie(w, cookie)
}

func (s *Server) setPreSessionCSRF(w http.ResponseWriter, token string) {
	http.SetCookie(w, &http.Cookie{
		Name:     preSessionCookie,
		Value:    token,
		Path:     "/",
		MaxAge:   3600,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   s.cfg.SecureCookies,
	})
}

func (s *Server) clearPreSessionCSRF(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     preSessionCookie,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   s.cfg.SecureCookies,
	})
}

// preSessionCSRF checks the double-submit pair on the sign-in form: the token
// in the cookie must equal the one in the header the SPA sends.
//
// A cross-site page can make the browser send the cookie, but it cannot read
// it, so it cannot fill the header.
func preSessionCSRFOK(r *http.Request) bool {
	cookie, err := r.Cookie(preSessionCookie)
	if err != nil || cookie.Value == "" {
		return false
	}

	header := r.Header.Get(auth.CSRFHeader)

	return subtle.ConstantTimeCompare([]byte(cookie.Value), []byte(header)) == 1
}

// requireAuth loads the session and the account, and refuses the request when
// either is missing, expired, or the account may no longer use the panel.
func (s *Server) requireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.applySession(w, r, next)
	})
}

// applySession is requireAuth's body, kept separate so requireRole can call it
// once and then check the role.
func (s *Server) applySession(w http.ResponseWriter, r *http.Request, next http.Handler) {
	if s.authenticate(w, r) {
		next.ServeHTTP(w, r)
	}
}

// authenticate resolves the request's session into the context. It writes the
// failure itself and reports whether the handler may run.
func (s *Server) authenticate(w http.ResponseWriter, r *http.Request) bool {
	// A token is an alternative to a cookie: the CLI and integrations have no
	// cookie jar. Bearer requests skip the CSRF check because they are not
	// authenticated by an ambient credential a browser attaches by itself.
	if s.authenticateToken(w, r) {
		return false
	}

	if r.Header.Get("Authorization") != "" {
		// authenticateToken already answered.
		return false
	}

	cookie, err := r.Cookie(auth.SessionCookieName)
	if err != nil || cookie.Value == "" {
		fail(w, http.StatusUnauthorized, codeUnauthorized, "Sign in to continue.")

		return false
	}

	now := store.Now()

	session, err := s.store.SessionByToken(r.Context(), auth.HashToken(cookie.Value), now)
	if err != nil {
		// An unknown or expired token is not an error worth logging: it is
		// what a stale tab looks like.
		s.clearSessionCookie(w)
		fail(w, http.StatusUnauthorized, codeUnauthorized, "Your session has ended. Sign in again.")

		return false
	}

	user, err := s.store.UserByID(r.Context(), session.UserID)
	if err != nil {
		s.clearSessionCookie(w)
		fail(w, http.StatusUnauthorized, codeUnauthorized, "Your session has ended. Sign in again.")

		return false
	}

	// A suspended hosting account loses the panel along with its websites.
	// Administrators and resellers are never suspended: the column has no
	// meaning for them.
	if user.Role == store.RoleUser && user.Status != store.StatusActive {
		s.clearSessionCookie(w)

		message := "This account is not active."
		if user.Status == store.StatusSuspended && user.SuspendReason != "" {
			message = "This account is suspended: " + user.SuspendReason
		}

		fail(w, http.StatusForbidden, codeForbidden, message)

		return false
	}

	// State-changing requests must carry the session's CSRF token.
	switch r.Method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
	default:
		header := r.Header.Get(auth.CSRFHeader)
		if subtle.ConstantTimeCompare([]byte(session.CSRFToken), []byte(header)) != 1 {
			fail(w, http.StatusForbidden, codeForbidden, "That request is missing its CSRF token. Reload the page.")

			return false
		}
	}

	// Keep a session alive while it is being used, but write at most once a
	// minute: a page that loads six requests must not write six rows.
	if now.Sub(session.LastSeenAt) >= time.Minute {
		_ = s.store.TouchSession(r.Context(), session.ID, now, now.Add(s.cfg.SessionTTL))
	}

	ctx := context.WithValue(r.Context(), sessionKey, session)
	ctx = context.WithValue(ctx, userKey, user)

	*r = *r.WithContext(ctx)

	return true
}

// applyUser puts a resolved user into the request context, with no session.
// Token requests go through this, because they have no session row.
func (s *Server) applyUser(r *http.Request, user store.User) {
	ctx := context.WithValue(r.Context(), userKey, user)

	*r = *r.WithContext(ctx)
}

// requireRole wraps requireAuth with a level check.
func (s *Server) requireRole(roles ...store.Role) func(http.Handler) http.Handler {
	allowed := map[store.Role]bool{}
	for _, role := range roles {
		allowed[role] = true
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !s.authenticate(w, r) {
				return
			}

			user, ok := userFrom(r.Context())
			if !ok || !allowed[user.Role] {
				fail(w, http.StatusForbidden, codeForbidden, "This account may not do that.")

				return
			}

			next.ServeHTTP(w, r)
		})
	}
}
