// Package firewall manages the host firewall. OpenPlex owns one nftables
// table (inet openplex) and replaces it atomically on every change:
//
//   - a prerouting chain (priority raw, before Docker's DNAT) drops blocked
//     addresses, so a block also stops visitors of the websites, whose traffic
//     Docker forwards to the edge container and never reaches the input hook;
//   - when the firewall is on, an input chain with policy drop only lets in
//     the ports the admin allowed (SSH, mail, …).
//
// Rule changes are applied with a safety net: unless the admin confirms
// within ConfirmWindow, the previous configuration is restored.
package firewall

import (
	"errors"
	"fmt"
	"net/netip"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"openplex.dev/openplex/internal/db"
)

// Limits.
const (
	MaxRules      = 200
	MaxBlocks     = 5000
	MaxCommentLen = 100
)

// Protocols.
const (
	TCP  = "tcp"
	UDP  = "udp"
	Both = "both"
)

// NormalizeRule validates r and rewrites it in canonical form.
func NormalizeRule(r *db.FirewallRule) error {
	r.Protocol = strings.ToLower(strings.TrimSpace(r.Protocol))
	if r.Protocol == "" {
		r.Protocol = TCP
	}
	if r.Protocol != TCP && r.Protocol != UDP && r.Protocol != Both {
		return errors.New("protocol must be tcp, udp or both")
	}
	ports, err := normalizePorts(r.Ports)
	if err != nil {
		return err
	}
	r.Ports = ports
	r.Source = strings.TrimSpace(r.Source)
	if r.Source != "" {
		p, err := ParsePrefix(r.Source)
		if err != nil {
			return errors.New("the source must be an IP address or a network in CIDR notation (e.g. 203.0.113.0/24)")
		}
		r.Source = FormatPrefix(p)
	}
	if r.Ports == "" && r.Source == "" {
		return errors.New("enter a port, or a source address to allow all its traffic")
	}
	if r.Ports == "" {
		r.Protocol = Both
	}
	c, err := normalizeComment(r.Comment)
	if err != nil {
		return err
	}
	r.Comment = c
	return nil
}

func normalizeComment(s string) (string, error) {
	s = strings.TrimSpace(s)
	if utf8.RuneCountInString(s) > MaxCommentLen {
		return "", errors.New("the comment is too long (100 characters at most)")
	}
	if strings.IndexFunc(s, unicode.IsControl) >= 0 {
		return "", errors.New("the comment contains invalid characters")
	}
	return s, nil
}

// normalizePorts accepts "", "N", "N-M" or "N:M".
func normalizePorts(s string) (string, error) {
	s = strings.ReplaceAll(strings.TrimSpace(s), ":", "-")
	if s == "" {
		return "", nil
	}
	bad := errors.New("the port must be a number from 1 to 65535 or a range such as 8000-8100")
	lo, hi, isRange := strings.Cut(s, "-")
	a, err := parsePort(lo)
	if err != nil {
		return "", bad
	}
	if !isRange {
		return strconv.Itoa(a), nil
	}
	b, err := parsePort(hi)
	if err != nil || b <= a {
		return "", bad
	}
	return fmt.Sprintf("%d-%d", a, b), nil
}

func parsePort(s string) (int, error) {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil || n < 1 || n > 65535 {
		return 0, errors.New("bad port")
	}
	return n, nil
}

// portRange returns the inclusive range of a normalised ports value; ok is
// false for "" (all ports).
func portRange(ports string) (lo, hi int, ok bool) {
	if ports == "" {
		return 1, 65535, false
	}
	a, b, isRange := strings.Cut(ports, "-")
	lo, _ = strconv.Atoi(a)
	hi = lo
	if isRange {
		hi, _ = strconv.Atoi(b)
	}
	return lo, hi, true
}

