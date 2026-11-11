// Package update finds new stable OpenPlex releases and installs them.
//
// A release on GitHub has one binary per architecture (openplexd-linux-<arch>),
// SHA256SUMS and SHA256SUMS.sig, an Ed25519 signature of SHA256SUMS by a
// release key OpenPlex trusts (keys.go). Installing:
//
//  1. the daemon downloads the binary, checks the signature and the checksum,
//     runs "<binary> version", and writes updates/pending.json;
//  2. it starts a copy of itself, outside its own sandbox, as a transient
//     systemd unit: "openplexd update apply" (Apply below);
//  3. Apply stops the service, backs up the binary and the database, installs
//     the new binary and starts the service;
//  4. the new daemon confirms the update once it serves requests (Confirm);
//  5. without that confirmation within a few minutes, Apply puts the previous
//     binary and database back.
package update

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

// DefaultURL is the latest stable release (GitHub leaves out drafts and
// pre-releases).
const DefaultURL = "https://api.github.com/repos/avakiam/openplex/releases/latest"

const (
	checkEvery    = 12 * time.Hour
	staleAfter    = 15 * time.Minute // an install that hasn't finished by then died
	maxBinarySize = 256 << 20
	maxSmallFile  = 64 << 10
)

// Errors that tell the user why an update can't be installed.
var (
	ErrDevBuild    = errors.New("this is a development build; install releases with the installer")
	ErrNoUpdate    = errors.New("no newer release to install")
	ErrBusy        = errors.New("an update is already being installed")
	ErrNoSystemd   = errors.New("installing updates needs systemd (systemd-run was not found)")
	ErrBadRelease  = errors.New("the release could not be verified")
	ErrUnsupported = errors.New("the release has no binary for this server's architecture")
)

// Release is a published OpenPlex release.
type Release struct {
	Version     string    `json:"version"`
	Name        string    `json:"name"`
	Notes       string    `json:"notes"`
	URL         string    `json:"url"`
	PublishedAt time.Time `json:"published_at"`
	assets      map[string]string
}

// Outcome is how the last installation ended.
type Outcome struct {
	From    string    `json:"from"`
	To      string    `json:"to"`
	Result  string    `json:"result"` // updated | rolled_back | failed
	Message string    `json:"message,omitempty"`
	Time    time.Time `json:"time"`
	Audited bool      `json:"audited,omitempty"`
}

// Status is what the dashboard shows.
type Status struct {
	Current    string    `json:"current"`
	Enabled    bool      `json:"enabled"`
	Latest     *Release  `json:"latest"`
	Available  bool      `json:"available"`
	CanInstall bool      `json:"can_install"`
	Reason     string    `json:"reason,omitempty"`
	CheckedAt  time.Time `json:"checked_at,omitzero"`
	CheckError string    `json:"check_error,omitempty"`
	State      string    `json:"state"` // idle | installing
	Last       *Outcome  `json:"last,omitempty"`
}

// pending is updates/pending.json, shared by the daemon and Apply.
type pending struct {
	From      string    `json:"from"`
	To        string    `json:"to"`
	Binary    string    `json:"binary"` // verified new binary
	Exe       string    `json:"exe"`    // installed binary to replace
	DB        string    `json:"db"`
	Unit      string    `json:"unit"`
	Started   time.Time `json:"started"`
	Confirmed bool      `json:"confirmed"`
}

// Options configure a Manager.
type Options struct {
	Current string // running version ("0.18.0"; anything else is a dev build)
	DataDir string
	DBPath  string
	URL     string // latest-release endpoint
	Unit    string // systemd unit of the daemon
	Enabled func(context.Context) bool
	Log     *slog.Logger
	HTTP    *http.Client
	Keys    map[string]ed25519.PublicKey // trusted release keys; nil = TrustedKeys
	// Start launches Apply outside the daemon (systemd-run); tests replace it.
	Start func(ctx context.Context, runner string, args []string) error
}

// Manager checks for and installs releases.
type Manager struct {
	o Options

	mu         sync.Mutex
	latest     *Release
	checkedAt  time.Time
	checkError string
	installing bool
}

