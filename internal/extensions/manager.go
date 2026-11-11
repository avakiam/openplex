package extensions

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"time"

	"openplex.dev/openplex/internal/config"
	"openplex.dev/openplex/internal/db"
	"openplex.dev/openplex/internal/docker"
	"openplex.dev/openplex/internal/secrets"
)

// Runtime runs extension containers (the Docker orchestrator).
type Runtime interface {
	EnsureExtension(ctx context.Context, s docker.ExtensionSpec) error
	RemoveExtension(ctx context.Context, id string) error
	ExtensionAddr(ctx context.Context, id string) (string, error)
	ExtensionIDs(ctx context.Context) ([]string, error)
}

// Status values of an installed extension.
const (
	StatusStarting = "starting"
	StatusRunning  = "running"
	StatusDisabled = "disabled"
	StatusError    = "error"
)

// ErrUnavailable: extensions need Docker.
var ErrUnavailable = errors.New("extensions need Docker, which is unavailable on this server")

// Manager installs, configures and runs extensions.
type Manager struct {
	cfg     *config.Config
	db      *db.DB
	box     *secrets.Box
	rt      Runtime // nil without Docker
	log     *slog.Logger
	version string
	trusted map[string]ed25519.PublicKey
	client  *http.Client
	baseCtx context.Context

	mu       sync.Mutex
	api      func(id string) http.Handler
	socks    map[string]*http.Server
	pending  map[string]*Pending
	sessions map[string]*Session
	catalog  []CatalogEntry
	catalogT time.Time
}

// New returns a manager; rt may be nil (Docker unavailable).
func New(ctx context.Context, cfg *config.Config, store *db.DB, box *secrets.Box, rt Runtime, version string, log *slog.Logger) *Manager {
	return &Manager{
		cfg: cfg, db: store, box: box, rt: rt, log: log, version: version, trusted: TrustedKeys(), baseCtx: ctx,
		client:   &http.Client{Timeout: 2 * time.Minute},
		socks:    map[string]*http.Server{},
		pending:  map[string]*Pending{},
		sessions: map[string]*Session{},
	}
}

// SetAPI sets the handler serving an extension's panel socket.
func (m *Manager) SetAPI(fn func(id string) http.Handler) {
	m.mu.Lock()
	m.api = fn
	m.mu.Unlock()
}

func (m *Manager) root() string             { return filepath.Join(m.cfg.DataDir, "extensions") }
func (m *Manager) pkgDir(id string) string  { return filepath.Join(m.root(), id, "pkg") }
func (m *Manager) dataDir(id string) string { return filepath.Join(m.root(), id, "data") }
func (m *Manager) sockDir(id string) string { return filepath.Join(m.cfg.RunDir(), "ext", id) }

// Manifest returns the parsed manifest of an installed extension.
func (m *Manager) Manifest(e *db.Extension) (*Manifest, error) { return Parse([]byte(e.Manifest)) }

// ---------- installing ----------

// Pending is a package received but not installed yet: the administrator
// reviews it (publisher, signature, permissions) and confirms.
type Pending struct {
	Token     string    `json:"token"`
	Manifest  *Manifest `json:"manifest"`
	Signer    string    `json:"signer"`
	Source    string    `json:"source"`
	Installed string    `json:"installed_version,omitempty"` // version being replaced
	license   string
	dir       string
	exp       time.Time
}

