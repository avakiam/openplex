// Package dns runs the authoritative DNS service. Zones and records live in
// the panel database (so panel backups include them) and are pushed to a
// PowerDNS container through its HTTP API, the same way the Caddy edge is
// rendered from the database. The apex NS and SOA records, and A/AAAA glue
// for nameservers inside a zone, come from the DNS settings and are not
// stored per zone.
package dns

import (
	"errors"
	"fmt"
	"net/netip"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"openplex.dev/openplex/internal/db"
)

// Record types users can manage.
var Types = []string{"A", "AAAA", "CNAME", "MX", "TXT", "SRV", "NS", "CAA"}

// Limits.
const (
	MaxRecordsPerZone = 500
	MinTTL            = 60
	MaxTTL            = 604800
	DefaultTTL        = 3600
	maxTXT            = 4000
)

var (
	labelRe = regexp.MustCompile(`^[a-z0-9_]([a-z0-9_-]{0,61}[a-z0-9_])?$`)
	caaRe   = regexp.MustCompile(`^(\d{1,3})\s+(issue|issuewild|iodef)\s+"?([^"]*)"?$`)
)

// ValidHost reports whether h is an absolute host name (no trailing dot)
// with at least two labels.
func ValidHost(h string) bool {
	if len(h) > 253 || !strings.Contains(h, ".") {
		return false
	}
	for _, l := range strings.Split(h, ".") {
		if !labelRe.MatchString(l) {
			return false
		}
	}
	return true
}

// RelativeName turns user input into a name relative to zone ("@" = apex).
func RelativeName(in, zone string) (string, error) {
	n := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(in)), ".")
	switch {
	case n == "" || n == "@" || n == zone:
		return "@", nil
	case strings.HasSuffix(n, "."+zone):
		n = strings.TrimSuffix(n, "."+zone)
	}
	if len(n)+len(zone)+1 > 253 {
		return "", errors.New("the name is too long")
	}
	for i, l := range strings.Split(n, ".") {
		if (l == "*" && i == 0) || labelRe.MatchString(l) {
			continue
		}
		return "", errors.New("the name may only contain letters, digits, hyphens and underscores (e.g. www, mail, _dmarc)")
	}
	return n, nil
}

// FQDN returns the absolute name of a relative record name.
func FQDN(name, zone string) string {
	if name == "@" {
		return zone
	}
	return name + "." + zone
}

// target normalises a host name pointed to by CNAME, MX, NS or SRV.
func target(in, zone string) (string, error) {
	h := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(in)), ".")
	if h == "@" {
		return zone, nil
	}
	if !ValidHost(h) {
		return "", errors.New("enter a full host name, such as mail.example.com")
	}
	return h, nil
}

