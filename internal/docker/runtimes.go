package docker

import (
	"fmt"
	"slices"
	"sort"

	"openplex.dev/openplex/internal/ssl"
)

// Runtime describes an application runtime a website can use.
type Runtime struct {
	Name         string      `json:"name"`
	Label        string      `json:"label"`
	Versions     []string    `json:"versions"`
	Default      string      `json:"default"`
	ImageFormat  string      `json:"-"` // fmt pattern taking the version
	Build        bool        `json:"-"` // image is built locally (see phpimage.go)
	NeedsCommand bool        `json:"needs_command"`
	DefaultPort  int         `json:"default_port"`
	Backend      ssl.Backend `json:"backend"`
}

// Image returns the container image for version v.
func (r Runtime) Image(v string) string {
	if r.Build {
		return PHPImage(v)
	}
	if r.ImageFormat == "" {
		return ""
	}
	return fmt.Sprintf(r.ImageFormat, v)
}

// Runtimes is the catalog of supported runtimes. Static sites are served by
// the edge directly and need no container.
var Runtimes = map[string]Runtime{
	"static": {Name: "static", Label: "Static (HTML/CSS/JS)", Backend: ssl.BackendStatic},
	"php": {Name: "php", Label: "PHP-FPM", Versions: []string{"7.4", "8.1", "8.2", "8.3", "8.4"}, Default: "8.3",
		ImageFormat: "php:%s-fpm-alpine", Build: true, DefaultPort: 9000, Backend: ssl.BackendFastCGI},
	"node": {Name: "node", Label: "Node.js", Versions: []string{"20", "22", "24"}, Default: "22",
		ImageFormat: "node:%s-alpine", NeedsCommand: true, DefaultPort: 3000, Backend: ssl.BackendProxy},
	"python": {Name: "python", Label: "Python", Versions: []string{"3.11", "3.12", "3.13"}, Default: "3.13",
		ImageFormat: "python:%s-slim", NeedsCommand: true, DefaultPort: 8000, Backend: ssl.BackendProxy},
	"go": {Name: "go", Label: "Go", Versions: []string{"1.25", "1.26"}, Default: "1.26",
		ImageFormat: "golang:%s-alpine", NeedsCommand: true, DefaultPort: 8080, Backend: ssl.BackendProxy},
}

// RuntimeList returns the catalog sorted by name.
func RuntimeList() []Runtime {
	out := make([]Runtime, 0, len(Runtimes))
	for _, r := range Runtimes {
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// ResolveRuntime validates name/version, filling in the default version.
func ResolveRuntime(name, version string) (Runtime, string, error) {
	r, ok := Runtimes[name]
	if !ok {
		return Runtime{}, "", fmt.Errorf("unknown runtime %q", name)
	}
	if len(r.Versions) == 0 {
		return r, "", nil
	}
	if version == "" {
		version = r.Default
	}
	if !slices.Contains(r.Versions, version) {
		return Runtime{}, "", fmt.Errorf("runtime %s does not support version %q (supported: %v)", name, version, r.Versions)
	}
	return r, version, nil
}