// New returns a Manager.
func New(o Options) *Manager {
	if o.URL == "" {
		o.URL = DefaultURL
	}
	if o.Unit == "" {
		o.Unit = "openplexd.service"
	}
	if o.HTTP == nil {
		o.HTTP = &http.Client{Timeout: 10 * time.Minute}
	}
	if o.Keys == nil {
		o.Keys = TrustedKeys
	}
	if o.Log == nil {
		o.Log = slog.New(slog.DiscardHandler)
	}
	if o.Enabled == nil {
		o.Enabled = func(context.Context) bool { return true }
	}
	if o.Start == nil {
		o.Start = systemdRun
	}
	return &Manager{o: o}
}

func (m *Manager) dir() string { return filepath.Join(m.o.DataDir, "updates") }

// Run checks for releases periodically while checking is enabled.
func (m *Manager) Run(ctx context.Context) {
	t := time.NewTimer(2 * time.Minute)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		if m.o.Enabled(ctx) {
			if err := m.Check(ctx); err != nil {
				m.o.Log.Warn("update check", "err", err)
			}
		}
		t.Reset(checkEvery)
	}
}

var versionRe = regexp.MustCompile(`^v?(\d+)\.(\d+)\.(\d+)$`)

// parseVersion reads "1.2.3" or "v1.2.3"; ok is false for anything else.
func parseVersion(s string) (v [3]int, ok bool) {
	m := versionRe.FindStringSubmatch(strings.TrimSpace(s))
	if m == nil {
		return v, false
	}
	for i := range 3 {
		v[i], _ = strconv.Atoi(m[i+1])
	}
	return v, true
}

// Newer reports whether a is a higher version than b.
func Newer(a, b string) bool {
	va, oka := parseVersion(a)
	vb, okb := parseVersion(b)
	if !oka || !okb {
		return false
	}
	for i := range 3 {
		if va[i] != vb[i] {
			return va[i] > vb[i]
		}
	}
	return false
}

// Check asks for the latest stable release.
func (m *Manager) Check(ctx context.Context) error {
	r, err := m.fetchLatest(ctx)
	m.mu.Lock()
	defer m.mu.Unlock()
	m.checkedAt = time.Now().UTC()
	if err != nil {
		m.checkError = err.Error()
		return err
	}
	m.latest, m.checkError = r, ""
	return nil
}

func (m *Manager) fetchLatest(ctx context.Context) (*Release, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", m.o.URL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "openplexd/"+m.o.Current)
	res, err := m.o.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("checking for updates: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode == http.StatusNotFound {
		return nil, nil // no release published yet
	}
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("checking for updates: %s", res.Status)
	}
	var gh struct {
		TagName     string    `json:"tag_name"`
		Name        string    `json:"name"`
		Body        string    `json:"body"`
		HTMLURL     string    `json:"html_url"`
		PublishedAt time.Time `json:"published_at"`
		Draft       bool      `json:"draft"`
		Prerelease  bool      `json:"prerelease"`
		Assets      []struct {
			Name string `json:"name"`
			URL  string `json:"browser_download_url"`
		} `json:"assets"`
	}
	if err := json.NewDecoder(io.LimitReader(res.Body, 4<<20)).Decode(&gh); err != nil {
		return nil, fmt.Errorf("checking for updates: %w", err)
	}
	if gh.Draft || gh.Prerelease {
		return nil, nil
	}
	if _, ok := parseVersion(gh.TagName); !ok {
		return nil, fmt.Errorf("checking for updates: unexpected release tag %q", gh.TagName)
	}
	r := &Release{Version: strings.TrimPrefix(gh.TagName, "v"), Name: gh.Name, Notes: gh.Body, URL: gh.HTMLURL,
		PublishedAt: gh.PublishedAt, assets: map[string]string{}}
	if len(r.Notes) > 20000 {
		r.Notes = r.Notes[:20000] + "…"
	}
	for _, a := range gh.Assets {
		r.assets[a.Name] = a.URL
	}
	return r, nil
}

// Status returns what the dashboard shows.
func (m *Manager) Status(ctx context.Context) Status {
	m.mu.Lock()
	s := Status{Current: m.o.Current, Latest: m.latest, CheckedAt: m.checkedAt, CheckError: m.checkError, State: "idle"}
	if m.installing {
		s.State = "installing"
	}
	m.mu.Unlock()
	s.Enabled = m.o.Enabled(ctx)
	if p, err := m.readPending(); err == nil && !p.Confirmed && time.Since(p.Started) < staleAfter {
		s.State = "installing"
	}
	s.Last = m.LastOutcome()
	// A development build is told about the latest release, but can't install it.
	s.Available = s.Latest != nil && (Newer(s.Latest.Version, s.Current) || !isRelease(s.Current))
	switch {
	case !s.Available:
	case !isRelease(s.Current):
		s.Reason = ErrDevBuild.Error()
	case s.Latest.assets[binaryName()] == "" || s.Latest.assets["SHA256SUMS"] == "" || s.Latest.assets["SHA256SUMS.sig"] == "":
		s.Reason = ErrUnsupported.Error()
	default:
		s.CanInstall = true
	}
	return s
}