// Receive reads a package (an upload or a marketplace download) for review.
func (m *Manager) Receive(ctx context.Context, r io.Reader, source, license string) (*Pending, error) {
	if m.rt == nil {
		return nil, ErrUnavailable
	}
	in := filepath.Join(m.root(), ".incoming")
	if err := os.MkdirAll(in, 0o700); err != nil {
		return nil, err
	}
	tok := randHex(16)
	dir := filepath.Join(in, tok)
	man, signer, err := Open(r, dir, m.trusted)
	if err != nil {
		return nil, err
	}
	if !man.Compatible(m.version) {
		_ = os.RemoveAll(dir)
		return nil, fmt.Errorf("%s %s needs OpenPlex %s (this server runs %s)", man.Name.In("en"), man.Version, man.OpenPlex, m.version)
	}
	p := &Pending{Token: tok, Manifest: man, Signer: signer, Source: source, license: license, dir: dir, exp: time.Now().Add(30 * time.Minute)}
	if e, err := m.db.GetExtension(ctx, man.ID); err == nil {
		p.Installed = e.Version
	}
	m.mu.Lock()
	for k, v := range m.pending {
		if time.Now().After(v.exp) {
			_ = os.RemoveAll(v.dir)
			delete(m.pending, k)
		}
	}
	m.pending[tok] = p
	m.mu.Unlock()
	return p, nil
}

// Install installs (or upgrades) a received package and starts it.
func (m *Manager) Install(ctx context.Context, token string) (*db.Extension, error) {
	m.mu.Lock()
	p := m.pending[token]
	delete(m.pending, token)
	m.mu.Unlock()
	if p == nil || time.Now().After(p.exp) {
		return nil, errors.New("this upload has expired; upload the package again")
	}
	defer os.RemoveAll(p.dir)
	man := p.Manifest
	raw, err := os.ReadFile(filepath.Join(p.dir, "pkg", ManifestFile))
	if err != nil {
		return nil, err
	}
	e, err := m.db.GetExtension(ctx, man.ID)
	switch {
	case errors.Is(err, db.ErrNotFound):
		e = &db.Extension{ID: man.ID, Enabled: true, Token: randHex(24)}
		if e.SettingsEnc, err = m.seal(defaults(man)); err != nil {
			return nil, err
		}
	case err != nil:
		return nil, err
	}
	if p.license != "" {
		if e.LicenseEnc, err = m.box.Seal(p.license); err != nil {
			return nil, err
		}
	}
	// Swap the unpacked files in.
	if err := os.MkdirAll(filepath.Join(m.root(), man.ID), 0o700); err != nil {
		return nil, err
	}
	old := m.pkgDir(man.ID) + ".old"
	_ = os.RemoveAll(old)
	if err := os.Rename(m.pkgDir(man.ID), old); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if err := os.Rename(filepath.Join(p.dir, "pkg"), m.pkgDir(man.ID)); err != nil {
		_ = os.Rename(old, m.pkgDir(man.ID))
		return nil, err
	}
	_ = os.RemoveAll(old)
	e.Version, e.Manifest, e.Source, e.Signer = man.Version, string(raw), p.Source, p.Signer
	e.Status, e.StatusMessage = StatusStarting, ""
	if !e.Enabled {
		e.Status = StatusDisabled
	}
	if err := m.db.SaveExtension(ctx, e); err != nil {
		return nil, err
	}
	m.log.Info("extension installed", "id", e.ID, "version", e.Version, "signer", e.Signer, "source", e.Source)
	if e.Enabled {
		m.startAsync(e.ID, true)
	}
	return e, nil
}

// SetEnabled turns an extension on or off.
func (m *Manager) SetEnabled(ctx context.Context, id string, on bool) (*db.Extension, error) {
	e, err := m.db.GetExtension(ctx, id)
	if err != nil {
		return nil, err
	}
	e.Enabled = on
	if on {
		e.Status = StatusStarting
	} else {
		e.Status = StatusDisabled
	}
	e.StatusMessage = ""
	if err := m.db.SaveExtension(ctx, e); err != nil {
		return nil, err
	}
	if on {
		m.startAsync(id, false)
	} else if err := m.stop(ctx, id); err != nil {
		return nil, err
	}
	return e, nil
}

// Uninstall stops an extension and deletes it with its data.
func (m *Manager) Uninstall(ctx context.Context, id string) error {
	if _, err := m.db.GetExtension(ctx, id); err != nil {
		return err
	}
	if err := m.stop(ctx, id); err != nil {
		return err
	}
	if err := os.RemoveAll(filepath.Join(m.root(), id)); err != nil {
		return err
	}
	_ = os.RemoveAll(m.sockDir(id))
	m.mu.Lock()
	for k, s := range m.sessions {
		if s.Extension == id {
			delete(m.sessions, k)
		}
	}
	m.mu.Unlock()
	m.log.Info("extension removed", "id", id)
	return m.db.DeleteExtension(ctx, id)
}

