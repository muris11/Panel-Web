package auth

import (
	"bytes"
	"encoding/base32"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
)

// The panel replaces a Laravel one, so the hashes in the database were
// written by PHP's password_hash(). If these two do not agree, every existing
// account is locked out.
func TestVerifyPasswordAcceptsLaravelHashes(t *testing.T) {
	// PHP's password_hash() writes $2y$ rather than Go's $2a$. The two differ
	// only in the marker, but a panel that refuses to read $2y$ would lock out
	// every account created by the Laravel panel it replaces.
	hash, err := HashPassword("correct horse battery")
	if err != nil {
		t.Fatalf("hash: %v", err)
	}

	phpHash := "$2y$" + strings.TrimPrefix(hash, "$2a$")

	if !strings.HasPrefix(phpHash, "$2y$") {
		t.Fatalf("the test did not build a $2y$ hash: %s", phpHash)
	}

	if err := VerifyPassword(phpHash, "correct horse battery"); err != nil {
		t.Fatalf("a $2y$ hash was rejected: %v", err)
	}

	if err := VerifyPassword(phpHash, "correct horse batteru"); err == nil {
		t.Fatal("a $2y$ hash accepted the wrong password")
	}
}

func TestPasswordRoundTrip(t *testing.T) {
	hash, err := HashPassword("rahasia123")
	if err != nil {
		t.Fatalf("hash: %v", err)
	}

	if err := VerifyPassword(hash, "rahasia123"); err != nil {
		t.Fatalf("the hash did not verify its own password: %v", err)
	}

	if err := VerifyPassword(hash, "rahasia124"); err == nil {
		t.Fatal("a wrong password verified")
	}

	if NeedsRehash(hash) {
		t.Fatal("a cost-12 hash asked to be rehashed")
	}

	if !NeedsRehash("$2y$10$abcdefghijklmnopqrstuv") {
		t.Fatal("a cost-10 hash did not ask to be rehashed")
	}
}

func TestPasswordLengthLimits(t *testing.T) {
	if _, err := HashPassword("short"); err == nil {
		t.Fatal("a 5-character password was accepted")
	}

	// bcrypt silently truncates past 72 bytes, so anything longer must be
	// refused rather than quietly weakened.
	if _, err := HashPassword(strings.Repeat("a", 73)); err == nil {
		t.Fatal("a 73-byte password was accepted")
	}
}

// The RFC 6238 test vectors, which is what every authenticator app
// implements: 8 digits here, taken as the last 6 to match the 6-digit form.
func TestVerifyTOTPAgainstRFC6238(t *testing.T) {
	secret := base32.StdEncoding.WithPadding(base32.NoPadding).
		EncodeToString([]byte("12345678901234567890"))

	raw, err := DecodeTOTPSecret(secret)
	if err != nil {
		t.Fatalf("decode secret: %v", err)
	}

	cases := []struct {
		unix int64
		code string
	}{
		{59, "287082"},
		{1111111109, "081804"},
		{1111111111, "050471"},
		{1234567890, "005924"},
	}

	for _, testCase := range cases {
		at := time.Unix(testCase.unix, 0)

		if got := TOTPCode(raw, at); got != testCase.code {
			t.Errorf("code at %d = %s, want %s", testCase.unix, got, testCase.code)
		}

		if !VerifyTOTP(raw, testCase.code, at) {
			t.Errorf("VerifyTOTP rejected the code for %d", testCase.unix)
		}
	}

	// One step either side is accepted (clock skew), two is not.
	at := time.Unix(59, 0)

	if !VerifyTOTP(raw, TOTPCode(raw, at), at.Add(30*time.Second)) {
		t.Error("a code one step old was rejected")
	}

	if !VerifyTOTP(raw, TOTPCode(raw, at), at.Add(-30*time.Second)) {
		t.Error("a code one step early was rejected")
	}

	if VerifyTOTP(raw, TOTPCode(raw, at), at.Add(90*time.Second)) {
		t.Error("a code three steps old was accepted")
	}
}

