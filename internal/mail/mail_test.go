package mail

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"openplex.dev/openplex/internal/db"
	"openplex.dev/openplex/internal/secrets"
)

func TestValidation(t *testing.T) {
	for in, ok := range map[string]bool{"info": true, "John.Doe": true, "a+b_c-d": true, ".x": false, "x.": false, "a..b": false, "a b": false, "ñ": false, "": false} {
		if _, err := NormalizeLocal(in); (err == nil) != ok {
			t.Errorf("NormalizeLocal(%q) err=%v", in, err)
		}
	}
	for in, ok := range map[string]bool{"Me@Example.com": true, "a@b.co": true, "a@b": false, "a@@b.com": false, "a@b..com": false, "a b@c.com": false} {
		if _, err := NormalizeAddress(in); (err == nil) != ok {
			t.Errorf("NormalizeAddress(%q) err=%v", in, err)
		}
	}
	if CheckPassword("short") == nil || CheckPassword("long enough pass") != nil || CheckPassword("tab\there-123") == nil {
		t.Error("password policy")
	}
}

func TestFiles(t *testing.T) {
	key, err := NewDKIMKey()
	if err != nil {
		t.Fatal(err)
	}
	snap := Snapshot{
		Domains:   []DomainConfig{{Name: "example.com", Selector: "opx", KeyPEM: key}},
		Mailboxes: []MailboxConfig{{Address: "info@example.com", Hash: HashPassword("correct horse battery"), QuotaMB: 500}},
		Aliases:   []AliasConfig{{Address: "sales@example.com", Destinations: []string{"info@example.com", "boss@other.net"}}},
	}
	files, err := Files(snap)
	if err != nil {
		t.Fatal(err)
	}
	if acc := string(files["postfix-accounts.cf"]); !strings.HasPrefix(acc, "info@example.com|{SHA512-CRYPT}$6$rounds=50000$") {
		t.Errorf("accounts = %q", acc)
	}
	if q := string(files["dovecot-quotas.cf"]); q != "info@example.com:500M\n" {
		t.Errorf("quotas = %q", q)
	}
	if v := string(files["postfix-virtual.cf"]); v != "sales@example.com info@example.com,boss@other.net\n" {
		t.Errorf("virtual = %q", v)
	}
	if c := string(files["rspamd/override.d/dkim_signing.conf"]); !strings.Contains(c, `path = "/tmp/docker-mailserver/rspamd/dkim/example.com.opx.key";`) {
		t.Errorf("dkim conf = %s", c)
	}
	bad := []Snapshot{
		{Mailboxes: []MailboxConfig{{Address: "x@example.com\nevil@x.com|{SHA512-CRYPT}$6$a$b", Hash: HashPassword("correct horse battery"), QuotaMB: 1}}},
		{Mailboxes: []MailboxConfig{{Address: "x@example.com", Hash: "plain", QuotaMB: 1}}},
		{Aliases: []AliasConfig{{Address: "x@example.com", Destinations: []string{"a@b.com y@z.com"}}}},
		{Domains: []DomainConfig{{Name: "example.com\"; }", Selector: "opx", KeyPEM: key}}},
	}
	for i, s := range bad {
		if _, err := Files(s); err == nil {
			t.Errorf("bad snapshot %d rendered", i)
		}
	}

	dir := t.TempDir()
	if changed, err := WriteFiles(dir, files); err != nil || !changed {
		t.Fatalf("first write: %v %v", changed, err)
	}
	if changed, _ := WriteFiles(dir, files); changed {
		t.Error("unchanged files rewritten (the container would reload for nothing)")
	}
	delete(files, "rspamd/dkim/example.com.opx.key")
	if _, err := WriteFiles(dir, files); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "rspamd", "dkim", "example.com.opx.key")); !os.IsNotExist(err) {
		t.Error("key of a removed domain kept")
	}
	txt, err := DKIMRecord(key)
	if err != nil || !strings.HasPrefix(txt, "v=DKIM1; k=rsa; p=MII") {
		t.Errorf("DKIM record = %q %v", txt, err)
	}
}

func TestRecords(t *testing.T) {
	apex := Records("", "mail.example.com", "opx", "v=DKIM1; p=x", 7)
	sub := Records("shop", "mail.example.com", "opx", "v=DKIM1; p=x", 8)
	names := func(rs []db.DNSRecord) string {
		var out []string
		for _, r := range rs {
			out = append(out, r.Name+" "+r.Type)
		}
		return strings.Join(out, ", ")
	}
	if got := names(apex); got != "@ MX, @ TXT, opx._domainkey TXT, _dmarc TXT, webmail CNAME" {
		t.Errorf("apex = %s", got)
	}
	if got := names(sub); got != "shop MX, shop TXT, opx._domainkey.shop TXT, _dmarc.shop TXT, webmail.shop CNAME" {
		t.Errorf("subdomain = %s", got)
	}
	if apex[0].Source != "mail:7" || apex[0].Content != "mail.example.com" || apex[0].Priority != 10 {
		t.Errorf("MX = %+v", apex[0])
	}
	userSPF := db.DNSRecord{Name: "@", Type: "TXT", Content: "v=spf1 include:_spf.google.com ~all"}
	if !clashes(userSPF, apex[1]) || clashes(userSPF, apex[2]) || clashes(db.DNSRecord{Name: "www", Type: "MX"}, apex[0]) {
		t.Error("clash rules")
	}
}

