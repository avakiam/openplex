package api

import (
	"context"
	"errors"
	"fmt"
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
	if m := dbSizeCap(a, in.MaxDBSize); m != nil {
		writeMsg(w, http.StatusBadRequest, "bad_request", m)
		return
	}
	if err := auth.CheckPolicy(in.Password); err != nil {
		badRequestErr(w, err)
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
			if m := dbSizeCap(a, *in.MaxDBSize); m != nil {
				writeMsg(w, http.StatusBadRequest, "bad_request", m)
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

// DELETE /users/{id}: an account that owns nothing. With ?purge=true its
// websites (with their files, mail and DNS) and databases go first.
func (s *Server) handleDeleteUser(w http.ResponseWriter, r *http.Request) {
	u := s.loadManagedUser(w, r)
	if u == nil {
		return
	}
	if u.ID == actor(r).ID {
		badRequest(w, "you cannot delete your own account")
		return
	}
	ctx := r.Context()
	purge := r.URL.Query().Get("purge") == "true"
	if purge {
		customers, err := s.db.ListUsers(ctx, db.UserFilter{ParentID: &u.ID})
		if err != nil {
			s.fail(w, r, err)
			return
		}
		if len(customers) > 0 {
			writeErr(w, http.StatusConflict, "has_customers", "this account has customers: delete them first")
			return
		}
		if err := s.purgeUser(r, u); err != nil {
			s.fail(w, r, err)
			return
		}
	}
	if err := s.db.DeleteUser(ctx, u.ID); err != nil {
		if errors.Is(err, db.ErrConflict) {
			writeErr(w, http.StatusConflict, "in_use", "this account still owns websites, databases or customers (?purge=true deletes its websites and databases)")
			return
		}
		s.fail(w, r, err)
		return
	}
	s.audit(r, "user.deleted", u.Username, fmt.Sprintf("purge=%v", purge))
	w.WriteHeader(http.StatusNoContent)
}

// purgeUser deletes the databases and websites u owns.
func (s *Server) purgeUser(r *http.Request, u *db.User) error {
	ctx := r.Context()
	dbs, err := s.db.ListDatabases(ctx, []int64{u.ID})
	if err != nil {
		return err
	}
	if len(dbs) > 0 && s.orch == nil {
		return errors.New("deleting databases needs Docker, which is unavailable")
	}
	for _, d := range dbs {
		if err := s.orch.DropDatabase(ctx, d); err != nil {
			return fmt.Errorf("delete database %s: %w", d.Name, err)
		}
		if err := s.db.DeleteDatabase(ctx, d.ID); err != nil {
			return err
		}
		s.audit(r, "database.deleted", d.Name, d.Engine)
		if d.RemoteAllow != "" {
			s.syncRemote(ctx)
		}
	}
	// Websites first (their subdomains and aliases go with them), then any
	// subdomain or alias it owns under someone else's website.
	for _, kind := range []string{db.KindPrimary, ""} {
		ds, err := s.db.ListDomains(ctx, db.DomainFilter{OwnerIDs: []int64{u.ID}, Kind: kind})
		if err != nil {
			return err
		}
		for _, d := range ds {
			if _, err := s.db.GetDomain(ctx, d.ID); errors.Is(err, db.ErrNotFound) {
				continue // went with its website
			}
			if err := s.deleteDomain(r, d, true); err != nil {
				return fmt.Errorf("delete %s: %w", d.Name, err)
			}
		}
	}
	return nil
}

// POST /users/{id}/suspend: the account can't sign in (nor use its API
// keys) and its websites — for a reseller, its customers' too — go offline.
func (s *Server) handleSuspendUser(w http.ResponseWriter, r *http.Request) {
	s.setSuspended(w, r, true)
}

// POST /users/{id}/unsuspend: the account and its websites are back as they
// were before the suspension.
func (s *Server) handleUnsuspendUser(w http.ResponseWriter, r *http.Request) {
	s.setSuspended(w, r, false)
}

func (s *Server) setSuspended(w http.ResponseWriter, r *http.Request, on bool) {
	u := s.loadManagedUser(w, r)
	if u == nil {
		return
	}
	if u.ID == actor(r).ID {
		badRequest(w, "you cannot suspend your own account")
		return
	}
	ctx := r.Context()
	owners := []int64{u.ID}
	if u.Role == db.RoleReseller {
		customers, err := s.db.ListUsers(ctx, db.UserFilter{ParentID: &u.ID})
		if err != nil {
			s.fail(w, r, err)
			return
		}
		for _, c := range customers {
			owners = append(owners, c.ID)
		}
	}
	u.Disabled = on
	if err := s.db.UpdateUser(ctx, u); err != nil {
		s.fail(w, r, err)
		return
	}
	var changed []*db.Domain
	var err error
	if on {
		_ = s.db.RevokeUserRefreshTokens(ctx, u.ID)
		changed, err = s.db.SuspendDomains(ctx, owners)
	} else {
		changed, err = s.db.ResumeDomains(ctx, owners)
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	action := map[bool]string{true: "user.suspended", false: "user.unsuspended"}[on]
	s.audit(r, action, u.Username, fmt.Sprintf("%d websites", len(changed)))
	if s.orch != nil && len(changed) > 0 {
		s.background(func(ctx context.Context) error {
			var errs []error
			for _, d := range changed {
				switch {
				case on:
					errs = append(errs, s.orch.HaltSite(ctx, d.ID))
				case d.Status != db.StatusStopped:
					d.Status = db.StatusRunning
					errs = append(errs, s.orch.DeploySite(ctx, d))
				}
			}
			errs = append(errs, s.orch.ApplyEdge(ctx))
			return errors.Join(errs...)
		})
	}
	writeJSON(w, http.StatusOK, u)
}

// POST /users/{id}/login-link {"expires_in_seconds"}: a one-time link that
// signs the account in, for a billing system's "log in to the panel" button.
// It lasts 5 minutes by default (at most a day).
func (s *Server) handleCreateLoginLink(w http.ResponseWriter, r *http.Request) {
	u := s.loadManagedUser(w, r)
	if u == nil {
		return
	}
	var in struct {
		ExpiresInSeconds int `json:"expires_in_seconds"`
	}
	if r.ContentLength != 0 && !decode(w, r, &in) {
		return
	}
	ttl := 5 * time.Minute
	switch {
	case in.ExpiresInSeconds < 0 || in.ExpiresInSeconds > 24*3600:
		badRequest(w, "expires_in_seconds must be between 1 and 86400")
		return
	case in.ExpiresInSeconds > 0:
		ttl = time.Duration(in.ExpiresInSeconds) * time.Second
	}
	if u.Disabled {
		writeErr(w, http.StatusConflict, "disabled", "the account is disabled")
		return
	}
	token := auth.NewRefreshToken()
	expires := time.Now().Add(ttl).UTC().Truncate(time.Second)
	if err := s.db.CreateLoginLink(r.Context(), u.ID, auth.HashToken(token), expires, db.LinkSSO); err != nil {
		s.fail(w, r, err)
		return
	}
	s.audit(r, "auth.login_link_created", u.Username, "single sign-on, valid "+ttl.String())
	path := "/login/?link=" + token
	out := map[string]any{"path": path, "expires_at": expires}
	if s.cfg.PanelDomain != "" {
		out["url"] = "https://" + s.cfg.PanelDomain + path
	}
	writeJSON(w, http.StatusCreated, out)
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
		badRequestErr(w, err)
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
		badRequestErr(w, err)
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
