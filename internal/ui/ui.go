// Package ui serves the embedded Next.js dashboard (static export copied into
// dist/ by `make web`).
package ui

import (
	"embed"
	"io/fs"
	"net/http"
	"path"
	"strings"
)

//go:embed all:dist
var dist embed.FS

const notBuilt = `<!doctype html><html><head><meta charset="utf-8"><title>OpenPlex</title></head>
<body style="font-family:system-ui;margin:4rem;max-width:40rem">
<h1>OpenPlex</h1><p>The API is running, but this binary was built without the dashboard.
Run <code>make build</code> to embed it. API base: <code>/api/v1</code>.</p></body></html>`

// Handler serves static assets; unknown routes fall back to index.html so
// client-side routing works.
func Handler() http.Handler {
	sub, _ := fs.Sub(dist, "dist")
	files := http.FileServer(http.FS(sub))
	_, err := fs.Stat(sub, "index.html")
	built := err == nil
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !built {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = w.Write([]byte(notBuilt))
			return
		}
		p := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if p == "" {
			p = "index.html"
		}
		for _, cand := range []string{p, p + ".html", path.Join(p, "index.html")} {
			if st, err := fs.Stat(sub, cand); err == nil && !st.IsDir() {
				if strings.HasPrefix(p, "_next/static/") {
					w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
				}
				if strings.HasSuffix(cand, "index.html") {
					// Both FileServer and ServeFileFS redirect paths ending in
					// /index.html, so serve with the original (clean) URL.
					http.ServeFileFS(w, r, sub, cand)
					return
				}
				r2 := r.Clone(r.Context())
				r2.URL.Path = "/" + cand
				files.ServeHTTP(w, r2)
				return
			}
		}
		http.ServeFileFS(w, r, sub, "index.html")
	})
}
