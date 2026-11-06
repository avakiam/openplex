package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"

	"openplex.dev/openplex/internal/db"
	"openplex.dev/openplex/internal/docker"
	"openplex.dev/openplex/internal/ssl"
)

var envKeyRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,127}$`)

// domainInput is shared by create and update; pointer fields are optional.
type domainInput struct {
	Name           string             `json:"name"`
	Kind           string             `json:"kind"`
	ParentID       *int64             `json:"parent_id"`
	OwnerID        *int64             `json:"owner_id"`
	Runtime        *string            `json:"runtime"`
	RuntimeVersion *string            `json:"runtime_version"`
	DocumentRoot   *string            `json:"document_root"`
	StartCommand   *string            `json:"start_command"`
	AppPort        *int               `json:"app_port"`
	SSLEnabled     *bool              `json:"ssl_enabled"`
	ForceHTTPS     *bool              `json:"force_https"`
	MemoryMB       *int               `json:"memory_mb"`
	CPUs           *float64           `json:"cpus"`
	Env            *map[string]string `json:"env"`
}

// apply copies settable fields onto d and validates the result.
func (in *domainInput) apply(d *db.Domain) error {
	if in.Runtime != nil {
		d.Runtime = *in.Runtime
		if in.RuntimeVersion == nil {
			d.RuntimeVersion = ""
		}
	}
	if in.RuntimeVersion != nil {
		d.RuntimeVersion = *in.RuntimeVersion
	}
	if in.DocumentRoot != nil {
		d.DocumentRoot = strings.Trim(*in.DocumentRoot, "/")
	}
	if in.StartCommand != nil {
		d.StartCommand = strings.TrimSpace(*in.StartCommand)
	}
	if in.AppPort != nil {
		d.AppPort = *in.AppPort
	}
	if in.SSLEnabled != nil {
		d.SSLEnabled = *in.SSLEnabled
	}
	if in.ForceHTTPS != nil {
		d.ForceHTTPS = *in.ForceHTTPS
	}
	if in.MemoryMB != nil {
		d.MemoryMB = *in.MemoryMB
	}
	if in.CPUs != nil {
		d.CPUs = *in.CPUs
	}
	if in.Env != nil {
		d.Env = *in.Env
	}

	if d.Kind == db.KindAlias {
		return nil // aliases only carry a name; the parent site defines behaviour
	}
	rt, ver, err := docker.ResolveRuntime(d.Runtime, d.RuntimeVersion)
	if err != nil {
		return err
	}
	d.RuntimeVersion = ver
	if d.DocumentRoot == "" {
		d.DocumentRoot = "public"
	}
	if !ssl.ValidDocumentRoot(d.DocumentRoot) {
		return errors.New("document_root must be a relative path inside the site directory")
	}
	if rt.NeedsCommand {
		if d.StartCommand == "" || len(d.StartCommand) > 2000 || strings.ContainsRune(d.StartCommand, 0) {
			return fmt.Errorf("runtime %s requires start_command (max 2000 chars)", rt.Name)
		}
		if d.AppPort == 0 {
			d.AppPort = rt.DefaultPort
		}
		if d.AppPort < 1024 || d.AppPort > 65535 {
			return errors.New("app_port must be between 1024 and 65535")
		}
	} else {
		d.StartCommand, d.AppPort = "", 0
	}
	if !d.SSLEnabled {
		d.ForceHTTPS = false
	}
	if d.MemoryMB < 64 || d.MemoryMB > 65536 {
		return errors.New("memory_mb must be between 64 and 65536")
	}
	if d.CPUs < 0.1 || d.CPUs > 64 {
		return errors.New("cpus must be between 0.1 and 64")
	}
	if len(d.Env) > 200 {
		return errors.New("at most 200 environment variables")
	}
	for k, v := range d.Env {
		if !envKeyRe.MatchString(k) {
			return fmt.Errorf("invalid environment variable name %q", k)
		}
		if len(v) > 8192 || strings.ContainsRune(v, 0) {
			return fmt.Errorf("environment variable %s is too long or contains NUL", k)
		}
	}
	return nil
}

func normalizeHost(h string) string {
	return strings.TrimSuffix(strings.ToLower(strings.TrimSpace(h)), ".")
}

func (s *Server) handleListRuntimes(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, docker.RuntimeList())
}

func (s *Server) handleListDomains(w http.ResponseWriter, r *http.Request) {
	ids, err := s.visibleOwnerIDs(r.Context(), actor(r))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	ds, err := s.db.ListDomains(r.Context(), db.DomainFilter{OwnerIDs: ids, Kind: r.URL.Query().Get("kind")})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, ds)
}

func (s *Server) handleGetDomain(w http.ResponseWriter, r *http.Request) {
	if d := s.loadDomain(w, r); d != nil {
		writeJSON(w, http.StatusOK, d)
	}
}

func (s *Server) handleCreateDomain(w http.ResponseWriter, r *http.Request) {
	var in domainInput
	if !decode(w, r, &in) {
		return
	}
	ctx := r.Context()
	a := actor(r)
	d := &db.Domain{
		Name: normalizeHost(in.Name), Kind: in.Kind, Runtime: "static", DocumentRoot: "public",
		SSLEnabled: true, ForceHTTPS: true, MemoryMB: 512, CPUs: 1, Env: map[string]string{},
	}
	if d.Kind == "" {
		d.Kind = db.KindPrimary
	}
	if !ssl.ValidHostname(d.Name) || strings.HasPrefix(d.Name, "*.") {
		badRequest(w, "name must be a valid lowercase hostname (wildcards are not supported)")
		return
	}
	if s.cfg.PanelDomain != "" && d.Name == s.cfg.PanelDomain {
		writeErr(w, http.StatusConflict, "conflict", "this hostname is reserved for the panel")
		return
	}

	switch d.Kind {
	case db.KindPrimary:
		d.OwnerID = a.ID
		if in.OwnerID != nil {
			d.OwnerID = *in.OwnerID
		}
		ok, err := s.canAccessOwner(ctx, a, d.OwnerID)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		if !ok {
			forbidden(w)
			return
		}
	case db.KindSubdomain, db.KindAlias:
		if in.ParentID == nil {
			badRequest(w, "parent_id is required for subdomains and aliases")
			return
		}
		parent, err := s.db.GetDomain(ctx, *in.ParentID)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		if ok, err := s.canAccessOwner(ctx, a, parent.OwnerID); err != nil || !ok {
			writeErr(w, http.StatusNotFound, "not_found", "parent domain not found")
			return
		}
		if parent.Kind == db.KindAlias {
			badRequest(w, "parent must be a website, not an alias")
			return
		}
		if d.Kind == db.KindSubdomain && (parent.Kind != db.KindPrimary || !strings.HasSuffix(d.Name, "."+parent.Name)) {
			badRequest(w, "a subdomain must be a child name of its primary domain")
			return
		}
		d.ParentID, d.OwnerID = &parent.ID, parent.OwnerID
	default:
		badRequest(w, "kind must be primary, subdomain or alias")
		return
	}
	if err := s.checkNameOwnership(ctx, d.Name, d.OwnerID); err != nil {
		writeErr(w, http.StatusConflict, "conflict", err.Error())
		return
	}
	if d.Kind != db.KindAlias {
		owner, err := s.db.GetUser(ctx, d.OwnerID)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		if owner.MaxDomains > 0 {
			n, err := s.db.CountDomainsByOwner(ctx, owner.ID)
			if err != nil {
				s.fail(w, r, err)
				return
			}
			if n >= owner.MaxDomains {
				writeErr(w, http.StatusForbidden, "quota_exceeded", fmt.Sprintf("website quota of %d reached", owner.MaxDomains))
				return
			}
		}
	}
	if err := in.apply(d); err != nil {
		badRequest(w, err.Error())
		return
	}
	if d.Kind == db.KindAlias {
		d.Status = db.StatusRunning
	}
	if err := s.db.CreateDomain(ctx, d); err != nil {
		s.fail(w, r, err)
		return
	}
	if d.Kind != db.KindAlias {
		if err := docker.PrepareSiteDir(s.cfg, d); err != nil {
			_ = s.db.DeleteDomain(ctx, d.ID)
			s.fail(w, r, err)
			return
		}
	}
	s.audit(r, "domain.created", d.Name, d.Kind)
	s.deployAsync(d)
	writeJSON(w, http.StatusCreated, d)
}

// checkNameOwnership prevents hijacking: a hostname inside another owner's
// domain (or a parent of another owner's domain) cannot be registered.
func (s *Server) checkNameOwnership(ctx context.Context, name string, ownerID int64) error {
	all, err := s.db.ListDomains(ctx, db.DomainFilter{})
	if err != nil {
		return err
	}
	for _, o := range all {
		if o.OwnerID == ownerID {
			continue
		}
		if strings.HasSuffix(name, "."+o.Name) || strings.HasSuffix(o.Name, "."+name) {
			return fmt.Errorf("%s overlaps with a domain owned by another account", name)
		}
	}
	return nil
}

func (s *Server) handleUpdateDomain(w http.ResponseWriter, r *http.Request) {
	d := s.loadDomain(w, r)
	if d == nil {
		return
	}
	var in domainInput
	if !decode(w, r, &in) {
		return
	}
	if in.Name != "" || in.Kind != "" || in.ParentID != nil || in.OwnerID != nil {
		badRequest(w, "name, kind, parent_id and owner_id cannot be changed")
		return
	}
	if d.Kind == db.KindAlias {
		badRequest(w, "aliases have no settings; edit the parent website")
		return
	}
	if err := in.apply(d); err != nil {
		badRequest(w, err.Error())
		return
	}
	if err := s.db.UpdateDomain(r.Context(), d); err != nil {
		s.fail(w, r, err)
		return
	}
	s.audit(r, "domain.updated", d.Name, "")
	if d.Status != db.StatusStopped && d.Status != db.StatusDisabled {
		s.deployAsync(d)
	} else {
		s.applyEdgeAsync()
	}
	writeJSON(w, http.StatusOK, d)
}

func (s *Server) handleDeleteDomain(w http.ResponseWriter, r *http.Request) {
	d := s.loadDomain(w, r)
	if d == nil {
		return
	}
	purge := r.URL.Query().Get("purge") == "true"
	ctx := r.Context()
	var children []*db.Domain
	if d.Kind != db.KindAlias {
		var err error
		children, err = s.db.ListDomains(ctx, db.DomainFilter{ParentID: &d.ID, Kind: db.KindSubdomain})
		if err != nil {
			s.fail(w, r, err)
			return
		}
	}
	if err := s.db.DeleteDomain(ctx, d.ID); err != nil {
		s.fail(w, r, err)
		return
	}
	s.audit(r, "domain.deleted", d.Name, fmt.Sprintf("purge=%v", purge))
	if s.orch != nil {
		s.background(func(ctx context.Context) error {
			for _, c := range append(children, d) {
				if c.Kind == db.KindAlias {
					continue
				}
				if err := s.orch.RemoveSite(ctx, c, purge); err != nil {
					return fmt.Errorf("remove %s: %w", c.Name, err)
				}
			}
			return s.orch.ApplyEdge(ctx)
		})
	}
	w.WriteHeader(http.StatusAccepted)
}

func (s *Server) handleDomainAction(w http.ResponseWriter, r *http.Request) {
	d := s.loadDomain(w, r)
	if d == nil {
		return
	}
	if s.orch == nil {
		writeErr(w, http.StatusServiceUnavailable, "docker_unavailable", "the Docker orchestrator is not running")
		return
	}
	if d.Kind == db.KindAlias {
		badRequest(w, "aliases have no lifecycle; act on the parent website")
		return
	}
	action := strings.TrimPrefix(r.URL.Path[strings.LastIndex(r.URL.Path, "/"):], "/")
	if d.Status == db.StatusDisabled && actor(r).Role == db.RoleCustomer {
		writeErr(w, http.StatusForbidden, "disabled", "this website was disabled by your provider")
		return
	}
	var fn func(context.Context) error
	switch action {
	case "deploy":
		fn = func(ctx context.Context) error { return s.orch.DeploySite(ctx, d) }
	case "start":
		fn = func(ctx context.Context) error { return s.orch.StartSite(ctx, d) }
	case "stop":
		fn = func(ctx context.Context) error { return s.orch.StopSite(ctx, d) }
	case "restart":
		fn = func(ctx context.Context) error { return s.orch.RestartSite(ctx, d) }
	default:
		badRequest(w, "unknown action")
		return
	}
	s.audit(r, "domain."+action, d.Name, "")
	if action != "restart" {
		_ = s.db.SetDomainStatus(r.Context(), d.ID, db.StatusPending, action+" in progress", d.ContainerID)
	}
	s.background(fn)
	w.WriteHeader(http.StatusAccepted)
}

func (s *Server) handleDomainStats(w http.ResponseWriter, r *http.Request) {
	d := s.loadDomain(w, r)
	if d == nil {
		return
	}
	if s.orch == nil {
		writeErr(w, http.StatusServiceUnavailable, "docker_unavailable", "the Docker orchestrator is not running")
		return
	}
	all, err := s.orch.Stats(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	for _, st := range all {
		if st.DomainID == d.ID {
			writeJSON(w, http.StatusOK, st)
			return
		}
	}
	writeErr(w, http.StatusNotFound, "not_found", "this website has no runtime container")
}

// ---------- redirects ----------

func (s *Server) handleListRedirects(w http.ResponseWriter, r *http.Request) {
	d := s.loadDomain(w, r)
	if d == nil {
		return
	}
	rs, err := s.db.ListRedirects(r.Context(), &d.ID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, rs)
}

func (s *Server) handleCreateRedirect(w http.ResponseWriter, r *http.Request) {
	d := s.loadDomain(w, r)
	if d == nil {
		return
	}
	if d.Kind == db.KindAlias {
		badRequest(w, "add redirects to the parent website")
		return
	}
	var in struct {
		SourcePath string `json:"source_path"`
		TargetURL  string `json:"target_url"`
		StatusCode int    `json:"status_code"`
	}
	if !decode(w, r, &in) {
		return
	}
	if in.StatusCode == 0 {
		in.StatusCode = 301
	}
	rule := ssl.Redirect{Path: strings.TrimSpace(in.SourcePath), Target: strings.TrimSpace(in.TargetURL), Code: in.StatusCode}
	if err := ssl.ValidRedirect(rule); err != nil {
		badRequest(w, err.Error())
		return
	}
	rd := &db.Redirect{DomainID: d.ID, SourcePath: rule.Path, TargetURL: rule.Target, StatusCode: rule.Code}
	if err := s.db.CreateRedirect(r.Context(), rd); err != nil {
		s.fail(w, r, err)
		return
	}
	s.audit(r, "redirect.created", d.Name, rd.SourcePath+" -> "+rd.TargetURL)
	s.applyEdgeAsync()
	writeJSON(w, http.StatusCreated, rd)
}

func (s *Server) handleDeleteRedirect(w http.ResponseWriter, r *http.Request) {
	d := s.loadDomain(w, r)
	if d == nil {
		return
	}
	rid, ok := idParam(w, r, "rid")
	if !ok {
		return
	}
	if err := s.db.DeleteRedirect(r.Context(), d.ID, rid); err != nil {
		s.fail(w, r, err)
		return
	}
	s.audit(r, "redirect.deleted", d.Name, "")
	s.applyEdgeAsync()
	w.WriteHeader(http.StatusNoContent)
}

// ---------- background work ----------

// background runs fn detached from the request with a generous timeout,
// tracked so shutdown can wait for it.
func (s *Server) background(fn func(context.Context) error) {
	s.bg.Add(1)
	go func() {
		defer s.bg.Done()
		ctx, cancel := context.WithTimeout(s.baseCtx, 15*time.Minute)
		defer cancel()
		if err := fn(ctx); err != nil {
			s.log.Error("background task failed", "err", err)
		}
	}()
}

func (s *Server) deployAsync(d *db.Domain) {
	if s.orch == nil {
		if d.Kind != db.KindAlias {
			_ = s.db.SetDomainStatus(s.baseCtx, d.ID, db.StatusError, "Docker is not available on this server", "")
		}
		return
	}
	if d.Kind == db.KindAlias {
		s.applyEdgeAsync()
		return
	}
	cp := *d
	s.background(func(ctx context.Context) error { return s.orch.DeploySite(ctx, &cp) })
}

func (s *Server) applyEdgeAsync() {
	if s.orch == nil {
		return
	}
	s.background(s.orch.ApplyEdge)
}
