package secrets

import (
	"regexp"
	"testing"
)

func TestRoundTrip(t *testing.T) {
	b, err := New([]byte("0123456789abcdef0123456789abcdef"), "db")
	if err != nil {
		t.Fatal(err)
	}
	s1, _ := b.Seal("hunter2")
	s2, _ := b.Seal("hunter2")
	if s1 == s2 {
		t.Fatal("nonce reuse: identical ciphertexts")
	}
	if pt, err := b.Open(s1); err != nil || pt != "hunter2" {
		t.Fatalf("open: %q %v", pt, err)
	}
	other, _ := New([]byte("0123456789abcdef0123456789abcdef"), "other-purpose")
	if _, err := other.Open(s1); err == nil {
		t.Fatal("key separation broken")
	}
	if _, err := b.Open(s1[:len(s1)-2] + "AA"); err == nil {
		t.Fatal("tampered ciphertext accepted")
	}
}

func TestPassword(t *testing.T) {
	p := Password(24)
	if !regexp.MustCompile(`^[A-Za-z0-9]{24}$`).MatchString(p) || p == Password(24) {
		t.Fatalf("bad password %q", p)
	}
}
