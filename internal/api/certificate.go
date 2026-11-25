package api

import (
	"net/http"
	"path/filepath"
	"time"

	"openplex.dev/openplex/internal/db"
	"openplex.dev/openplex/internal/ssl"
)

// certDir is Caddy's certificate storage.
func (s *Server) certDir() string {
	return filepath.Join(s.cfg.CaddyDir(), "data", "caddy", "certificates")
}

// handleGetCertificate reports the HTTPS certificate of a website and of its
// aliases (www.<name> too once it has one): issued, expiring, pending (being
// requested) or missing (none ssl.IssueGrace after the name was added).
func (s *Server) handleGetCertificate(w http.ResponseWriter, r *http.Request) {
	d := s.loadDomain(w, r)
	if d == nil {
		return
	}
	out := map[string]any{"ssl_enabled": d.SSLEnabled, "hosts": []ssl.CertState{}, "grace_minutes": int(ssl.IssueGrace / time.Minute)}
	if !d.SSLEnabled || d.Kind == db.KindAlias {
		writeJSON(w, http.StatusOK, out)
		return
	}
	certs := ssl.Certificates(s.certDir())
	now := time.Now()
	hosts := []ssl.CertState{ssl.StateOf(certs, d.Name, d.CreatedAt, now)}
	if _, ok := certs["www."+d.Name]; ok {
		hosts = append(hosts, ssl.StateOf(certs, "www."+d.Name, d.CreatedAt, now))
	}
	aliases, err := s.db.ListDomains(r.Context(), db.DomainFilter{ParentID: &d.ID, Kind: db.KindAlias})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	for _, a := range aliases {
		hosts = append(hosts, ssl.StateOf(certs, a.Name, a.CreatedAt, now))
	}
	out["hosts"] = hosts
	writeJSON(w, http.StatusOK, out)
}