func isRelease(v string) bool { _, ok := parseVersion(v); return ok }

func binaryName() string { return "openplexd-linux-" + runtime.GOARCH }

// LastOutcome is how the last installation ended (nil: none recorded).
func (m *Manager) LastOutcome() *Outcome {
	b, err := os.ReadFile(filepath.Join(m.dir(), "last.json"))
	if err != nil {
		return nil
	}
	var o Outcome
	if json.Unmarshal(b, &o) != nil {
		return nil
	}
	return &o
}

// MarkAudited records that the last outcome reached the audit log.
func (m *Manager) MarkAudited() {
	if o := m.LastOutcome(); o != nil && !o.Audited {
		o.Audited = true
		_ = writeJSON(filepath.Join(m.dir(), "last.json"), o)
	}
}

func (m *Manager) readPending() (*pending, error) {
	return readPending(filepath.Join(m.dir(), "pending.json"))
}

func readPending(path string) (*pending, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var p pending
	return &p, json.Unmarshal(b, &p)
}

func writeJSON(path string, v any) error {
	b, err := json.MarshalIndent(v, "", " ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// Install verifies the release version and hands it to Apply. It returns
// once Apply has been started; the daemon is restarted shortly after.
func (m *Manager) Install(ctx context.Context, version string) error {
	st := m.Status(ctx)
	switch {
	case st.State == "installing":
		return ErrBusy
	case !isRelease(m.o.Current):
		return ErrDevBuild
	case !st.Available || st.Latest.Version != version:
		return ErrNoUpdate
	case !st.CanInstall:
		return ErrUnsupported
	}
	m.mu.Lock()
	if m.installing {
		m.mu.Unlock()
		return ErrBusy
	}
	m.installing = true
	m.mu.Unlock()
	err := m.install(ctx, st.Latest)
	if err != nil {
		m.mu.Lock()
		m.installing = false
		m.mu.Unlock()
		_ = writeJSON(filepath.Join(m.dir(), "last.json"), Outcome{From: m.o.Current, To: version, Result: "failed", Message: err.Error(), Time: time.Now().UTC()})
	}
	return err
}

func (m *Manager) install(ctx context.Context, r *Release) error {
	if err := os.MkdirAll(m.dir(), 0o700); err != nil {
		return err
	}
	sums, err := m.download(ctx, r.assets["SHA256SUMS"], maxSmallFile)
	if err != nil {
		return err
	}
	sig, err := m.download(ctx, r.assets["SHA256SUMS.sig"], maxSmallFile)
	if err != nil {
		return err
	}
	if err := VerifySums(sums, sig, m.o.Keys); err != nil {
		return err
	}
	want, err := sumFor(sums, binaryName())
	if err != nil {
		return err
	}
	bin := filepath.Join(m.dir(), "openplexd-"+r.Version)
	if err := m.downloadFile(ctx, r.assets[binaryName()], bin, want); err != nil {
		return err
	}
	out, err := exec.CommandContext(ctx, bin, "version").Output()
	if err != nil || strings.TrimSpace(string(out)) != r.Version {
		_ = os.Remove(bin)
		return fmt.Errorf("%w: the new binary does not run here or reports another version", ErrBadRelease)
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	if exe, err = filepath.EvalSymlinks(exe); err != nil {
		return err
	}
	// Apply runs from a copy of the running (known good) binary, so it can
	// still put things back if the new one misbehaves.
	runner := filepath.Join(m.dir(), "openplexd-runner")
	if err := copyFile(exe, runner, 0o700); err != nil {
		return err
	}
	p := pending{From: m.o.Current, To: r.Version, Binary: bin, Exe: exe, DB: m.o.DBPath, Unit: m.o.Unit, Started: time.Now().UTC()}
	if err := writeJSON(filepath.Join(m.dir(), "pending.json"), p); err != nil {
		return err
	}
	if err := m.o.Start(ctx, runner, []string{"update", "apply", "--data-dir", m.o.DataDir}); err != nil {
		_ = os.Remove(filepath.Join(m.dir(), "pending.json"))
		return err
	}
	m.o.Log.Info("update started", "from", m.o.Current, "to", r.Version)
	return nil
}

func (m *Manager) download(ctx context.Context, url string, limit int64) ([]byte, error) {
	var buf bytes.Buffer
	if err := m.fetch(ctx, url, &buf, limit); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func (m *Manager) fetch(ctx context.Context, url string, w io.Writer, limit int64) error {
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "openplexd/"+m.o.Current)
	res, err := m.o.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("downloading the release: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("downloading the release: %s", res.Status)
	}
	n, err := io.Copy(w, io.LimitReader(res.Body, limit+1))
	if err != nil {
		return fmt.Errorf("downloading the release: %w", err)
	}
	if n > limit {
		return fmt.Errorf("%w: a file is too large", ErrBadRelease)
	}
	return nil
}

// downloadFile saves url to path if its SHA-256 is want.
func (m *Manager) downloadFile(ctx context.Context, url, path, want string) error {
	tmp := path + ".part"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o700)
	if err != nil {
		return err
	}
	h := sha256.New()
	err = m.fetch(ctx, url, io.MultiWriter(f, h), maxBinarySize)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil && hex.EncodeToString(h.Sum(nil)) != want {
		err = fmt.Errorf("%w: checksum mismatch", ErrBadRelease)
	}
	if err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, path)
}

// Signature is SHA256SUMS.sig.
type Signature struct {
	KeyID     string `json:"key_id"`
	Signature string `json:"signature"` // base64 Ed25519 signature of SHA256SUMS
}

// VerifySums checks the signature of a SHA256SUMS file.
func VerifySums(sums, sigFile []byte, keys map[string]ed25519.PublicKey) error {
	var s Signature
	if err := json.Unmarshal(sigFile, &s); err != nil {
		return fmt.Errorf("%w: unreadable signature", ErrBadRelease)
	}
	pub, ok := keys[s.KeyID]
	if !ok {
		return fmt.Errorf("%w: signed with an unknown key %q", ErrBadRelease, s.KeyID)
	}
	raw, err := base64.StdEncoding.DecodeString(s.Signature)
	if err != nil || !ed25519.Verify(pub, sums, raw) {
		return fmt.Errorf("%w: bad signature", ErrBadRelease)
	}
	return nil
}

// Sign signs a SHA256SUMS file (release tooling).
func Sign(sums []byte, keyID string, key ed25519.PrivateKey) ([]byte, error) {
	return json.MarshalIndent(Signature{KeyID: keyID, Signature: base64.StdEncoding.EncodeToString(ed25519.Sign(key, sums))}, "", "  ")
}

func sumFor(sums []byte, name string) (string, error) {
	for _, line := range strings.Split(string(sums), "\n") {
		f := strings.Fields(line)
		if len(f) == 2 && strings.TrimPrefix(f[1], "*") == name && len(f[0]) == 64 {
			return strings.ToLower(f[0]), nil
		}
	}
	return "", ErrUnsupported
}

func copyFile(src, dst string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	tmp := dst + ".tmp"
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	_, err = io.Copy(out, in)
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Chmod(tmp, mode)
	}
	if err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, dst)
}

// systemdRun starts the runner as a transient systemd service: it is not
// part of openplexd.service, so stopping that service doesn't stop it, and
// it isn't bound by the service's sandbox (read-only /usr).
func systemdRun(ctx context.Context, runner string, args []string) error {
	path, err := exec.LookPath("systemd-run")
	if err != nil {
		return ErrNoSystemd
	}
	unit := fmt.Sprintf("openplex-update-%d", time.Now().Unix())
	cmd := exec.CommandContext(ctx, path, append([]string{"--unit=" + unit, "--collect", "--quiet", runner}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("starting the update: %v: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// Confirm is called by the daemon once it serves requests: if it is the
// version being installed, the update has worked. It reports whether it
// confirmed one.
func (m *Manager) Confirm() bool {
	path := filepath.Join(m.dir(), "pending.json")
	p, err := readPending(path)
	if err != nil || p.To != m.o.Current || p.Confirmed {
		return false
	}
	p.Confirmed = true
	if err := writeJSON(path, p); err != nil {
		m.o.Log.Warn("update confirm", "err", err)
		return false
	}
	m.o.Log.Info("update confirmed", "from", p.From, "to", p.To)
	return true
}