// NormalizeRecord validates r for zone and rewrites it in canonical form.
func NormalizeRecord(r *db.DNSRecord, zone string) error {
	r.Type = strings.ToUpper(strings.TrimSpace(r.Type))
	if !slices.Contains(Types, r.Type) {
		return errors.New("unsupported record type")
	}
	name, err := RelativeName(r.Name, zone)
	if err != nil {
		return err
	}
	r.Name = name
	if r.TTL == 0 {
		r.TTL = DefaultTTL
	}
	if r.TTL < MinTTL || r.TTL > MaxTTL {
		return errors.New("the TTL must be between 60 seconds and 7 days")
	}
	c := strings.TrimSpace(r.Content)
	if r.Type != "MX" && r.Type != "SRV" {
		r.Priority = 0
	} else if r.Priority < 0 || r.Priority > 65535 {
		return errors.New("the priority must be between 0 and 65535")
	}
	switch r.Type {
	case "A", "AAAA":
		a, err := netip.ParseAddr(c)
		if err != nil || a.Zone() != "" || (r.Type == "A") != a.Is4() {
			if r.Type == "A" {
				return errors.New("enter an IPv4 address, such as 203.0.113.10")
			}
			return errors.New("enter an IPv6 address, such as 2001:db8::10")
		}
		c = a.String()
	case "CNAME":
		if r.Name == "@" {
			return errors.New("the domain itself cannot be a CNAME; use A/AAAA records")
		}
		if c, err = target(c, zone); err != nil {
			return err
		}
	case "NS":
		if r.Name == "@" {
			return errors.New("the domain's own nameservers come from the DNS settings; NS records here delegate a subdomain")
		}
		if c, err = target(c, zone); err != nil {
			return err
		}
	case "MX":
		if c, err = target(c, zone); err != nil {
			return err
		}
	case "TXT":
		c = strings.Trim(r.Content, "\r\n")
		if len(c) > maxTXT || !utf8.ValidString(c) || strings.ContainsAny(c, "\r\n\x00") {
			return errors.New("the text must be one line of at most 4000 characters")
		}
		if len(c) >= 2 && c[0] == '"' && c[len(c)-1] == '"' && !strings.Contains(c[1:len(c)-1], `"`) {
			c = c[1 : len(c)-1] // pasted with quotes
		}
		if c == "" {
			return errors.New("enter the text")
		}
	case "SRV":
		if !strings.HasPrefix(r.Name, "_") {
			return errors.New("an SRV name looks like _service._tcp (e.g. _sip._tcp)")
		}
		f := strings.Fields(c)
		if len(f) != 3 {
			return errors.New("an SRV value is: weight port target (e.g. 5 5060 sip.example.com)")
		}
		w, err1 := strconv.Atoi(f[0])
		p, err2 := strconv.Atoi(f[1])
		t, err3 := target(f[2], zone)
		if err1 != nil || err2 != nil || err3 != nil || w < 0 || w > 65535 || p < 0 || p > 65535 {
			return errors.New("an SRV value is: weight port target (e.g. 5 5060 sip.example.com)")
		}
		c = fmt.Sprintf("%d %d %s", w, p, t)
	case "CAA":
		m := caaRe.FindStringSubmatch(c)
		flags, _ := strconv.Atoi(m2(m, 1))
		if m == nil || flags > 255 || m[3] == "" || len(m[3]) > 255 || strings.ContainsAny(m[3], "\\\r\n") {
			return errors.New(`a CAA value is: flags tag value (e.g. 0 issue letsencrypt.org)`)
		}
		c = fmt.Sprintf("%d %s %s", flags, m[2], m[3])
	}
	r.Content = c
	return nil
}

func m2(m []string, i int) string {
	if len(m) > i {
		return m[i]
	}
	return ""
}

// CheckConflicts enforces the CNAME rule: a name with a CNAME has nothing else.
// others are the zone's records apart from r itself.
func CheckConflicts(r db.DNSRecord, others []db.DNSRecord) error {
	for _, o := range others {
		if o.Name != r.Name || o.ID == r.ID {
			continue
		}
		if r.Type == "CNAME" || o.Type == "CNAME" {
			return errors.New("a name with a CNAME record cannot have any other record")
		}
		if o.Type == r.Type && o.Content == r.Content && o.Priority == r.Priority {
			return errors.New("this record already exists")
		}
	}
	return nil
}

// RRSet is a PowerDNS resource record set.
type RRSet struct {
	Name    string // absolute, with trailing dot
	Type    string
	TTL     int
	Records []string // PowerDNS content strings, sorted
}

func (s RRSet) key() string { return s.Name + "|" + s.Type }

// content renders a stored record in PowerDNS presentation format.
func content(r db.DNSRecord) string {
	switch r.Type {
	case "CNAME", "NS":
		return r.Content + "."
	case "MX":
		return fmt.Sprintf("%d %s.", r.Priority, r.Content)
	case "SRV":
		f := strings.Fields(r.Content)
		return fmt.Sprintf("%d %s %s %s.", r.Priority, f[0], f[1], f[2])
	case "TXT":
		return quoteTXT(r.Content)
	case "CAA":
		f := strings.SplitN(r.Content, " ", 3)
		return fmt.Sprintf("%s %s %q", f[0], f[1], f[2])
	}
	return r.Content
}

// quoteTXT splits text into quoted strings of at most 255 bytes.
func quoteTXT(s string) string {
	var parts []string
	for len(s) > 0 || len(parts) == 0 {
		n := min(len(s), 255)
		for n < len(s) && !utf8.RuneStart(s[n]) { // do not split a character
			n--
		}
		chunk := strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s[:n])
		parts = append(parts, `"`+chunk+`"`)
		s = s[n:]
	}
	return strings.Join(parts, " ")
}

