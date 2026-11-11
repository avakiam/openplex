// Package apps is the catalog of applications OpenPlex installs on a website
// (WordPress, Joomla, Drupal) and their unattended install scripts. A script
// runs as the website's user inside its PHP container, downloads the latest
// release from the project itself and installs it with a database the panel
// created for it.
package apps

import (
	"embed"
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
}

// Catalog lists the applications in display order.
var Catalog = []App{
	{ID: "wordpress", Name: "WordPress", Description: "Websites and blogs; the most used content management system.",
		MinPHP: "7.4", AdminPath: "/wp-admin/", Languages: []string{"en", "es", "ca"}, DBPrefix: "wp"},
	{ID: "joomla", Name: "Joomla", Description: "Content management with users, access levels and multilingual sites built in.",
		MinPHP: "8.3", AdminPath: "/administrator/", Languages: []string{"en"}, DBPrefix: "joomla"},
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
func (a App) Script() (string, error) {
	common, err := scripts.ReadFile("scripts/common.sh")
	if err != nil {
		return "", err
	}
	body, err := scripts.ReadFile("scripts/" + a.ID + ".sh")
	if err != nil {
		return "", fmt.Errorf("no install script for %s", a.ID)
	}
	return string(common) + "\n" + string(body), nil
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
