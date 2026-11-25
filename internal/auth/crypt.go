package auth

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// ErrUnreadableSecret means the stored value is not something this key can
// open. The caller must treat that as "no secret", never as "no two-factor".
var ErrUnreadableSecret = errors.New("the stored secret cannot be read with this APP_KEY")

// laravelPayload is the envelope Laravel's encrypter writes:
// base64(JSON{iv, value, mac}). The Laravel panel stored every account's
// two-factor secret with it, through the model's "encrypted" cast.
type laravelPayload struct {
	IV    string `json:"iv"`
	Value string `json:"value"`
	MAC   string `json:"mac"`
	Tag   string `json:"tag"`
}

// DecodeAppKey turns a Laravel APP_KEY ("base64:..." or 32 raw characters)
// into the 32 bytes AES-256 needs.
func DecodeAppKey(appKey string) ([]byte, error) {
	appKey = strings.TrimSpace(appKey)
	if appKey == "" {
		return nil, fmt.Errorf("APP_KEY is empty")
	}

	if encoded, ok := strings.CutPrefix(appKey, "base64:"); ok {
		key, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil {
			return nil, fmt.Errorf("APP_KEY is not valid base64: %w", err)
		}

		if len(key) != 32 {
			return nil, fmt.Errorf("APP_KEY must be 32 bytes, not %d", len(key))
		}

		return key, nil
	}

	if len(appKey) == 32 {
		return []byte(appKey), nil
	}

	return nil, fmt.Errorf("APP_KEY must be 32 bytes or \"base64:...\"")
}

// DecryptLaravel opens a value the Laravel panel encrypted with
// Crypt::encryptString, which is what its "encrypted" model cast uses for a
// string column.
func DecryptLaravel(appKey, payload string) (string, error) {
	key, err := DecodeAppKey(appKey)
	if err != nil {
		return "", err
	}

	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(payload))
	if err != nil {
		return "", ErrUnreadableSecret
	}

	var envelope laravelPayload
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return "", ErrUnreadableSecret
	}

	iv, err := base64.StdEncoding.DecodeString(envelope.IV)
	if err != nil || len(iv) != aes.BlockSize {
		return "", ErrUnreadableSecret
	}

	ciphertext, err := base64.StdEncoding.DecodeString(envelope.Value)
	if err != nil || len(ciphertext) == 0 || len(ciphertext)%aes.BlockSize != 0 {
		return "", ErrUnreadableSecret
	}

	// The MAC covers the base64 strings exactly as they were written, not
	// the bytes they decode to. Checking it before decrypting means a
	// tampered row never reaches the cipher.
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(envelope.IV + envelope.Value))

	expected := mac.Sum(nil)

	provided, err := hex.DecodeString(envelope.MAC)
	if err != nil || !hmac.Equal(expected, provided) {
		return "", ErrUnreadableSecret
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return "", ErrUnreadableSecret
	}

	plain := make([]byte, len(ciphertext))
	cipher.NewCBCDecrypter(block, iv).CryptBlocks(plain, ciphertext)

	plain, err = unpadPKCS7(plain, aes.BlockSize)
	if err != nil {
		return "", ErrUnreadableSecret
	}

	return string(plain), nil
}

// EncryptLaravel writes a value the way Crypt::encryptString does, so a value
// this panel stores is still readable by the Laravel panel. The MAC covers the
// base64 strings, exactly as DecryptLaravel checks it.
func EncryptLaravel(appKey, plain string) (string, error) {
	key, err := DecodeAppKey(appKey)
	if err != nil {
		return "", err
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return "", ErrUnreadableSecret
	}

	iv := make([]byte, aes.BlockSize)
	if _, err := rand.Read(iv); err != nil {
		return "", err
	}

	padded := padPKCS7([]byte(plain), aes.BlockSize)

	ciphertext := make([]byte, len(padded))
	cipher.NewCBCEncrypter(block, iv).CryptBlocks(ciphertext, padded)

	envelope := laravelPayload{
		IV:    base64.StdEncoding.EncodeToString(iv),
		Value: base64.StdEncoding.EncodeToString(ciphertext),
	}

	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(envelope.IV + envelope.Value))
	envelope.MAC = hex.EncodeToString(mac.Sum(nil))

	raw, err := json.Marshal(envelope)
	if err != nil {
		return "", err
	}

	return base64.StdEncoding.EncodeToString(raw), nil
}

func padPKCS7(data []byte, blockSize int) []byte {
	padding := blockSize - len(data)%blockSize
	return append(data, bytes.Repeat([]byte{byte(padding)}, padding)...)
}

func unpadPKCS7(data []byte, blockSize int) ([]byte, error) {
	if len(data) == 0 || len(data)%blockSize != 0 {
		return nil, ErrUnreadableSecret
	}

	padding := int(data[len(data)-1])
	if padding == 0 || padding > blockSize || padding > len(data) {
		return nil, ErrUnreadableSecret
	}

	for _, b := range data[len(data)-padding:] {
		if int(b) != padding {
			return nil, ErrUnreadableSecret
		}
	}

	return data[:len(data)-padding], nil
}
