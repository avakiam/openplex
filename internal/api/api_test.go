package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/pquerna/otp/totp"

	"openplex.dev/openplex/internal/auth"
	"openplex.dev/openplex/internal/config"
	"openplex.dev/openplex/internal/cron"
	"openplex.dev/openplex/internal/db"
	"openplex.dev/openplex/internal/dns"
	"openplex.dev/openplex/internal/extensions"
	"openplex.dev/openplex/internal/firewall"
	"openplex.dev/openplex/internal/mail"
	"openplex.dev/openplex/internal/monitor"
	"openplex.dev/openplex/internal/secrets"
)

type harness struct {
	t   *testing.T
	ts  *httptest.Server
	db  *db.DB
	cfg *config.Config
	box *secrets.Box
	srv *Server
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	dir := t.TempDir()
	cfg, err := config.Load([]string{"--data-dir", dir, "--docker=false", "--dev"})
	if err != nil {
		t.Fatal(err)
	}
	store := db.OpenMemory(t)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	box, _ := secrets.New(cfg.Secret, "database-passwords")
	dnsM := dns.New(store, nil, dir, "http://127.0.0.1:1", log)
	srv := New(Options{Config: cfg, DB: store, Monitor: monitor.New(time.Second, 10, dir, log), Logger: log, BaseContext: context.Background(), Box: box, Cron: cron.New(store, nil, log), Firewall: firewall.New(store, log, false), DNS: dnsM, Mail: mail.New(store, nil, dnsM, box, dir, dir, nil, log),
		Extensions: extensions.New(context.Background(), cfg, store, box, nil, "dev", log)})
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	h := &harness{t: t, ts: ts, db: store, cfg: cfg, box: box, srv: srv}
	h.mkUser("admin", db.RoleAdmin, nil)
	return h
}

func (h *harness) mkUser(name, role string, parent *int64) *db.User {
	hash, _ := auth.HashPassword("password123")
	u := &db.User{Username: name, PasswordHash: hash, Role: role, ParentID: parent}
	if err := h.db.CreateUser(context.Background(), u); err != nil {
		h.t.Fatal(err)
	}
	return u
}

func (h *harness) do(method, path, token string, body any, out any) int {
	h.t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, h.ts.URL+"/api/v1"+path, rd)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		h.t.Fatal(err)
	}
	defer res.Body.Close()
	if out != nil {
		_ = json.NewDecoder(res.Body).Decode(out)
	}
	return res.StatusCode
}

func (h *harness) login(user string) tokenResponse {
	h.t.Helper()
	var tr tokenResponse
	if code := h.do("POST", "/auth/login", "", map[string]string{"username": user, "password": "password123"}, &tr); code != 200 {
		h.t.Fatalf("login %s: %d", user, code)
	}
	return tr
}

func TestAuthFlow(t *testing.T) {
	h := newHarness(t)
	if code := h.do("POST", "/auth/login", "", map[string]string{"username": "admin", "password": "nope"}, nil); code != 401 {
		t.Fatalf("bad password: %d", code)
	}
	tr := h.login("admin")
	var me db.User
	if code := h.do("GET", "/auth/me", tr.AccessToken, nil, &me); code != 200 || me.Username != "admin" {
		t.Fatalf("me: %d %+v", code, me)
	}
	if code := h.do("GET", "/auth/me", "garbage", nil, nil); code != 401 {
		t.Fatalf("garbage token: %d", code)
	}

	// Refresh rotation and reuse detection.
	var tr2 tokenResponse
	if code := h.do("POST", "/auth/refresh", "", map[string]string{"refresh_token": tr.RefreshToken}, &tr2); code != 200 {
		t.Fatalf("refresh: %d", code)
	}
	if code := h.do("POST", "/auth/refresh", "", map[string]string{"refresh_token": tr.RefreshToken}, nil); code != 401 {
		t.Fatalf("reuse must fail: %d", code)
	}
	if code := h.do("POST", "/auth/refresh", "", map[string]string{"refresh_token": tr2.RefreshToken}, nil); code != 401 {
		t.Fatalf("family must be revoked after reuse: %d", code)
	}

	// API key.
	var created struct {
		Key string `json:"key"`
	}
	if code := h.do("POST", "/api-keys", tr.AccessToken, map[string]any{"name": "ci"}, &created); code != 201 {
		t.Fatalf("create key: %d", code)
	}
	if code := h.do("GET", "/auth/me", created.Key, nil, nil); code != 200 {
		t.Fatalf("api key auth: %d", code)
	}
}

func TestTOTPLogin(t *testing.T) {
	h := newHarness(t)
	tok := h.login("admin").AccessToken
	var setup map[string]string
	h.do("POST", "/auth/2fa/setup", tok, nil, &setup)
	code, _ := totp.GenerateCode(setup["secret"], time.Now())
	if c := h.do("POST", "/auth/2fa/enable", tok, map[string]string{"code": code}, nil); c != 204 {
		t.Fatalf("enable: %d", c)
	}
	var e apiError
	if c := h.do("POST", "/auth/login", "", map[string]string{"username": "admin", "password": "password123"}, &e); c != 401 || e.Error != "totp_required" {
		t.Fatalf("expected totp_required, got %d %+v", c, e)
	}
	if c := h.do("POST", "/auth/login", "", map[string]string{"username": "admin", "password": "password123", "totp_code": code}, nil); c != 200 {
		t.Fatalf("totp login: %d", c)
	}
}

