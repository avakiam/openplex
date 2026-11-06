package auth

import (
	"testing"
	"time"

	"github.com/pquerna/otp/totp"
)

func TestPasswordRoundTrip(t *testing.T) {
	h, err := HashPassword("correct horse 42")
	if err != nil {
		t.Fatal(err)
	}
	if !VerifyPassword(h, "correct horse 42") {
		t.Fatal("expected match")
	}
	if VerifyPassword(h, "wrong") || VerifyPassword("", "x") {
		t.Fatal("expected mismatch")
	}
}

func TestPolicy(t *testing.T) {
	for pw, ok := range map[string]bool{"short1": false, "lettersonlyhere": false, "abcdefghij1": true} {
		if (CheckPolicy(pw) == nil) != ok {
			t.Errorf("CheckPolicy(%q) ok=%v", pw, !ok)
		}
	}
	if CheckPolicy(RandomPassword(18)) != nil {
		t.Error("random password must satisfy policy")
	}
}

func TestJWT(t *testing.T) {
	s := NewSigner([]byte("0123456789abcdef0123456789abcdef"), time.Minute)
	tok, _, err := s.Issue(7, "admin")
	if err != nil {
		t.Fatal(err)
	}
	id, c, err := s.Verify(tok)
	if err != nil || id != 7 || c.Role != "admin" {
		t.Fatalf("verify: id=%d err=%v", id, err)
	}
	other := NewSigner([]byte("ffffffffffffffffffffffffffffffff"), time.Minute)
	if _, _, err := other.Verify(tok); err == nil {
		t.Fatal("token verified with wrong key")
	}
	expired := NewSigner([]byte("0123456789abcdef0123456789abcdef"), -time.Hour)
	tok, _, _ = expired.Issue(7, "admin")
	if _, _, err := s.Verify(tok); err == nil {
		t.Fatal("expired token accepted")
	}
}

func TestTOTP(t *testing.T) {
	k, err := NewTOTP("alice")
	if err != nil {
		t.Fatal(err)
	}
	code, _ := totp.GenerateCode(k.Secret(), time.Now().UTC())
	if !ValidateTOTP(k.Secret(), code) {
		t.Fatal("valid code rejected")
	}
	if ValidateTOTP(k.Secret(), "000000") && code != "000000" {
		t.Fatal("invalid code accepted")
	}
}

func TestSSHKey(t *testing.T) {
	const key = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIOMqqnkVzrm0SdG6UOoqKLsabgH5C9okWi0dh2l9GKJl user@host"
	n, fp, err := ParseSSHPublicKey(key)
	if err != nil {
		t.Fatal(err)
	}
	if n != key || fp == "" {
		t.Fatalf("normalized=%q fp=%q", n, fp)
	}
	if _, _, err := ParseSSHPublicKey("not a key"); err == nil {
		t.Fatal("garbage accepted")
	}
}

func TestAPIKey(t *testing.T) {
	k, p := NewAPIKey()
	if !IsAPIKey(k) || len(p) >= len(k) || k[:len(p)] != p {
		t.Fatalf("key=%q prefix=%q", k, p)
	}
}
