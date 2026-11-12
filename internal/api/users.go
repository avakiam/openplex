package api

import (
	"context"
	"net/http"
	"regexp"
	"strings"
	"time"

	"openplex.dev/openplex/internal/auth"
	"openplex.dev/openplex/internal/db"
)

var usernameRe = regexp.MustCompile(`^[a-z][a-z0-9_.-]{2,31}$`)

func withPrincipal(ctx context.Context, u *db.User) context.Context {
	return context.WithValue(ctx, principalKey, &Principal{User: u})
}

func (s *Server) handleListUsers(w http.ResponseWriter, r *http.Request) {
	a := actor(r)
	var users []*db.User
	var err error
	switch a.Role {
	case db.RoleAdmin:
		users, err = s.db.ListUsers(r.Context(), db.UserFilter{})
	case db.RoleReseller:
		users, err = s.db.ListUsers(r.Context(), db.UserFilter{ParentID: &a.ID})
		users = append([]*db.User{a}, users...)
	default:
		users = []*db.User{a}
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, users)
}

func (s *Server) handleCreateUser(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Username   string `json:"username"`
		Email      string `json:"email"`
		Password   string `json:"password"`
		Role       string `json:"role"`
		ParentID   *int64 `json:"parent_id"`
		MaxDomains int    `json:"max_domains"`
		MaxDBs     int    `json:"max_databases"`
		MaxDBSize  int    `json:"max_db_size_mb"`
		Locale     string `json:"locale"`
	}
	if !decode(w, r, &in) {
		return
	}
	a := actor(r)
	in.Username = strings.ToLower(strings.TrimSpace(in.Username))
	if !usernameRe.MatchString(in.Username) {
		badRequest(w, "username must be 3-32 chars: lowercase letters, digits, '_', '.', '-' and start with a letter")
		return
	}
	if in.Role == "" {
		in.Role = db.RoleCustomer
	}
	if !db.ValidRole(in.Role) {
		badRequest(w, "role must be admin, reseller or customer")
		return
	}
	if !db.ValidLocale(in.Locale) {
		badRequest(w, "unsupported language")
		return
	}
	if in.MaxDomains < 0 || in.MaxDBs < 0 {
		badRequest(w, "quotas must be >= 0")
		return
	}
	switch a.Role {
	case db.RoleAdmin:
		if in.ParentID != nil {
			p, err := s.db.GetUser(r.Context(), *in.ParentID)
			if err != nil || p.Role != db.RoleReseller || in.Role != db.RoleCustomer {
				badRequest(w, "parent_id must reference a reseller and is only valid for customers")
				return
			}
		}
	case db.RoleReseller:
		if in.Role != db.RoleCustomer {
			forbidden(w)
			return
		}
		in.ParentID = &a.ID
	default:
		forbidden(w)
		return
	}
	if a.Role == db.RoleReseller && in.MaxDBSize == 0 {
		in.MaxDBSize = a.MaxDBSizeMB // a reseller's customers stay within its own limit
	}
	if msg := dbSizeCap(a, in.MaxDBSize); msg != "" {
		badRequest(w, msg)
		return
	}
	if err := auth.CheckPolicy(in.Password); err != nil {
		badRequest(w, err.Error())
		return
	}
	hash, err := auth.HashPassword(in.Password)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	u := &db.User{Username: in.Username, Email: strings.TrimSpace(in.Email), PasswordHash: hash, Role: in.Role, ParentID: in.ParentID, MaxDomains: in.MaxDomains, MaxDatabases: in.MaxDBs, MaxDBSizeMB: in.MaxDBSize, Locale: in.Locale}
	if err := s.db.CreateUser(r.Context(), u); err != nil {
		s.fail(w, r, err)
		return
	}
	s.audit(r, "user.created", u.Username, u.Role)
	writeJSON(w, http.StatusCreated, u)
}

func (s *Server) handleGetUser(w http.ResponseWriter, r *http.Request) {
	if u := s.loadManagedUser(w, r); u != nil {
		writeJSON(w, http.StatusOK, u)
	}
}