func TestRBAC(t *testing.T) {
	h := newHarness(t)
	res := h.mkUser("reseller", db.RoleReseller, nil)
	c1 := h.mkUser("cust1", db.RoleCustomer, &res.ID)
	c2 := h.mkUser("cust2", db.RoleCustomer, nil)
	rTok, c1Tok, c2Tok := h.login("reseller").AccessToken, h.login("cust1").AccessToken, h.login("cust2").AccessToken

	// Reseller sees itself + its customer only.
	var users []db.User
	h.do("GET", "/users", rTok, nil, &users)
	if len(users) != 2 {
		t.Fatalf("reseller sees %d users", len(users))
	}
	if c := h.do("GET", "/users/"+itoa(c2.ID), rTok, nil, nil); c != 404 {
		t.Fatalf("reseller reading foreign customer: %d", c)
	}
	// Reseller may only create customers.
	if c := h.do("POST", "/users", rTok, map[string]any{"username": "evil", "password": "password123", "role": "admin"}, nil); c != 403 {
		t.Fatalf("reseller creating admin: %d", c)
	}
	// Customers cannot use admin endpoints.
	if c := h.do("GET", "/audit", c1Tok, nil, nil); c != 403 {
		t.Fatalf("customer audit: %d", c)
	}

	// Domains: customer creates; other customer cannot see; reseller can.
	var d db.Domain
	if c := h.do("POST", "/domains", c1Tok, map[string]any{"name": "Example.COM", "runtime": "php"}, &d); c != 201 {
		t.Fatalf("create domain: %d", c)
	}
	if d.Name != "example.com" || d.RuntimeVersion != "8.3" || d.OwnerID != c1.ID {
		t.Fatalf("domain defaults: %+v", d)
	}
	if c := h.do("GET", "/domains/"+itoa(d.ID), c2Tok, nil, nil); c != 404 {
		t.Fatalf("foreign domain visible: %d", c)
	}
	if c := h.do("GET", "/domains/"+itoa(d.ID), rTok, nil, nil); c != 200 {
		t.Fatalf("reseller domain access: %d", c)
	}
	// Hijack protection: cust2 cannot claim a subdomain of cust1's domain.
	if c := h.do("POST", "/domains", c2Tok, map[string]any{"name": "shop.example.com"}, nil); c != 409 {
		t.Fatalf("hijack: %d", c)
	}
	// Customer cannot assign a domain to someone else.
	if c := h.do("POST", "/domains", c1Tok, map[string]any{"name": "other.org", "owner_id": c2.ID}, nil); c != 403 {
		t.Fatalf("foreign owner: %d", c)
	}
	// Validation.
	if c := h.do("POST", "/domains", c1Tok, map[string]any{"name": "app.example.net", "runtime": "node"}, nil); c != 400 {
		t.Fatalf("node without command: %d", c)
	}
	// Subdomain + alias + redirect.
	var sub db.Domain
	if c := h.do("POST", "/domains", c1Tok, map[string]any{"name": "blog.example.com", "kind": "subdomain", "parent_id": d.ID}, &sub); c != 201 {
		t.Fatalf("subdomain: %d", c)
	}
	if c := h.do("POST", "/domains/"+itoa(d.ID)+"/redirects", c1Tok, map[string]any{"source_path": "/old", "target_url": "/new"}, nil); c != 201 {
		t.Fatalf("redirect: %d", c)
	}
	if c := h.do("POST", "/domains/"+itoa(d.ID)+"/redirects", c1Tok, map[string]any{"source_path": "/x\n}", "target_url": "/y"}, nil); c != 400 {
		t.Fatalf("redirect injection: %d", c)
	}
}

func TestFileManager(t *testing.T) {
	h := newHarness(t)
	tok := h.login("admin").AccessToken
	var d db.Domain
	h.do("POST", "/domains", tok, map[string]any{"name": "files.test"}, &d)
	base := "/domains/" + itoa(d.ID) + "/files"
	if c := h.do("POST", base+"/mkdir", tok, map[string]string{"path": "public/css"}, nil); c != 200 {
		t.Fatalf("mkdir: %d", c)
	}
	if c := h.do("PUT", base+"/content", tok, map[string]string{"path": "public/index.php", "content": "<?php echo 1;"}, nil); c != 200 {
		t.Fatalf("write: %d", c)
	}
	var rd struct {
		Content string `json:"content"`
	}
	if c := h.do("GET", base+"/content?path=public/index.php", tok, nil, &rd); c != 200 || rd.Content != "<?php echo 1;" {
		t.Fatalf("read: %d %q", c, rd.Content)
	}
	if c := h.do("GET", base+"/content?path=../../secret.key", tok, nil, nil); c != 404 {
		t.Fatalf("traversal should resolve inside jail and 404, got %d", c)
	}
	if c := h.do("POST", base+"/delete", tok, map[string]any{"paths": []string{"/"}}, nil); c != 400 {
		t.Fatalf("delete root: %d", c)
	}
}

func itoa(i int64) string {
	return json.Number(func() string { b, _ := json.Marshal(i); return string(b) }()).String()
}

func TestDatabases(t *testing.T) {
	h := newHarness(t)
	alice := h.mkUser("alice", db.RoleCustomer, nil)
	h.mkUser("bob", db.RoleCustomer, nil)
	aTok, bTok := h.login("alice").AccessToken, h.login("bob").AccessToken

	// Without Docker, creation is refused cleanly.
	if c := h.do("POST", "/databases", aTok, map[string]any{"engine": "mariadb", "name": "shop"}, nil); c != 503 {
		t.Fatalf("create without docker: %d", c)
	}
	// A provisioned database (as the orchestrator would leave it).
	enc, _ := h.box.Seal("S3cretPassw0rdS3cretPass")
	d := &db.Database{OwnerID: alice.ID, Engine: "postgres", Name: "alice_shop", Username: "alice_shop", PasswordEnc: enc, Status: "running"}
	if err := h.db.CreateDatabase(context.Background(), d); err != nil {
		t.Fatal(err)
	}
	var list []map[string]any
	h.do("GET", "/databases", aTok, nil, &list)
	if len(list) != 1 || list[0]["host"] != "postgres" || list[0]["port"].(float64) != 5432 {
		t.Fatalf("list: %+v", list)
	}
	if _, leaked := list[0]["password_enc"]; leaked {
		t.Fatal("encrypted password leaked in list")
	}
	var creds map[string]any
	if c := h.do("GET", "/databases/"+itoa(d.ID)+"/credentials", aTok, nil, &creds); c != 200 || creds["password"] != "S3cretPassw0rdS3cretPass" {
		t.Fatalf("credentials: %d %+v", c, creds)
	}
	if creds["url"] != "postgresql://alice_shop:S3cretPassw0rdS3cretPass@postgres:5432/alice_shop" {
		t.Fatalf("url: %v", creds["url"])
	}
	// Another customer can neither see nor touch it.
	h.do("GET", "/databases", bTok, nil, &list)
	if len(list) != 0 {
		t.Fatalf("bob sees %d databases", len(list))
	}
	if c := h.do("GET", "/databases/"+itoa(d.ID)+"/credentials", bTok, nil, nil); c != 404 {
		t.Fatalf("bob credentials: %d", c)
	}
	if c := h.do("DELETE", "/databases/"+itoa(d.ID), bTok, nil, nil); c != 404 {
		t.Fatalf("bob delete: %d", c)
	}
}

