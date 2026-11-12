package config

import (
	"bytes"
	"path/filepath"
	"sync"
	"testing"
)

// Processes starting together on a fresh server must agree on one secret.
func TestSecretCreatedOnce(t *testing.T) {
	path := filepath.Join(t.TempDir(), "secret.key")
	const n = 16
	got := make([][]byte, n)
	var wg sync.WaitGroup
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			b, err := loadOrCreateSecret(path)
			if err != nil {
				t.Error(err)
			}
			got[i] = b
		}()
	}
	wg.Wait()
	for i := 1; i < n; i++ {
		if !bytes.Equal(got[i], got[0]) {
			t.Fatalf("caller %d got a different secret", i)
		}
	}
	again, err := loadOrCreateSecret(path)
	if err != nil || !bytes.Equal(again, got[0]) {
		t.Fatalf("reload: %v", err)
	}
}

func TestAutoHostname(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want bool
	}{
		{"vps-1234.provider.example", true},
		{"localhost", false},
		{"server.local", false},
		{"box.localdomain", false},
		{"bad_name.example.com", false},
	} {
		if got := validAutoHost(tc.in); got != tc.want {
			t.Errorf("%s: got %v", tc.in, got)
		}
	}
}

func TestFQDNFromHosts(t *testing.T) {
	hosts := "127.0.0.1 localhost\n# 127.0.1.1 vps-1.wrong.example vps-1\n127.0.1.1 vps-1234.provider.example vps-1234\n::1 ip6-localhost\n"
	for _, tc := range []struct{ short, want string }{
		{"vps-1234", "vps-1234.provider.example"},
		{"vps-1", "vps-1"},
		{"other", "other"},
	} {
		if got := fqdnFromHosts(hosts, tc.short); got != tc.want {
			t.Errorf("%s: got %s, want %s", tc.short, got, tc.want)
		}
	}
}
