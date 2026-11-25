package api

import (
	"context"
	"errors"
	"net/http"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"openplex.dev/openplex/internal/auth"
	"openplex.dev/openplex/internal/db"
	"openplex.dev/openplex/internal/docker"
	"openplex.dev/openplex/internal/files"
	"openplex.dev/openplex/internal/sftpd"
)

// maxSFTPAccounts is how many SFTP accounts one website may have.
const maxSFTPAccounts = 20

// sftpAccountNameRe is the part before "@website" (panel usernames have no "@").
var sftpAccountNameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9_.-]{0,31}$`)

// SFTPAccountSites is the website an SFTP account reaches: none while the
// account is off or expired, or its website or owner is suspended.
func (s *Server) SFTPAccountSites(ctx context.Context, a *db.SFTPAccount) ([]sftpd.Site, error) {
	if !a.Enabled || a.Expired(time.Now()) {
		return nil, nil
	}
	d, err := s.db.GetDomain(ctx, a.DomainID)
	if errors.Is(err, db.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if d.Kind == db.KindAlias || d.Status == db.StatusDisabled {
		return nil, nil
	}
	if owner, err := s.db.GetUser(ctx, d.OwnerID); err != nil || owner.Disabled {
		return nil, err
	}
	return []sftpd.Site{{
		Name: d.Name, Root: filepath.Join(s.cfg.SitesDir(), d.Name), UID: docker.SiteUID(d.ID),
		ReadOnly: a.ReadOnly, Sub: a.Directory,
	}}, nil
}

// sftpAccountView is an account as the dashboard sees it (never its password).
type sftpAccountView struct {
	*db.SFTPAccount
	HasPassword bool `json:"has_password"`
	Expired     bool `json:"expired"`
}

func viewSFTPAccount(a *db.SFTPAccount) sftpAccountView {
	return sftpAccountView{SFTPAccount: a, HasPassword: a.PasswordHash != "", Expired: a.Expired(time.Now())}
}

// loadFilesSite resolves the website for SFTP accounts (aliases have no files).
func (s *Server) loadFilesSite(w http.ResponseWriter, r *http.Request) *db.Domain {
	d := s.loadDomain(w, r)
	if d == nil {
		return nil
	}
	if d.Kind == db.KindAlias {
		badRequest(w, "aliases have no files; open the parent website")
		return nil
	}
	return d
}

func (s *Server) loadSFTPAccount(w http.ResponseWriter, r *http.Request) (*db.Domain, *db.SFTPAccount) {
	d := s.loadFilesSite(w, r)
	if d == nil {
		return nil, nil
	}
	aid, ok := idParam(w, r, "aid")
	if !ok {
		return nil, nil
	}
	a, err := s.db.GetSFTPAccount(r.Context(), aid)
	if err != nil || a.DomainID != d.ID {
		writeErr(w, http.StatusNotFound, "not_found", "resource not found")
		return nil, nil
	}
	return d, a
}

type sftpAccountInput struct {
	Name      *string `json:"name"`       // create only
	Password  *string `json:"password"`   // "" removes it
	PublicKey *string `json:"public_key"` // "" removes it
	Directory *string `json:"directory"`  // "" or "." = the whole website
	ReadOnly  *bool   `json:"read_only"`
	Enabled   *bool   `json:"enabled"`
	ExpiresAt *string `json:"expires_at"` // RFC 3339; "" = never
}

// apply validates in and copies it onto a; it returns a user-facing message on error.
func (s *Server) applySFTPAccount(in sftpAccountInput, d *db.Domain, a *db.SFTPAccount) string {
	if in.Password != nil {
		a.PasswordHash = ""
		if *in.Password != "" {
			if err := auth.CheckPolicy(*in.Password); err != nil {
				return err.Error()
			}
			h, err := auth.HashPassword(*in.Password)
			if err != nil {
				return "could not store the password"
			}
			a.PasswordHash = h
		}
	}
	if in.PublicKey != nil {
		a.PublicKey, a.KeyFingerprint = "", ""
		if k := strings.TrimSpace(*in.PublicKey); k != "" {
			norm, fp, err := auth.ParseSSHPublicKey(k)
			if err != nil {
				return "paste an SSH public key, such as the contents of id_ed25519.pub"
			}
			a.PublicKey, a.KeyFingerprint = norm, fp
		}
	}
	if in.Directory != nil {
		dir := files.Clean(*in.Directory)
		if dir == "." {
			dir = ""
		}
		if dir != "" {
			j := files.Jail{Root: filepath.Join(s.cfg.SitesDir(), d.Name)}
			if st, err := j.Stat(dir); err != nil || !st.IsDir() {
				return "the folder does not exist in this website"
			}
		}
		a.Directory = dir
	}
	if in.ReadOnly != nil {
		a.ReadOnly = *in.ReadOnly
	}
	if in.Enabled != nil {
		a.Enabled = *in.Enabled
	}
	if in.ExpiresAt != nil {
		a.ExpiresAt = nil
		if *in.ExpiresAt != "" {
			t, err := time.Parse(time.RFC3339, *in.ExpiresAt)
			if err != nil {
				return "enter the end date as a date and time"
			}
			if !t.After(time.Now()) {
				return "the end date must be in the future"
			}
			t = t.UTC()
			a.ExpiresAt = &t
		}
	}
	if a.PasswordHash == "" && a.PublicKey == "" {
		return "set a password or an SSH key for the account"
	}
	return ""
}

func (s *Server) handleListSFTPAccounts(w http.ResponseWriter, r *http.Request) {
	d := s.loadFilesSite(w, r)
	if d == nil {
		return
	}
	accounts, err := s.db.ListSFTPAccounts(r.Context(), d.ID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	out := make([]sftpAccountView, len(accounts))
	for i, a := range accounts {
		out[i] = viewSFTPAccount(a)
	}
	v := map[string]any{"accounts": out, "max_accounts": maxSFTPAccounts, "enabled": s.sftp != nil, "suffix": "@" + d.Name}
	if s.sftp != nil {
		v["port"], v["fingerprint"] = s.sftp.Port, s.sftp.Fingerprint
	}
	writeJSON(w, http.StatusOK, v)
}

func (s *Server) handleCreateSFTPAccount(w http.ResponseWriter, r *http.Request) {
	d := s.loadFilesSite(w, r)
	if d == nil {
		return
	}
	var in sftpAccountInput
	if !decode(w, r, &in) {
		return
	}
	name := ""
	if in.Name != nil {
		name = strings.ToLower(strings.TrimSpace(*in.Name))
	}
	if !sftpAccountNameRe.MatchString(name) {
		badRequest(w, "the account name must be 1-32 characters: lowercase letters, digits, '_', '.', '-'")
		return
	}
	n, err := s.db.CountSFTPAccounts(r.Context(), d.ID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if n >= maxSFTPAccounts {
		writeErr(w, http.StatusForbidden, "quota_exceeded", "this website already has the maximum of 20 SFTP accounts")
		return
	}
	a := &db.SFTPAccount{DomainID: d.ID, Username: name + "@" + d.Name, Enabled: true}
	if msg := s.applySFTPAccount(in, d, a); msg != "" {
		badRequest(w, msg)
		return
	}
	if err := s.db.CreateSFTPAccount(r.Context(), a); err != nil {
		if errors.Is(err, db.ErrConflict) {
			writeErr(w, http.StatusConflict, "conflict", "this website already has an account with that name")
			return
		}
		s.fail(w, r, err)
		return
	}
	s.audit(r, "sftp.account_created", a.Username, sftpAccountDetail(a))
	writeJSON(w, http.StatusCreated, viewSFTPAccount(a))
}

func (s *Server) handleUpdateSFTPAccount(w http.ResponseWriter, r *http.Request) {
	d, a := s.loadSFTPAccount(w, r)
	if a == nil {
		return
	}
	var in sftpAccountInput
	if !decode(w, r, &in) {
		return
	}
	if in.Name != nil && strings.ToLower(strings.TrimSpace(*in.Name))+"@"+d.Name != a.Username {
		badRequest(w, "an account's name cannot change; create a new account")
		return
	}
	if msg := s.applySFTPAccount(in, d, a); msg != "" {
		badRequest(w, msg)
		return
	}
	if err := s.db.UpdateSFTPAccount(r.Context(), a); err != nil {
		s.fail(w, r, err)
		return
	}
	s.audit(r, "sftp.account_updated", a.Username, sftpAccountDetail(a))
	writeJSON(w, http.StatusOK, viewSFTPAccount(a))
}

func (s *Server) handleDeleteSFTPAccount(w http.ResponseWriter, r *http.Request) {
	d, a := s.loadSFTPAccount(w, r)
	if a == nil {
		return
	}
	if err := s.db.DeleteSFTPAccount(r.Context(), d.ID, a.ID); err != nil {
		s.fail(w, r, err)
		return
	}
	s.audit(r, "sftp.account_deleted", a.Username, "")
	w.WriteHeader(http.StatusNoContent)
}

// sftpAccountDetail summarizes an account for the audit log (no secrets).
func sftpAccountDetail(a *db.SFTPAccount) string {
	parts := []string{"folder=/" + a.Directory}
	if a.ReadOnly {
		parts = append(parts, "read-only")
	}
	if !a.Enabled {
		parts = append(parts, "off")
	}
	if a.PasswordHash != "" {
		parts = append(parts, "password")
	}
	if a.KeyFingerprint != "" {
		parts = append(parts, "key "+a.KeyFingerprint)
	}
	if a.ExpiresAt != nil {
		parts = append(parts, "until "+a.ExpiresAt.Format(time.RFC3339))
	}
	return strings.Join(parts, " ")
}
