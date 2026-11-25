// Package sshkey checks an OpenSSH public key the way the agent checks it
// again (agent/internal/sshkey): one line, no options in front of it, of a type
// sshd accepts by default, and an RSA key of at least 2048 bits.
//
// Keeping the same rules here means the panel refuses a key the agent would
// refuse, instead of queueing a task that fails a second later.
package sshkey

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	// MaxLength is the longest key line accepted.
	MaxLength = 16384
	// MaxComment is the longest comment accepted, in characters.
	MaxComment = 200
	// MinRSABits is the smallest RSA key accepted.
	MinRSABits = 2048

	maxRSABits = 16384
)

// curves maps an ECDSA key type to the size of its public point.
var curves = map[string]int{"nistp256": 65, "nistp384": 97, "nistp521": 133}

// Key is a parsed public key.
type Key struct {
	Type    string
	Blob    []byte
	Comment string
}

// ErrPrivateKey is returned when the line holds a private key rather than a
// public one, because that mistake needs a different sentence from the others.
var ErrPrivateKey = errors.New("this is a private key; paste the public key instead, the .pub file")

// Parse checks one key line, "type base64 comment".
func Parse(line string) (Key, error) {
	line = strings.TrimSpace(line)

	if strings.Contains(line, "PRIVATE KEY") {
		return Key{}, ErrPrivateKey
	}

	if len(line) > MaxLength || strings.ContainsAny(line, "\r\n\x00") || !utf8.ValidString(line) {
		return Key{}, errors.New("paste one public key, on one line")
	}

	fields := strings.Fields(line)
	if len(fields) < 2 {
		return Key{}, errors.New("a public key is a type, a base64 body and an optional comment, all on one line")
	}

	kind := fields[0]
	if !supportedType(kind) {
		return Key{}, fmt.Errorf("%q is not a key type this server accepts (ssh-ed25519, ecdsa-sha2-*, ssh-rsa or a security key), or the line has options in front of the key", kind)
	}

	blob, err := base64.StdEncoding.DecodeString(fields[1])
	if err != nil || base64.StdEncoding.EncodeToString(blob) != fields[1] {
		return Key{}, errors.New("the key is damaged: copy the whole line of the .pub file")
	}

	if !validBlob(kind, blob) {
		return Key{}, errors.New("the key is damaged: copy the whole line of the .pub file")
	}

	if kind == "ssh-rsa" && rsaBits(blob) < MinRSABits {
		return Key{}, fmt.Errorf("RSA keys need at least %d bits; make an ed25519 key instead: ssh-keygen -t ed25519", MinRSABits)
	}

	comment := strings.Join(fields[2:], " ")

	if utf8.RuneCountInString(comment) > MaxComment {
		return Key{}, fmt.Errorf("the comment of the key may have at most %d characters", MaxComment)
	}

	for _, character := range comment {
		if unicode.IsControl(character) {
			return Key{}, errors.New("the comment of the key must be printable text")
		}
	}

	return Key{Type: kind, Blob: blob, Comment: comment}, nil
}

// Line renders the key as one line of authorized_keys.
func (k Key) Line() string {
	return strings.TrimRight(k.Type+" "+base64.StdEncoding.EncodeToString(k.Blob)+" "+k.Comment, " ")
}

// Fingerprint is what ssh-keygen -l and ssh show: SHA256 of the key, base64
// without padding.
func (k Key) Fingerprint() string {
	sum := sha256.Sum256(k.Blob)

	return "SHA256:" + strings.TrimRight(base64.StdEncoding.EncodeToString(sum[:]), "=")
}

func supportedType(kind string) bool {
	switch kind {
	case "ssh-ed25519", "sk-ssh-ed25519@openssh.com", "ssh-rsa", "sk-ecdsa-sha2-nistp256@openssh.com":
		return true
	}

	return kind == "ecdsa-sha2-nistp256" || kind == "ecdsa-sha2-nistp384" || kind == "ecdsa-sha2-nistp521"
}

// validBlob reports whether the decoded key holds what its type says, and
// nothing after it.
func validBlob(kind string, blob []byte) bool {
	offset := 0

	if readString(blob, &offset) != kind {
		return false
	}

	securityKey := strings.HasPrefix(kind, "sk-")
	base := strings.NewReplacer("sk-", "", "@openssh.com", "").Replace(kind)

	var ok bool

	switch {
	case base == "ssh-ed25519":
		ok = readsBytes(blob, &offset, 32)
	case strings.HasPrefix(base, "ecdsa-sha2-"):
		ok = validPoint(base, blob, &offset)
	default:
		ok = rsaBits(blob) > 0 && skipRSA(blob, &offset)
	}

	if ok && securityKey {
		application := readString(blob, &offset)
		ok = strings.HasPrefix(application, "ssh:")
	}

	return ok && offset == len(blob)
}

func validPoint(kind string, blob []byte, offset *int) bool {
	curve := strings.TrimPrefix(kind, "ecdsa-sha2-")
	size, known := curves[curve]

	if !known || readString(blob, offset) != curve {
		return false
	}

	point := readString(blob, offset)

	return len(point) == size && strings.HasPrefix(point, "\x04")
}

func skipRSA(blob []byte, offset *int) bool {
	exponent := readString(blob, offset)
	modulus := readString(blob, offset)

	return exponent != "" && modulus != ""
}

func readsBytes(blob []byte, offset *int, length int) bool {
	return len(readString(blob, offset)) == length
}

// rsaBits is the size of an RSA key's modulus in bits, or 0 when the key is
// damaged or larger than sshd allows.
func rsaBits(blob []byte) int {
	offset := 0

	_ = readString(blob, &offset)
	exponent := readString(blob, &offset)
	modulus := readString(blob, &offset)

	if exponent == "" || len(exponent) > 8 || modulus == "" || modulus[0] >= 0x80 {
		return 0
	}

	modulus = strings.TrimLeft(modulus, "\x00")
	if modulus == "" {
		return 0
	}

	bits := (len(modulus)-1)*8 + bitLength(modulus[0])
	if bits > maxRSABits {
		return 0
	}

	return bits
}

func bitLength(value byte) int {
	bits := 0
	for value > 0 {
		bits++
		value >>= 1
	}

	return bits
}

// readString reads one string of the SSH wire format: a four byte big-endian
// length and that many bytes. It returns "" and leaves the offset alone when
// the key ends first, which every caller treats as damage.
func readString(blob []byte, offset *int) string {
	if len(blob)-*offset < 4 {
		return ""
	}

	length := int(binary.BigEndian.Uint32(blob[*offset:]))

	if length < 0 || length > len(blob)-*offset-4 {
		return ""
	}

	value := string(blob[*offset+4 : *offset+4+length])
	*offset += 4 + length

	return value
}
