package api

import (
	"bytes"
	"net/http"
	"strings"
	"testing"

	"openplex.dev/openplex/internal/db"
)

func TestGitDeploy(t *testing.T) {
	h := newHarness(t)
	h.mkUser("carla", db.RoleCustomer, nil)
	h.mkUser("dani", db.RoleCustomer, nil)
	carla, dani := h.login("carla").AccessToken, h.login("dani").AccessToken
	var site db.Domain
	if c := h.do("POST", "/domains", carla, map[string]any{"name": "git.test"}, &site); c != 201 {
		t.Fatalf("create site: %d", c)
	}
	base := "/domains/" + itoa(site.ID) + "/git"
	ok := map[string]any{"repo_url": "git@github.com:carla/site.git", "branch": "main", "target": "public", "auto_deploy": true}
	for _, bad := range []map[string]any{
		{"repo_url": "file:///etc", "branch": "main"},
		{"repo_url": "https://user:secret@github.com/a/b.git", "branch": "main"},
		{"repo_url": "git@github.com:a/b.git", "branch": "-x"},
		{"repo_url": "git@github.com:a/b.git", "branch": "main", "target": "../other"},
	} {
		if c := h.do("PUT", base, carla, bad, nil); c != 400 {
			t.Errorf("%v: %d, want 400", bad, c)
		}
	}
	var g gitView
	if c := h.do("PUT", base, carla, ok, &g); c != 201 || !strings.HasPrefix(g.PublicKey, "ssh-ed25519 ") || g.WebhookPath == "" {
		t.Fatalf("create: %d %+v", c, g)
	}
	if c := h.do("GET", base, dani, nil, nil); c != 404 {
		t.Fatalf("another customer reads it: %d", c)
	}

	hook := func(path, event, body string) int {
		req, _ := http.NewRequest("POST", h.ts.URL+path, bytes.NewReader([]byte(body)))
		if event != "" {
			req.Header.Set("X-GitHub-Event", event)
		}
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		return res.StatusCode
	}
	wrong := strings.TrimSuffix(g.WebhookPath, g.WebhookPath[strings.LastIndex(g.WebhookPath, "/")+1:]) + "nope"
	if c := hook(wrong, "", `{}`); c != 404 {
		t.Fatalf("wrong token: %d", c)
	}
	if c := hook(g.WebhookPath, "ping", `{}`); c != 200 {
		t.Fatalf("ping: %d", c)
	}
	if c := hook(g.WebhookPath, "push", `{"ref":"refs/heads/other"}`); c != 200 {
		t.Fatalf("other branch: %d", c)
	}
	// No Docker in tests: a push to the branch is accepted but cannot run.
	if c := hook(g.WebhookPath, "push", `{"ref":"refs/heads/main"}`); c != 503 {
		t.Fatalf("push without Docker: %d", c)
	}

	// Rotating the token invalidates the old webhook address.
	old := g.WebhookPath
	ok["rotate_webhook"], ok["auto_deploy"] = true, false
	if c := h.do("PUT", base, carla, ok, &g); c != 200 || g.WebhookPath == old {
		t.Fatalf("rotate: %d", c)
	}
	if c := hook(old, "push", `{}`); c != 404 {
		t.Fatalf("old token: %d", c)
	}
	if c := hook(g.WebhookPath, "push", `{}`); c != 200 {
		t.Fatalf("auto deploy off: %d", c)
	}
	if c := h.do("DELETE", base, carla, nil, nil); c != 204 {
		t.Fatalf("delete: %d", c)
	}
}