func TestAdminerGuard(t *testing.T) {
	h := newHarness(t)
	for _, p := range []string{"/tools/adminer/?sqlite=&username=x", "/tools/adminer/?server=169.254.169.254&username=x", "/tools/adminer/?pgsql=evil.com"} {
		res, err := http.Get(h.ts.URL + p)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.StatusCode != 403 {
			t.Errorf("%s: %d, want 403", p, res.StatusCode)
		}
	}
	res, err := http.PostForm(h.ts.URL+"/tools/adminer/", map[string][]string{"auth[driver]": {"server"}, "auth[server]": {"10.0.0.1"}})
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 403 {
		t.Errorf("POST to foreign server: %d", res.StatusCode)
	}
	// Even an allowed server needs a panel session.
	res, _ = http.Get(h.ts.URL + "/tools/adminer/?server=mariadb")
	res.Body.Close()
	if res.StatusCode != 403 {
		t.Errorf("without a session: %d, want 403", res.StatusCode)
	}
	// A one-time ticket opens a session cookie; then the proxy is reached
	// (502: no Adminer running in tests).
	ticket, _ := h.srv.tickets.issueFor(1, time.Minute)
	jar, _ := cookiejar.New(nil)
	c := &http.Client{Jar: jar}
	res, err = c.Get(h.ts.URL + "/tools/adminer/?_opx=" + ticket + "&server=mariadb")
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 502 {
		t.Errorf("with a ticket: %d, want 502 from proxy", res.StatusCode)
	}
	if res, _ = c.Get(h.ts.URL + "/tools/adminer/?_opx=" + ticket); res.StatusCode != 403 {
		t.Errorf("reused ticket: %d, want 403", res.StatusCode)
	}
	res.Body.Close()
}