// Reconcile starts every enabled extension and removes containers of
// extensions that are no longer installed or enabled.
func (m *Manager) Reconcile(ctx context.Context) {
	if m.rt == nil {
		return
	}
	list, err := m.db.ListExtensions(ctx)
	if err != nil {
		m.log.Error("list extensions", "err", err)
		return
	}
	want := map[string]bool{}
	for _, e := range list {
		if e.Enabled {
			want[e.ID] = true
			m.start(ctx, e.ID, false)
		}
	}
	ids, err := m.rt.ExtensionIDs(ctx)
	if err != nil {
		m.log.Error("list extension containers", "err", err)
		return
	}
	for _, id := range ids {
		if !want[id] {
			if err := m.rt.RemoveExtension(ctx, id); err != nil {
				m.log.Warn("remove extension container", "id", id, "err", err)
			}
		}
	}
}

// Close stops serving the panel sockets (containers keep running).
func (m *Manager) Close() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, s := range m.socks {
		_ = s.Close()
		delete(m.socks, id)
	}
}

func (m *Manager) startAsync(id string, installed bool) {
	go m.start(m.baseCtx, id, installed)
}

func (m *Manager) start(ctx context.Context, id string, installed bool) {
	err := m.run(ctx, id)
	if err != nil {
		m.log.Error("extension failed to start", "id", id, "err", err)
		_ = m.db.SetExtensionStatus(ctx, id, StatusError, err.Error())
		return
	}
	_ = m.db.SetExtensionStatus(ctx, id, StatusRunning, "")
	if installed {
		m.deliverTo(ctx, id, Event{Type: "installed"})
	}
}

func (m *Manager) run(ctx context.Context, id string) error {
	if m.rt == nil {
		return ErrUnavailable
	}
	e, err := m.db.GetExtension(ctx, id)
	if err != nil {
		return err
	}
	man, err := m.Manifest(e)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(m.dataDir(id), 0o700); err != nil {
		return err
	}
	_ = os.Chown(m.dataDir(id), e.UID, e.UID)
	if err := m.serveSocket(id, e.UID); err != nil {
		return fmt.Errorf("panel socket: %w", err)
	}
	spec := docker.ExtensionSpec{
		ID: id, Image: man.Backend.Image, Port: man.Backend.Port, MemoryMB: man.Backend.MemoryMB, CPUs: man.Backend.CPUs,
		UID: e.UID, DataDir: m.dataDir(id), SocketDir: m.sockDir(id),
		Env: []string{"OPENPLEX_EXTENSION=" + id, "OPENPLEX_EXTENSION_VERSION=" + e.Version},
	}
	if a := man.Backend.Archives[runtime.GOARCH]; a != "" {
		spec.Archive = filepath.Join(m.pkgDir(id), filepath.FromSlash(a))
	}
	return m.rt.EnsureExtension(ctx, spec)
}

func (m *Manager) stop(ctx context.Context, id string) error {
	m.mu.Lock()
	if s := m.socks[id]; s != nil {
		_ = s.Close()
		delete(m.socks, id)
	}
	m.mu.Unlock()
	if m.rt == nil {
		return nil
	}
	return m.rt.RemoveExtension(ctx, id)
}

