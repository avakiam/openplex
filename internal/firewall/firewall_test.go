package firewall

import (
	"context"
	"io"
	"log/slog"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"openplex.dev/openplex/internal/db"
)

func TestNormalizeRule(t *testing.T) {
	cases := []struct {
		in   db.FirewallRule
		want db.FirewallRule
		ok   bool
	}{
		{db.FirewallRule{Protocol: "TCP", Ports: " 22 "}, db.FirewallRule{Protocol: TCP, Ports: "22"}, true},
		{db.FirewallRule{Protocol: "udp", Ports: "8000:8100"}, db.FirewallRule{Protocol: UDP, Ports: "8000-8100"}, true},
		{db.FirewallRule{Ports: "25", Source: "203.0.113.9/24"}, db.FirewallRule{Protocol: TCP, Ports: "25", Source: "203.0.113.0/24"}, true},
		{db.FirewallRule{Protocol: "tcp", Source: "2001:db8::1"}, db.FirewallRule{Protocol: Both, Source: "2001:db8::1"}, true},
		{db.FirewallRule{Ports: "::ffff:1.2.3.4"}, db.FirewallRule{}, false},
		{db.FirewallRule{Ports: "0"}, db.FirewallRule{}, false},
		{db.FirewallRule{Ports: "70000"}, db.FirewallRule{}, false},
		{db.FirewallRule{Ports: "100-90"}, db.FirewallRule{}, false},
		{db.FirewallRule{Ports: "22; flush ruleset"}, db.FirewallRule{}, false},
		{db.FirewallRule{Ports: "22", Source: "evil } drop"}, db.FirewallRule{}, false},
		{db.FirewallRule{Protocol: "icmp", Ports: "1"}, db.FirewallRule{}, false},
		{db.FirewallRule{}, db.FirewallRule{}, false}, // neither port nor source
		{db.FirewallRule{Ports: "22", Comment: "a\nb"}, db.FirewallRule{}, false},
	}
	for _, c := range cases {
		r := c.in
		err := NormalizeRule(&r)
		if (err == nil) != c.ok {
			t.Errorf("NormalizeRule(%+v) err=%v, want ok=%v", c.in, err, c.ok)
			continue
		}
		if c.ok && r != c.want {
			t.Errorf("NormalizeRule(%+v) = %+v, want %+v", c.in, r, c.want)
		}
	}
}

func TestNormalizeBlock(t *testing.T) {
	for in, want := range map[string]string{"203.0.113.7": "203.0.113.7", "198.51.100.9/24": "198.51.100.0/24", "::ffff:192.0.2.1": "192.0.2.1", "2001:db8::/32": "2001:db8::/32"} {
		p, err := NormalizeBlock(in)
		if err != nil || FormatPrefix(p) != want {
			t.Errorf("NormalizeBlock(%q) = %v %v, want %s", in, p, err, want)
		}
	}
	for _, in := range []string{"", "0.0.0.0/0", "10.0.0.0/7", "::/0", "127.0.0.1", "::1", "fe80::1%eth0", "1.2.3.4/33", "a.b.c.d"} {
		if _, err := NormalizeBlock(in); err == nil {
			t.Errorf("NormalizeBlock(%q) accepted", in)
		}
	}
}

func TestAllows(t *testing.T) {
	rules := []db.FirewallRule{
		{Protocol: TCP, Ports: "22", Source: "203.0.113.0/24"},
		{Protocol: Both, Ports: "8000-8100"},
		{Protocol: Both, Source: "198.51.100.5"},
	}
	ip := netip.MustParseAddr
	cases := []struct {
		proto string
		port  int
		ip    netip.Addr
		want  bool
	}{
		{TCP, 22, ip("203.0.113.40"), true},
		{TCP, 22, ip("192.0.2.1"), false},
		{TCP, 22, netip.Addr{}, false},
		{UDP, 8050, netip.Addr{}, true},
		{TCP, 7080, ip("198.51.100.5"), true},
		{UDP, 22, ip("203.0.113.40"), false},
	}
	for _, c := range cases {
		if got := Allows(rules, c.proto, c.port, c.ip); got != c.want {
			t.Errorf("Allows(%s %d %v) = %v", c.proto, c.port, c.ip, got)
		}
	}
}

