package api

import (
	"context"
	"net/http"
	"net/netip"
	"strings"

	"openplex.dev/openplex/internal/db"
	"openplex.dev/openplex/internal/dbproxy"
	"openplex.dev/openplex/internal/firewall"
	"openplex.dev/openplex/internal/msg"
)

// RemoteDB is remote access to the shared database servers (dbproxy.Proxy).
type RemoteDB interface {
	Sync(ctx context.Context) error
	Port(engine string) int
	Certificate() (pem []byte, fingerprint string)
}

// remotePort is the public port of d's engine, 0 when it cannot be reached remotely.
func (s *Server) remotePort(d *db.Database) int {
	if s.remote == nil || (d.Engine != db.EngineMariaDB && d.Engine != db.EnginePostgres) {
		return 0
	}
	return s.remote.Port(d.Engine)
}

// splitAllow turns a stored allow-list into its entries.
func splitAllow(s string) []string {
	if s == "" {
		return []string{}
	}
	return strings.Split(s, ",")
}

type remoteInfo struct {
	Available   bool           `json:"available"`
	Ports       map[string]int `json:"ports"`
	Fingerprint string         `json:"fingerprint"`
	ClientIP    string         `json:"client_ip"`
	// UFWBlocks lists the ports ufw (the admin's own firewall) keeps closed.
	UFWBlocks []int `json:"ufw_blocks"`
}

func (s *Server) handleRemoteDBInfo(w http.ResponseWriter, r *http.Request) {
	out := remoteInfo{Ports: map[string]int{}, ClientIP: clientIP(r), UFWBlocks: []int{}}
	// Only an address the server sees from outside is worth offering.
	if a, err := netip.ParseAddr(out.ClientIP); err != nil || a.IsLoopback() || a.IsUnspecified() {
		out.ClientIP = ""
	}
	if s.remote != nil {
		out.Available = true
		_, out.Fingerprint = s.remote.Certificate()
		for _, e := range []string{db.EngineMariaDB, db.EnginePostgres} {
			p := s.remote.Port(e)
			out.Ports[e] = p
			if p > 0 && firewall.UFWBlocksTCP("/", p) {
				out.UFWBlocks = append(out.UFWBlocks, p)
			}
		}
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleRemoteDBCertificate(w http.ResponseWriter, r *http.Request) {
	if s.remote == nil {
		writeErr(w, http.StatusNotFound, "not_found", "remote database access is off on this server")
		return
	}
	pem, _ := s.remote.Certificate()
	w.Header().Set("Content-Type", "application/x-pem-file")
	w.Header().Set("Content-Disposition", `attachment; filename="openplex-databases.pem"`)
	_, _ = w.Write(pem)
}

func (s *Server) handleSetDatabaseRemote(w http.ResponseWriter, r *http.Request) {
	d := s.loadDatabase(w, r)
	if d == nil {
		return
	}
	ok, err := s.canAccessOwner(r.Context(), actor(r), d.OwnerID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if !ok {
		writeErr(w, http.StatusForbidden, "forbidden", "only the owner of this database can change its remote access")
		return
	}
	if s.remotePort(d) == 0 {
		writeErr(w, http.StatusBadRequest, "invalid", "this database cannot be reached remotely")
		return
	}
	var req struct {
		Allow []string `json:"allow"`
	}
	if !decode(w, r, &req) {
		return
	}
	for i := range req.Allow {
		req.Allow[i] = strings.TrimSpace(req.Allow[i])
	}
	list, err := dbproxy.ParseAllow(req.Allow)
	if err != nil {
		writeErrFrom(w, http.StatusBadRequest, "invalid", err)
		return
	}
	parts := make([]string, len(list))
	for i, p := range list {
		parts[i] = firewall.FormatPrefix(p)
	}
	allow := strings.Join(parts, ",")
	if err := s.db.SetDatabaseRemote(r.Context(), d.ID, allow); err != nil {
		s.fail(w, r, err)
		return
	}
	if err := s.remote.Sync(r.Context()); err != nil {
		// Put the previous list back: a port in use leaves nothing to reach.
		_ = s.db.SetDatabaseRemote(context.WithoutCancel(r.Context()), d.ID, d.RemoteAllow)
		_ = s.remote.Sync(context.WithoutCancel(r.Context()))
		s.log.Warn("remote database access", "err", err)
		writeMsg(w, http.StatusConflict, "remote_unavailable", msg.New("remote access could not be switched on: {error}", "error", err.Error()))
		return
	}
	detail := "off"
	if allow != "" {
		detail = allow
	}
	s.audit(r, "database.remote_access", d.Name, detail)
	d.RemoteAllow = allow
	writeJSON(w, http.StatusOK, s.viewOf(r, d))
}

// syncRemote refreshes remote access after a database is gone.
func (s *Server) syncRemote(ctx context.Context) {
	if s.remote == nil {
		return
	}
	if err := s.remote.Sync(ctx); err != nil {
		s.log.Warn("remote database access", "err", err)
	}
}