// serveSocket serves the panel API to the extension on its own unix socket,
// mounted in its container at /run/openplex/panel.sock.
func (m *Manager) serveSocket(id string, uid int) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.socks[id] != nil {
		return nil
	}
	if m.api == nil {
		return errors.New("panel API not ready")
	}
	dir := m.sockDir(id)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	_ = os.Chown(dir, uid, uid)
	sock := filepath.Join(dir, "panel.sock")
	_ = os.Remove(sock)
	l, err := net.Listen("unix", sock)
	if err != nil {
		return err
	}
	_ = os.Chown(sock, uid, uid)
	_ = os.Chmod(sock, 0o600)
	srv := &http.Server{Handler: m.api(id), ReadHeaderTimeout: 10 * time.Second,
		BaseContext: func(net.Listener) context.Context { return m.baseCtx }}
	m.socks[id] = srv
	go func() {
		if err := srv.Serve(l); err != nil && !errors.Is(err, http.ErrServerClosed) {
			m.log.Warn("extension socket stopped", "id", id, "err", err)
		}
	}()
	return nil
}

// ---------- settings ----------

func defaults(man *Manifest) map[string]string {
	v := map[string]string{}
	for _, s := range man.Settings {
		if s.Default != "" {
			v[s.Key] = s.Default
		}
	}
	return v
}

func (m *Manager) seal(v map[string]string) (string, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	return m.box.Seal(string(b))
}

// Settings returns the stored values of e.
func (m *Manager) Settings(e *db.Extension) map[string]string {
	v := map[string]string{}
	if e.SettingsEnc == "" {
		return v
	}
	if s, err := m.box.Open(e.SettingsEnc); err == nil {
		_ = json.Unmarshal([]byte(s), &v)
	}
	return v
}

// License returns e's licence key ("" if none).
func (m *Manager) License(e *db.Extension) string {
	if e.LicenseEnc == "" {
		return ""
	}
	s, _ := m.box.Open(e.LicenseEnc)
	return s
}

// PublicSettings is what the dashboard may see: secrets are never returned,
// only whether they are set.
func (m *Manager) PublicSettings(e *db.Extension) (values map[string]string, secretsSet []string) {
	man, err := m.Manifest(e)
	values = map[string]string{}
	if err != nil {
		return values, nil
	}
	all := m.Settings(e)
	for _, s := range man.Settings {
		v := all[s.Key]
		if s.Type == "secret" {
			if v != "" {
				secretsSet = append(secretsSet, s.Key)
			}
			continue
		}
		values[s.Key] = v
	}
	return values, secretsSet
}

// SaveSettings validates and stores new values. A secret left out keeps its
// value; an empty string clears it.
func (m *Manager) SaveSettings(ctx context.Context, id string, in map[string]string) error {
	e, err := m.db.GetExtension(ctx, id)
	if err != nil {
		return err
	}
	man, err := m.Manifest(e)
	if err != nil {
		return err
	}
	old := m.Settings(e)
	out := map[string]string{}
	for k := range in {
		if !slices.ContainsFunc(man.Settings, func(s Setting) bool { return s.Key == k }) {
			return fmt.Errorf("unknown setting %q", k)
		}
	}
	for _, s := range man.Settings {
		v, ok := in[s.Key]
		if !ok {
			v = old[s.Key]
		}
		v = strings.TrimSpace(v)
		if len(v) > 8192 {
			return fmt.Errorf("%s is too long", s.Label.In("en"))
		}
		switch s.Type {
		case "boolean":
			if v != "" && v != "true" && v != "false" {
				return fmt.Errorf("%s must be true or false", s.Label.In("en"))
			}
		case "select":
			if v != "" && !slices.ContainsFunc(s.Options, func(o Option) bool { return o.Value == v }) {
				return fmt.Errorf("%s: choose one of the options", s.Label.In("en"))
			}
		}
		if s.Required && v == "" {
			return fmt.Errorf("%s is required", s.Label.In("en"))
		}
		if v != "" {
			out[s.Key] = v
		}
	}
	if e.SettingsEnc, err = m.seal(out); err != nil {
		return err
	}
	if err := m.db.SaveExtension(ctx, e); err != nil {
		return err
	}
	m.emitTo(e, Event{Type: "settings.changed"})
	return nil
}

