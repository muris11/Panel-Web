package httpapi

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"time"

	"muriscore/panel/internal/auth"
	"muriscore/panel/internal/store"
)

// handleTwoFactor starts an enrolment or turns the second factor off.
func (s *Server) handleTwoFactor(w http.ResponseWriter, r *http.Request) {
	user, ok := s.caller(w, r)
	if !ok {
		return
	}

	switch r.Method {
	case http.MethodPost:
		s.startTwoFactor(w, r, user)

	case http.MethodDelete:
		var request struct {
			Password string `json:"password"`
		}

		if !decodeJSON(w, r, &request) {
			return
		}

		if err := auth.VerifyPassword(user.PasswordHash, request.Password); err != nil {
			failFields(w, http.StatusUnprocessableEntity, codeInvalid, "That password is not correct.", map[string]string{
				"password": "Check the password and try again.",
			})

			return
		}

		if err := s.store.ClearTwoFactor(r.Context(), user.ID); err != nil {
			s.failStore(w, r, err)

			return
		}

		s.audit(r, user, "two_factor.disabled", "user", user.Username, nil)

		writeJSON(w, http.StatusOK, map[string]any{"enabled": false})
	}
}

// startTwoFactor writes an unconfirmed secret and answers with the QR payload
// and the recovery codes. Signing in does not ask for a code until the account
// proves it can read one, so a browser tab closed mid-way cannot lock anyone
// out of their own panel.
func (s *Server) startTwoFactor(w http.ResponseWriter, r *http.Request, user store.User) {
	secret, err := auth.NewTOTPSecret()
	if err != nil {
		fail(w, http.StatusInternalServerError, codeServerError, "Could not create a secret.")

		return
	}

	codes, err := auth.NewRecoveryCodes()
	if err != nil {
		fail(w, http.StatusInternalServerError, codeServerError, "Could not create recovery codes.")

		return
	}

	encoded, err := s.encodeSecret(secret)
	if err != nil {
		fail(w, http.StatusInternalServerError, codeServerError, "Could not store the secret.")

		return
	}

	raw, err := json.Marshal(codes)
	if err != nil {
		fail(w, http.StatusInternalServerError, codeServerError, "Could not store the recovery codes.")

		return
	}

	stored := string(raw)
	if s.cfg.AppKey != "" {
		if encrypted, err := auth.EncryptLaravel(s.cfg.AppKey, stored); err == nil {
			stored = encrypted
		}
	}

	if err := s.store.SetTwoFactorSecret(r.Context(), user.ID, encoded, stored); err != nil {
		s.failStore(w, r, err)

		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"secret":        secret,
		"otpauthUrl":    otpauthURL(user.Username, secret),
		"recoveryCodes": codes,
	})
}

// handleTwoFactorConfirm checks one code and switches the factor on.
func (s *Server) handleTwoFactorConfirm(w http.ResponseWriter, r *http.Request) {
	user, ok := s.caller(w, r)
	if !ok {
		return
	}

	var request struct {
		Code string `json:"code"`
	}

	if !decodeJSON(w, r, &request) {
		return
	}

	secret, err := s.twoFactorSecret(user)
	if err != nil {
		fail(w, http.StatusUnprocessableEntity, codeInvalid, "Start the setup again: there is no secret to confirm.")

		return
	}

	if !auth.VerifyTOTP(secret, request.Code, time.Now()) {
		failFields(w, http.StatusUnprocessableEntity, codeInvalid, "That code is not valid.", map[string]string{
			"code": "Check the clock on your phone and try the newest code.",
		})

		return
	}

	if err := s.store.ConfirmTwoFactor(r.Context(), user.ID); err != nil {
		s.failStore(w, r, err)

		return
	}

	s.audit(r, user, "two_factor.enabled", "user", user.Username, nil)

	writeJSON(w, http.StatusOK, map[string]any{"enabled": true})
}

// encodeSecret stores a secret the way the Laravel panel does when an APP_KEY
// is configured, and as-is when there is none: a panel without a key has no
// way to encrypt anything, and refusing to enrol would be worse.
func (s *Server) encodeSecret(secret string) (string, error) {
	if s.cfg.AppKey == "" {
		return secret, nil
	}

	return auth.EncryptLaravel(s.cfg.AppKey, secret)
}

// decodeSecret is the reverse of encodeSecret: encrypted values written by
// either panel come back as plain text.
func (s *Server) decodeSecret(stored string) (string, error) {
	stored = strings.TrimSpace(stored)
	if stored == "" || s.cfg.AppKey == "" || !strings.HasPrefix(stored, "eyJ") {
		return stored, nil
	}

	return auth.DecryptLaravel(s.cfg.AppKey, stored)
}

// otpauthURL is the URI an authenticator app scans. It is rendered as text
// rather than a QR image: no dependency, and it can be pasted on a phone.
func otpauthURL(username, secret string) string {
	label := url.PathEscape("MurisCore:" + username)
	query := url.Values{
		"secret":    {secret},
		"issuer":    {"MurisCore"},
		"algorithm": {"SHA1"},
		"digits":    {"6"},
		"period":    {"30"},
	}

	return "otpauth://totp/" + label + "?" + query.Encode()
}

// recoveryCodes reads the stored codes, decrypting them when they were written
// with the panel's key.
func (s *Server) recoveryCodes(r *http.Request, user store.User) ([]string, error) {
	stored, err := s.store.RecoveryCodes(r.Context(), user.ID)
	if err != nil || strings.TrimSpace(stored) == "" {
		return nil, err
	}

	plain := stored

	if !strings.HasPrefix(strings.TrimSpace(stored), "[") && s.cfg.AppKey != "" {
		if opened, err := auth.DecryptLaravel(s.cfg.AppKey, stored); err == nil {
			plain = opened
		}
	}

	var codes []string
	if err := json.Unmarshal([]byte(plain), &codes); err != nil {
		return nil, err
	}

	return codes, nil
}