func TestRender(t *testing.T) {
	now := time.Now()
	past := now.Add(-time.Minute)
	rs := Ruleset{
		Enabled: true,
		Rules: []db.FirewallRule{
			{Protocol: TCP, Ports: "22"},
			{Protocol: Both, Ports: "8000-8100", Source: "203.0.113.0/24"},
			{Protocol: Both, Source: "2001:db8::/64"},
		},
		Blocks: []db.FirewallBlock{{Address: "192.0.2.9"}, {Address: "2001:db8:1::/48"}, {Address: "198.51.100.1", ExpiresAt: &past}},
	}
	got, err := Render(rs, now)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"table inet openplex\ndelete table inet openplex\n",
		"type filter hook prerouting priority -300; policy accept;",
		"iifname \"br-*\" accept",
		"elements = { 192.0.2.9 }",
		"elements = { 2001:db8:1::/48 }",
		"type filter hook input priority 0; policy drop;",
		"ct state established,related accept",
		"\t\ttcp dport 22 accept\n",
		"ip saddr 203.0.113.0/24 tcp dport 8000-8100 accept",
		"ip saddr 203.0.113.0/24 udp dport 8000-8100 accept",
		"\t\tip6 saddr 2001:db8::/64 accept\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("render lacks %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "198.51.100.1") {
		t.Error("expired block rendered")
	}

	off, _ := Render(Ruleset{}, now)
	if off != "table inet openplex\ndelete table inet openplex\n" {
		t.Errorf("disabled render = %q", off)
	}
	blocksOnly, _ := Render(Ruleset{Blocks: rs.Blocks[:1]}, now)
	if strings.Contains(blocksOnly, "hook input") || !strings.Contains(blocksOnly, "192.0.2.9") {
		t.Errorf("blocks-only render:\n%s", blocksOnly)
	}
	if _, err := Render(Ruleset{Enabled: true, Rules: []db.FirewallRule{{Protocol: TCP, Ports: "22 accept; flush ruleset"}}}, now); err == nil {
		t.Error("bad stored rule rendered")
	}
}

func write(t *testing.T, root, name, content string) {
	t.Helper()
	p := filepath.Join(root, name)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestHostDetection(t *testing.T) {
	root := t.TempDir()
	if got := SSHPorts(root); len(got) != 1 || got[0] != 22 {
		t.Errorf("default SSH ports = %v", got)
	}
	write(t, root, "etc/ssh/sshd_config", "Include /etc/ssh/sshd_config.d/*.conf\n#Port 2200\nPort 22\nListenAddress 0.0.0.0:2222\nMatch User x\nPort 9999\n")
	write(t, root, "etc/ssh/sshd_config.d/10-port.conf", "Port=4422\n")
	if got := SSHPorts(root); len(got) != 3 || got[0] != 22 || got[1] != 2222 || got[2] != 4422 {
		t.Errorf("SSH ports = %v", got)
	}

	if UFWActive(root) {
		t.Error("ufw active without config")
	}
	write(t, root, "etc/ufw/ufw.conf", "# comment\nENABLED=yes\nLOGLEVEL=low\n")
	if !UFWActive(root) {
		t.Error("ufw not detected")
	}
	write(t, root, "etc/ufw/user.rules", `*filter
### tuple ### allow tcp 22 0.0.0.0/0 any 0.0.0.0/0 in
-A ufw-user-input -p tcp --dport 22 -j ACCEPT
### tuple ### allow tcp 3000 0.0.0.0/0 any 0.0.0.0/0 in
### tuple ### allow any 8000:8100 0.0.0.0/0 any 203.0.113.4 in comment=7765
### tuple ### allow tcp 80,443 0.0.0.0/0 any 0.0.0.0/0 in
### tuple ### deny tcp 25 0.0.0.0/0 any 0.0.0.0/0 in
### tuple ### allow tcp 9000 0.0.0.0/0 any 0.0.0.0/0 out
`)
	write(t, root, "etc/ufw/user6.rules", "### tuple ### allow tcp 3000 ::/0 any ::/0 in\n### tuple ### allow udp 51820 ::/0 any ::/0 in\n")
	got := DefaultRules(root)
	want := []string{"tcp 22 ", "tcp 2222 ", "tcp 4422 ", "tcp 3000 ", "both 8000-8100 203.0.113.4", "tcp 80 ", "tcp 443 ", "udp 51820 "}
	if len(got) != len(want) {
		t.Fatalf("DefaultRules = %+v", got)
	}
	for i, r := range got {
		if s := r.Protocol + " " + r.Ports + " " + r.Source; s != want[i] {
			t.Errorf("rule %d = %q, want %q", i, s, want[i])
		}
	}
}

func TestFail2BanParsing(t *testing.T) {
	list := "Status\n|- Number of jail:\t2\n`- Jail list:\topenplex, sshd\n"
	if got := parseJailList(list); len(got) != 2 || got[0] != "openplex" || got[1] != "sshd" {
		t.Errorf("jails = %v", got)
	}
	jail := "Status for the jail: sshd\n|- Filter\n|  |- Currently failed:\t3\n|  |- Total failed:\t41\n|  `- Journal matches:\t_SYSTEMD_UNIT=ssh.service + _COMM=sshd\n`- Actions\n   |- Currently banned:\t2\n   |- Total banned:\t7\n   `- Banned IP list:\t203.0.113.5 2001:db8::7\n"
	j := parseJail("sshd", jail)
	if j.CurrentlyFailed != 3 || j.TotalFailed != 41 || j.CurrentlyBanned != 2 || j.TotalBanned != 7 || len(j.Banned) != 2 || j.Banned[1] != "2001:db8::7" {
		t.Errorf("jail = %+v", j)
	}
}

// fakeNFT records applied scripts.
type fakeNFT struct {
	mu      sync.Mutex
	scripts []string
	fail    bool
}

func (f *fakeNFT) run(_ context.Context, stdin, _ string, args ...string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fail && stdin != "" && strings.Contains(stdin, "9999") {
		return "Error: syntax error", os.ErrInvalid
	}
	if stdin != "" {
		f.scripts = append(f.scripts, stdin)
	}
	return "", nil
}

func (f *fakeNFT) last() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.scripts) == 0 {
		return ""
	}
	return f.scripts[len(f.scripts)-1]
}