func (s *Server) handleUpdateUser(w http.ResponseWriter, r *http.Request) {
	u := s.loadManagedUser(w, r)
	if u == nil {
		return
	}
	var in struct {
		Email      *string `json:"email"`
		Role       *string `json:"role"`
		ParentID   *int64  `json:"parent_id"`
		Disabled   *bool   `json:"disabled"`
		MaxDomains *int    `json:"max_domains"`
		MaxDBs     *int    `json:"max_databases"`
		MaxDBSize  *int    `json:"max_db_size_mb"`
		Locale     *string `json:"locale"`
	}
	if !decode(w, r, &in) {
		return
	}
	a := actor(r)
	self := a.ID == u.ID
	if in.Email != nil {
		u.Email = strings.TrimSpace(*in.Email)
	}
	if in.Locale != nil {
		if !db.ValidLocale(*in.Locale) {
			badRequest(w, "unsupported language")
			return
		}
		u.Locale = *in.Locale
	}
	if in.Role != nil || in.ParentID != nil {
		if a.Role != db.RoleAdmin || self {
			forbidden(w)
			return
		}
		if in.Role != nil {
			if !db.ValidRole(*in.Role) {
				badRequest(w, "invalid role")
				return
			}
			u.Role = *in.Role
		}
		if in.ParentID != nil {
			if *in.ParentID == 0 {
				u.ParentID = nil
			} else {
				p, err := s.db.GetUser(r.Context(), *in.ParentID)
				if err != nil || p.Role != db.RoleReseller {
					badRequest(w, "parent_id must reference a reseller")
					return
				}
				u.ParentID = in.ParentID
			}
		}
		if u.Role != db.RoleCustomer {
			u.ParentID = nil
		}
	}
	if in.Disabled != nil || in.MaxDomains != nil || in.MaxDBs != nil || in.MaxDBSize != nil {
		if self { // nobody changes their own status or quota
			forbidden(w)
			return
		}
		if in.Disabled != nil {
			u.Disabled = *in.Disabled
		}
		if in.MaxDomains != nil {
			if *in.MaxDomains < 0 {
				badRequest(w, "max_domains must be >= 0")
				return
			}
			u.MaxDomains = *in.MaxDomains
		}
		if in.MaxDBs != nil {
			if *in.MaxDBs < 0 {
				badRequest(w, "max_databases must be >= 0")
				return
			}
			u.MaxDatabases = *in.MaxDBs
		}
		if in.MaxDBSize != nil {
			if msg := dbSizeCap(a, *in.MaxDBSize); msg != "" {
				badRequest(w, msg)
				return
			}
			u.MaxDBSizeMB = *in.MaxDBSize
		}
	}
	if err := s.db.UpdateUser(r.Context(), u); err != nil {
		s.fail(w, r, err)
		return
	}
	if u.Disabled {
		_ = s.db.RevokeUserRefreshTokens(r.Context(), u.ID)
	}
	if in.MaxDBSize != nil {
		s.kickDBLimits()
	}
	s.audit(r, "user.updated", u.Username, "")
	writeJSON(w, http.StatusOK, u)
}