// ParsePrefix parses an IP address or CIDR network, returning it masked.
func ParsePrefix(s string) (netip.Prefix, error) {
	s = strings.TrimSpace(s)
	if strings.Contains(s, "/") {
		p, err := netip.ParsePrefix(s)
		if err != nil {
			return netip.Prefix{}, err
		}
		p = netip.PrefixFrom(p.Addr().Unmap(), p.Bits())
		if p.Addr().Is4() && p.Bits() > 32 {
			return netip.Prefix{}, errors.New("bad prefix")
		}
		return p.Masked(), nil
	}
	a, err := netip.ParseAddr(s)
	if err != nil || a.Zone() != "" {
		return netip.Prefix{}, errors.New("bad address")
	}
	a = a.Unmap()
	return netip.PrefixFrom(a, a.BitLen()), nil
}

// FormatPrefix renders single hosts without the /32 or /128 suffix.
func FormatPrefix(p netip.Prefix) string {
	if p.IsSingleIP() {
		return p.Addr().String()
	}
	return p.String()
}

// NormalizeBlock validates a blocked address and returns its canonical form.
func NormalizeBlock(s string) (netip.Prefix, error) {
	p, err := ParsePrefix(s)
	if err != nil {
		return p, errors.New("enter an IP address or a network in CIDR notation (e.g. 203.0.113.0/24)")
	}
	if (p.Addr().Is4() && p.Bits() < 8) || (p.Addr().Is6() && p.Bits() < 16) {
		return p, errors.New("this network is too broad to block")
	}
	if p.Addr().IsLoopback() || p.Addr().IsUnspecified() {
		return p, errors.New("this address cannot be blocked")
	}
	return p, nil
}

// Allows reports whether rules let a new connection to proto/port from ip in.
// An invalid ip matches only rules open to anywhere.
func Allows(rules []db.FirewallRule, proto string, port int, ip netip.Addr) bool {
	for _, r := range rules {
		if r.Protocol != proto && r.Protocol != Both {
			continue
		}
		if lo, hi, _ := portRange(r.Ports); port < lo || port > hi {
			continue
		}
		if r.Source == "" {
			return true
		}
		if p, err := ParsePrefix(r.Source); err == nil && ip.IsValid() && p.Contains(ip.Unmap()) {
			return true
		}
	}
	return false
}

// CoversPort reports whether any rule (from any source) opens proto/port.
func CoversPort(rules []db.FirewallRule, proto string, port int) bool {
	for _, r := range rules {
		if lo, hi, _ := portRange(r.Ports); (r.Protocol == proto || r.Protocol == Both) && port >= lo && port <= hi {
			return true
		}
	}
	return false
}

// Ruleset is everything the kernel table is rendered from.
type Ruleset struct {
	Enabled bool
	Rules   []db.FirewallRule
	Blocks  []db.FirewallBlock
	// Services are TCP ports OpenPlex itself serves on the host (SFTP): open
	// whenever the firewall is on, like the panel's own web ports.
	Services []int
	// Restricted are TCP ports OpenPlex serves only to some addresses (remote
	// database access): open to those sources whenever the firewall is on.
	Restricted []Restricted
}

// Restricted is a TCP port open only to some source networks.
type Restricted struct {
	Port    int
	Sources []netip.Prefix
}

// Table is the nftables table OpenPlex owns.
const Table = "openplex"

