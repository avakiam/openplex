package dbproxy

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net"
	"os"
	"slices"
	"strings"
	"time"
)

// Cert is the certificate remote database clients see.
type Cert struct {
	TLS         *tls.Config
	PEM         []byte // the certificate, for clients that verify it
	Fingerprint string // SHA-256 of the certificate, "AB:CD:…"
}

// LoadCert loads the proxy certificate from path (certificate and key, PEM),
// creating a self-signed one for names when there is none, when it expires
// within 30 days or when it lacks one of the host names.
func LoadCert(path string, names []string) (*Cert, error) {
	if raw, err := os.ReadFile(path); err == nil {
		if c, err := parseCert(raw, names); err == nil {
			return c, nil
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	raw, err := newCert(names)
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		return nil, fmt.Errorf("write database proxy certificate: %w", err)
	}
	return parseCert(raw, nil)
}

func parseCert(raw []byte, names []string) (*Cert, error) {
	pair, err := tls.X509KeyPair(raw, raw)
	if err != nil {
		return nil, err
	}
	leaf, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil {
		return nil, err
	}
	if time.Until(leaf.NotAfter) < 30*24*time.Hour {
		return nil, errors.New("certificate expires soon")
	}
	for _, n := range names {
		if net.ParseIP(n) == nil && !slices.Contains(leaf.DNSNames, n) {
			return nil, errors.New("certificate lacks a host name")
		}
	}
	sum := sha256.Sum256(leaf.Raw)
	h := strings.ToUpper(hex.EncodeToString(sum[:]))
	var fp []string
	for i := 0; i < len(h); i += 2 {
		fp = append(fp, h[i:i+2])
	}
	return &Cert{
		TLS:         &tls.Config{Certificates: []tls.Certificate{pair}, MinVersion: tls.VersionTLS12},
		PEM:         pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: leaf.Raw}),
		Fingerprint: strings.Join(fp, ":"),
	}, nil
}

func newCert(names []string) ([]byte, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, err
	}
	tpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: "OpenPlex databases"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().AddDate(10, 0, 0),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	for _, n := range names {
		if ip := net.ParseIP(n); ip != nil {
			tpl.IPAddresses = append(tpl.IPAddresses, ip)
		} else if n != "" {
			tpl.DNSNames = append(tpl.DNSNames, n)
		}
	}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &key.PublicKey, key)
	if err != nil {
		return nil, err
	}
	kb, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return nil, err
	}
	out := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	return append(out, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: kb})...), nil
}

// HostNames returns the names a certificate for this host should carry: the
// given names, the host name and the addresses of its interfaces (not
// loopback, link-local or container bridges).
func HostNames(extra ...string) []string {
	var out []string
	add := func(s string) {
		if s != "" && !slices.Contains(out, s) {
			out = append(out, s)
		}
	}
	for _, e := range extra {
		add(e)
	}
	if h, err := os.Hostname(); err == nil {
		add(h)
	}
	add("localhost")
	ifs, _ := net.Interfaces()
	for _, i := range ifs {
		if i.Flags&net.FlagLoopback != 0 || strings.HasPrefix(i.Name, "docker") || strings.HasPrefix(i.Name, "br-") || strings.HasPrefix(i.Name, "veth") {
			continue
		}
		addrs, _ := i.Addrs()
		for _, a := range addrs {
			if n, ok := a.(*net.IPNet); ok && !n.IP.IsLinkLocalUnicast() {
				add(n.IP.String())
			}
		}
	}
	return out
}