// PHP reads parameters differently from Go; none of these may get through.
func TestCheckAdminerRequest(t *testing.T) {
	form := func(v url.Values) *http.Request {
		r := httptest.NewRequest("POST", "/tools/adminer/", strings.NewReader(v.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		return r
	}
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	_ = mw.WriteField("auth[driver]", "sqlite")
	_ = mw.Close()
	multi := httptest.NewRequest("POST", "/tools/adminer/", &body)
	multi.Header.Set("Content-Type", mw.FormDataContentType())
	upper := form(url.Values{"auth[driver]": {"sqlite"}})
	upper.Header.Set("Content-Type", "Application/X-WWW-Form-Urlencoded")
	semi := httptest.NewRequest("POST", "/tools/adminer/", strings.NewReader("a=1;b=2&auth[driver]=sqlite"))
	semi.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	for name, r := range map[string]*http.Request{
		"last value wins":  form(url.Values{"auth[driver]": {"server", "sqlite"}, "auth[server]": {"mariadb"}}),
		"trailing garbage": form(url.Values{"auth[driver]": {"server"}, "auth[driver]x": {"sqlite"}, "auth[server]": {"mariadb"}}),
		"second server":    form(url.Values{"auth[driver]": {"server"}, "auth[server]": {"mariadb", "10.0.0.1"}}),
		"array server":     form(url.Values{"auth[server][]": {"10.0.0.1"}}),
		"leading space":    httptest.NewRequest("GET", "/tools/adminer/?%20sqlite=&username=x", nil),
		"array driver":     httptest.NewRequest("GET", "/tools/adminer/?sqlite[]=x", nil),
		"repeated server":  httptest.NewRequest("GET", "/tools/adminer/?server=mariadb&server=10.0.0.1", nil),
		"query semicolon":  httptest.NewRequest("GET", "/tools/adminer/?a=1;server=10.0.0.1", nil),
		"multipart":        multi,
		"mixed-case type":  upper,
		"body semicolon":   semi,
	} {
		if err := checkAdminerRequest(r); err == nil {
			t.Errorf("%s: allowed", name)
		}
	}
	for name, r := range map[string]*http.Request{
		"login":  form(url.Values{"auth[driver]": {"server"}, "auth[server]": {"mariadb"}, "auth[username]": {"u"}, "auth[password]": {"p"}, "auth[db]": {"d"}}),
		"browse": httptest.NewRequest("GET", "/tools/adminer/?server=mariadb&username=u&db=d&table=t", nil),
	} {
		if err := checkAdminerRequest(r); err != nil {
			t.Errorf("%s: refused: %v", name, err)
		}
	}
}

func TestTerminalGuards(t *testing.T) {
	h := newHarness(t)
	h.mkUser("carla", db.RoleCustomer, nil)
	adm, cust := h.login("admin").AccessToken, h.login("carla").AccessToken
	var d db.Domain
	h.do("POST", "/domains", adm, map[string]any{"name": "term.test"}, &d)
	ticket := func(tok string) string {
		var tk map[string]string
		h.do("POST", "/auth/ws-ticket", tok, nil, &tk)
		return tk["ticket"]
	}
	get := func(path string) int {
		res, err := http.Get(h.ts.URL + "/api/v1" + path)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		return res.StatusCode
	}
	// Another customer cannot open a terminal on the admin's site.
	if c := get("/domains/" + itoa(d.ID) + "/terminal/ws?ticket=" + ticket(cust)); c != 404 {
		t.Fatalf("foreign terminal: %d", c)
	}
	// Tickets are single-use.
	tk := ticket(adm)
	get("/domains/" + itoa(d.ID) + "/terminal/ws?ticket=" + tk)
	if c := get("/domains/" + itoa(d.ID) + "/terminal/ws?ticket=" + tk); c != 401 {
		t.Fatalf("ticket reuse: %d", c)
	}
	// Without Docker the endpoint refuses cleanly.
	if c := get("/domains/" + itoa(d.ID) + "/terminal/ws?ticket=" + ticket(adm)); c != 503 {
		t.Fatalf("no docker: %d", c)
	}
}

func TestUserLocale(t *testing.T) {
	h := newHarness(t)
	carla := h.mkUser("carla", db.RoleCustomer, nil)
	adm, cust := h.login("admin").AccessToken, h.login("carla").AccessToken
	cases := []struct {
		name, token, method, path string
		body                      map[string]any
		want                      int
	}{
		{"customer sets own language", cust, "PATCH", "/auth/me", map[string]any{"locale": "ca"}, 200},
		{"unknown language rejected", cust, "PATCH", "/auth/me", map[string]any{"locale": "xx"}, 400},
		{"back to browser default", cust, "PATCH", "/auth/me", map[string]any{"locale": ""}, 200},
		{"admin sets a customer's language", adm, "PATCH", "/users/" + itoa(carla.ID), map[string]any{"locale": "es"}, 200},
		{"admin creates user with language", adm, "POST", "/users", map[string]any{"username": "joan", "password": "password123", "locale": "ca"}, 201},
		{"create with unknown language", adm, "POST", "/users", map[string]any{"username": "pere", "password": "password123", "locale": "fr"}, 400},
	}
	for _, c := range cases {
		if got := h.do(c.method, c.path, c.token, c.body, nil); got != c.want {
			t.Errorf("%s: got %d, want %d", c.name, got, c.want)
		}
	}
	var me db.User
	h.do("GET", "/auth/me", cust, nil, &me)
	if me.Locale != "es" {
		t.Fatalf("locale = %q, want es", me.Locale)
	}
	// The login response carries the stored language so the UI can switch at once.
	if tr := h.login("carla"); tr.User.Locale != "es" {
		t.Fatalf("login user locale = %q", tr.User.Locale)
	}
}

func TestCronJobs(t *testing.T) {
	h := newHarness(t)
	h.mkUser("carla", db.RoleCustomer, nil)
	h.mkUser("dani", db.RoleCustomer, nil)
	carla, dani := h.login("carla").AccessToken, h.login("dani").AccessToken
	var site, alias db.Domain
	if c := h.do("POST", "/domains", carla, map[string]any{"name": "cron.test"}, &site); c != 201 {
		t.Fatalf("create site: %d", c)
	}
	h.do("POST", "/domains", carla, map[string]any{"name": "alias.test", "kind": "alias", "parent_id": site.ID}, &alias)
	base := "/domains/" + itoa(site.ID) + "/cron"

	var job struct {
		db.CronJob
		Running bool `json:"running"`
	}
	cases := []struct {
		name, token, method, path string
		body                      map[string]any
		want                      int
	}{
		{"bad schedule", carla, "POST", base, map[string]any{"schedule": "every day", "command": "php artisan schedule:run"}, 400},
		{"sub-minute schedule", carla, "POST", base, map[string]any{"schedule": "@every 10s", "command": "true"}, 400},
		{"empty command", carla, "POST", base, map[string]any{"schedule": "@daily", "command": "  "}, 400},
		{"time limit too long", carla, "POST", base, map[string]any{"schedule": "@daily", "command": "true", "timeout_sec": 99999}, 400},
		{"foreign website", dani, "GET", base, nil, 404},
		{"alias has no tasks", carla, "GET", "/domains/" + itoa(alias.ID) + "/cron", nil, 400},
		{"preview ok", carla, "POST", "/cron/preview", map[string]any{"schedule": "*/10 * * * *"}, 200},
		{"preview bad", carla, "POST", "/cron/preview", map[string]any{"schedule": "61 * * * *"}, 400},
	}
	for _, c := range cases {
		if got := h.do(c.method, c.path, c.token, c.body, nil); got != c.want {
			t.Errorf("%s: got %d, want %d", c.name, got, c.want)
		}
	}
	if c := h.do("POST", base, carla, map[string]any{"name": "Laravel", "schedule": " */5  *  * * * ", "command": "php artisan schedule:run"}, &job); c != 201 {
		t.Fatalf("create job: %d", c)
	}
	if job.Schedule != "*/5 * * * *" || job.NextRunAt == nil || !job.Enabled {
		t.Fatalf("job = %+v", job.CronJob)
	}
	jpath := base + "/" + itoa(job.ID)
	if c := h.do("PATCH", jpath, dani, map[string]any{"enabled": false}, nil); c != 404 {
		t.Fatalf("foreign update: %d", c)
	}
	if c := h.do("PATCH", jpath, carla, map[string]any{"enabled": false}, &job); c != 200 || job.Enabled || job.NextRunAt != nil {
		t.Fatalf("disable: %d %+v", c, job.CronJob)
	}
	if c := h.do("POST", jpath+"/run", carla, nil, nil); c != 503 {
		t.Fatalf("run without docker: %d", c)
	}
	if c := h.do("GET", jpath+"/runs", carla, nil, nil); c != 200 {
		t.Fatalf("runs: %d", c)
	}
	for i := 1; i < cron.MaxJobsPerSite; i++ {
		h.do("POST", base, carla, map[string]any{"schedule": "@hourly", "command": "true"}, nil)
	}
	if c := h.do("POST", base, carla, map[string]any{"schedule": "@hourly", "command": "true"}, nil); c != 403 {
		t.Fatalf("quota: %d", c)
	}
	if c := h.do("DELETE", jpath, carla, nil, nil); c != 204 {
		t.Fatalf("delete: %d", c)
	}
}

func TestFirewallGuards(t *testing.T) {
	h := newHarness(t)
	h.mkUser("carla", db.RoleCustomer, nil)
	admin, carla := h.login("admin").AccessToken, h.login("carla").AccessToken
	var st struct {
		Available   bool   `json:"available"`
		Unavailable string `json:"unavailable"`
	}
	if c := h.do("GET", "/firewall", admin, nil, &st); c != 200 || st.Available || st.Unavailable == "" {
		t.Fatalf("status: %d %+v", c, st)
	}
	ssh := map[string]any{"protocol": "tcp", "ports": "22"}
	var e apiError
	if c := h.do("POST", "/firewall/apply", admin, map[string]any{"enabled": true, "rules": []any{map[string]any{"ports": "443"}}}, &e); c != 400 || e.Error != "lockout" {
		t.Errorf("no SSH rule: %d %+v", c, e)
	}
	cases := []struct {
		name, token, method, path string
		body                      any
		want                      int
	}{
		{"customer", carla, "GET", "/firewall", nil, 403},
		{"customer apply", carla, "POST", "/firewall/apply", map[string]any{"enabled": false}, 403},
		{"bad port", admin, "POST", "/firewall/apply", map[string]any{"enabled": true, "rules": []any{map[string]any{"ports": "22; flush ruleset"}}}, 400},
		{"restricted SSH is fine", admin, "POST", "/firewall/apply", map[string]any{"enabled": true, "rules": []any{map[string]any{"ports": "22", "source": "203.0.113.0/24"}}}, 409},
		{"unavailable", admin, "POST", "/firewall/apply", map[string]any{"enabled": true, "rules": []any{ssh}}, 409},
		{"block own address", admin, "POST", "/firewall/blocks", map[string]any{"address": "127.0.0.1"}, 400},
		{"block too broad", admin, "POST", "/firewall/blocks", map[string]any{"address": "0.0.0.0/0"}, 400},
		{"block unavailable", admin, "POST", "/firewall/blocks", map[string]any{"address": "203.0.113.9"}, 409},
		{"confirm nothing", admin, "POST", "/firewall/confirm", nil, 409},
		{"unban bad ip", admin, "POST", "/fail2ban/unban", map[string]any{"jail": "sshd", "ip": "nope"}, 400},
		{"customer fail2ban", carla, "GET", "/fail2ban", nil, 403},
	}
	for _, c := range cases {
		if got := h.do(c.method, c.path, c.token, c.body, nil); got != c.want {
			t.Errorf("%s: got %d, want %d", c.name, got, c.want)
		}
	}
}

func TestDNSZones(t *testing.T) {
	h := newHarness(t)
	h.mkUser("carla", db.RoleCustomer, nil)
	h.mkUser("dani", db.RoleCustomer, nil)
	admin, carla, dani := h.login("admin").AccessToken, h.login("carla").AccessToken, h.login("dani").AccessToken
	if c := h.do("PUT", "/dns/settings", admin, map[string]any{"ns1": "ns1.example.com", "ns2": "ns2.example.com", "ipv4": "203.0.113.10"}, nil); c != 200 {
		t.Fatalf("settings: %d", c)
	}
	var site, sub db.Domain
	if c := h.do("POST", "/domains", carla, map[string]any{"name": "example.com"}, &site); c != 201 {
		t.Fatalf("create site: %d", c)
	}
	if c := h.do("POST", "/domains", carla, map[string]any{"name": "blog.example.com", "kind": "subdomain", "parent_id": site.ID}, &sub); c != 201 {
		t.Fatalf("create subdomain: %d", c)
	}
	base := "/domains/" + itoa(site.ID) + "/dns"
	var zone struct {
		Records     []db.DNSRecord `json:"records"`
		Nameservers []string       `json:"nameservers"`
	}
	if c := h.do("GET", base, carla, nil, &zone); c != 200 {
		t.Fatalf("get zone: %d", c)
	}
	have := map[string]bool{}
	for _, r := range zone.Records {
		have[r.Name+" "+r.Type+" "+r.Content] = true
	}
	for _, want := range []string{"@ A 203.0.113.10", "www CNAME example.com", "blog A 203.0.113.10"} {
		if !have[want] {
			t.Errorf("missing default record %q in %+v", want, zone.Records)
		}
	}
	var mx db.DNSRecord
	if c := h.do("POST", base+"/records", carla, map[string]any{"name": "@", "type": "MX", "content": "mail.example.com", "priority": 10}, &mx); c != 201 || mx.ID == 0 {
		t.Fatalf("create MX: %d", c)
	}
	cases := []struct {
		name, token, method, path string
		body                      any
		want                      int
	}{
		{"foreign zone", dani, "GET", base, nil, 404},
		{"foreign record edit", dani, "PATCH", base + "/records/" + itoa(mx.ID), map[string]any{"name": "@", "type": "MX", "content": "evil.net", "priority": 1}, 404},
		{"subdomain has no zone", carla, "GET", "/domains/" + itoa(sub.ID) + "/dns", nil, 400},
		{"A next to CNAME", carla, "POST", base + "/records", map[string]any{"name": "www", "type": "A", "content": "203.0.113.11"}, 400},
		{"apex NS", carla, "POST", base + "/records", map[string]any{"name": "@", "type": "NS", "content": "ns.evil.net"}, 400},
		{"bad IP", carla, "POST", base + "/records", map[string]any{"name": "x", "type": "A", "content": "999.1.1.1"}, 400},
		{"edit MX", carla, "PATCH", base + "/records/" + itoa(mx.ID), map[string]any{"name": "@", "type": "MX", "content": "mx.example.com", "priority": 5}, 200},
		{"customer settings", carla, "PUT", "/dns/settings", map[string]any{"ns1": "a.b.c"}, 403},
		{"customer status", carla, "GET", "/dns", nil, 403},
		{"enable without address", admin, "PUT", "/dns/settings", map[string]any{"enabled": true, "ns1": "ns1.example.com", "ns2": "ns2.example.com"}, 400},
		{"delete MX", carla, "DELETE", base + "/records/" + itoa(mx.ID), nil, 204},
	}
	for _, c := range cases {
		if got := h.do(c.method, c.path, c.token, c.body, nil); got != c.want {
			t.Errorf("%s: got %d, want %d", c.name, got, c.want)
		}
	}
	if c := h.do("DELETE", "/domains/"+itoa(site.ID), carla, nil, nil); c != 204 && c != 200 && c != 202 {
		t.Fatalf("delete site: %d", c)
	}
	if zs, _ := h.db.ListDNSZones(context.Background()); len(zs) != 0 {
		t.Errorf("zone left after deleting the website: %+v", zs)
	}
}

func TestMail(t *testing.T) {
	h := newHarness(t)
	h.mkUser("carla", db.RoleCustomer, nil)
	h.mkUser("dani", db.RoleCustomer, nil)
	admin, carla, dani := h.login("admin").AccessToken, h.login("carla").AccessToken, h.login("dani").AccessToken
	if c := h.do("PUT", "/mail/settings", admin, map[string]any{"enabled": true, "hostname": "mail.provider.test"}, nil); c != 200 {
		t.Fatalf("settings: %d", c)
	}
	var site db.Domain
	if c := h.do("POST", "/domains", carla, map[string]any{"name": "example.com"}, &site); c != 201 {
		t.Fatalf("create site: %d", c)
	}
	base := "/domains/" + itoa(site.ID) + "/mail"
	if c := h.do("POST", base+"/mailboxes", carla, map[string]any{"local_part": "info", "password": "correct horse battery"}, nil); c != 409 {
		t.Errorf("mailbox before enabling: %d", c)
	}
	if c := h.do("POST", base, carla, nil, nil); c != 201 {
		t.Fatalf("enable: %d", c)
	}
	var mb struct {
		ID      int64  `json:"id"`
		Address string `json:"address"`
		QuotaMB int    `json:"quota_mb"`
	}
	if c := h.do("POST", base+"/mailboxes", carla, map[string]any{"local_part": "Info", "password": "correct horse battery"}, &mb); c != 201 || mb.Address != "info@example.com" || mb.QuotaMB != 1024 {
		t.Fatalf("create mailbox: %d %+v", c, mb)
	}
	cases := []struct {
		name, token, method, path string
		body                      any
		want                      int
	}{
		{"foreign domain", dani, "GET", base, nil, 404},
		{"foreign mailbox", dani, "PATCH", base + "/mailboxes/" + itoa(mb.ID), map[string]any{"password": "stolen password!"}, 404},
		{"duplicate mailbox", carla, "POST", base + "/mailboxes", map[string]any{"local_part": "info", "password": "correct horse battery"}, 409},
		{"short password", carla, "POST", base + "/mailboxes", map[string]any{"local_part": "a", "password": "short"}, 400},
		{"bad local part", carla, "POST", base + "/mailboxes", map[string]any{"local_part": "a b", "password": "correct horse battery"}, 400},
		{"alias over mailbox", carla, "POST", base + "/aliases", map[string]any{"local_part": "info", "destinations": []string{"x@other.net"}}, 409},
		{"alias to itself", carla, "POST", base + "/aliases", map[string]any{"local_part": "loop", "destinations": []string{"loop@example.com"}}, 400},
		{"alias bad target", carla, "POST", base + "/aliases", map[string]any{"local_part": "sales", "destinations": []string{"nope"}}, 400},
		{"alias ok", carla, "POST", base + "/aliases", map[string]any{"local_part": "sales", "destinations": []string{"info@example.com", "Boss@Other.net"}}, 201},
		{"mailbox over alias", carla, "POST", base + "/mailboxes", map[string]any{"local_part": "sales", "password": "correct horse battery"}, 409},
		{"change password", carla, "PATCH", base + "/mailboxes/" + itoa(mb.ID), map[string]any{"password": "another long pass", "quota_mb": 2048}, 200},
		{"quota too big", carla, "PATCH", base + "/mailboxes/" + itoa(mb.ID), map[string]any{"quota_mb": 999999}, 400},
		{"customer settings", carla, "PUT", "/mail/settings", map[string]any{"enabled": false}, 403},
		{"delete mailbox", carla, "DELETE", base + "/mailboxes/" + itoa(mb.ID), nil, 204},
		{"disable", carla, "DELETE", base, nil, 204},
	}
	for _, c := range cases {
		if got := h.do(c.method, c.path, c.token, c.body, nil); got != c.want {
			t.Errorf("%s: got %d, want %d", c.name, got, c.want)
		}
	}
}

func TestAccessGrants(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	res := h.mkUser("reseller", db.RoleReseller, nil)
	owner := h.mkUser("owner", db.RoleCustomer, &res.ID)
	dev := h.mkUser("dev", db.RoleCustomer, &res.ID)
	other := h.mkUser("other", db.RoleCustomer, nil)
	adminTok, rTok, oTok, dTok, xTok := h.login("admin").AccessToken, h.login("reseller").AccessToken, h.login("owner").AccessToken, h.login("dev").AccessToken, h.login("other").AccessToken

	var site db.Domain
	if c := h.do("POST", "/domains", oTok, map[string]any{"name": "shop.example"}, &site); c != 201 {
		t.Fatalf("create site: %d", c)
	}
	var alias db.Domain
	if c := h.do("POST", "/domains", oTok, map[string]any{"name": "shop-alias.example", "kind": "alias", "parent_id": site.ID}, &alias); c != 201 {
		t.Fatalf("create alias: %d", c)
	}
	enc, _ := h.box.Seal("S3cretPassw0rdS3cretPass")
	dbase := &db.Database{OwnerID: owner.ID, Engine: "postgres", Name: "owner_shop", Username: "owner_shop", PasswordEnc: enc, Status: "running"}
	if err := h.db.CreateDatabase(ctx, dbase); err != nil {
		t.Fatal(err)
	}
	base := "/domains/" + itoa(site.ID)
	if c := h.do("GET", base, dTok, nil, nil); c != 404 {
		t.Fatalf("site visible before sharing: %d", c)
	}

	// Who may share: the owner cannot hand out access to other users, a
	// reseller can (its customers, its customers' sites), nobody to an admin.
	grant := map[string]any{"kind": "site", "resource_id": site.ID, "areas": []string{"mail", "files"}}
	if c := h.do("PUT", "/users/"+itoa(dev.ID)+"/access", oTok, grant, nil); c != 404 {
		t.Fatalf("customer sharing with another user: %d", c)
	}
	if c := h.do("PUT", "/users/"+itoa(other.ID)+"/access", rTok, grant, nil); c != 404 {
		t.Fatalf("reseller sharing with a foreign user: %d", c)
	}
	if c := h.do("PUT", "/users/1/access", adminTok, grant, nil); c != 400 {
		t.Fatalf("sharing with an admin: %d", c)
	}
	if c := h.do("PUT", "/users/"+itoa(dev.ID)+"/access", rTok, map[string]any{"kind": "site", "resource_id": site.ID, "areas": []string{"everything"}}, nil); c != 400 {
		t.Fatalf("unknown area: %d", c)
	}
	var g db.AccessGrant
	if c := h.do("PUT", "/users/"+itoa(dev.ID)+"/access", rTok, grant, &g); c != 200 || g.ResourceName != "shop.example" || len(g.Areas) != 2 {
		t.Fatalf("share site: %d %+v", c, g)
	}

	// The grantee sees the website and its alias, marked as shared.
	var list []map[string]any
	h.do("GET", "/domains", dTok, nil, &list)
	if len(list) != 2 || list[0]["access"] == nil {
		t.Fatalf("grantee domain list: %+v", list)
	}
	checks := []struct {
		method, path string
		want         int
	}{
		{"GET", base, 200},
		{"GET", base + "/stats", 503}, // past the access check; no Docker in tests
		{"GET", base + "/mail", 200},
		{"GET", base + "/files", 200},
		{"GET", "/domains/" + itoa(alias.ID), 200},
		{"GET", base + "/dns", 403},              // not granted
		{"GET", base + "/redirects", 403},        // site settings not granted
		{"PATCH", base, 403},                     // settings
		{"DELETE", base, 403},                    // never through a grant
		{"POST", base + "/actions/restart", 403}, // site settings
		{"GET", base + "/backups", 403},
	}
	for _, c := range checks {
		if got := h.do(c.method, c.path, dTok, map[string]any{}, nil); got != c.want {
			t.Errorf("%s %s = %d, want %d", c.method, c.path, got, c.want)
		}
	}

	// Read-only: views work, changes and the terminal don't.
	grant["read_only"] = true
	if c := h.do("PUT", "/users/"+itoa(dev.ID)+"/access", rTok, grant, nil); c != 200 {
		t.Fatalf("make read-only: %d", c)
	}
	if c := h.do("GET", base+"/mail", dTok, nil, nil); c != 200 {
		t.Errorf("read-only view: %d", c)
	}
	if c := h.do("POST", base+"/files/mkdir", dTok, map[string]any{"path": "x"}, nil); c != 403 {
		t.Errorf("read-only change: %d", c)
	}
	if c := h.do("GET", base+"/terminal/ws", dTok, nil, nil); c != 403 {
		t.Errorf("read-only terminal: %d", c)
	}

	// Databases: open, but not deletable.
	if c := h.do("PUT", "/users/"+itoa(dev.ID)+"/access", rTok, map[string]any{"kind": "database", "resource_id": dbase.ID}, nil); c != 200 {
		t.Fatalf("share database: %d", c)
	}
	var dbs []map[string]any
	h.do("GET", "/databases", dTok, nil, &dbs)
	if len(dbs) != 1 || dbs[0]["shared"] != true {
		t.Fatalf("grantee databases: %+v", dbs)
	}
	if c := h.do("GET", "/databases/"+itoa(dbase.ID)+"/credentials", dTok, nil, nil); c != 200 {
		t.Errorf("shared credentials: %d", c)
	}
	if c := h.do("DELETE", "/databases/"+itoa(dbase.ID), dTok, nil, nil); c != 403 {
		t.Errorf("shared delete: %d", c)
	}
	if c := h.do("GET", "/databases/"+itoa(dbase.ID), xTok, nil, nil); c != 404 {
		t.Errorf("stranger sees database: %d", c)
	}

	// Revoking closes it again; deleting the site drops its grants.
	var grants []db.AccessGrant
	h.do("GET", "/users/"+itoa(dev.ID)+"/access", rTok, nil, &grants)
	if len(grants) != 2 {
		t.Fatalf("grants = %+v", grants)
	}
	for _, x := range grants {
		if x.Kind == "database" {
			if c := h.do("DELETE", "/users/"+itoa(dev.ID)+"/access/"+itoa(x.ID), rTok, nil, nil); c != 204 {
				t.Fatalf("revoke: %d", c)
			}
		}
	}
	if c := h.do("GET", "/databases/"+itoa(dbase.ID), dTok, nil, nil); c != 404 {
		t.Errorf("revoked database still open: %d", c)
	}
	if err := h.db.DeleteDomain(ctx, site.ID); err != nil {
		t.Fatal(err)
	}
	if gs, _ := h.db.ListUserGrants(ctx, dev.ID); len(gs) != 0 {
		t.Errorf("grants left after deleting the site: %+v", gs)
	}
}

func TestTenantLimits(t *testing.T) {
	h := newHarness(t)
	h.cfg.PanelDomain = "panel.provider.test"
	h.mkUser("carla", db.RoleCustomer, nil)
	admin, carla := h.login("admin").AccessToken, h.login("carla").AccessToken
	for _, name := range []string{"co.uk", "github.io", "panel.provider.test", "provider.test", "x.panel.provider.test"} {
		if c := h.do("POST", "/domains", carla, map[string]any{"name": name}, nil); c != 409 {
			t.Errorf("customer created %s: %d, want 409", name, c)
		}
	}
	var site db.Domain
	if c := h.do("POST", "/domains", carla, map[string]any{"name": "carla.test"}, &site); c != 201 {
		t.Fatalf("create: %d", c)
	}
	if c := h.do("POST", "/domains", carla, map[string]any{"name": "big.test", "memory_mb": 8192}, nil); c != 403 {
		t.Errorf("customer chose its memory: %d, want 403", c)
	}
	if c := h.do("PATCH", "/domains/"+itoa(site.ID), carla, map[string]any{"memory_mb": 8192, "cpus": 8}, nil); c != 403 {
		t.Errorf("customer raised its limits: %d, want 403", c)
	}
	if c := h.do("PATCH", "/domains/"+itoa(site.ID), carla, map[string]any{"memory_mb": site.MemoryMB, "cpus": site.CPUs, "force_https": false}, nil); c != 200 {
		t.Errorf("customer saving unchanged limits: %d, want 200", c)
	}
	if c := h.do("PATCH", "/domains/"+itoa(site.ID), admin, map[string]any{"memory_mb": 1024}, nil); c != 200 {
		t.Errorf("admin raising limits: %d", c)
	}
	if c := h.do("POST", "/domains", admin, map[string]any{"name": "provider.test"}, nil); c != 201 {
		t.Errorf("admin creating the provider's own domain: %d", c)
	}

	// A reseller's quota covers its customers.
	rs := h.mkUser("res", db.RoleReseller, nil)
	rs.MaxDomains = 1
	if err := h.db.UpdateUser(context.Background(), rs); err != nil {
		t.Fatal(err)
	}
	cust := h.mkUser("rcust", db.RoleCustomer, &rs.ID)
	res := h.login("res").AccessToken
	if c := h.do("POST", "/domains", res, map[string]any{"name": "one.test", "owner_id": cust.ID}, nil); c != 201 {
		t.Fatalf("first site: %d", c)
	}
	if c := h.do("POST", "/domains", res, map[string]any{"name": "two.test", "owner_id": cust.ID}, nil); c != 403 {
		t.Errorf("reseller quota bypassed through a customer: %d, want 403", c)
	}
}

func TestExtensionSocket(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	carla := h.mkUser("carla", db.RoleCustomer, nil)
	dani := h.mkUser("dani", db.RoleCustomer, nil)
	for _, d := range []*db.Domain{{Name: "carla.test", OwnerID: carla.ID}, {Name: "dani.test", OwnerID: dani.ID}} {
		d.Kind, d.Runtime, d.DocumentRoot, d.MemoryMB, d.CPUs = db.KindPrimary, "static", "public", 128, 1
		if err := h.db.CreateDomain(ctx, d); err != nil {
			t.Fatal(err)
		}
	}
	man := `{"schema":1,"id":"acme.test","name":"Test","version":"1.0.0","publisher":{"name":"Acme"},
		"permissions":["domains:read"],"backend":{"image":"acme/test:1","port":8080},
		"settings":[{"key":"token","type":"secret","label":"Token"}],
		"pages":[{"id":"main","title":"Main","path":"/"}],
		"site_tabs":[{"id":"dns","title":"DNS","path":"/site","area":"dns"}]}`
	sealed, _ := h.box.Seal(`{"token":"s3cret"}`)
	if err := h.db.SaveExtension(ctx, &db.Extension{ID: "acme.test", Version: "1.0.0", Manifest: man, Source: "upload", Enabled: true,
		SettingsEnc: sealed, Token: "tok", Status: "running"}); err != nil {
		t.Fatal(err)
	}
	api := h.srv.ExtensionAPI("acme.test")
	call := func(method, path, user string) (int, string) {
		r := httptest.NewRequest(method, path, nil)
		if user != "" {
			r.Header.Set("X-OpenPlex-User", user)
		}
		w := httptest.NewRecorder()
		api.ServeHTTP(w, r)
		return w.Code, w.Body.String()
	}
	if c, body := call("GET", "/api/v1/domains", ""); c != 200 || !strings.Contains(body, "carla.test") || !strings.Contains(body, "dani.test") {
		t.Errorf("list domains: %d %s", c, body)
	}
	if c, body := call("GET", "/api/v1/domains", itoa(carla.ID)); c != 200 || !strings.Contains(body, "carla.test") || strings.Contains(body, "dani.test") {
		t.Errorf("acting as carla: %d %s", c, body)
	}
	for _, rq := range [][2]string{{"POST", "/api/v1/domains"}, {"GET", "/api/v1/users"}, {"GET", "/api/v1/audit"},
		{"POST", "/api/v1/api-keys"}, {"GET", "/api/v1/domains/1/files"}, {"GET", "/api/v1/backups/recovery-key"}} {
		if c, _ := call(rq[0], rq[1], ""); c != 403 {
			t.Errorf("%s %s: %d, want 403", rq[0], rq[1], c)
		}
	}
	if c, body := call("GET", "/api/v1/extension", ""); c != 200 || !strings.Contains(body, "s3cret") {
		t.Errorf("self: %d %s", c, body)
	}

	// Dashboard side: secrets never come back; customers get the site tab
	// for their own site only, admins the page.
	admin, ct := h.login("admin").AccessToken, h.login("carla").AccessToken
	var list []map[string]any
	if c := h.do("GET", "/extensions", admin, nil, &list); c != 200 || len(list) != 1 {
		t.Fatalf("list: %d %v", c, list)
	}
	if b, _ := json.Marshal(list); strings.Contains(string(b), "s3cret") {
		t.Error("a secret setting reached the dashboard")
	}
	if c := h.do("GET", "/extensions", ct, nil, nil); c != 403 {
		t.Errorf("customer listing extensions: %d", c)
	}
	var out map[string]string
	if c := h.do("POST", "/extensions/acme.test/session", ct, map[string]any{"page": "main"}, nil); c != 404 {
		t.Errorf("customer opening an admin page: %d", c)
	}
	doms, _ := h.db.ListDomains(ctx, db.DomainFilter{})
	ids := map[string]int64{}
	for _, d := range doms {
		ids[d.Name] = d.ID
	}
	if c := h.do("POST", "/extensions/acme.test/session", ct, map[string]any{"site_tab": "dns", "domain_id": ids["dani.test"]}, nil); c != 404 {
		t.Errorf("site tab of someone else's site: %d", c)
	}
	if c := h.do("POST", "/extensions/acme.test/session", ct, map[string]any{"site_tab": "dns", "domain_id": ids["carla.test"]}, &out); c != 200 || !strings.HasPrefix(out["url"], "/ext/acme.test/s/") {
		t.Errorf("own site tab: %d %v", c, out)
	}
	if c := h.do("POST", "/extensions/acme.test/session", admin, map[string]any{"page": "main"}, &out); c != 200 {
		t.Errorf("admin page: %d", c)
	}
	// Without Docker the page can't be served, but the session is checked first.
	res, _ := http.Get(h.ts.URL + "/ext/acme.test/s/forged/")
	res.Body.Close()
	if res.StatusCode != 403 {
		t.Errorf("forged session: %d", res.StatusCode)
	}
}

func TestExtensionEdgePolicy(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	d := &db.Domain{Name: "shop.test", OwnerID: 1, Kind: db.KindPrimary, Runtime: "static", DocumentRoot: "public", MemoryMB: 128, CPUs: 1}
	if err := h.db.CreateDomain(ctx, d); err != nil {
		t.Fatal(err)
	}
	mk := func(id string, perms string) {
		man := `{"schema":1,"id":"` + id + `","name":"T","version":"1.0.0","publisher":{"name":"A"},
			"permissions":[` + perms + `],"backend":{"image":"a/t:1","port":8080}}`
		if err := h.db.SaveExtension(ctx, &db.Extension{ID: id, Version: "1.0.0", Manifest: man, Source: "upload", Enabled: true, Token: "t", Status: "running"}); err != nil {
			t.Fatal(err)
		}
	}
	mk("acme.edge", `"edge:write"`)
	mk("acme.noedge", `"domains:read"`)
	call := func(id, method, body string) (int, string) {
		r := httptest.NewRequest(method, "/api/v1/extension/edge", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		h.srv.ExtensionAPI(id).ServeHTTP(w, r)
		return w.Code, w.Body.String()
	}
	if c, _ := call("acme.noedge", "PUT", `{}`); c != 403 {
		t.Fatalf("without edge:write: %d", c)
	}
	for _, bad := range []string{
		`{"trusted_proxies":["0.0.0.0/0"]}`,
		`{"trusted_proxies":["10.0.0.0/8"],"client_ip_headers":["Bad Header"]}`,
		`{"client_ip_headers":["CF-Connecting-IP"]}`,
		`{"restrict":[{"host":"other.test","allow":["173.245.48.0/20"]}]}`,
		`{"restrict":[{"host":"shop.test","allow":[]}]}`,
		`{"restrict":[{"host":"shop.test","allow":["2000::/3"]}]}`,
	} {
		if c, body := call("acme.edge", "PUT", bad); c != 400 {
			t.Errorf("%s: %d %s", bad, c, body)
		}
	}
	c, body := call("acme.edge", "PUT", `{"trusted_proxies":["173.245.48.1/20","2400:cb00::/32","173.245.48.0/20"],
		"client_ip_headers":["CF-Connecting-IP"],"restrict":[{"host":"SHOP.test","allow":["173.245.48.0/20","198.51.100.7"]}]}`)
	if c != 200 || !strings.Contains(body, `"trusted_proxies":["173.245.48.0/20","2400:cb00::/32"]`) || !strings.Contains(body, `"198.51.100.7/32"`) || !strings.Contains(body, `"host":"shop.test"`) {
		t.Fatalf("put: %d %s", c, body)
	}
	if c, body := call("acme.edge", "GET", ""); c != 200 || !strings.Contains(body, "shop.test") {
		t.Fatalf("get: %d %s", c, body)
	}
	pols, err := h.db.EdgePolicies(ctx)
	if err != nil || len(pols) != 1 || len(pols["acme.edge"].Restrict) != 1 {
		t.Fatalf("stored: %v %v", pols, err)
	}
	entries, _ := h.db.ListAudit(ctx, 0, 50)
	found := false
	for _, e := range entries {
		found = found || (e.Action == "extension.edge_updated" && strings.Contains(e.Detail, "by extension acme.edge"))
	}
	if !found {
		t.Error("no audit entry")
	}
	if c, _ := call("acme.edge", "PUT", `{}`); c != 200 {
		t.Fatal("clear")
	}
	if pols, _ := h.db.EdgePolicies(ctx); len(pols) != 0 {
		t.Fatalf("not cleared: %v", pols)
	}
}
