package dbproxy

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/binary"
	"io"
	"log/slog"
	"net"
	"net/netip"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestParseAllow(t *testing.T) {
	tests := []struct {
		in   []string
		want string
		bad  bool
	}{
		{in: []string{"198.51.100.7"}, want: "198.51.100.7/32"},
		{in: []string{"10.1.2.3/8", "10.0.0.0/8", ""}, want: "10.0.0.0/8"},
		{in: []string{"::ffff:192.0.2.1"}, want: "192.0.2.1/32"},
		{in: []string{"2001:db8::/48"}, want: "2001:db8::/48"},
		{in: []string{"example.com"}, bad: true},
		{in: []string{"fe80::1%eth0"}, bad: true},
	}
	for _, tt := range tests {
		got, err := ParseAllow(tt.in)
		if tt.bad {
			if err == nil {
				t.Errorf("ParseAllow(%v) accepted", tt.in)
			}
			continue
		}
		if err != nil {
			t.Fatalf("ParseAllow(%v): %v", tt.in, err)
		}
		var s []string
		for _, p := range got {
			s = append(s, p.String())
		}
		if strings.Join(s, ",") != tt.want {
			t.Errorf("ParseAllow(%v) = %v, want %s", tt.in, s, tt.want)
		}
	}
	many := make([]string, 21)
	for i := range many {
		many[i] = netip.AddrFrom4([4]byte{10, 0, 0, byte(i)}).String()
	}
	if _, err := ParseAllow(many); err == nil {
		t.Error("21 entries accepted")
	}
}

func TestLoadCert(t *testing.T) {
	path := filepath.Join(t.TempDir(), "db.pem")
	a, err := LoadCert(path, []string{"db.example.test", "192.0.2.10"})
	if err != nil {
		t.Fatal(err)
	}
	b, err := LoadCert(path, []string{"db.example.test"})
	if err != nil {
		t.Fatal(err)
	}
	if a.Fingerprint != b.Fingerprint || len(a.Fingerprint) != 95 {
		t.Errorf("fingerprints %q / %q", a.Fingerprint, b.Fingerprint)
	}
	c, err := LoadCert(path, []string{"other.example.test"})
	if err != nil {
		t.Fatal(err)
	}
	if c.Fingerprint == a.Fingerprint {
		t.Error("certificate not renewed for a new host name")
	}
}

