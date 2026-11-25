package httpapi

import (
	"context"
	"net/http"
	"testing"
	"time"

	"muriscore/panel/internal/auth"
)

// enrolTwoFactor starts and confirms the second factor for the signed-in
// administrator, and returns the secret and the recovery codes.
func enrolTwoFactor(t *testing.T, server string, httpClient *http.Client, csrf string) (string, []string) {
	t.Helper()

	start := call(t, httpClient, http.MethodPost, server+"/api/v1/me/two-factor", map[string]any{}, csrf)
	if start.Status != http.StatusOK {
		t.Fatalf("start two-factor answered %d: %v", start.Status, start.Body)
	}

	secret, _ := start.Body["secret"].(string)
	if secret == "" {
		t.Fatal("the enrolment did not return a secret")
	}

	rawCodes, _ := start.Body["recoveryCodes"].([]any)

	codes := make([]string, 0, len(rawCodes))

	for _, raw := range rawCodes {
		if code, ok := raw.(string); ok {
			codes = append(codes, code)
		}
	}

	if len(codes) == 0 {
		t.Fatal("the enrolment did not return recovery codes")
	}

	if url, _ := start.Body["otpauthUrl"].(string); url == "" {
		t.Fatal("the enrolment did not return an otpauth URL")
	}

	key, err := auth.DecodeTOTPSecret(secret)
	if err != nil {
		t.Fatalf("decode the secret: %v", err)
	}

	confirm := call(t, httpClient, http.MethodPost, server+"/api/v1/me/two-factor/confirm", map[string]any{
		"code": auth.TOTPCode(key, time.Now()),
	}, csrf)

	if confirm.Status != http.StatusOK {
		t.Fatalf("confirming answered %d: %v", confirm.Status, confirm.Body)
	}

	return secret, codes
}

// signInWith starts a new browser and signs in with a password and a code.
func signInWith(t *testing.T, server, password, code string) (*http.Client, envelope) {
	t.Helper()

	fresh := client(t)

	probe := call(t, fresh, http.MethodGet, server+"/api/v1/auth/session", nil, "")
	csrf, _ := probe.Body["csrfToken"].(string)

	answer := call(t, fresh, http.MethodPost, server+"/api/v1/auth/login", map[string]any{
		"login":    "admin",
		"password": password,
		"totp":     code,
	}, csrf)

	return fresh, answer
}

func TestTwoFactorEnrolmentProtectsSignIn(t *testing.T) {
	server, st, _ := harness(t)
	httpClient, csrf := signIn(t, server, st)

	secret, codes := enrolTwoFactor(t, server.URL, httpClient, csrf)

	// A password alone is no longer enough.
	_, first := signInWith(t, server.URL, "rahasia123", "")

	if first.Status != http.StatusUnauthorized {
		t.Fatalf("signing in without a code answered %d", first.Status)
	}

	if code, _ := first.Body["error"].(map[string]any)["code"].(string); code != "two_factor_required" {
		t.Fatalf("the refusal code was %q, not two_factor_required", code)
	}

	// A wrong code is refused and the refusal is the ordinary one.
	_, wrong := signInWith(t, server.URL, "rahasia123", "000000")

	if wrong.Status != http.StatusUnauthorized {
		t.Fatalf("signing in with a wrong code answered %d", wrong.Status)
	}

	key, err := auth.DecodeTOTPSecret(secret)
	if err != nil {
		t.Fatalf("decode the secret: %v", err)
	}

	_, good := signInWith(t, server.URL, "rahasia123", auth.TOTPCode(key, time.Now()))

	if good.Status != http.StatusOK {
		t.Fatalf("signing in with a current code answered %d: %v", good.Status, good.Body)
	}

	// A recovery code works once.
	_, recovered := signInWith(t, server.URL, "rahasia123", codes[0])

	if recovered.Status != http.StatusOK {
		t.Fatalf("signing in with a recovery code answered %d: %v", recovered.Status, recovered.Body)
	}

	_, reused := signInWith(t, server.URL, "rahasia123", codes[0])

	if reused.Status == http.StatusOK {
		t.Fatal("a recovery code was accepted twice")
	}

	// Turning the second factor off needs the password.
	refused := call(t, httpClient, http.MethodDelete, server.URL+"/api/v1/me/two-factor", map[string]any{
		"password": "not-the-password",
	}, csrf)

	if refused.Status != http.StatusUnprocessableEntity {
		t.Fatalf("turning two-factor off with a wrong password answered %d", refused.Status)
	}

	disabled := call(t, httpClient, http.MethodDelete, server.URL+"/api/v1/me/two-factor", map[string]any{
		"password": "rahasia123",
	}, csrf)

	if disabled.Status != http.StatusOK {
		t.Fatalf("turning two-factor off answered %d: %v", disabled.Status, disabled.Body)
	}

	_, plain := signInWith(t, server.URL, "rahasia123", "")

	if plain.Status != http.StatusOK {
		t.Fatalf("signing in after turning the factor off answered %d", plain.Status)
	}
}

func TestConfirmingWithoutStartingIsRefused(t *testing.T) {
	server, st, _ := harness(t)
	httpClient, csrf := signIn(t, server, st)

	answer := call(t, httpClient, http.MethodPost, server.URL+"/api/v1/me/two-factor/confirm", map[string]any{
		"code": "123456",
	}, csrf)

	if answer.Status != http.StatusUnprocessableEntity {
		t.Fatalf("confirming without a secret answered %d", answer.Status)
	}
}

func TestTwoFactorIsNotRequiredUntilConfirmed(t *testing.T) {
	server, st, _ := harness(t)
	httpClient, csrf := signIn(t, server, st)

	start := call(t, httpClient, http.MethodPost, server.URL+"/api/v1/me/two-factor", map[string]any{}, csrf)
	if start.Status != http.StatusOK {
		t.Fatalf("start two-factor answered %d", start.Status)
	}

	// The secret exists but is unconfirmed, so a password alone still works.
	// This is what keeps a half-finished enrolment from locking an account out.
	_, answer := signInWith(t, server.URL, "rahasia123", "")

	if answer.Status != http.StatusOK {
		t.Fatalf("signing in with an unconfirmed secret answered %d: %v", answer.Status, answer.Body)
	}

	user, err := st.UserByUsername(context.Background(), "admin")
	if err != nil {
		t.Fatalf("read the administrator: %v", err)
	}

	if user.HasTwoFactor() {
		t.Fatal("an unconfirmed secret was treated as an active second factor")
	}
}