func TestDecodeTOTPSecretRejectsGarbage(t *testing.T) {
	for _, value := range []string{"", "not base32!", "AA"} {
		if _, err := DecodeTOTPSecret(value); err == nil {
			t.Errorf("DecodeTOTPSecret(%q) accepted a value it should not", value)
		}
	}
}

func TestSessionTokensAreRandomAndHashed(t *testing.T) {
	token, hash, err := NewSessionToken()
	if err != nil {
		t.Fatalf("token: %v", err)
	}

	if len(token) < 40 {
		t.Fatalf("session token is only %d characters", len(token))
	}

	if hash == token {
		t.Fatal("the stored hash is the token itself")
	}

	if HashToken(token) != hash {
		t.Fatal("HashToken did not reproduce the stored hash")
	}

	other, otherHash, err := NewSessionToken()
	if err != nil {
		t.Fatalf("token: %v", err)
	}

	if token == other || hash == otherHash {
		t.Fatal("two sessions were given the same token")
	}
}

func TestLimiterBlocksThenFrees(t *testing.T) {
	limiter := NewLimiter(3, time.Minute)
	now := time.Unix(1_000_000, 0)

	for attempt := 0; attempt < 3; attempt++ {
		if ok, _ := limiter.Allow("10.0.0.1", now); !ok {
			t.Fatalf("attempt %d was refused too early", attempt+1)
		}
	}

	ok, wait := limiter.Allow("10.0.0.1", now)
	if ok {
		t.Fatal("a fourth attempt inside the window was allowed")
	}

	if wait <= 0 {
		t.Fatal("a refusal did not say how long to wait")
	}

	// A different key is unaffected.
	if ok, _ := limiter.Allow("10.0.0.2", now); !ok {
		t.Fatal("one address's failures locked out another address")
	}

	// After the window, the key is usable again.
	if ok, _ := limiter.Allow("10.0.0.1", now.Add(2*time.Minute)); !ok {
		t.Fatal("the limit did not expire")
	}
}

// DecryptLaravel has to read what Laravel's encrypter wrote, using the same
// AES-256-CBC plus HMAC layout. This test builds that payload the way
// Illuminate\Encryption\Encrypter does.
func TestDecryptLaravelRoundTrip(t *testing.T) {
	key := []byte(strings.Repeat("k", 32))
	appKey := "base64:" + base64.StdEncoding.EncodeToString(key)

	decoded, err := DecodeAppKey(appKey)
	if err != nil {
		t.Fatalf("decode key: %v", err)
	}

	if len(decoded) != 32 {
		t.Fatalf("decoded key is %d bytes", len(decoded))
	}

	payload := laravelEncrypt(t, key, "JBSWY3DPEHPK3PXP")

	plain, err := DecryptLaravel(appKey, payload)
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}

	if plain != "JBSWY3DPEHPK3PXP" {
		t.Fatalf("decrypted %q", plain)
	}

	// A tampered payload must fail the MAC check, not decrypt to nonsense.
	tampered := payload[:len(payload)-2] + "AA"

	if _, err := DecryptLaravel(appKey, tampered); err == nil {
		t.Fatal("a tampered payload was accepted")
	}
}

// laravelEncrypt writes a payload the way Laravel does: base64 of
// {iv, value, mac} with value encrypted AES-256-CBC and mac an HMAC-SHA256
// over iv||value.
func laravelEncrypt(t *testing.T, key []byte, plain string) string {
	t.Helper()

	padded := pkcs7([]byte(plain), aes.BlockSize)

	block, err := aes.NewCipher(key)
	if err != nil {
		t.Fatalf("cipher: %v", err)
	}

	iv := make([]byte, aes.BlockSize)
	if _, err := rand.Read(iv); err != nil {
		t.Fatalf("iv: %v", err)
	}

	encrypted := make([]byte, len(padded))
	cipher.NewCBCEncrypter(block, iv).CryptBlocks(encrypted, padded)

	ivB64 := base64.StdEncoding.EncodeToString(iv)
	valueB64 := base64.StdEncoding.EncodeToString(encrypted)

	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(ivB64 + valueB64))

	// Laravel hex-encodes the MAC over the base64 iv and value.
	envelope := map[string]string{
		"iv":    ivB64,
		"value": valueB64,
		"mac":   hexString(mac.Sum(nil)),
	}

	encoded, err := json.Marshal(envelope)
	if err != nil {
		t.Fatalf("encode envelope: %v", err)
	}

	return base64.StdEncoding.EncodeToString(encoded)
}

