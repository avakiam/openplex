package ssl

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// writeCert stores a self-signed certificate for host the way Caddy does.
func writeCert(t *testing.T, dir, issuerDir, host string, notBefore, notAfter time.Time) {
	t.Helper()
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tpl := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()),
		Subject:      pkix.Name{CommonName: host},
		DNSNames:     []string{host},
		NotBefore:    notBefore, NotAfter: notAfter,
	}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	d := filepath.Join(dir, issuerDir, host)
	if err := os.MkdirAll(d, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(d, host+".crt"), pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestCertificateStates(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	day := 24 * time.Hour
	writeCert(t, dir, "acme-v02.api.letsencrypt.org-directory", "ok.example", now.Add(-10*day), now.Add(80*day))
	writeCert(t, dir, "acme.zerossl.com-v2-dv90", "ok.example", now.Add(-80*day), now.Add(10*day)) // older one from another issuer
	writeCert(t, dir, "acme-v02.api.letsencrypt.org-directory", "late.example", now.Add(-85*day), now.Add(5*day))
	writeCert(t, dir, "acme-v02.api.letsencrypt.org-directory", "gone.example", now.Add(-100*day), now.Add(-10*day))
	certs := Certificates(dir)

	for _, tc := range []struct {
		host  string
		since time.Time
		want  string
	}{
		{"ok.example", now.Add(-day), "issued"},
		{"late.example", now.Add(-day), "expiring"},
		{"gone.example", now.Add(-day), "missing"},
		{"new.example", now.Add(-time.Minute), "pending"},
		{"broken.example", now.Add(-IssueGrace - time.Minute), "missing"},
	} {
		st := StateOf(certs, tc.host, tc.since, now)
		if st.State != tc.want {
			t.Errorf("%s: state %q, want %q", tc.host, st.State, tc.want)
		}
		if (st.State == "issued" || st.State == "expiring") && (st.ExpiresAt == nil || st.Issuer == "") {
			t.Errorf("%s: %+v", tc.host, st)
		}
	}
	if st := StateOf(certs, "ok.example", now, now); st.ExpiresAt.Before(now.Add(70 * day)) {
		t.Errorf("the newest certificate was not chosen: %v", st.ExpiresAt)
	}
}
