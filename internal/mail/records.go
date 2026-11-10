// Package mail runs e-mail for the panel's domains with docker-mailserver
// (Postfix, Dovecot, Rspamd). Domains, mailboxes, aliases and DKIM keys live
// in the panel database; the manager writes docker-mailserver's account,
// alias, quota and DKIM files from it (the container picks changes up by
// itself) and adds MX/SPF/DKIM/DMARC records to the domains' DNS zones.
package mail

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"regexp"
	"strconv"
	"strings"
	"unicode"

	"openplex.dev/openplex/internal/db"
)

// Limits.
const (
	MaxMailboxesPerDomain = 200
	MaxAliasesPerDomain   = 200
	MaxDestinations       = 50
	MinPasswordLen        = 10
	MaxPasswordLen        = 128
	DefaultQuotaMB        = 1024
	MaxQuotaMB            = 102400
	DKIMSelector          = "opx"
)

var (
	localRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9._+-]{0,62}[a-z0-9])?$`)
	emailRe = regexp.MustCompile(`^[a-z0-9._%+-]{1,64}@([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z]{2,63}$`)
)

// NormalizeLocal validates the part before the @.
func NormalizeLocal(s string) (string, error) {
	s = strings.ToLower(strings.TrimSpace(s))
	if !localRe.MatchString(s) || strings.Contains(s, "..") {
		return "", errors.New("the name before the @ may contain letters, digits, dots, hyphens, underscores and +")
	}
	return s, nil
}

// NormalizeAddress validates a full e-mail address.
func NormalizeAddress(s string) (string, error) {
	s = strings.ToLower(strings.TrimSpace(s))
	if len(s) > 254 || !emailRe.MatchString(s) || strings.Contains(s, "..") {
		return "", errors.New("enter valid e-mail addresses, such as name@example.com")
	}
	return s, nil
}

// CheckPassword enforces the mailbox password policy.
func CheckPassword(p string) error {
	if len(p) < MinPasswordLen || len(p) > MaxPasswordLen {
		return errors.New("the password must have between 10 and 128 characters")
	}
	if strings.IndexFunc(p, unicode.IsControl) >= 0 {
		return errors.New("the password contains invalid characters")
	}
	return nil
}

// NewDKIMKey generates a 2048-bit RSA key in PEM (PKCS#1, as Rspamd writes).
func NewDKIMKey() (string, error) {
	k, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return "", err
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(k)})), nil
}

// DKIMRecord returns the TXT value publishing the key's public half.
func DKIMRecord(keyPEM string) (string, error) {
	blk, _ := pem.Decode([]byte(keyPEM))
	if blk == nil {
		return "", errors.New("bad DKIM key")
	}
	k, err := x509.ParsePKCS1PrivateKey(blk.Bytes)
	if err != nil {
		return "", err
	}
	pub, err := x509.MarshalPKIXPublicKey(&k.PublicKey)
	if err != nil {
		return "", err
	}
	return "v=DKIM1; k=rsa; p=" + base64.StdEncoding.EncodeToString(pub), nil
}

// Source tags DNS records added for a mail domain.
func Source(domainID int64) string { return "mail:" + strconv.FormatInt(domainID, 10) }

// Records are the DNS records a mail domain needs, relative to its zone
// (prefix is "" when the domain is the zone apex, else e.g. "blog").
func Records(prefix, host, selector, dkim string, domainID int64) []db.DNSRecord {
	at := func(name string) string {
		switch {
		case prefix == "" && name == "@":
			return "@"
		case prefix == "":
			return name
		case name == "@":
			return prefix
		}
		return name + "." + prefix
	}
	src := Source(domainID)
	return []db.DNSRecord{
		{Name: at("@"), Type: "MX", Content: host, Priority: 10, TTL: 3600, Source: src},
		{Name: at("@"), Type: "TXT", Content: "v=spf1 mx ~all", TTL: 3600, Source: src},
		{Name: at(selector + "._domainkey"), Type: "TXT", Content: dkim, TTL: 3600, Source: src},
		{Name: at("_dmarc"), Type: "TXT", Content: "v=DMARC1; p=none", TTL: 3600, Source: src},
	}
}
