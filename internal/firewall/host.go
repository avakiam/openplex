package firewall

import (
	"bufio"
	"bytes"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"openplex.dev/openplex/internal/db"
)

// SSHPorts returns the ports sshd listens on, read from its configuration
// under root ("/" in production); 22 when nothing is configured.
func SSHPorts(root string) []int {
	files := []string{filepath.Join(root, "etc/ssh/sshd_config")}
	extra, _ := filepath.Glob(filepath.Join(root, "etc/ssh/sshd_config.d/*.conf"))
	files = append(extra, files...) // Include of sshd_config.d comes first on Debian/Ubuntu
	var ports []int
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		sc := bufio.NewScanner(bytes.NewReader(b))
	scan:
		for sc.Scan() {
			fields := strings.Fields(strings.ReplaceAll(sc.Text(), "=", " "))
			if len(fields) < 2 || strings.HasPrefix(fields[0], "#") {
				continue
			}
			switch strings.ToLower(fields[0]) {
			case "match":
				break scan // later lines are conditional
			case "port":
				if n, err := parsePort(fields[1]); err == nil {
					ports = append(ports, n)
				}
			case "listenaddress":
				if _, p, err := net.SplitHostPort(fields[1]); err == nil {
					if n, err := parsePort(p); err == nil {
						ports = append(ports, n)
					}
				}
			}
		}
	}
	if len(ports) == 0 {
		return []int{22}
	}
	slices.Sort(ports)
	return slices.Compact(ports)
}

// UFWActive reports whether ufw is enabled on the host.
func UFWActive(root string) bool {
	b, err := os.ReadFile(filepath.Join(root, "etc/ufw/ufw.conf"))
	if err != nil {
		return false
	}
	sc := bufio.NewScanner(bytes.NewReader(b))
	for sc.Scan() {
		k, v, ok := strings.Cut(strings.TrimSpace(sc.Text()), "=")
		if ok && strings.TrimSpace(k) == "ENABLED" {
			return strings.EqualFold(strings.Trim(strings.TrimSpace(v), `"'`), "yes")
		}
	}
	return false
}

// UFWBlocksTCP reports whether ufw is active and none of its rules lets
// everyone reach TCP port p. The panel cannot change ufw (it is the admin's),
// so it only warns: an OpenPlex service on that port is unreachable.
func UFWBlocksTCP(root string, p int) bool {
	if !UFWActive(root) {
		return false
	}
	for _, r := range ImportUFW(root) {
		if r.Source != "" || (r.Protocol != TCP && r.Protocol != Both) {
			continue
		}
		if r.Ports == "" {
			return false
		}
		lo, hi, isRange := strings.Cut(r.Ports, "-")
		a, _ := strconv.Atoi(lo)
		b := a
		if isRange {
			b, _ = strconv.Atoi(hi)
		}
		if p >= a && p <= b {
			return false
		}
	}
	return true
}

// ImportUFW converts ufw's incoming allow rules into OpenPlex rules.
// Rule lines look like:
//
//	### tuple ### allow tcp 3000 0.0.0.0/0 any 0.0.0.0/0 in
//	### tuple ### allow any 8000:8100 0.0.0.0/0 any 203.0.113.4 in comment=…
func ImportUFW(root string) []db.FirewallRule {
	var out []db.FirewallRule
	for _, f := range []string{"etc/ufw/user.rules", "etc/ufw/user6.rules"} {
		b, err := os.ReadFile(filepath.Join(root, f))
		if err != nil {
			continue
		}
		sc := bufio.NewScanner(bytes.NewReader(b))
		for sc.Scan() {
			line, ok := strings.CutPrefix(strings.TrimSpace(sc.Text()), "### tuple ###")
			if !ok {
				continue
			}
			fl := strings.Fields(line)
			if len(fl) < 7 || fl[0] != "allow" || fl[6] != "in" {
				continue
			}
			proto, dports, src := fl[1], fl[2], fl[5]
			switch proto {
			case "any":
				proto = Both
			case TCP, UDP:
			default:
				continue
			}
			if dports == "any" {
				dports = ""
			}
			if src == "0.0.0.0/0" || src == "::/0" {
				src = ""
			}
			for _, p := range strings.Split(dports, ",") { // multiport lists become one rule per entry
				r := db.FirewallRule{Protocol: proto, Ports: p, Source: src, Comment: "ufw"}
				if NormalizeRule(&r) == nil {
					out = appendUnique(out, r)
				}
			}
		}
	}
	return out
}

func appendUnique(rules []db.FirewallRule, r db.FirewallRule) []db.FirewallRule {
	for _, x := range rules {
		if x.Protocol == r.Protocol && x.Ports == r.Ports && x.Source == r.Source {
			return rules
		}
	}
	return append(rules, r)
}

// DefaultRules is the first configuration: SSH open to everyone, plus
// whatever ufw already allowed.
func DefaultRules(root string) []db.FirewallRule {
	var out []db.FirewallRule
	for _, p := range SSHPorts(root) {
		out = append(out, db.FirewallRule{Protocol: TCP, Ports: strconv.Itoa(p), Comment: "SSH"})
	}
	for _, r := range ImportUFW(root) {
		out = appendUnique(out, r)
	}
	return out
}

// ConnectedClients returns the remote addresses with an established TCP
// connection to one of ports on this host (read from root/proc/net/tcp{,6}),
// e.g. the SSH sessions open right now.
func ConnectedClients(root string, ports []int) []netip.Addr {
	var out []netip.Addr
	for _, f := range []string{"proc/net/tcp", "proc/net/tcp6"} {
		b, err := os.ReadFile(filepath.Join(root, f))
		if err != nil {
			continue
		}
		sc := bufio.NewScanner(bytes.NewReader(b))
		for sc.Scan() {
			fl := strings.Fields(sc.Text())
			if len(fl) < 4 || fl[3] != "01" { // 01 = ESTABLISHED
				continue
			}
			_, lport, ok1 := parseProcAddr(fl[1])
			raddr, _, ok2 := parseProcAddr(fl[2])
			if ok1 && ok2 && slices.Contains(ports, lport) && !raddr.IsLoopback() {
				out = append(out, raddr)
			}
		}
	}
	slices.SortFunc(out, func(a, b netip.Addr) int { return a.Compare(b) })
	return slices.Compact(out)
}

// parseProcAddr decodes "0100007F:0016" (IPv4) or a 32-digit IPv6 address:
// the address is stored as 32-bit words in host (little-endian) order.
func parseProcAddr(s string) (netip.Addr, int, bool) {
	h, p, ok := strings.Cut(s, ":")
	if !ok || (len(h) != 8 && len(h) != 32) {
		return netip.Addr{}, 0, false
	}
	port, err := strconv.ParseUint(p, 16, 16)
	if err != nil {
		return netip.Addr{}, 0, false
	}
	raw := make([]byte, len(h)/2)
	for i := range raw {
		v, err := strconv.ParseUint(h[2*i:2*i+2], 16, 8)
		if err != nil {
			return netip.Addr{}, 0, false
		}
		raw[i] = byte(v)
	}
	for w := 0; w < len(raw); w += 4 { // reverse each 32-bit word
		raw[w], raw[w+1], raw[w+2], raw[w+3] = raw[w+3], raw[w+2], raw[w+1], raw[w]
	}
	a, _ := netip.AddrFromSlice(raw)
	return a.Unmap(), int(port), true
}