func newTestManager(t *testing.T) (*Manager, *fakeNFT, *db.DB) {
	t.Helper()
	store := db.OpenMemory(t)
	nft := &fakeNFT{}
	root := t.TempDir()
	write(t, root, "etc/ssh/sshd_config", "Port 2222\n")
	m := &Manager{db: store, log: slog.New(slog.NewTextHandler(io.Discard, nil)), root: root, run: nft.run, nft: "nft", window: 200 * time.Millisecond}
	return m, nft, store
}

func TestManager(t *testing.T) {
	ctx := context.Background()
	t.Run("seed and confirm", func(t *testing.T) {
		m, nft, store := newTestManager(t)
		if err := m.init(ctx); err != nil {
			t.Fatal(err)
		}
		cfg, _ := store.GetFirewall(ctx)
		if !cfg.Seeded || !cfg.Enabled || len(cfg.Rules) != 1 || cfg.Rules[0].Ports != "2222" {
			t.Fatalf("seeded config = %+v", cfg)
		}
		if !strings.Contains(nft.last(), "tcp dport 2222 accept") {
			t.Fatalf("seed not applied:\n%s", nft.last())
		}
		rules := []db.FirewallRule{{Protocol: TCP, Ports: "2222"}, {Protocol: TCP, Ports: "25"}}
		p, err := m.Apply(ctx, true, rules)
		if err != nil || p == nil {
			t.Fatal(err)
		}
		if !strings.Contains(nft.last(), "tcp dport 25 accept") {
			t.Fatal("pending rules not applied")
		}
		if err := m.Confirm(ctx); err != nil {
			t.Fatal(err)
		}
		time.Sleep(300 * time.Millisecond) // past the window: nothing may roll back
		cfg, _ = store.GetFirewall(ctx)
		if len(cfg.Rules) != 2 || !strings.Contains(nft.last(), "tcp dport 25 accept") {
			t.Fatalf("confirmed config = %+v\n%s", cfg, nft.last())
		}
		if err := m.Confirm(ctx); err != ErrNoPending {
			t.Fatalf("second confirm: %v", err)
		}
	})
	t.Run("rollback on timeout", func(t *testing.T) {
		m, nft, store := newTestManager(t)
		_ = m.init(ctx)
		if _, err := m.Apply(ctx, false, nil); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(nft.last(), "hook input") {
			t.Fatal("disable not applied")
		}
		deadline := time.Now().Add(2 * time.Second)
		for !strings.Contains(nft.last(), "hook input") && time.Now().Before(deadline) {
			time.Sleep(20 * time.Millisecond)
		}
		if !strings.Contains(nft.last(), "tcp dport 2222 accept") {
			t.Fatal("not rolled back")
		}
		st, _ := m.Status(ctx)
		if st.Pending != nil || !st.Enabled {
			t.Fatalf("status after rollback = %+v", st)
		}
		entries, _ := store.ListAudit(ctx, 0, 10)
		if len(entries) == 0 || entries[0].Action != "firewall.rolled_back" {
			t.Fatalf("audit = %+v", entries)
		}
	})
	t.Run("failed apply restores", func(t *testing.T) {
		m, nft, _ := newTestManager(t)
		_ = m.init(ctx)
		nft.fail = true
		if _, err := m.Apply(ctx, true, []db.FirewallRule{{Protocol: TCP, Ports: "9999"}}); err == nil {
			t.Fatal("apply succeeded")
		}
		st, _ := m.Status(ctx)
		if st.Pending != nil || !strings.Contains(nft.last(), "2222") {
			t.Fatalf("state after failed apply: %+v", st)
		}
	})
	t.Run("blocks", func(t *testing.T) {
		m, nft, store := newTestManager(t)
		_ = m.init(ctx)
		b := &db.FirewallBlock{Address: "203.0.113.66"}
		if err := m.AddBlock(ctx, b); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(nft.last(), "203.0.113.66") {
			t.Fatal("block not applied")
		}
		if err := m.AddBlock(ctx, &db.FirewallBlock{Address: "203.0.113.66"}); err == nil {
			t.Fatal("duplicate block accepted")
		}
		past := time.Now().Add(-time.Second)
		_ = store.CreateFirewallBlock(ctx, &db.FirewallBlock{Address: "198.51.100.3", ExpiresAt: &past})
		if n, _ := store.DeleteExpiredFirewallBlocks(ctx, time.Now()); n != 1 {
			t.Fatalf("expired = %d", n)
		}
		if err := m.RemoveBlock(ctx, b.ID); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(nft.last(), "203.0.113.66") {
			t.Fatal("block not removed")
		}
	})
}