// ZoneInput is everything a zone's PowerDNS content is built from.
type ZoneInput struct {
	Zone     string
	Settings db.DNSSettings
	Records  []db.DNSRecord
	Hosts    []string // other server host names (the mail host) that get the server addresses when in the zone
}

// Hostmaster returns the SOA rname for zone ("hostmaster.example.com.").
func Hostmaster(s db.DNSSettings, zone string) string {
	email := s.Hostmaster
	if email == "" {
		email = "hostmaster@" + zone
	}
	user, domain, _ := strings.Cut(email, "@")
	return strings.ReplaceAll(user, ".", `\.`) + "." + domain + "."
}

// SOAContent is the SOA without a meaningful serial (PowerDNS bumps it).
func SOAContent(s db.DNSSettings, zone string, serial uint32) string {
	mname := s.NS1
	if mname == "" {
		mname = "ns1." + zone
	}
	return fmt.Sprintf("%s. %s %d 10800 3600 604800 3600", mname, Hostmaster(s, zone), serial)
}

// Desired builds the zone's record sets (SOA excluded).
func Desired(in ZoneInput) []RRSet {
	sets := map[string]*RRSet{}
	add := func(name, typ string, ttl int, c string) {
		k := name + "|" + typ
		s := sets[k]
		if s == nil {
			s = &RRSet{Name: name, Type: typ, TTL: ttl}
			sets[k] = s
		}
		s.TTL = min(s.TTL, ttl)
		if !slices.Contains(s.Records, c) {
			s.Records = append(s.Records, c)
		}
	}
	zone := in.Zone
	for _, ns := range []string{in.Settings.NS1, in.Settings.NS2} {
		if ns != "" {
			add(zone+".", "NS", DefaultTTL, ns+".")
		}
	}
	stored := map[string]bool{}
	for _, r := range in.Records {
		add(FQDN(r.Name, zone)+".", r.Type, r.TTL, content(r))
		stored[FQDN(r.Name, zone)] = true
	}
	// Glue: nameservers (and the mail host) inside this zone need addresses here.
	for _, ns := range append([]string{in.Settings.NS1, in.Settings.NS2}, in.Hosts...) {
		if ns == "" || stored[ns] || (ns != zone && !strings.HasSuffix(ns, "."+zone)) {
			continue
		}
		if in.Settings.IPv4 != "" {
			add(ns+".", "A", DefaultTTL, in.Settings.IPv4)
		}
		if in.Settings.IPv6 != "" {
			add(ns+".", "AAAA", DefaultTTL, in.Settings.IPv6)
		}
	}
	out := make([]RRSet, 0, len(sets))
	for _, s := range sets {
		slices.Sort(s.Records)
		out = append(out, *s)
	}
	slices.SortFunc(out, func(a, b RRSet) int { return strings.Compare(a.key(), b.key()) })
	return out
}

// DefaultRecords are created with a new zone: the website's address and www.
func DefaultRecords(s db.DNSSettings, zone string) []db.DNSRecord {
	var out []db.DNSRecord
	if s.IPv4 != "" {
		out = append(out, db.DNSRecord{Name: "@", Type: "A", Content: s.IPv4, TTL: DefaultTTL})
	}
	if s.IPv6 != "" {
		out = append(out, db.DNSRecord{Name: "@", Type: "AAAA", Content: s.IPv6, TTL: DefaultTTL})
	}
	out = append(out, db.DNSRecord{Name: "www", Type: "CNAME", Content: zone, TTL: DefaultTTL})
	return out
}

// SubdomainRecords point a subdomain website (relative name) at the server.
func SubdomainRecords(s db.DNSSettings, name string, domainID int64) []db.DNSRecord {
	var out []db.DNSRecord
	if s.IPv4 != "" {
		out = append(out, db.DNSRecord{Name: name, Type: "A", Content: s.IPv4, TTL: DefaultTTL, DomainID: &domainID})
	}
	if s.IPv6 != "" {
		out = append(out, db.DNSRecord{Name: name, Type: "AAAA", Content: s.IPv6, TTL: DefaultTTL, DomainID: &domainID})
	}
	return out
}
