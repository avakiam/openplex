package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/pquerna/otp/totp"

	"openplex.dev/openplex/internal/auth"
	"openplex.dev/openplex/internal/config"
	"openplex.dev/openplex/internal/cron"
	"openplex.dev/openplex/internal/db"
	"openplex.dev/openplex/internal/monitor"
	"openplex.dev/openplex/internal/secrets"
)

type harness struct {
	t   *testing.T
	ts  *httptest.Server
	db  *db.DB
	cfg *config.Config
	box *secrets.Box
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
	srv := New(Options{Config: cfg, DB: store, Monitor: monitor.New(time.Second, 10, dir, log), Logger: log, BaseContext: context.Background(), Box: box, Cron: cron.New(store, nil, log)})
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	h := &harness{t: t, ts: ts, db: store, cfg: cfg, box: box}
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
	// An allowed server reaches the proxy (502: no Adminer running in tests).
	res, _ = http.Get(h.ts.URL + "/tools/adminer/?server=mariadb")
	res.Body.Close()
	if res.StatusCode != 502 {
		t.Errorf("allowed server: %d, want 502 from proxy", res.StatusCode)
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
