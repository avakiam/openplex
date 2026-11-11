// Package extensions installs and runs OpenPlex extensions: signed packages
// whose backend runs in its own container and talks to the panel only
// through the extension interface (docs/adr/0001-extensions.md).
package extensions

import (
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// ManifestFile is the manifest's name at the root of a package.
const ManifestFile = "openplex-extension.json"

// Text is a translatable string: {"en": "...", "es": "...", "ca": "..."}.
// In JSON it may also be a plain string (English).
type Text map[string]string

// UnmarshalJSON accepts a string or a language → text object.
func (t *Text) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err == nil {
		*t = Text{"en": s}
		return nil
	}
	m := map[string]string{}
	if err := json.Unmarshal(b, &m); err != nil {
		return errors.New("expected a string or an object of translations")
	}
	*t = m
	return nil
}

// In returns the text in lang, falling back to English.
func (t Text) In(lang string) string {
	if v := t[lang]; v != "" {
		return v
	}
	return t["en"]
}

// Manifest is openplex-extension.json.
type Manifest struct {
	Schema      int       `json:"schema"`
	ID          string    `json:"id"`
	Name        Text      `json:"name"`
	Description Text      `json:"description,omitempty"`
	Version     string    `json:"version"`
	Publisher   Publisher `json:"publisher"`
	Homepage    string    `json:"homepage,omitempty"`
	OpenPlex    string    `json:"openplex,omitempty"` // minimum panel version: ">=0.16.0"
	Permissions []string  `json:"permissions,omitempty"`
	Events      []string  `json:"events,omitempty"`
	Backend     Backend   `json:"backend"`
	Settings    []Setting `json:"settings,omitempty"`
	Pages       []Page    `json:"pages,omitempty"`
	SiteTabs    []Page    `json:"site_tabs,omitempty"`
}

// Publisher is who made the extension.
type Publisher struct {
	Name string `json:"name"`
	URL  string `json:"url,omitempty"`
}

// Backend is the extension's container.
type Backend struct {
	Image    string            `json:"image"`              // reference; also the tag inside the archives
	Archives map[string]string `json:"archives,omitempty"` // arch (amd64, arm64) → `docker save` file in the package
	Port     int               `json:"port"`
	MemoryMB int               `json:"memory_mb,omitempty"` // default 128, at most 1024
	CPUs     float64           `json:"cpus,omitempty"`      // default 0.5, at most 2
}

// Setting is one field of the extension's settings form.
type Setting struct {
	Key      string   `json:"key"`
	Type     string   `json:"type"` // text | secret | boolean | select
	Label    Text     `json:"label"`
	Help     Text     `json:"help,omitempty"`
	Required bool     `json:"required,omitempty"`
	Default  string   `json:"default,omitempty"`
	Options  []Option `json:"options,omitempty"` // select only
}

// Option of a select setting.
type Option struct {
	Value string `json:"value"`
	Label Text   `json:"label"`
}

// Page is an extension page (menu) or website tab, served by the backend.
type Page struct {
	ID    string   `json:"id"`
	Title Text     `json:"title"`
	Path  string   `json:"path"`            // path on the backend, e.g. "/" or "/site"
	Roles []string `json:"roles,omitempty"` // who sees it (pages default: admin; site tabs: everyone with access)
	Area  string   `json:"area,omitempty"`  // site tabs: website area a shared user needs (site, files, dns, mail, backups)
}

// Permissions an extension may ask for, with what they allow.
var Permissions = map[string]Text{
	"domains:read":   {"en": "See websites and domains", "es": "Ver webs y dominios", "ca": "Veure webs i dominis"},
	"dns:read":       {"en": "See DNS records", "es": "Ver registros DNS", "ca": "Veure registres DNS"},
	"dns:write":      {"en": "Change DNS records", "es": "Cambiar registros DNS", "ca": "Canviar registres DNS"},
	"mail:read":      {"en": "See mail domains and mailboxes", "es": "Ver dominios de correo y buzones", "ca": "Veure dominis de correu i bústies"},
	"databases:read": {"en": "See databases (not their passwords)", "es": "Ver bases de datos (no sus contraseñas)", "ca": "Veure bases de dades (no les contrasenyes)"},
	"users:read":     {"en": "See user accounts", "es": "Ver cuentas de usuario", "ca": "Veure comptes d'usuari"},
	"system:read":    {"en": "See server information and usage", "es": "Ver información y uso del servidor", "ca": "Veure informació i ús del servidor"},
}

// Events an extension may subscribe to.
var Events = []string{"installed", "settings.changed", "domain.created", "domain.deleted", "dns.changed"}