type fakeDNS struct {
	store   *db.DB
	changed int
}

func (f *fakeDNS) ZoneFor(ctx context.Context, host string) (*db.DNSZone, error) {
	zones, _ := f.store.ListDNSZones(ctx)
	for _, z := range zones {
		if host == z.Name || strings.HasSuffix(host, "."+z.Name) {
			return z, nil
		}
	}
	return nil, nil
}
func (f *fakeDNS) Changed() { f.changed++ }

type fakeServer struct {
	running  bool
	hostname string
	execs    []string
	webmail  bool
	version  string
}

func (f *fakeServer) EnsureWebmail(_ context.Context, _, v string) error {
	f.webmail, f.version = true, v
	return nil
}
func (f *fakeServer) StopWebmail(context.Context) error   { f.webmail = false; return nil }
func (f *fakeServer) WebmailRunning(context.Context) bool { return f.webmail }
func (f *fakeServer) NetworkSubnets(context.Context) (string, string, error) {
	return "172.30.0.0/16", "172.31.0.0/16", nil
}

func (f *fakeServer) EnsureMail(_ context.Context, host, _ string) error {
	f.running, f.hostname = true, host
	return nil
}
func (f *fakeServer) StopMail(context.Context) error            { f.running = false; return nil }
func (f *fakeServer) MailStatus(context.Context) (bool, string) { return f.running, "healthy" }
func (f *fakeServer) MailExec(_ context.Context, c ...string) (string, error) {
	f.execs = append(f.execs, strings.Join(c, " "))
	return "", nil
}

func TestManager(t *testing.T) {
	ctx := context.Background()
	store := db.OpenMemory(t)
	box, _ := secrets.New(make([]byte, 32), "mail-test")
	u := &db.User{Username: "o", PasswordHash: "x", Role: db.RoleCustomer}
	_ = store.CreateUser(ctx, u)
	d := &db.Domain{OwnerID: u.ID, Name: "example.com", Kind: db.KindPrimary, Runtime: "static", Status: db.StatusRunning}
	if err := store.CreateDomain(ctx, d); err != nil {
		t.Fatal(err)
	}
	z := &db.DNSZone{DomainID: d.ID, Name: "example.com"}
	_ = store.CreateDNSZone(ctx, z, []db.DNSRecord{{Name: "@", Type: "TXT", Content: "v=spf1 include:other.net ~all", TTL: 3600}})
	srv, fdns := &fakeServer{}, &fakeDNS{store: store}
	dir := t.TempDir()
	m := New(store, srv, fdns, box, dir, dir, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))

	if err := m.UpdateSettings(ctx, db.MailSettings{Enabled: true}); err == nil {
		t.Error("enabled without a host name")
	}
	if err := m.UpdateSettings(ctx, db.MailSettings{Enabled: true, Hostname: "mail.example.com", Webmail: true}); err != nil {
		t.Fatal(err)
	}
	md, err := m.EnableDomain(ctx, d)
	if err != nil {
		t.Fatal(err)
	}
	recs, _ := store.ListDNSRecords(ctx, z.ID)
	var spf, mx, dkim int
	for _, r := range recs {
		switch {
		case r.Type == "TXT" && strings.HasPrefix(r.Content, "v=spf1"):
			spf++
		case r.Type == "MX" && r.Content == "mail.example.com":
			mx++
		case r.Name == "opx._domainkey":
			dkim++
		}
	}
	if spf != 1 || mx != 1 || dkim != 1 {
		t.Errorf("records: spf=%d mx=%d dkim=%d (%+v)", spf, mx, dkim, recs)
	}
	_ = store.CreateMailbox(ctx, &db.Mailbox{MailDomainID: md.ID, LocalPart: "info", PasswordHash: HashPassword("correct horse battery"), QuotaMB: 100, Enabled: true})
	_ = store.CreateMailbox(ctx, &db.Mailbox{MailDomainID: md.ID, LocalPart: "off", PasswordHash: HashPassword("correct horse battery"), QuotaMB: 100})
	m.Sync(ctx)
	if st, _ := m.Status(ctx); st.LastError != "" || !srv.running || srv.hostname != "mail.example.com" || st.Certificate != "self-signed" {
		t.Fatalf("status = %+v", st)
	}
	if !srv.webmail || srv.version == "" {
		t.Error("webmail not started")
	}
	masters, _ := os.ReadFile(filepath.Join(dir, "mail", "config", "dovecot-masters.cf"))
	if !strings.HasPrefix(string(masters), "opx-webmail|{SHA512-CRYPT}$6$") || !strings.HasSuffix(string(masters), "::::::allow_nets=172.30.0.0/16\n") {
		t.Errorf("masters = %q", masters)
	}
	conf, _ := os.ReadFile(filepath.Join(dir, "webmail", "config", "openplex.inc.php"))
	if !strings.Contains(string(conf), "$config['proxy_whitelist'] = ['172.31.0.0/16'];") || strings.Contains(string(conf), "openplex_captcha") {
		t.Errorf("webmail config = %s", conf)
	}
	if _, err := os.Stat(filepath.Join(dir, "webmail", "plugin", "openplex", "openplex.php")); err != nil {
		t.Error("plugin not written:", err)
	}
	link, err := m.WebmailLogin(ctx, "info@example.com")
	if err != nil || !strings.HasPrefix(link, "https://mail.example.com/?_task=login&_opx=") {
		t.Errorf("webmail link = %q %v", link, err)
	}
	acc, _ := os.ReadFile(filepath.Join(dir, "mail", "config", "postfix-accounts.cf"))
	if !strings.Contains(string(acc), "info@example.com|") || strings.Contains(string(acc), "off@") {
		t.Errorf("accounts file = %s", acc)
	}

	// A Caddy certificate replaces the temporary one.
	cdir := filepath.Join(dir, "data", "caddy", "certificates", "acme-v02.api.letsencrypt.org-directory", "mail.example.com")
	_ = os.MkdirAll(cdir, 0o755)
	_ = os.WriteFile(filepath.Join(cdir, "mail.example.com.crt"), []byte("CERT"), 0o644)
	_ = os.WriteFile(filepath.Join(cdir, "mail.example.com.key"), []byte("KEY"), 0o600)
	m.Sync(ctx)
	if c, _ := os.ReadFile(filepath.Join(dir, "mail", "config", "ssl", "cert.pem")); string(c) != "CERT" {
		t.Errorf("certificate not taken from Caddy: %q", c)
	}

	// New host name: MX follows.
	_ = m.UpdateSettings(ctx, db.MailSettings{Enabled: true, Hostname: "mx.example.com"})
	recs, _ = store.ListDNSRecords(ctx, z.ID)
	for _, r := range recs {
		if r.Type == "MX" && r.Content != "mx.example.com" {
			t.Errorf("MX not moved: %+v", r)
		}
	}

	if err := m.DisableDomain(ctx, md); err != nil {
		t.Fatal(err)
	}
	recs, _ = store.ListDNSRecords(ctx, z.ID)
	if len(recs) != 1 {
		t.Errorf("mail records left after disabling: %+v", recs)
	}
	if len(srv.execs) == 0 || srv.execs[len(srv.execs)-1] != "rm -rf -- /var/mail/example.com" {
		t.Errorf("stored mail not removed: %v", srv.execs)
	}
	_ = m.UpdateSettings(ctx, db.MailSettings{Hostname: "mx.example.com"})
	m.Sync(ctx)
	if srv.running || srv.webmail {
		t.Error("server still running after turning mail off")
	}
}