// SetLicense stores (or clears) the licence key.
func (m *Manager) SetLicense(ctx context.Context, id, key string) error {
	e, err := m.db.GetExtension(ctx, id)
	if err != nil {
		return err
	}
	e.LicenseEnc = ""
	if key = strings.TrimSpace(key); key != "" {
		if e.LicenseEnc, err = m.box.Seal(key); err != nil {
			return err
		}
	}
	if err := m.db.SaveExtension(ctx, e); err != nil {
		return err
	}
	m.emitTo(e, Event{Type: "settings.changed"})
	return nil
}

// ---------- events ----------

// Event is POSTed as JSON to the extension's /openplex/events.
type Event struct {
	Type string         `json:"type"`
	Time time.Time      `json:"time"`
	Data map[string]any `json:"data,omitempty"`
}

// Emit sends an event to every enabled extension subscribed to it, in the
// background.
func (m *Manager) Emit(typ string, data map[string]any) {
	if m.rt == nil {
		return
	}
	go func() {
		list, err := m.db.ListExtensions(m.baseCtx)
		if err != nil {
			return
		}
		for _, e := range list {
			m.emitTo(e, Event{Type: typ, Data: data})
		}
	}()
}

func (m *Manager) emitTo(e *db.Extension, ev Event) {
	if m.rt == nil || !e.Enabled {
		return
	}
	man, err := m.Manifest(e)
	if err != nil || !slices.Contains(man.Events, ev.Type) {
		return
	}
	go m.deliverTo(m.baseCtx, e.ID, ev)
}

func (m *Manager) deliverTo(ctx context.Context, id string, ev Event) {
	e, err := m.db.GetExtension(ctx, id)
	if err != nil {
		return
	}
	if man, err := m.Manifest(e); err != nil || !slices.Contains(man.Events, ev.Type) {
		return
	}
	ev.Time = time.Now().UTC()
	body, _ := json.Marshal(ev)
	for attempt := 0; attempt < 5; attempt++ { // a freshly started container may not listen yet
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return
			case <-time.After(time.Duration(attempt) * 2 * time.Second):
			}
		}
		addr, err := m.rt.ExtensionAddr(ctx, id)
		if err != nil {
			continue
		}
		rctx, cancel := context.WithTimeout(ctx, 15*time.Second)
		req, _ := http.NewRequestWithContext(rctx, http.MethodPost, "http://"+addr+"/openplex/events", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-OpenPlex-Token", e.Token)
		res, err := m.client.Do(req)
		cancel()
		if err != nil {
			continue
		}
		res.Body.Close()
		if res.StatusCode >= 500 {
			continue
		}
		return
	}
	m.log.Warn("extension did not accept an event", "id", id, "event", ev.Type)
}

// Addr is where the extension's backend listens (ip:port).
func (m *Manager) Addr(ctx context.Context, id string) (string, error) {
	if m.rt == nil {
		return "", ErrUnavailable
	}
	return m.rt.ExtensionAddr(ctx, id)
}

// ---------- page sessions ----------

// Session lets one signed-in user load one extension's pages.
type Session struct {
	Extension string
	UserID    int64
	DomainID  int64  // site tabs
	Area      string // website area a shared user needs (site tabs)
	exp       time.Time
}

const sessionTTL = 8 * time.Hour

// NewSession starts a page session and returns its token.
func (m *Manager) NewSession(ext string, userID, domainID int64, area string) string {
	tok := randHex(24)
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now()
	for k, s := range m.sessions {
		if now.After(s.exp) {
			delete(m.sessions, k)
		}
	}
	if len(m.sessions) > 20000 {
		m.sessions = map[string]*Session{}
	}
	m.sessions[tok] = &Session{Extension: ext, UserID: userID, DomainID: domainID, Area: area, exp: now.Add(sessionTTL)}
	return tok
}

// LookupSession returns a live session of ext (extending it).
func (m *Manager) LookupSession(ext, tok string) *Session {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.sessions[tok]
	if s == nil || s.Extension != ext || time.Now().After(s.exp) {
		return nil
	}
	s.exp = time.Now().Add(sessionTTL)
	cp := *s
	return &cp
}

func randHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