func TestConnectedClients(t *testing.T) {
	root := t.TempDir()
	write(t, root, "proc/net/tcp", `  sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode
   0: 00000000:0016 00000000:0000 0A 00000000:00000000 00:00000000 00000000     0        0 1 1 0 100 0 0 10 0
   1: CA00A8C0:0016 6400A8C0:D2F0 01 00000000:00000000 02:000A7D2B 00000000     0        0 2 4 0 20 4 30 10 -1
   2: CA00A8C0:0016 6500A8C0:D2F1 06 00000000:00000000 02:000A7D2B 00000000     0        0 3 4 0 20 4 30 10 -1
   3: 0100007F:1BA8 0100007F:A000 01 00000000:00000000 02:000A7D2B 00000000     0        0 4 4 0 20 4 30 10 -1
   4: CA00A8C0:01BB 6600A8C0:A001 01 00000000:00000000 02:000A7D2B 00000000     0        0 5 4 0 20 4 30 10 -1
`)
	write(t, root, "proc/net/tcp6", `  sl  local_address                         remote_address                        st
   0: 00000000000000000000000000000000:0016 B80D0120000000000000000007000000:C350 01 00000000:00000000
   1: 00000000000000000000000000000000:0016 0000000000000000FFFF0000057100CB:C351 01 00000000:00000000
`)
	got := ConnectedClients(root, []int{22})
	want := []string{"192.168.0.100", "203.0.113.5", "2001:db8::7"}
	if len(got) != len(want) {
		t.Fatalf("clients = %v", got)
	}
	for i := range want {
		if got[i].String() != want[i] {
			t.Errorf("client %d = %s, want %s", i, got[i], want[i])
		}
	}
}
