package httpapi

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"

	"muriscore/panel/internal/store"
)

// Error codes the SPA switches on. They are stable strings, not numbers, so a
// translated message can change without breaking the client.
const (
	codeUnauthorized = "unauthorized"
	codeForbidden    = "forbidden"
	codeInvalid      = "invalid_request"
	codeNotFound     = "not_found"
	codeConflict     = "conflict"
	codeRateLimited  = "rate_limited"
	codeServerError  = "server_error"
)

// apiError is the one shape every failure takes.
type apiError struct {
	Error struct {
		Code    string            `json:"code"`
		Message string            `json:"message"`
		Fields  map[string]string `json:"fields,omitempty"`
	} `json:"error"`
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)

	if body == nil {
		return
	}

	if err := json.NewEncoder(w).Encode(body); err != nil {
		// The status line is already out, so the only thing left is to stop.
		return
	}
}

func fail(w http.ResponseWriter, status int, code, message string) {
	var body apiError
	body.Error.Code = code
	body.Error.Message = message

	writeJSON(w, status, body)
}

func failFields(w http.ResponseWriter, status int, code, message string, fields map[string]string) {
	var body apiError
	body.Error.Code = code
	body.Error.Message = message
	body.Error.Fields = fields

	writeJSON(w, status, body)
}

// failStore turns a store error into the right HTTP answer, and logs the ones
// that are the panel's own fault rather than the caller's.
func (s *Server) failStore(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, store.ErrNotFound) {
		fail(w, http.StatusNotFound, codeNotFound, "Not found.")

		return
	}

	s.log.Error("database error", "path", r.URL.Path, "error", err)
	fail(w, http.StatusInternalServerError, codeServerError, "The panel could not complete that. Try again.")
}

// decodeJSON reads a request body with a size limit and rejects unknown
// fields, so a typo in the SPA is a loud error instead of a silent no-op.
func decodeJSON(w http.ResponseWriter, r *http.Request, dest any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)

	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()

	if err := decoder.Decode(dest); err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			fail(w, http.StatusRequestEntityTooLarge, codeInvalid, "That request is too large.")

			return false
		}

		message := "That request is not valid JSON."
		if fieldErr, ok := err.(interface{ Field() string }); ok && fieldErr.Field() != "" {
			message = "Unknown field: " + fieldErr.Field()
		}

		fail(w, http.StatusBadRequest, codeInvalid, message)

		return false
	}

	return true
}

// clientIP is the address rate limiting and the audit log record. It trusts
// X-Forwarded-For only from the proxies the operator listed, because a
// stranger can send that header themselves.
func (s *Server) clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}

	if len(s.cfg.TrustedProxies) == 0 {
		return host
	}

	trusted := false
	for _, proxy := range s.cfg.TrustedProxies {
		if proxy == host {
			trusted = true

			break
		}
	}

	if !trusted {
		return host
	}

	forwarded := r.Header.Get("X-Forwarded-For")
	if forwarded == "" {
		return host
	}

	// The left-most entry is the client; the rest are proxies.
	first, _, _ := strings.Cut(forwarded, ",")

	if trimmed := strings.TrimSpace(first); trimmed != "" {
		return trimmed
	}

	return host
}

// userAgent is stored truncated: the column is for a human reading the
// session list, not for analytics.
func userAgent(r *http.Request) string {
	const limit = 250

	agent := r.UserAgent()
	if len(agent) > limit {
		return agent[:limit]
	}

	return agent
}

// withRecovery keeps one panicking handler from taking the panel down.
func withRecovery(log *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if recovered := recover(); recovered != nil {
				log.Error("handler panic", "path", r.URL.Path, "panic", recovered)

				fail(w, http.StatusInternalServerError, codeServerError, "The panel hit an unexpected error.")
			}
		}()

		next.ServeHTTP(w, r)
	})
}

// statusRecorder remembers the status for the request log.
type statusRecorder struct {
	http.ResponseWriter

	status int
	bytes  int
}

func (rec *statusRecorder) WriteHeader(status int) {
	rec.status = status
	rec.ResponseWriter.WriteHeader(status)
}

func (rec *statusRecorder) Write(body []byte) (int, error) {
	if rec.status == 0 {
		rec.status = http.StatusOK
	}

	written, err := rec.ResponseWriter.Write(body)
	rec.bytes += written

	return written, err
}

// withRequestLog logs one line per request: method, path, status, size and
// duration. Query strings are left out on purpose; nothing secret should
// reach a log file.
func withRequestLog(log *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()

		recorder := &statusRecorder{ResponseWriter: w}

		next.ServeHTTP(recorder, r)

		status := recorder.status
		if status == 0 {
			status = http.StatusOK
		}

		level := slog.LevelInfo
		if status >= 500 {
			level = slog.LevelError
		}

		log.Log(r.Context(), level, "request",
			"method", r.Method,
			"path", r.URL.Path,
			"status", status,
			"bytes", recorder.bytes,
			"duration", time.Since(start).Round(time.Millisecond).String(),
		)
	})
}

// withSecurityHeaders sets the headers a panel must never be without.
func withSecurityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		header := w.Header()

		header.Set("X-Content-Type-Options", "nosniff")
		header.Set("X-Frame-Options", "DENY")
		header.Set("Referrer-Policy", "no-referrer")
		header.Set("Cross-Origin-Opener-Policy", "same-origin")
		header.Set("Cross-Origin-Resource-Policy", "same-origin")
		header.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")

		// The SPA ships its own CSS and JavaScript from the same origin.
		// Inline styles are allowed because React sets progress widths that
		// way; no inline script is, which is what matters for XSS.
		header.Set("Content-Security-Policy",
			"default-src 'self'; "+
				"script-src 'self'; "+
				"style-src 'self' 'unsafe-inline'; "+
				"img-src 'self' data:; "+
				"font-src 'self'; "+
				"connect-src 'self'; "+
				"frame-ancestors 'none'; "+
				"base-uri 'none'; "+
				"form-action 'self'")

		next.ServeHTTP(w, r)
	})
}