func (s *Server) handleDeleteUser(w http.ResponseWriter, r *http.Request) {
	u := s.loadManagedUser(w, r)
	if u == nil {
		return
	}
	if u.ID == actor(r).ID {
		badRequest(w, "you cannot delete your own account")
		return
	}
	if err := s.db.DeleteUser(r.Context(), u.ID); err != nil {
		s.fail(w, r, err)
		return
	}
	s.audit(r, "user.deleted", u.Username, "")
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleSetUserPassword(w http.ResponseWriter, r *http.Request) {
	u := s.loadManagedUser(w, r)
	if u == nil {
		return
	}
	if u.ID == actor(r).ID {
		badRequest(w, "use /auth/password to change your own password")
		return
	}
	var in struct {
		Password string `json:"password"`
	}
	if !decode(w, r, &in) {
		return
	}
	if err := s.setPassword(r, u, in.Password); err != nil {
		badRequest(w, err.Error())
		return
	}
	s.audit(r, "user.password_reset", u.Username, "")
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleResetUserTOTP(w http.ResponseWriter, r *http.Request) {
	u := s.loadManagedUser(w, r)
	if u == nil {
		return
	}
	if u.ID == actor(r).ID {
		badRequest(w, "use /auth/2fa/disable for your own account")
		return
	}
	u.TOTPEnabled, u.TOTPSecret = false, ""
	if err := s.db.UpdateUser(r.Context(), u); err != nil {
		s.fail(w, r, err)
		return
	}
	s.audit(r, "user.2fa_reset", u.Username, "")
	w.WriteHeader(http.StatusNoContent)
}

// ---------- API keys ----------

func (s *Server) handleListAPIKeys(w http.ResponseWriter, r *http.Request) {
	keys, err := s.db.ListAPIKeys(r.Context(), actor(r).ID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, keys)
}

func (s *Server) handleCreateAPIKey(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name          string `json:"name"`
		ExpiresInDays int    `json:"expires_in_days"`
	}
	if !decode(w, r, &in) {
		return
	}
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" || len(in.Name) > 64 {
		badRequest(w, "name is required (max 64 chars)")
		return
	}
	if in.ExpiresInDays < 0 || in.ExpiresInDays > 3650 {
		badRequest(w, "expires_in_days must be 0 (never) to 3650")
		return
	}
	full, prefix := auth.NewAPIKey()
	k := &db.APIKey{UserID: actor(r).ID, Name: in.Name, Prefix: prefix, KeyHash: auth.HashToken(full)}
	if in.ExpiresInDays > 0 {
		t := time.Now().UTC().Add(time.Duration(in.ExpiresInDays) * 24 * time.Hour)
		k.ExpiresAt = &t
	}
	if err := s.db.CreateAPIKey(r.Context(), k); err != nil {
		s.fail(w, r, err)
		return
	}
	s.audit(r, "apikey.created", prefix, in.Name)
	writeJSON(w, http.StatusCreated, map[string]any{"key": full, "api_key": k})
}

func (s *Server) handleDeleteAPIKey(w http.ResponseWriter, r *http.Request) {
	id, ok := idParam(w, r, "id")
	if !ok {
		return
	}
	if err := s.db.DeleteAPIKey(r.Context(), actor(r).ID, id); err != nil {
		s.fail(w, r, err)
		return
	}
	s.audit(r, "apikey.deleted", "", "")
	w.WriteHeader(http.StatusNoContent)
}

// ---------- SSH keys ----------

func (s *Server) handleListSSHKeys(w http.ResponseWriter, r *http.Request) {
	keys, err := s.db.ListSSHKeys(r.Context(), actor(r).ID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, keys)
}

func (s *Server) handleCreateSSHKey(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name      string `json:"name"`
		PublicKey string `json:"public_key"`
	}
	if !decode(w, r, &in) {
		return
	}
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" || len(in.Name) > 64 {
		badRequest(w, "name is required (max 64 chars)")
		return
	}
	norm, fp, err := auth.ParseSSHPublicKey(in.PublicKey)
	if err != nil {
		badRequest(w, err.Error())
		return
	}
	k := &db.SSHKey{UserID: actor(r).ID, Name: in.Name, PublicKey: norm, Fingerprint: fp}
	if err := s.db.CreateSSHKey(r.Context(), k); err != nil {
		s.fail(w, r, err)
		return
	}
	s.audit(r, "sshkey.created", fp, in.Name)
	writeJSON(w, http.StatusCreated, k)
}

func (s *Server) handleDeleteSSHKey(w http.ResponseWriter, r *http.Request) {
	id, ok := idParam(w, r, "id")
	if !ok {
		return
	}
	if err := s.db.DeleteSSHKey(r.Context(), actor(r).ID, id); err != nil {
		s.fail(w, r, err)
		return
	}
	s.audit(r, "sshkey.deleted", "", "")
	w.WriteHeader(http.StatusNoContent)
}
