package extensions

import (
	"bytes"
	"encoding/base64"
	"os"
	"path/filepath"
)

// MaxIconSize is the largest icon an extension's package may carry.
const MaxIconSize = 256 << 10

// iconFiles are the names an icon may have at the package's root, beside
// openplex-extension.json, with their content types.
var iconFiles = []struct{ name, mime string }{
	{"icon.svg", "image/svg+xml"}, {"icon.png", "image/png"}, {"icon.webp", "image/webp"},
}

// iconData returns the icon in dir as a data: URL, or "" when there is none
// or it isn't the image its name says.
func iconData(dir string) string {
	for _, f := range iconFiles {
		p := filepath.Join(dir, f.name)
		fi, err := os.Lstat(p)
		if err != nil || !fi.Mode().IsRegular() || fi.Size() > MaxIconSize {
			continue
		}
		b, err := os.ReadFile(p)
		if err != nil || !isImage(f.name, b) {
			continue
		}
		return "data:" + f.mime + ";base64," + base64.StdEncoding.EncodeToString(b)
	}
	return ""
}

func isImage(name string, b []byte) bool {
	switch filepath.Ext(name) {
	case ".svg":
		return bytes.Contains(b[:min(len(b), 1024)], []byte("<svg"))
	case ".png":
		return bytes.HasPrefix(b, []byte("\x89PNG\r\n\x1a\n"))
	case ".webp":
		return len(b) > 12 && string(b[:4]) == "RIFF" && string(b[8:12]) == "WEBP"
	}
	return false
}

// Icon is an installed extension's icon as a data: URL ("" without one).
// Pages show it in an <img>, where an SVG can't run anything.
func (m *Manager) Icon(id string) string { return iconData(m.pkgDir(id)) }
