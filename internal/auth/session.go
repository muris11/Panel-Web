package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"sync"
	"time"
)

// SessionCookieName is the browser cookie that carries a session token.
const SessionCookieName = "rayxo_session"

// CSRFHeader is the header the SPA echoes the session's CSRF token in.
// A header, not a form field: the SPA sends JSON, and a cross-site page
// cannot set a custom header without a CORS preflight the panel refuses.
const CSRFHeader = "X-CSRF-Token"

// NewSessionToken returns a token for a cookie, and the SHA-256 stored in
// the database. Only the hash is kept, so a database leak cannot be turned
// into a signed-in browser.
func NewSessionToken() (token, hash string, err error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", "", fmt.Errorf("generate a session token: %w", err)
	}

	token = base64.RawURLEncoding.EncodeToString(raw)

	return token, HashToken(token), nil
}

// HashToken is the SHA-256 of a session token, as stored in the database.
func HashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// NewCSRFToken returns a token for the double-submit check.
func NewCSRFToken() (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("generate a CSRF token: %w", err)
	}

	return hex.EncodeToString(raw), nil
}

// Limiter is a small in-memory rate limiter: a fixed window per key.
//
// It is enough for the panel, which is a single process serving a handful of
// operators. Slow sign-ins are the one thing a stranger can try at volume,
// so that window is per username and per address.
type Limiter struct {
	mu      sync.Mutex
	window  time.Duration
	limit   int
	buckets map[string]*bucket
}

type bucket struct {
	count int
	reset time.Time
}

// NewLimiter allows limit attempts per window per key.
func NewLimiter(limit int, window time.Duration) *Limiter {
	return &Limiter{
		window:  window,
		limit:   limit,
		buckets: map[string]*bucket{},
	}
}

// Allow records one attempt and reports whether it may proceed. It also
// reports how long to wait when it may not.
func (l *Limiter) Allow(key string, now time.Time) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()

	current, ok := l.buckets[key]
	if !ok || now.After(current.reset) {
		l.buckets[key] = &bucket{count: 1, reset: now.Add(l.window)}

		return true, 0
	}

	current.count++

	if current.count > l.limit {
		return false, current.reset.Sub(now)
	}

	return true, 0
}

// Forget clears a key, so a successful sign-in wipes the failures before it.
func (l *Limiter) Forget(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()

	delete(l.buckets, key)
}

// Sweep drops expired buckets. Call it from time to time so a scan of many
// usernames cannot grow the map without bound.
func (l *Limiter) Sweep(now time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()

	for key, current := range l.buckets {
		if now.After(current.reset) {
			delete(l.buckets, key)
		}
	}
}