// Render produces an nft script that atomically replaces the openplex table.
// Every value is re-validated, so a bad DB row can never inject nft syntax.
// Expired blocks (relative to now) are left out.
func Render(rs Ruleset, now time.Time) (string, error) {
	var v4, v6 []string
	for _, b := range rs.Blocks {
		if b.ExpiresAt != nil && !b.ExpiresAt.After(now) {
			continue
		}
		p, err := NormalizeBlock(b.Address)
		if err != nil {
			return "", fmt.Errorf("blocked address %q: %w", b.Address, err)
		}
		if p.Addr().Is4() {
			v4 = append(v4, FormatPrefix(p))
		} else {
			v6 = append(v6, FormatPrefix(p))
		}
	}
	var b strings.Builder
	// "table …" first makes the delete valid when the table does not exist yet.
	fmt.Fprintf(&b, "table inet %s\ndelete table inet %s\n", Table, Table)
	if !rs.Enabled && len(v4) == 0 && len(v6) == 0 {
		return b.String(), nil
	}
	fmt.Fprintf(&b, "table inet %s {\n", Table)
	writeSet(&b, "blocked4", "ipv4_addr", v4)
	writeSet(&b, "blocked6", "ipv6_addr", v6)
	// Container traffic (bridges) is never subject to blocks: blocking a
	// private range must not cut the edge off from the websites.
	b.WriteString("\tchain blocklist {\n\t\ttype filter hook prerouting priority -300; policy accept;\n")
	b.WriteString("\t\tiifname \"lo\" accept\n\t\tiifname \"docker0\" accept\n\t\tiifname \"br-*\" accept\n")
	b.WriteString("\t\tip saddr @blocked4 drop\n\t\tip6 saddr @blocked6 drop\n\t}\n")
	if rs.Enabled {
		b.WriteString("\tchain input {\n\t\ttype filter hook input priority 0; policy drop;\n")
		b.WriteString("\t\tct state established,related accept\n\t\tct state invalid drop\n\t\tiifname \"lo\" accept\n")
		b.WriteString("\t\tip protocol icmp accept\n\t\tmeta l4proto ipv6-icmp accept\n")
		b.WriteString("\t\tip6 saddr fe80::/10 udp dport 546 accept\n")
		for _, p := range rs.Services {
			if p < 1 || p > 65535 {
				return "", fmt.Errorf("service port %d out of range", p)
			}
			fmt.Fprintf(&b, "\t\ttcp dport %d accept\n", p)
		}
		for _, r := range rs.Restricted {
			if r.Port < 1 || r.Port > 65535 {
				return "", fmt.Errorf("service port %d out of range", r.Port)
			}
			var s4, s6 []string
			for _, p := range r.Sources {
				if p.Addr().Is4() {
					s4 = append(s4, FormatPrefix(p))
				} else {
					s6 = append(s6, FormatPrefix(p))
				}
			}
			if len(s4) > 0 {
				fmt.Fprintf(&b, "\t\tip saddr { %s } tcp dport %d accept\n", strings.Join(s4, ", "), r.Port)
			}
			if len(s6) > 0 {
				fmt.Fprintf(&b, "\t\tip6 saddr { %s } tcp dport %d accept\n", strings.Join(s6, ", "), r.Port)
			}
		}
		for _, r := range rs.Rules {
			rr := r
			if err := NormalizeRule(&rr); err != nil {
				return "", fmt.Errorf("rule %d: %w", r.ID, err)
			}
			for _, line := range ruleLines(rr) {
				fmt.Fprintf(&b, "\t\t%s accept\n", line)
			}
		}
		b.WriteString("\t}\n")
	}
	b.WriteString("}\n")
	return b.String(), nil
}

func writeSet(b *strings.Builder, name, typ string, elems []string) {
	fmt.Fprintf(b, "\tset %s {\n\t\ttype %s\n\t\tflags interval\n\t\tauto-merge\n", name, typ)
	if len(elems) > 0 {
		slices.Sort(elems)
		elems = slices.Compact(elems)
		fmt.Fprintf(b, "\t\telements = { %s }\n", strings.Join(elems, ", "))
	}
	b.WriteString("\t}\n")
}

// ruleLines renders a normalised rule as nft match expressions.
func ruleLines(r db.FirewallRule) []string {
	src := ""
	if r.Source != "" {
		p, _ := ParsePrefix(r.Source)
		fam := "ip"
		if p.Addr().Is6() {
			fam = "ip6"
		}
		src = fam + " saddr " + FormatPrefix(p) + " "
	}
	if r.Ports == "" {
		return []string{strings.TrimSpace(src)}
	}
	var out []string
	for _, proto := range []string{TCP, UDP} {
		if r.Protocol == proto || r.Protocol == Both {
			out = append(out, fmt.Sprintf("%s%s dport %s", src, proto, r.Ports))
		}
	}
	return out
}
