// Package auth holds the panel's primitives: password hashes, two-factor
// codes, sessions, and the decryptor for secrets the Laravel panel wrote.
//
// Each piece is deliberately small and tested on its own, because this is
// the code a bug in would expose every host on the server.
package auth

import (
	"errors"
	"fmt"

	"golang.org/x/crypto/bcrypt"
)

// MinPasswordLength mirrors the Laravel panel's rule, so an account created
// in either panel can sign in to both.
const MinPasswordLength = 8

// ErrPasswordMismatch is returned for a wrong password, and for a hash that
// is not bcrypt at all. The two are not distinguished: the caller must not
// tell a stranger which one it was.
var ErrPasswordMismatch = errors.New("password does not match")

// DefaultCost is bcrypt's default and Laravel's, so a hash made here is
// accepted there and the other way round.
const DefaultCost = 12

// HashPassword returns a bcrypt hash in the "$2a$" format; Laravel writes
// "$2y$", which Go's bcrypt reads just as well.
func HashPassword(password string) (string, error) {
	if len(password) < MinPasswordLength {
		return "", fmt.Errorf("password must be at least %d characters", MinPasswordLength)
	}

	// bcrypt reads at most 72 bytes and ignores the rest; refusing is better
	// than silently accepting a different password than the one typed.
	if len(password) > 72 {
		return "", fmt.Errorf("password must be at most 72 bytes")
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(password), DefaultCost)
	if err != nil {
		return "", fmt.Errorf("hash the password: %w", err)
	}

	return string(hash), nil
}

// VerifyPassword reports whether password matches hash.
func VerifyPassword(hash, password string) error {
	if hash == "" {
		return ErrPasswordMismatch
	}

	if err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)); err != nil {
		return ErrPasswordMismatch
	}

	return nil
}

// NeedsRehash reports whether hash was made with another cost than the
// current one, so a successful sign-in can quietly upgrade it.
func NeedsRehash(hash string) bool {
	cost, err := bcrypt.Cost([]byte(hash))
	if err != nil {
		return true
	}

	return cost != DefaultCost
}
