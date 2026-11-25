package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"crypto/subtle"
	"encoding/base32"
	"encoding/binary"
	"fmt"
	"math/big"
	"strconv"
	"strings"
	"time"
)

// TOTP parameters. These are the defaults every authenticator app assumes
// and the ones Laravel Fortify / Google Authenticator use: 6 digits, a
// 30-second step, SHA-1.
const (
	TOTPDigits = 6
	TOTPStep   = 30 * time.Second
	// TOTPSkew is how many steps before and after now are accepted, to
	// allow for a phone clock that is a few seconds off.
	TOTPSkew = 1
)

// DecodeTOTPSecret reads a base32 secret as authenticator apps write it
// (upper case, no padding, spaces allowed for readability).
func DecodeTOTPSecret(secret string) ([]byte, error) {
	cleaned := strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(secret), " ", ""))
	cleaned = strings.TrimRight(cleaned, "=")

	if cleaned == "" {
		return nil, fmt.Errorf("the two-factor secret is empty")
	}

	key, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(cleaned)
	if err != nil {
		return nil, fmt.Errorf("the two-factor secret is not valid base32: %w", err)
	}

	// RFC 4226 asks for at least 128 bits of shared secret. A shorter one is
	// either a misconfiguration or a value that is not really a secret, and
	// either way codes from it are guessable.
	if len(key) < 16 {
		return nil, fmt.Errorf("the two-factor secret is only %d bytes; it must be at least 16", len(key))
	}

	return key, nil
}

// NewTOTPSecret returns a fresh base32 secret: 20 random bytes, which is what
// Google Authenticator and Fortify both generate.
func NewTOTPSecret() (string, error) {
	raw := make([]byte, 20)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}

	return base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(raw), nil
}

// RecoveryCodeCount is how many single-use codes an enrolment hands out.
const RecoveryCodeCount = 8

// recoveryAlphabet leaves out characters that are easy to misread on paper.
const recoveryAlphabet = "abcdefghijkmnpqrstuvwxyz23456789"

// NewRecoveryCodes returns fresh single-use codes, grouped as xxxxx-xxxxx.
func NewRecoveryCodes() ([]string, error) {
	codes := make([]string, 0, RecoveryCodeCount)

	for range RecoveryCodeCount {
		body := make([]byte, 10)

		for i := range body {
			n, err := rand.Int(rand.Reader, big.NewInt(int64(len(recoveryAlphabet))))
			if err != nil {
				return nil, err
			}

			body[i] = recoveryAlphabet[n.Int64()]
		}

		codes = append(codes, string(body[:5])+"-"+string(body[5:]))
	}

	return codes, nil
}

// NormalizeRecoveryCode puts a typed code in the shape NewRecoveryCodes wrote
// it, so case and a missing dash do not matter.
func NormalizeRecoveryCode(code string) string {
	cleaned := strings.ToLower(strings.TrimSpace(code))
	cleaned = strings.NewReplacer("-", "", " ", "").Replace(cleaned)

	if len(cleaned) != 10 {
		return ""
	}

	return cleaned[:5] + "-" + cleaned[5:]
}

// ConsumeRecoveryCode returns the codes with the matching one removed. A code
// works once, so the caller stores the remainder.
func ConsumeRecoveryCode(codes []string, code string) ([]string, bool) {
	wanted := NormalizeRecoveryCode(code)
	if wanted == "" {
		return codes, false
	}

	remaining := make([]string, 0, len(codes))

	found := false

	for _, candidate := range codes {
		if !found && NormalizeRecoveryCode(candidate) == wanted {
			found = true

			continue
		}

		remaining = append(remaining, candidate)
	}

	return remaining, found
}

// TOTPCode returns the code for one point in time.
func TOTPCode(secret []byte, at time.Time) string {
	counter := uint64(at.Unix()) / uint64(TOTPStep.Seconds())

	var message [8]byte
	binary.BigEndian.PutUint64(message[:], counter)

	mac := hmac.New(sha1.New, secret)
	mac.Write(message[:])
	sum := mac.Sum(nil)

	offset := sum[len(sum)-1] & 0x0f

	value := (uint32(sum[offset]&0x7f) << 24) |
		(uint32(sum[offset+1]) << 16) |
		(uint32(sum[offset+2]) << 8) |
		uint32(sum[offset+3])

	return fmt.Sprintf("%0*d", TOTPDigits, value%1_000_000)
}

// VerifyTOTP reports whether code is valid for secret at time now, within
// TOTPSkew steps either way.
//
// The comparison is constant time: a code is only 6 digits, so a timing
// signal here would be worth more than a brute-force lockout.
func VerifyTOTP(secret []byte, code string, now time.Time) bool {
	code = strings.TrimSpace(code)
	if len(code) != TOTPDigits {
		return false
	}

	for _, candidate := range []string{code} {
		if _, err := strconv.Atoi(candidate); err != nil {
			return false
		}
	}

	for offset := -TOTPSkew; offset <= TOTPSkew; offset++ {
		expected := TOTPCode(secret, now.Add(time.Duration(offset)*TOTPStep))
		if subtle.ConstantTimeCompare([]byte(expected), []byte(code)) == 1 {
			return true
		}
	}

	return false
}