// harness runs a Proxy for one engine on 127.0.0.1 in front of backend.
func harness(t *testing.T, engine string, access []Access, backend func(net.Conn)) string {
	t.Helper()
	bl, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { bl.Close() })
	go func() {
		for {
			c, err := bl.Accept()
			if err != nil {
				return
			}
			go func() { defer c.Close(); backend(c) }()
		}
	}()
	cert, err := LoadCert(filepath.Join(t.TempDir(), "db.pem"), []string{"localhost"})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	p := New(ctx, Options{
		Listen:    map[string]string{engine: "127.0.0.1:0"},
		Databases: func(context.Context) ([]Access, error) { return access, nil },
		Backend:   func(context.Context, string) (string, error) { return bl.Addr().String(), nil },
		Cert:      cert,
		Log:       slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	if err := p.Sync(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(p.Close)
	return p.listeners[engine].Addr().String()
}

var local = []netip.Prefix{netip.MustParsePrefix("127.0.0.1/32")}

func greeting() []byte {
	g := []byte{10}
	g = append(g, "11.8.0-MariaDB\x00"...)
	g = append(g, 1, 0, 0, 0)        // connection id
	g = append(g, "abcdefgh\x00"...) // auth data 1 + filler
	g = append(g, 0xff, 0xf7)        // capabilities (no SSL)
	return append(g, make([]byte, 20)...)
}

func handshakeResponse(user string) []byte {
	b := make([]byte, 32)
	binary.LittleEndian.PutUint32(b[0:4], clientSSL|clientSecureConnection|clientConnectWithDB|0x200)
	b = append(b, user...)
	b = append(b, 0, 3, 'x', 'y', 'z')
	return append(b, "shop\x00"...)
}

func mysqlBackend(t *testing.T) func(net.Conn) {
	return func(c net.Conn) {
		_ = writePacket(c, 0, greeting())
		seq, resp, err := readPacket(c)
		if err != nil {
			return
		}
		if seq != 1 || binary.LittleEndian.Uint32(resp[0:4])&clientSSL != 0 {
			t.Errorf("backend got seq %d caps %x", seq, binary.LittleEndian.Uint32(resp[0:4]))
		}
		// An auth switch, then OK.
		_ = writePacket(c, 2, []byte{0xfe, 'p', 0})
		seq, _, err = readPacket(c)
		if err != nil || seq != 3 {
			t.Errorf("backend auth reply seq %d err %v", seq, err)
		}
		_ = writePacket(c, 4, []byte{0x00, 0, 0, 2, 0})
		// Echo.
		_, _ = io.Copy(c, c)
	}
}

func mysqlClient(t *testing.T, addr, user string, useTLS bool) (net.Conn, byte, []byte) {
	t.Helper()
	c, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	_ = c.SetDeadline(time.Now().Add(10 * time.Second))
	_, g, err := readPacket(c)
	if err != nil {
		return c, 0, nil
	}
	off, _ := greetingCapsOffset(g)
	if binary.LittleEndian.Uint16(g[off:])&clientSSL == 0 {
		t.Error("greeting does not offer TLS")
	}
	if !useTLS {
		resp := handshakeResponse(user)
		binary.LittleEndian.PutUint32(resp[0:4], clientSecureConnection|clientConnectWithDB)
		_ = writePacket(c, 1, resp)
		seq, pkt, _ := readPacket(c)
		return c, seq, pkt
	}
	req := make([]byte, 32)
	binary.LittleEndian.PutUint32(req, clientSSL|clientSecureConnection)
	_ = writePacket(c, 1, req)
	tc := tls.Client(c, &tls.Config{InsecureSkipVerify: true})
	if err := tc.Handshake(); err != nil {
		t.Fatal(err)
	}
	_ = writePacket(tc, 2, handshakeResponse(user))
	seq, pkt, err := readPacket(tc)
	if err != nil {
		return tc, 0, nil
	}
	return tc, seq, pkt
}

func TestMySQL(t *testing.T) {
	addr := harness(t, MariaDB, []Access{{Engine: MariaDB, Database: "shop", User: "alice_shop", Allow: local}}, mysqlBackend(t))

	c, seq, pkt := mysqlClient(t, addr, "alice_shop", true)
	if seq != 3 || len(pkt) == 0 || pkt[0] != 0xfe {
		t.Fatalf("auth switch: seq %d pkt %x", seq, pkt)
	}
	_ = writePacket(c, 4, []byte("scrambled"))
	seq, pkt, err := readPacket(c)
	if err != nil || seq != 5 || pkt[0] != 0x00 {
		t.Fatalf("OK: seq %d pkt %x err %v", seq, pkt, err)
	}
	_, _ = c.Write([]byte("ping"))
	buf := make([]byte, 4)
	if _, err := io.ReadFull(c, buf); err != nil || string(buf) != "ping" {
		t.Fatalf("echo %q %v", buf, err)
	}
	c.Close()

	c, _, pkt = mysqlClient(t, addr, "bob_other", true)
	if len(pkt) == 0 || pkt[0] != 0xff || !bytes.Contains(pkt, []byte("Access denied")) {
		t.Errorf("other user: %q", pkt)
	}
	c.Close()

	c, _, pkt = mysqlClient(t, addr, "alice_shop", false)
	if len(pkt) == 0 || pkt[0] != 0xff || !bytes.Contains(pkt, []byte("TLS")) {
		t.Errorf("plaintext: %q", pkt)
	}
	c.Close()
}

func TestRefusedAddress(t *testing.T) {
	other := []netip.Prefix{netip.MustParsePrefix("192.0.2.0/24")}
	addr := harness(t, MariaDB, []Access{{Engine: MariaDB, Database: "shop", User: "alice_shop", Allow: other}}, mysqlBackend(t))
	c, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(5 * time.Second))
	if n, err := c.Read(make([]byte, 16)); n != 0 || err == nil {
		t.Errorf("refused address got %d bytes, err %v", n, err)
	}
}

func startup(user, database string) []byte {
	var b bytes.Buffer
	b.Write([]byte{0, 0, 0, 0, 0, 3, 0, 0})
	b.WriteString("user\x00" + user + "\x00")
	if database != "" {
		b.WriteString("database\x00" + database + "\x00")
	}
	b.WriteByte(0)
	m := b.Bytes()
	binary.BigEndian.PutUint32(m, uint32(len(m)))
	return m
}

func sslRequest() []byte {
	m := make([]byte, 8)
	binary.BigEndian.PutUint32(m, 8)
	binary.BigEndian.PutUint32(m[4:], pgSSLRequest)
	return m
}

func TestPostgres(t *testing.T) {
	got := make(chan []byte, 4)
	addr := harness(t, Postgres, []Access{{Engine: Postgres, Database: "alice_shop", User: "alice_shop", Allow: local}}, func(c net.Conn) {
		m, err := readStartup(c)
		if err != nil {
			return
		}
		got <- m
		_, _ = c.Write([]byte{'R', 0, 0, 0, 8, 0, 0, 0, 0})
		_, _ = io.Copy(c, c)
	})
	dial := func() net.Conn {
		c, err := net.DialTimeout("tcp", addr, 5*time.Second)
		if err != nil {
			t.Fatal(err)
		}
		_ = c.SetDeadline(time.Now().Add(10 * time.Second))
		return c
	}
	tlsUp := func(c net.Conn) net.Conn {
		_, _ = c.Write(sslRequest())
		b := make([]byte, 1)
		if _, err := io.ReadFull(c, b); err != nil || b[0] != 'S' {
			t.Fatalf("SSLRequest answer %q %v", b, err)
		}
		tc := tls.Client(c, &tls.Config{InsecureSkipVerify: true})
		if err := tc.Handshake(); err != nil {
			t.Fatal(err)
		}
		return tc
	}

	// Allowed: the database defaults to the user name.
	c := tlsUp(dial())
	_, _ = c.Write(startup("alice_shop", ""))
	b := make([]byte, 9)
	if _, err := io.ReadFull(c, b); err != nil || b[0] != 'R' {
		t.Fatalf("auth %q %v", b, err)
	}
	if m := <-got; !bytes.Contains(m, []byte("alice_shop")) {
		t.Errorf("backend startup %q", m)
	}
	c.Close()

	// Another database.
	c = tlsUp(dial())
	_, _ = c.Write(startup("alice_shop", "postgres"))
	b, _ = io.ReadAll(c)
	if len(b) == 0 || b[0] != 'E' || !bytes.Contains(b, []byte("no remote access")) {
		t.Errorf("other database: %q", b)
	}
	c.Close()

	// Plaintext.
	c = dial()
	_, _ = c.Write(startup("alice_shop", ""))
	b, _ = io.ReadAll(c)
	if len(b) == 0 || b[0] != 'E' || !bytes.Contains(b, []byte("TLS")) {
		t.Errorf("plaintext: %q", b)
	}
	c.Close()
	select {
	case m := <-got:
		t.Errorf("backend reached by a refused client: %q", m)
	default:
	}
}