func pkcs7(data []byte, blockSize int) []byte {
	padding := blockSize - len(data)%blockSize
	return append(data, bytesRepeat(byte(padding), padding)...)
}

func bytesRepeat(value byte, count int) []byte {
	out := make([]byte, count)
	for i := range out {
		out[i] = value
	}

	return out
}

func hexString(data []byte) string {
	const digits = "0123456789abcdef"

	out := make([]byte, 0, len(data)*2)
	for _, b := range data {
		out = append(out, digits[b>>4], digits[b&0x0f])
	}

	return string(out)
}

func TestEncryptionRoundTripsThroughTheLaravelFormat(t *testing.T) {
	key := "base64:" + base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{7}, 32))

	for _, plain := range []string{"JBSWY3DPEHPK3PXP", `["aaaaa-bbbbb","ccccc-ddddd"]`, ""} {
		payload, err := EncryptLaravel(key, plain)
		if err != nil {
			t.Fatalf("encrypt %q: %v", plain, err)
		}

		opened, err := DecryptLaravel(key, payload)
		if err != nil {
			t.Fatalf("decrypt %q: %v", plain, err)
		}

		if opened != plain {
			t.Fatalf("round trip gave %q, wanted %q", opened, plain)
		}
	}
}

func TestEncryptionRefusesATamperedPayload(t *testing.T) {
	key := "base64:" + base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{9}, 32))

	payload, err := EncryptLaravel(key, "JBSWY3DPEHPK3PXP")
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}

	tampered := payload[:len(payload)-4] + "AAAA"
	if _, err := DecryptLaravel(key, tampered); err == nil {
		t.Fatal("a tampered payload was accepted")
	}

	other := "base64:" + base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{8}, 32))
	if _, err := DecryptLaravel(other, payload); err == nil {
		t.Fatal("a payload was opened with the wrong key")
	}
}

func TestRecoveryCodesAreSingleUseAndForgiving(t *testing.T) {
	codes, err := NewRecoveryCodes()
	if err != nil {
		t.Fatalf("codes: %v", err)
	}

	if len(codes) != RecoveryCodeCount {
		t.Fatalf("got %d codes, wanted %d", len(codes), RecoveryCodeCount)
	}

	// Upper case and no dash still match, because that is how people type them.
	typed := strings.ToUpper(strings.ReplaceAll(codes[0], "-", ""))

	remaining, ok := ConsumeRecoveryCode(codes, typed)
	if !ok {
		t.Fatalf("the code %q was not accepted", typed)
	}

	if len(remaining) != len(codes)-1 {
		t.Fatalf("a used code was not removed: %d codes left", len(remaining))
	}

	if _, again := ConsumeRecoveryCode(remaining, codes[0]); again {
		t.Fatal("a recovery code was accepted twice")
	}

	if _, ok := ConsumeRecoveryCode(codes, "short"); ok {
		t.Fatal("a malformed code was accepted")
	}
}

func TestNewSecretsAreUsable(t *testing.T) {
	secret, err := NewTOTPSecret()
	if err != nil {
		t.Fatalf("secret: %v", err)
	}

	key, err := DecodeTOTPSecret(secret)
	if err != nil {
		t.Fatalf("decode the new secret: %v", err)
	}

	if len(key) != 20 {
		t.Fatalf("the secret has %d bytes, wanted 20", len(key))
	}

	if code := TOTPCode(key, time.Now()); !VerifyTOTP(key, code, time.Now()) {
		t.Fatal("a code from a fresh secret did not verify")
	}
}
