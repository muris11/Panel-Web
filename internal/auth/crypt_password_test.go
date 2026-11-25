package auth

import "testing"

// The hashes below were produced by PHP's crypt(), which the Laravel panel
// used for FTP and mail passwords. They are the contract: a login created by
// the old panel must be able to sign in through this one.
func TestCryptPasswordMatchesPHP(t *testing.T) {
	cases := []struct{ password, hash string }{
		{"Hello world!", "$6$saltstring$svn8UoSVapNtMuq1ukKS4tPQd8iKwSMHWjl/O817G3uBnIFNjnQJuesI68u4OTLiBFdcbYEdFCoEOfaS35inz1"},
		{"correct horse battery staple", "$6$abcdefghijklmnop$UY4jc6.rVibJ9tqDqiG0GMdZRHkv1j4sPRRH2eUSo3Kszltzbk30CmYcWPNRTD/KsYFHF7WTtNkAxF3dZ3zPE."},
	}

	for _, c := range cases {
		if !VerifyCryptPassword(c.hash, c.password) {
			t.Errorf("could not verify %q against %q", c.password, c.hash)
		}

		if VerifyCryptPassword(c.hash, c.password+"x") {
			t.Errorf("a wrong password matched %q", c.hash)
		}
	}
}

func TestHashCryptPasswordRoundTrip(t *testing.T) {
	hash, err := HashCryptPassword("rahasia-ftps")
	if err != nil {
		t.Fatalf("hashing failed: %v", err)
	}

	if len(hash) != 3+16+1+86 || hash[:3] != "$6$" {
		t.Fatalf("unexpected hash shape: %q", hash)
	}

	if !VerifyCryptPassword(hash, "rahasia-ftps") {
		t.Error("a fresh hash did not verify")
	}

	if VerifyCryptPassword(hash, "something else") {
		t.Error("a wrong password verified")
	}
}
