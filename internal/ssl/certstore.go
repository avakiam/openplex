package ssl

import (
	"crypto/x509"
	"encoding/pem"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// IssueGrace is how long a new name may go without a certificate before it
// counts as failing: Caddy usually gets one within a minute, and retries
// with growing pauses after a failure.
const IssueGrace = 30 * time.Minute

// Certificates reads Caddy's certificate storage (dir is
// <caddy data>/caddy/certificates) and returns the newest certificate of each
// host name: a renewed name may keep an older file from another issuer.
func Certificates(dir string) map[string]*x509.Certificate {
	newest := map[string]*x509.Certificate{}
	_ = filepath.WalkDir(dir, func(p string, e fs.DirEntry, err error) error {
		if err != nil || e.IsDir() || !strings.HasSuffix(p, ".crt") {
			return nil
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return nil
		}
		blk, _ := pem.Decode(b)
		if blk == nil {
			return nil
		}
		c, err := x509.ParseCertificate(blk.Bytes)
		if err != nil {
			return nil
		}
		host := strings.TrimSuffix(filepath.Base(p), ".crt")
		if old := newest[host]; old == nil || c.NotAfter.After(old.NotAfter) {
			newest[host] = c
		}
		return nil
	})
	return newest
}

// CertState says how a name's certificate stands.
type CertState struct {
	Host      string     `json:"host"`
	State     string     `json:"state"` // issued | expiring | pending | missing
	Issuer    string     `json:"issuer,omitempty"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
}

// StateOf describes host's certificate at now; since is when the name was
// added (a name without one counts as missing after IssueGrace).
func StateOf(certs map[string]*x509.Certificate, host string, since, now time.Time) CertState {
	c := certs[host]
	if c == nil || !now.Before(c.NotAfter) {
		st := CertState{Host: host, State: "pending"}
		if c != nil || now.Sub(since) >= IssueGrace {
			st.State = "missing"
		}
		return st
	}
	exp := c.NotAfter.UTC()
	issuer := c.Issuer.CommonName
	if len(c.Issuer.Organization) > 0 {
		issuer = c.Issuer.Organization[0]
	}
	st := CertState{Host: host, State: "issued", Issuer: issuer, ExpiresAt: &exp}
	if c.NotAfter.Sub(now) < c.NotAfter.Sub(c.NotBefore)/6 { // Caddy renews with a third left
		st.State = "expiring"
	}
	return st
}
