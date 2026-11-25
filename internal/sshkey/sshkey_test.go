package sshkey

import (
	"encoding/base64"
	"encoding/binary"
	"strings"
	"testing"
)

// wire builds one SSH wire-format string.
func wire(value []byte) []byte {
	out := make([]byte, 4, 4+len(value))
	binary.BigEndian.PutUint32(out, uint32(len(value)))

	return append(out, value...)
}

func ed25519Line(t *testing.T, body byte, comment string) string {
	t.Helper()

	blob := append(wire([]byte("ssh-ed25519")), wire(byteFill(body, 32))...)

	return "ssh-ed25519 " + base64.StdEncoding.EncodeToString(blob) + comment
}

func byteFill(value byte, count int) []byte {
	blob := make([]byte, count)
	for i := range blob {
		blob[i] = value
	}

	return blob
}

func TestParseAcceptsAnEd25519Key(t *testing.T) {
	key, err := Parse(ed25519Line(t, 7, " siti@laptop"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	if key.Type != "ssh-ed25519" || key.Comment != "siti@laptop" {
		t.Errorf("type = %q comment = %q", key.Type, key.Comment)
	}

	if !strings.HasPrefix(key.Fingerprint(), "SHA256:") || strings.HasSuffix(key.Fingerprint(), "=") {
		t.Errorf("fingerprint = %q, want SHA256: with no padding", key.Fingerprint())
	}

	if key.Line() != strings.TrimSpace(ed25519Line(t, 7, " siti@laptop")) {
		t.Errorf("Line() = %q, which is not the input line", key.Line())
	}
}

func TestParseRefusesTheMistakesPeopleMake(t *testing.T) {
	cases := map[string]string{
		"a private key":      "-----BEGIN OPENSSH PRIVATE KEY-----\nabc\n-----END OPENSSH PRIVATE KEY-----",
		"two lines":          ed25519Line(t, 1, "") + "\n" + ed25519Line(t, 2, ""),
		"options in front":   `command="rm -rf /" ` + strings.TrimSpace(ed25519Line(t, 3, "")),
		"not a key type":     "ssh-dss AAAAB3NzaC1kc3M=",
		"damaged base64":     "ssh-ed25519 not-base64!!",
		"blob ends early":    "ssh-ed25519 " + base64.StdEncoding.EncodeToString(wire([]byte("ssh-ed25519"))),
		"wrong inner type":   "ssh-ed25519 " + base64.StdEncoding.EncodeToString(append(wire([]byte("ssh-rsa")), wire(byteFill(1, 32))...)),
		"trailing extra bit": ed25519Line(t, 4, "") + "AA==",
	}

	for name, line := range cases {
		if _, err := Parse(line); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
}

func TestParseTreatsAPrivateKeyAsItsOwnError(t *testing.T) {
	_, err := Parse("-----BEGIN RSA PRIVATE KEY-----")
	if err == nil || !strings.Contains(err.Error(), "private key") {
		t.Fatalf("err = %v, want the private key message", err)
	}
}

func TestRSABits(t *testing.T) {
	// An SSH mpint keeps the modulus positive with a leading zero byte, which
	// is exactly what a real RSA public key carries.
	modulus := func(size int) []byte {
		value := byteFill(0x41, size)
		value[0] = 0x00
		value[1] = 0x80

		return value
	}

	key := func(size int) string {
		blob := append(wire([]byte("ssh-rsa")), wire([]byte{1, 0, 1})...)
		blob = append(blob, wire(modulus(size))...)

		return "ssh-rsa " + base64.StdEncoding.EncodeToString(blob)
	}

	if _, err := Parse(key(128)); err == nil {
		t.Error("a 1032-bit key was accepted")
	}

	found, err := Parse(key(257))
	if err != nil {
		t.Fatalf("a 2048-bit key was refused: %v", err)
	}

	if !strings.HasPrefix(found.Fingerprint(), "SHA256:") {
		t.Errorf("fingerprint = %q", found.Fingerprint())
	}
}

func TestCommentLengthIsCountedInCharacters(t *testing.T) {
	long := strings.Repeat("é", MaxComment+1)

	if _, err := Parse(ed25519Line(t, 9, " "+long)); err == nil {
		t.Error("an over-long comment was accepted")
	}

	if _, err := Parse(ed25519Line(t, 9, " "+strings.Repeat("é", MaxComment))); err != nil {
		t.Errorf("exactly MaxComment characters was refused: %v", err)
	}
}