var (
	idRe      = regexp.MustCompile(`^[a-z0-9]+(?:[.-][a-z0-9]+)*$`)
	versionRe = regexp.MustCompile(`^(\d+)\.(\d+)\.(\d+)(?:-[0-9A-Za-z.-]+)?$`)
	imageRe   = regexp.MustCompile(`^[a-z0-9][a-z0-9._/:@-]{0,254}$`)
	keyRe     = regexp.MustCompile(`^[a-z][a-z0-9_]{0,40}$`)
	pageIDRe  = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,40}$`)
	roles     = []string{"admin", "reseller", "customer"}
	areas     = []string{"site", "files", "dns", "mail", "backups"}
)

// Parse reads and validates a manifest, filling in defaults.
func Parse(b []byte) (*Manifest, error) {
	var m Manifest
	dec := json.NewDecoder(strings.NewReader(string(b)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&m); err != nil {
		return nil, fmt.Errorf("manifest: %w", err)
	}
	if err := m.validate(); err != nil {
		return nil, fmt.Errorf("manifest: %w", err)
	}
	return &m, nil
}

func (m *Manifest) validate() error {
	if m.Schema != 1 {
		return errors.New(`"schema" must be 1`)
	}
	if len(m.ID) < 2 || len(m.ID) > 48 || !idRe.MatchString(m.ID) {
		return errors.New(`"id" must be 2-48 lowercase letters, digits, dots or dashes (e.g. "avakiam.cloudflare")`)
	}
	if m.Name.In("en") == "" || len(m.Name.In("en")) > 60 {
		return errors.New(`"name" needs an English text of at most 60 characters`)
	}
	if !versionRe.MatchString(m.Version) {
		return errors.New(`"version" must look like 1.2.3`)
	}
	if strings.TrimSpace(m.Publisher.Name) == "" {
		return errors.New(`"publisher.name" is required`)
	}
	if m.OpenPlex != "" {
		if _, ok := strings.CutPrefix(m.OpenPlex, ">="); !ok || !versionRe.MatchString(strings.TrimPrefix(m.OpenPlex, ">=")) {
			return errors.New(`"openplex" must look like ">=0.16.0"`)
		}
	}
	for _, p := range m.Permissions {
		if _, ok := Permissions[p]; !ok {
			return fmt.Errorf("unknown permission %q", p)
		}
	}
	for _, e := range m.Events {
		if !slices.Contains(Events, e) {
			return fmt.Errorf("unknown event %q", e)
		}
	}
	b := &m.Backend
	if !imageRe.MatchString(b.Image) {
		return errors.New(`"backend.image" must be an image reference`)
	}
	for arch, f := range b.Archives {
		if arch != "amd64" && arch != "arm64" {
			return fmt.Errorf("backend.archives: unknown architecture %q", arch)
		}
		if !cleanRel(f) {
			return fmt.Errorf("backend.archives.%s: %q is not a path inside the package", arch, f)
		}
	}
	if b.Port < 1024 || b.Port > 65535 {
		return errors.New(`"backend.port" must be between 1024 and 65535`)
	}
	if b.MemoryMB == 0 {
		b.MemoryMB = 128
	}
	if b.MemoryMB < 32 || b.MemoryMB > 1024 {
		return errors.New(`"backend.memory_mb" must be between 32 and 1024`)
	}
	if b.CPUs == 0 {
		b.CPUs = 0.5
	}
	if b.CPUs < 0.1 || b.CPUs > 2 {
		return errors.New(`"backend.cpus" must be between 0.1 and 2`)
	}
	seen := map[string]bool{}
	for _, s := range m.Settings {
		if !keyRe.MatchString(s.Key) || seen[s.Key] {
			return fmt.Errorf("setting %q: keys are unique lowercase identifiers", s.Key)
		}
		seen[s.Key] = true
		if s.Label.In("en") == "" {
			return fmt.Errorf("setting %q needs a label", s.Key)
		}
		switch s.Type {
		case "text", "secret":
		case "boolean":
			if s.Default != "" && s.Default != "true" && s.Default != "false" {
				return fmt.Errorf("setting %q: a boolean default is true or false", s.Key)
			}
		case "select":
			if len(s.Options) == 0 {
				return fmt.Errorf("setting %q: a select needs options", s.Key)
			}
		default:
			return fmt.Errorf("setting %q: unknown type %q", s.Key, s.Type)
		}
	}
	if len(m.Settings) > 50 {
		return errors.New("at most 50 settings")
	}
	if err := checkPages(m.Pages, false); err != nil {
		return err
	}
	return checkPages(m.SiteTabs, true)
}

func checkPages(ps []Page, siteTab bool) error {
	seen := map[string]bool{}
	for i := range ps {
		p := &ps[i]
		if !pageIDRe.MatchString(p.ID) || seen[p.ID] {
			return fmt.Errorf("page %q: ids are unique lowercase identifiers", p.ID)
		}
		seen[p.ID] = true
		if p.Title.In("en") == "" {
			return fmt.Errorf("page %q needs a title", p.ID)
		}
		if !strings.HasPrefix(p.Path, "/") || strings.Contains(p.Path, "..") || strings.ContainsAny(p.Path, "?#\\ ") {
			return fmt.Errorf("page %q: path must start with / and stay simple", p.ID)
		}
		if len(p.Roles) == 0 && !siteTab {
			p.Roles = []string{"admin"}
		}
		for _, r := range p.Roles {
			if !slices.Contains(roles, r) {
				return fmt.Errorf("page %q: unknown role %q", p.ID, r)
			}
		}
		if p.Area != "" && (!siteTab || !slices.Contains(areas, p.Area)) {
			return fmt.Errorf("page %q: unknown area %q", p.ID, p.Area)
		}
	}
	return nil
}

// cleanRel reports whether p is a relative path that stays inside its root.
func cleanRel(p string) bool {
	return p != "" && !strings.HasPrefix(p, "/") && path.Clean(p) == p && p != "." && !strings.HasPrefix(p, "../") && p != ".."
}

// Visible reports whether role may see page p.
func (p Page) Visible(role string) bool {
	return len(p.Roles) == 0 || slices.Contains(p.Roles, role)
}

// Compatible reports whether panel version v satisfies the manifest's
// "openplex" requirement ("dev" builds satisfy everything).
func (m *Manifest) Compatible(v string) bool {
	min := strings.TrimPrefix(m.OpenPlex, ">=")
	if min == "" || v == "dev" || !versionRe.MatchString(v) {
		return true
	}
	return compareVersions(v, min) >= 0
}

func compareVersions(a, b string) int {
	pa, pb := versionRe.FindStringSubmatch(a), versionRe.FindStringSubmatch(b)
	for i := 1; i <= 3; i++ {
		x, _ := strconv.Atoi(pa[i])
		y, _ := strconv.Atoi(pb[i])
		if x != y {
			return x - y
		}
	}
	return 0
}
