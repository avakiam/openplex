package api

import (
	"context"
	"net/http"
	"path/filepath"
	"slices"

	"openplex.dev/openplex/internal/auth"
	"openplex.dev/openplex/internal/db"
	"openplex.dev/openplex/internal/docker"
	"openplex.dev/openplex/internal/firewall"
	"openplex.dev/openplex/internal/sftpd"
)

// SFTPInfo describes the built-in SFTP server to the dashboard.
type SFTPInfo struct {
	Port        int
	Fingerprint string // SHA256:… of the host key
}

// SFTPSites lists the websites u may reach over SFTP: those whose files u can
// manage in the panel (own or managed accounts, and grants with Files; a
// read-only grant stays read-only). Aliases have no files of their own.
func (s *Server) SFTPSites(ctx context.Context, u *db.User) ([]sftpd.Site, error) {
	if u.Disabled {
		return nil, nil
	}
	views, err := s.visibleDomains(ctx, u, "")
	if err != nil {
		return nil, err
	}
	out := make([]sftpd.Site, 0, len(views))
	for _, v := range views {
		if v.Kind == db.KindAlias {
			continue
		}
		ro := false
		if v.Access != nil {
			if !slices.Contains(v.Access.Areas, db.AreaFiles) {
				continue
			}
			ro = v.Access.ReadOnly
		}
		out = append(out, sftpd.Site{Name: v.Name, Root: filepath.Join(s.cfg.SitesDir(), v.Name), UID: docker.SiteUID(v.ID), ReadOnly: ro})
	}
	return out, nil
}

type sftpView struct {
	Enabled     bool   `json:"enabled"`
	Port        int    `json:"port,omitempty"`
	Fingerprint string `json:"fingerprint,omitempty"`
	Username    string `json:"username"`
	PasswordSet bool   `json:"password_set"`
	Keys        int    `json:"keys"`
	Sites       int    `json:"sites"`
	UFWBlocked  bool   `json:"ufw_blocked"` // ufw is active and does not let the port in
}

func (s *Server) sftpStatus(ctx context.Context, u *db.User) (*sftpView, error) {
	v := &sftpView{Enabled: s.sftp != nil, Username: u.Username}
	if s.sftp != nil {
		v.Port, v.Fingerprint = s.sftp.Port, s.sftp.Fingerprint
		v.UFWBlocked = firewall.UFWBlocksTCP("/", s.sftp.Port)
	}
	h, err := s.db.SFTPPasswordHash(ctx, u.ID)
	if err != nil {
		return nil, err
	}
	keys, err := s.db.ListSSHKeys(ctx, u.ID)
	if err != nil {
		return nil, err
	}
	sites, err := s.SFTPSites(ctx, u)
	if err != nil {
		return nil, err
	}
	v.PasswordSet, v.Keys, v.Sites = h != "", len(keys), len(sites)
	return v, nil
}

func (s *Server) handleGetSFTP(w http.ResponseWriter, r *http.Request) {
	u, err := s.db.GetUser(r.Context(), actor(r).ID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	v, err := s.sftpStatus(r.Context(), u)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

// handleSetSFTPPassword sets the SFTP password, which must differ from the
// panel password: an SFTP client stores it, and it can never open the panel.
func (s *Server) handleSetSFTPPassword(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Password string `json:"password"`
	}
	if !decode(w, r, &in) {
		return
	}
	u, err := s.db.GetUser(r.Context(), actor(r).ID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if err := auth.CheckPolicy(in.Password); err != nil {
		badRequestErr(w, err)
		return
	}
	if auth.VerifyPassword(u.PasswordHash, in.Password) {
		badRequest(w, "use a different password from your panel password")
		return
	}
	h, err := auth.HashPassword(in.Password)
	if err == nil {
		err = s.db.SetSFTPPassword(r.Context(), u.ID, h)
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.audit(r, "sftp.password_set", u.Username, "")
	s.handleGetSFTP(w, r)
}

func (s *Server) handleDeleteSFTPPassword(w http.ResponseWriter, r *http.Request) {
	u := actor(r)
	if err := s.db.DeleteSFTPPassword(r.Context(), u.ID); err != nil {
		s.fail(w, r, err)
		return
	}
	s.audit(r, "sftp.password_removed", u.Username, "")
	s.handleGetSFTP(w, r)
}
