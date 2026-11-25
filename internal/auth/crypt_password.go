package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"errors"
	"strings"

	sha512crypt "github.com/GehirnInc/crypt/sha512_crypt"
)

// FTP and mail check their passwords with crypt(3), not with bcrypt: vsftpd and
// Dovecot hand the stored hash to the C library and compare the result. The
// Laravel panel wrote "$6$" (SHA-512 crypt) hashes, so this panel writes the
// same scheme, and an FTP login made by either panel works.

const cryptAlphabet = "./0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"

// HashCryptPassword hashes a password as "$6$salt$digest".
func HashCryptPassword(password string) (string, error) {
	salt, err := randomSalt(16)
	if err != nil {
		return "", err
	}

	digest, err := sha512crypt.New().Generate([]byte(password), []byte("$6$"+salt+"$"))
	if err != nil {
		return "", err
	}

	return digest, nil
}

// VerifyCryptPassword reports whether password matches a crypt(3) hash. Only
// the "$6$" scheme is accepted, which is the only one the panel writes.
func VerifyCryptPassword(hash, password string) bool {
	if !strings.HasPrefix(hash, "$6$") {
		return false
	}

	digest, err := sha512crypt.New().Generate([]byte(password), []byte(hash))
	if err != nil {
		return false
	}

	return subtle.ConstantTimeCompare([]byte(digest), []byte(hash)) == 1
}

// randomSalt returns n characters from the crypt alphabet.
func randomSalt(n int) (string, error) {
	buf := make([]byte, n)

	if _, err := rand.Read(buf); err != nil {
		return "", err
	}

	salt := make([]byte, n)

	// 64 divides 256, so masking a random byte picks a character evenly.
	for i, b := range buf {
		salt[i] = cryptAlphabet[int(b)&0x3f]
	}

	return string(salt), nil
}

// ErrNotCryptHash is returned when a stored hash is not a crypt(3) hash.
var ErrNotCryptHash = errors.New("not a crypt(3) hash")
