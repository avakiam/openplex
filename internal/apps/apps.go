// Package apps is the catalog of applications OpenPlex installs on a website
// (WordPress, Joomla, Drupal) and their unattended install scripts. A script
// runs as the website's user inside its PHP container, downloads the latest
// release from the project itself and installs it with a database the panel
// created for it.
package apps

import (
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"net/mail"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

//go:embed scripts/*.sh
var scripts embed.FS

// App describes an installable application.
type App struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	MinPHP      string   `json:"min_php"`    // lowest PHP version it runs on
	AdminPath   string   `json:"admin_path"` // where its administration lives
	Languages   []string `json:"languages"`  // languages it can be installed in (en/es/ca)
	DBPrefix    string   `json:"-"`          // short name used in its database name
	// ManualExtensions: its extensions are updated only from its own administration.
	ManualExtensions bool `json:"manual_extensions"`
}

// Catalog lists the applications in display order.
var Catalog = []App{
	{ID: "wordpress", Name: "WordPress", Description: "Websites and blogs; the most used content management system.",
		MinPHP: "7.4", AdminPath: "/wp-admin/", Languages: []string{"en", "es", "ca"}, DBPrefix: "wp"},
	{ID: "joomla", Name: "Joomla", Description: "Content management with users, access levels and multilingual sites built in.",
		MinPHP: "8.3", AdminPath: "/administrator/", Languages: []string{"en"}, DBPrefix: "joomla", ManualExtensions: true},
	{ID: "drupal", Name: "Drupal", Description: "Structured content and complex websites, with a flexible content model.",
		MinPHP: "8.3", AdminPath: "/user/login", Languages: []string{"en", "es", "ca"}, DBPrefix: "drupal"},
}

// Get returns the application with id.
func Get(id string) (App, bool) {
	i := slices.IndexFunc(Catalog, func(a App) bool { return a.ID == id })
	if i < 0 {
		return App{}, false
	}
	return Catalog[i], true
}

// Params are the settings of one install.
type Params struct {
	Title         string
	AdminUser     string
	AdminEmail    string
	AdminPassword string
	Language      string
}

var adminUserRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{2,59}$`)

// Validate checks p for app a (filling in the default language).
func (a App) Validate(p *Params) error {
	p.Title = strings.TrimSpace(p.Title)
	p.AdminEmail = strings.TrimSpace(p.AdminEmail)
	if p.Title == "" || utf8.RuneCountInString(p.Title) > 100 || strings.IndexFunc(p.Title, unicode.IsControl) >= 0 {
		return errors.New("the site title must be 1-100 characters")
	}
	if !adminUserRe.MatchString(p.AdminUser) {
		return errors.New("the administrator username must be 3-60 characters: letters, digits, dots, dashes and underscores")
	}
	if addr, err := mail.ParseAddress(p.AdminEmail); err != nil || addr.Address != p.AdminEmail || len(p.AdminEmail) > 254 {
		return errors.New("the administrator e-mail address is not valid")
	}
	if n := utf8.RuneCountInString(p.AdminPassword); n < 12 || n > 128 || strings.IndexFunc(p.AdminPassword, unicode.IsControl) >= 0 {
		return errors.New("the administrator password must be 12-128 characters")
	}
	if p.Language == "" || !slices.Contains(a.Languages, p.Language) {
		p.Language = "en"
	}
	return nil
}

// SupportsPHP reports whether the app runs on PHP version v ("8.3").
func (a App) SupportsPHP(v string) bool { return cmpVersion(v, a.MinPHP) >= 0 }

func cmpVersion(a, b string) int {
	as, bs := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < max(len(as), len(bs)); i++ {
		var x, y int
		if i < len(as) {
			x, _ = strconv.Atoi(as[i])
		}
		if i < len(bs) {
			y, _ = strconv.Atoi(bs[i])
		}
		if x != y {
			return x - y
		}
	}
	return 0
}

// Script returns the shell script installing a.
func (a App) Script() (string, error) { return compose("lib", "empty", a.ID) }

// CheckScript returns the shell script listing the updates of a; it prints
// them as one "OPX_RESULT=<json>" line (see ParseUpdates).
func (a App) CheckScript() (string, error) { return compose("lib", a.ID+"-check") }

// UpdateScript returns the shell script updating a. Its environment says what
// to update: OPX_CORE=1 the application itself, OPX_EXTENSIONS=1 its
// plugins, themes or modules.
func (a App) UpdateScript() (string, error) { return compose("lib", a.ID+"-update") }

func compose(parts ...string) (string, error) {
	var b strings.Builder
	for _, p := range parts {
		s, err := scripts.ReadFile("scripts/" + p + ".sh")
		if err != nil {
			return "", fmt.Errorf("no script %s", p)
		}
		b.Write(s)
		b.WriteByte('\n')
	}
	return b.String(), nil
}

// UpdateEnv is the environment of the update script.
// host is the website's name, used to check it still answers afterwards.
func UpdateEnv(docroot, host string, core, extensions bool) []string {
	flag := func(b bool) string {
		if b {
			return "1"
		}
		return "0"
	}
	return []string{"OPX_DOCROOT=" + docroot, "OPX_HOST=" + host, "OPX_CORE=" + flag(core), "OPX_EXTENSIONS=" + flag(extensions)}
}

// Update is one update available for a plugin, theme, module or extension.
type Update struct {
	Kind    string `json:"kind"` // plugin, theme, module, component…
	Name    string `json:"name"`
	Title   string `json:"title"`
	Version string `json:"version"` // installed ("" when unknown)
	Latest  string `json:"latest"`
}

// Updates is the result of an update check.
type Updates struct {
	Version string   `json:"version"` // installed version of the application
	Core    string   `json:"core"`    // newer version available ("" = up to date)
	Items   []Update `json:"items"`
	// ManualExtensions: the application updates its extensions only from its
	// own administration (Joomla).
	ManualExtensions bool `json:"manual_extensions,omitempty"`
}

// maxUpdates bounds what a check may report.
const maxUpdates = 500

// ParseUpdates reads the "OPX_RESULT=" line of a check script's output.
func ParseUpdates(output string) (*Updates, error) {
	line := ""
	for _, l := range strings.Split(output, "\n") {
		if rest, ok := strings.CutPrefix(strings.TrimSpace(l), "OPX_RESULT="); ok {
			line = rest
		}
	}
	if line == "" {
		return nil, errors.New("the update check gave no result")
	}
	var u Updates
	if err := json.Unmarshal([]byte(line), &u); err != nil {
		return nil, fmt.Errorf("reading the update check: %w", err)
	}
	clip := func(s string, n int) string {
		s = strings.Map(func(r rune) rune {
			if unicode.IsControl(r) {
				return -1
			}
			return r
		}, strings.TrimSpace(s))
		if utf8.RuneCountInString(s) > n {
			s = string([]rune(s)[:n])
		}
		return s
	}
	u.Version, u.Core = clip(u.Version, 40), clip(u.Core, 40)
	if len(u.Items) > maxUpdates {
		u.Items = u.Items[:maxUpdates]
	}
	items := u.Items[:0]
	for _, it := range u.Items {
		it.Kind, it.Name, it.Title = clip(it.Kind, 20), clip(it.Name, 100), clip(it.Title, 200)
		it.Version, it.Latest = clip(it.Version, 40), clip(it.Latest, 40)
		if it.Name == "" {
			continue
		}
		if it.Title == "" {
			it.Title = it.Name
		}
		items = append(items, it)
	}
	u.Items = items
	if u.Items == nil {
		u.Items = []Update{}
	}
	return &u, nil
}

// Database is where the app stores its data.
type Database struct {
	Host, Name, User, Password string
}

// Env is the environment of the install script.
func Env(p Params, docroot, siteURL string, d Database) []string {
	return []string{
		"OPX_DOCROOT=" + docroot, "OPX_URL=" + siteURL, "OPX_TITLE=" + p.Title, "OPX_LANG=" + p.Language,
		"OPX_ADMIN_USER=" + p.AdminUser, "OPX_ADMIN_EMAIL=" + p.AdminEmail, "OPX_ADMIN_PASSWORD=" + p.AdminPassword,
		"OPX_DB_HOST=" + d.Host, "OPX_DB_NAME=" + d.Name, "OPX_DB_USER=" + d.User, "OPX_DB_PASSWORD=" + d.Password,
	}
}

// Version extracts the installed version the script reports ("OPX_VERSION=x").
func Version(output string) string {
	v := ""
	for _, line := range strings.Split(output, "\n") {
		if rest, ok := strings.CutPrefix(strings.TrimSpace(line), "OPX_VERSION="); ok {
			v = strings.TrimSpace(rest)
		}
	}
	if len(v) > 40 {
		v = v[:40]
	}
	return v
}

// placeholder is the start page PrepareSiteDir writes into a new website.
const placeholder = "This site is hosted with OpenPlex"

// EmptyDocroot reports whether a document root with these entries is free for
// an install: nothing in it, or only OpenPlex's placeholder index.html (whose
// content is given by index).
func EmptyDocroot(names []string, index func() ([]byte, error)) bool {
	switch {
	case len(names) == 0:
		return true
	case len(names) == 1 && names[0] == "index.html":
		b, err := index()
		return err == nil && len(b) < 4096 && strings.Contains(string(b), placeholder)
	}
	return false
}
