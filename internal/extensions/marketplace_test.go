package extensions

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"openplex.dev/openplex/internal/config"
	"openplex.dev/openplex/internal/db"
	"openplex.dev/openplex/internal/docker"
	"openplex.dev/openplex/internal/secrets"
)

type fakeRuntime struct{}

func (fakeRuntime) EnsureExtension(context.Context, docker.ExtensionSpec) error { return nil }
func (fakeRuntime) RemoveExtension(context.Context, string) error               { return nil }
func (fakeRuntime) ExtensionAddr(context.Context, string) (string, error)       { return "", nil }
func (fakeRuntime) ExtensionIDs(context.Context) ([]string, error)              { return nil, nil }

// Updating a paid extension uses the licence it was installed with.
func TestDownloadUpdateUsesStoredLicence(t *testing.T) {
	ctx := context.Background()
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ManifestFile), []byte(goodManifest), 0o644); err != nil {
		t.Fatal(err)
	}
	_ = os.MkdirAll(filepath.Join(dir, "images"), 0o755)
	_ = os.WriteFile(filepath.Join(dir, "images", "amd64.tar"), []byte("image"), 0o644)
	var pkg bytes.Buffer
	if _, err := Pack(dir, &pkg, "test-key", priv); err != nil {
		t.Fatal(err)
	}

	const key = "OPX-AAAAA-BBBBB-CCCCC-DDDDD"
	var srv *httptest.Server
	srv = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/extensions":
			_ = json.NewEncoder(w).Encode(map[string]any{"extensions": []CatalogEntry{{
				ID: "avakiam.test", Version: "1.2.3", Name: Text{"en": "Test"}, Publisher: Publisher{Name: "Avakiam"},
				Price: &Price{Amount: "9.00", Currency: "EUR"}, Download: srv.URL + "/download/avakiam.test/1.2.3.opx",
			}}})
		case "/download/avakiam.test/1.2.3.opx":
			if r.Header.Get("X-License-Key") != key {
				w.WriteHeader(http.StatusPaymentRequired)
				return
			}
			_, _ = w.Write(pkg.Bytes())
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	store := db.OpenMemory(t)
	box, err := secrets.New(bytes.Repeat([]byte{7}, 32), "extensions")
	if err != nil {
		t.Fatal(err)
	}
	m := New(ctx, &config.Config{DataDir: t.TempDir(), MarketplaceURL: srv.URL}, store, box, fakeRuntime{}, "0.30.1",
		slog.New(slog.NewTextHandler(io.Discard, nil)))
	m.client = srv.Client()
	m.trusted = map[string]ed25519.PublicKey{"test-key": pub}

	// Not installed: a paid extension needs the key.
	if _, err := m.Download(ctx, "avakiam.test", ""); err == nil || !strings.Contains(err.Error(), "paid") {
		t.Fatalf("paid without a key: %v", err)
	}
	// Installed with its licence: the update downloads without asking again.
	sealed, err := box.Seal(key)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SaveExtension(ctx, &db.Extension{ID: "avakiam.test", Version: "1.0.0", Manifest: goodManifest, Source: "marketplace",
		Enabled: true, LicenseEnc: sealed, Token: "t", Status: StatusRunning}); err != nil {
		t.Fatal(err)
	}
	p, err := m.Download(ctx, "avakiam.test", "")
	if err != nil {
		t.Fatalf("update with the stored licence: %v", err)
	}
	defer m.discard(p)
	if p.Installed != "1.0.0" || p.Manifest.Version != "1.2.3" || p.license != key {
		t.Fatalf("pending: installed %q version %q", p.Installed, p.Manifest.Version)
	}
}