func TestTicket(t *testing.T) {
	key := strings.Repeat("ab", 32)
	now := time.Unix(1700000000, 0)
	tk, err := Ticket(key, "info@example.com", now)
	if err != nil {
		t.Fatal(err)
	}
	p, sig, ok := strings.Cut(tk, ".")
	if !ok {
		t.Fatalf("ticket = %q", tk)
	}
	raw, _ := hex.DecodeString(key)
	mac := hmac.New(sha256.New, raw)
	mac.Write([]byte(p))
	if base64.RawURLEncoding.EncodeToString(mac.Sum(nil)) != sig {
		t.Error("bad signature")
	}
	body, _ := base64.RawURLEncoding.DecodeString(p)
	var v struct {
		U string
		E int64
		N string
	}
	if json.Unmarshal(body, &v) != nil || v.U != "info@example.com" || v.E != now.Add(TicketTTL).Unix() || len(v.N) != 32 {
		t.Errorf("payload = %s", body)
	}
	if _, err := Ticket("zz", "a@b.c", now); err == nil {
		t.Error("bad key accepted")
	}
}

func TestWebmailConfig(t *testing.T) {
	k := &webmailKeys{DESKey: "x'y", SSOKey: "ab", MasterPassword: `p\w`, MasterHash: "h"}
	c, err := webmailConfig(k, "172.30.0.0/16", "172.31.0.0/16", &WebmailCaptcha{Provider: "turnstile", SiteKey: "0x4AAAAAAAsite", Secret: "0x4AAAAAAAsecret"})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`$config['des_key'] = 'x\'y';`, `$config['openplex_master_password'] = 'p\\w';`, `'provider' => 'turnstile'`} {
		if !strings.Contains(string(c), want) {
			t.Errorf("config lacks %s:\n%s", want, c)
		}
	}
	if _, err := webmailConfig(k, "172.30.0.0/16'];", "172.31.0.0/16", nil); err == nil {
		t.Error("bad network accepted")
	}
	if _, err := webmailConfig(k, "172.30.0.0/16", "172.31.0.0/16", &WebmailCaptcha{Provider: "turnstile", SiteKey: "a'b", Secret: "0x4AAAAAAAsecret"}); err == nil {
		t.Error("bad site key accepted")
	}
}
