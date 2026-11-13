package api

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	"openplex.dev/openplex/internal/alerts"
	"openplex.dev/openplex/internal/db"
)

// files:read and files:write open the website file endpoints to an
// extension; alerts:send lets it e-mail the alert recipients, once per key.
func TestExtensionFilesAndAlerts(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	d := &db.Domain{Name: "shop.test", OwnerID: 1, Kind: db.KindPrimary, Runtime: "static", DocumentRoot: "public", MemoryMB: 128, CPUs: 1}
	if err := h.db.CreateDomain(ctx, d); err != nil {
		t.Fatal(err)
	}
	mk := func(id, perms string) {
		man := `{"schema":1,"id":"` + id + `","name":"Guard","version":"1.0.0","publisher":{"name":"A"},
			"permissions":[` + perms + `],"backend":{"image":"a/t:1","port":8080}}`
		if err := h.db.SaveExtension(ctx, &db.Extension{ID: id, Version: "1.0.0", Manifest: man, Source: "upload", Enabled: true, Token: "t", Status: "running"}); err != nil {
			t.Fatal(err)
		}
	}
	mk("acme.read", `"files:read"`)
	mk("acme.write", `"files:read","files:write","alerts:send"`)
	call := func(id, method, path, body string) (int, string) {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		h.srv.ExtensionAPI(id).ServeHTTP(w, r)
		return w.Code, w.Body.String()
	}
	files := "/api/v1/domains/" + itoa(d.ID) + "/files"
	for _, c := range []struct {
		id, method, path string
		denied           bool
	}{
		{"acme.read", "GET", files, false},
		{"acme.read", "GET", files + "/download?path=public/index.html", false},
		{"acme.read", "POST", files + "/delete", true},
		{"acme.read", "PUT", files + "/content", true},
		{"acme.write", "POST", files + "/delete", false},
		{"acme.write", "POST", files + "/chmod", false},
		{"acme.write", "POST", files + "/extract", true}, // not part of files:write
		{"acme.write", "POST", files + "/compress", true},
	} {
		code, body := call(c.id, c.method, c.path, `{"paths":["public/x.php"]}`)
		if denied := code == 403; denied != c.denied {
			t.Errorf("%s %s %s: %d %s", c.id, c.method, c.path, code, body)
		}
	}

	alert := `{"key":"shop.test:abc","subject":"Malware found on shop.test","body":"public/x.php"}`
	if c, _ := call("acme.read", "POST", "/api/v1/extension/alert", alert); c != 403 {
		t.Errorf("alert without alerts:send: %d", c)
	}
	var sent []string
	h.srv.alerts = alerts.New(alerts.Options{DB: h.db, Server: "srv.test", LocalSend: func(_ context.Context, _ string, to []string, msg []byte) error {
		sent = append(sent, string(msg))
		return nil
	}})
	if c, body := call("acme.write", "POST", "/api/v1/extension/alert", alert); c != 200 || !strings.Contains(body, `"sent":false`) {
		t.Errorf("alerts off: %d %s", c, body)
	}
	if err := h.db.SaveAlertSettings(ctx, &db.AlertSettings{Enabled: true, Recipients: []string{"ops@example.com"}, Language: "es", Transport: "local", SMTPSecurity: "starttls", Events: []string{alerts.KindSiteDown}}); err != nil {
		t.Fatal(err)
	}
	if c, body := call("acme.write", "POST", "/api/v1/extension/alert", alert); c != 200 || !strings.Contains(body, `"sent":true`) {
		t.Fatalf("send: %d %s", c, body)
	}
	if c, body := call("acme.write", "POST", "/api/v1/extension/alert", alert); c != 200 || !strings.Contains(body, `"sent":false`) {
		t.Errorf("same key again: %d %s", c, body)
	}
	if len(sent) != 1 || !strings.Contains(sent[0], "Malware found on shop.test") || !strings.Contains(sent[0], "Enviado por la extensi") {
		t.Errorf("e-mails: %q", sent)
	}
	for _, bad := range []string{`{"key":"","subject":"x"}`, `{"key":"k","subject":"two\nlines"}`, `{"key":"k","subject":""}`} {
		if c, _ := call("acme.write", "POST", "/api/v1/extension/alert", bad); c != 400 {
			t.Errorf("%s: %d", bad, c)
		}
	}
}
