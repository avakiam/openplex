package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httputil"
	"net/url"
	"reflect"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"openplex.dev/openplex/internal/db"
	"openplex.dev/openplex/internal/extensions"
	"openplex.dev/openplex/internal/msg"
	"openplex.dev/openplex/internal/ssl"
)

// emit tells subscribed extensions about a change.
func (s *Server) emit(typ string, data map[string]any) {
	if s.ext != nil {
		s.ext.Emit(typ, data)
	}
}

// ---------- administration (admins) ----------

type extensionView struct {
	*db.Extension
	Manifest   *extensions.Manifest `json:"manifest"`
	Settings   map[string]string    `json:"settings"`
	SecretsSet []string             `json:"secrets_set"`
	HasLicense bool                 `json:"has_license"`
	Icon       string               `json:"icon,omitempty"` // data: URL of the package's, or the marketplace's https URL
}

func (s *Server) extensionView(e *db.Extension) extensionView {
	man, _ := s.ext.Manifest(e)
	v, set := s.ext.PublicSettings(e)
	return extensionView{Extension: e, Manifest: man, Settings: v, SecretsSet: set, HasLicense: e.LicenseEnc != "", Icon: s.ext.Icon(e.ID)}
}

// GET /extensions
func (s *Server) handleListExtensions(w http.ResponseWriter, r *http.Request) {
	list, err := s.db.ListExtensions(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	out := make([]extensionView, 0, len(list))
	for _, e := range list {
		out = append(out, s.extensionView(e))
	}
	writeJSON(w, http.StatusOK, out)
}

// POST /extensions/upload (multipart "package"): a package for review.
func (s *Server) handleUploadExtension(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, extensions.MaxPackageSize+(1<<20))
	mr, err := r.MultipartReader()
	if err != nil {
		badRequest(w, "send the package as multipart/form-data")
		return
	}
	for {
		part, err := mr.NextPart()
		if err != nil {
			badRequest(w, "no package in the upload")
			return
		}
		if part.FormName() != "package" {
			continue
		}
		p, err := s.ext.Receive(r.Context(), part, "upload", "")
		if err != nil {
			s.extensionError(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, p)
		return
	}
}

// GET /extensions/marketplace
func (s *Server) handleMarketplace(w http.ResponseWriter, r *http.Request) {
	cat, err := s.ext.Catalog(r.Context())
	if err != nil {
		writeErrFrom(w, http.StatusBadGateway, "marketplace_unavailable", err)
		return
	}
	writeJSON(w, http.StatusOK, cat)
}

// POST /extensions/marketplace/download {id, license_key}: a package for review.
func (s *Server) handleMarketplaceDownload(w http.ResponseWriter, r *http.Request) {
	var in struct {
		ID      string `json:"id"`
		License string `json:"license_key"`
	}
	if !decode(w, r, &in) {
		return
	}
	p, err := s.ext.Download(r.Context(), in.ID, in.License)
	if err != nil {
		s.extensionError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, p)
}

// POST /extensions/install {token}
func (s *Server) handleInstallExtension(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Token string `json:"token"`
	}
	if !decode(w, r, &in) {
		return
	}
	e, err := s.ext.Install(r.Context(), in.Token)
	if err != nil {
		s.extensionError(w, r, err)
		return
	}
	s.audit(r, "extension.installed", e.ID, e.Version+" "+e.Source+" signer="+e.Signer)
	if man, err := s.ext.Manifest(e); err == nil && !slices.Contains(man.Permissions, "edge:write") {
		_ = s.db.SetExtensionEdgePolicy(r.Context(), e.ID, db.EdgePolicy{})
	}
	s.applyEdgeAsync()
	writeJSON(w, http.StatusOK, s.extensionView(e))
}

// DELETE /extensions/pending/{token}: the reviewed package is not wanted.
func (s *Server) handleDiscardExtension(w http.ResponseWriter, r *http.Request) {
	s.ext.Discard(chi.URLParam(r, "token"))
	w.WriteHeader(http.StatusNoContent)
}

// PATCH /extensions/{id} {enabled}
func (s *Server) handleUpdateExtension(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Enabled *bool `json:"enabled"`
	}
	if !decode(w, r, &in) {
		return
	}
	id := chi.URLParam(r, "id")
	e, err := s.db.GetExtension(r.Context(), id)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if in.Enabled != nil {
		if e, err = s.ext.SetEnabled(r.Context(), id, *in.Enabled); err != nil {
			s.extensionError(w, r, err)
			return
		}
		s.audit(r, "extension.updated", id, "enabled="+strconv.FormatBool(*in.Enabled))
		s.applyEdgeAsync() // its edge policy applies only while it is enabled
	}
	writeJSON(w, http.StatusOK, s.extensionView(e))
}

// PUT /extensions/{id}/settings {values}
func (s *Server) handlePutExtensionSettings(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Values map[string]string `json:"values"`
	}
	if !decode(w, r, &in) {
		return
	}
	id := chi.URLParam(r, "id")
	if err := s.ext.SaveSettings(r.Context(), id, in.Values); err != nil {
		s.extensionError(w, r, err)
		return
	}
	s.audit(r, "extension.settings", id, "")
	e, err := s.db.GetExtension(r.Context(), id)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, s.extensionView(e))
}

// PUT /extensions/{id}/license {license_key}
func (s *Server) handlePutExtensionLicense(w http.ResponseWriter, r *http.Request) {
	var in struct {
		License string `json:"license_key"`
	}
	if !decode(w, r, &in) {
		return
	}
	id := chi.URLParam(r, "id")
	if err := s.ext.SetLicense(r.Context(), id, in.License); err != nil {
		s.extensionError(w, r, err)
		return
	}
	s.audit(r, "extension.license", id, "")
	w.WriteHeader(http.StatusNoContent)
}

// DELETE /extensions/{id}
func (s *Server) handleDeleteExtension(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if err := s.ext.Uninstall(r.Context(), id); err != nil {
		s.extensionError(w, r, err)
		return
	}
	s.audit(r, "extension.removed", id, "")
	s.applyEdgeAsync()
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) extensionError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, db.ErrNotFound):
		writeErr(w, http.StatusNotFound, "not_found", "extension not found")
	case errors.Is(err, extensions.ErrUnavailable):
		writeErrFrom(w, http.StatusServiceUnavailable, "docker_unavailable", err)
	case errors.Is(err, extensions.ErrMarketplace):
		writeErrFrom(w, http.StatusBadGateway, "marketplace_unavailable", err)
	default:
		var mb *http.MaxBytesError
		if errors.As(err, &mb) {
			writeErr(w, http.StatusRequestEntityTooLarge, "too_large", "the package is too big")
			return
		}
		badRequestErr(w, err)
	}
}

// ---------- pages for every user ----------

type extensionUI struct {
	ID       string             `json:"id"`
	Name     extensions.Text    `json:"name"`
	Icon     string             `json:"icon,omitempty"` // data: URL of the package's, or the marketplace's https URL
	Pages    []extensionUIEntry `json:"pages"`
	SiteTabs []extensionUIEntry `json:"site_tabs"`
}

type extensionUIEntry struct {
	ID    string          `json:"id"`
	Title extensions.Text `json:"title"`
	Area  string          `json:"area,omitempty"`
}

// GET /extensions/ui: the pages and website tabs the caller may open.
func (s *Server) handleExtensionsUI(w http.ResponseWriter, r *http.Request) {
	list, err := s.db.ListExtensions(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	role := actor(r).Role
	out := []extensionUI{}
	for _, e := range list {
		man, err := s.ext.Manifest(e)
		if !e.Enabled || err != nil {
			continue
		}
		v := extensionUI{ID: e.ID, Name: man.Name, Pages: []extensionUIEntry{}, SiteTabs: []extensionUIEntry{}}
		for _, p := range man.Pages {
			if p.Visible(role) {
				v.Pages = append(v.Pages, extensionUIEntry{ID: p.ID, Title: p.Title})
			}
		}
		for _, p := range man.SiteTabs {
			if p.Visible(role) {
				v.SiteTabs = append(v.SiteTabs, extensionUIEntry{ID: p.ID, Title: p.Title, Area: p.Area})
			}
		}
		if len(v.Pages)+len(v.SiteTabs) > 0 {
			v.Icon = s.ext.Icon(e.ID)
			out = append(out, v)
		}
	}
	writeJSON(w, http.StatusOK, out)
}

// POST /extensions/{id}/session {page | site_tab + domain_id}: the URL that
// shows that page in the dashboard's frame.
func (s *Server) handleExtensionSession(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Page     string `json:"page"`
		SiteTab  string `json:"site_tab"`
		DomainID int64  `json:"domain_id"`
	}
	if !decode(w, r, &in) {
		return
	}
	id := chi.URLParam(r, "id")
	e, err := s.db.GetExtension(r.Context(), id)
	if err != nil || !e.Enabled {
		writeErr(w, http.StatusNotFound, "not_found", "extension not found")
		return
	}
	man, err := s.ext.Manifest(e)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	u := actor(r)
	var page *extensions.Page
	list, key := man.Pages, in.Page
	if in.SiteTab != "" {
		list, key = man.SiteTabs, in.SiteTab
	}
	for i := range list {
		if list[i].ID == key && list[i].Visible(u.Role) {
			page = &list[i]
		}
	}
	if page == nil {
		writeErr(w, http.StatusNotFound, "not_found", "no such page")
		return
	}
	var domain int64
	if in.SiteTab != "" {
		if _, _, ok := s.extensionSiteAccess(r.Context(), u, in.DomainID, page.Area); !ok {
			writeErr(w, http.StatusNotFound, "not_found", "resource not found")
			return
		}
		domain = in.DomainID
	}
	tok := s.ext.NewSession(id, u.ID, domain, page.Area)
	writeJSON(w, http.StatusOK, map[string]string{"url": "/ext/" + id + "/s/" + tok + page.Path})
}

// extensionSiteAccess: may u open a site tab (needing area) of domain id?
func (s *Server) extensionSiteAccess(ctx context.Context, u *db.User, id int64, area string) (d *db.Domain, readOnly, ok bool) {
	d, err := s.db.GetDomain(ctx, id)
	if err != nil {
		return nil, false, false
	}
	if allowed, err := s.canAccessOwner(ctx, u, d.OwnerID); err == nil && allowed {
		return d, false, true
	}
	g, err := s.siteGrant(ctx, u.ID, d)
	if err != nil || g == nil || (area != "" && !g.Has(area)) {
		return nil, false, false
	}
	return d, g.ReadOnly, true
}

// extensionPages proxies /ext/{id}/s/{session}/... to the extension's
// backend for the session's user. Pages run sandboxed (opaque origin): they
// can't use the panel's session even though they share its host.
func (s *Server) extensionPages() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rest := strings.TrimPrefix(r.URL.Path, "/ext/")
		id, rest, _ := strings.Cut(rest, "/s/")
		tok, sub, _ := strings.Cut(rest, "/")
		sess := s.ext.LookupSession(id, tok)
		if sess == nil {
			http.Error(w, "This page has expired. Open it again from the OpenPlex panel.", http.StatusForbidden)
			return
		}
		e, err := s.db.GetExtension(r.Context(), id)
		u, uerr := s.db.GetUser(r.Context(), sess.UserID)
		if err != nil || !e.Enabled || uerr != nil || u.Disabled {
			http.Error(w, "This extension is not available.", http.StatusNotFound)
			return
		}
		readOnly := false
		if sess.DomainID != 0 { // checked again on every request: access may be revoked
			_, ro, ok := s.extensionSiteAccess(r.Context(), u, sess.DomainID, sess.Area)
			if !ok {
				http.Error(w, "You no longer have access to this website.", http.StatusForbidden)
				return
			}
			readOnly = ro
		}
		addr, err := s.ext.Addr(r.Context(), id)
		if err != nil {
			http.Error(w, "The extension is not running.", http.StatusBadGateway)
			return
		}
		base := "/ext/" + id + "/s/" + tok + "/"
		lang := r.URL.Query().Get("lang")
		if lang == "" {
			lang = u.Locale
		}
		target := &url.URL{Scheme: "http", Host: addr}
		rp := &httputil.ReverseProxy{
			Rewrite: func(pr *httputil.ProxyRequest) {
				pr.SetURL(target)
				pr.Out.URL.Path = "/" + sub
				pr.Out.URL.RawPath = ""
				pr.Out.Header.Del("Cookie")
				pr.Out.Header.Del("Authorization")
				for k := range pr.Out.Header {
					if strings.HasPrefix(strings.ToLower(k), "x-openplex-") {
						pr.Out.Header.Del(k)
					}
				}
				h := pr.Out.Header
				h.Set("X-OpenPlex-Token", e.Token)
				h.Set("X-OpenPlex-User", strconv.FormatInt(u.ID, 10))
				h.Set("X-OpenPlex-Username", u.Username)
				h.Set("X-OpenPlex-Role", u.Role)
				h.Set("X-OpenPlex-Lang", lang)
				h.Set("X-OpenPlex-Base", base)
				if sess.DomainID != 0 {
					h.Set("X-OpenPlex-Domain", strconv.FormatInt(sess.DomainID, 10))
					h.Set("X-OpenPlex-Read-Only", strconv.FormatBool(readOnly))
				}
			},
			ModifyResponse: func(res *http.Response) error {
				h := res.Header
				h.Del("Set-Cookie")
				h.Set("Content-Security-Policy", "sandbox allow-scripts allow-forms allow-popups allow-downloads allow-modals; frame-ancestors 'self'")
				h.Set("Referrer-Policy", "no-referrer")
				h.Set("X-Content-Type-Options", "nosniff")
				h.Set("Cache-Control", "no-store")
				return nil
			},
			ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
				s.log.Warn("extension page", "id", id, "err", err)
				http.Error(w, "The extension did not answer.", http.StatusBadGateway)
			},
		}
		w.Header().Del("X-Frame-Options") // shown inside the dashboard's frame
		w.Header().Del("Content-Security-Policy")
		rp.ServeHTTP(w, r)
	})
}

// ---------- the extension's own panel socket ----------

// extensionScopes maps API requests to the permission an extension needs.
var extensionScopes = []struct {
	methods string
	path    *regexp.Regexp
	scope   string
}{
	{"GET", regexp.MustCompile(`^/api/v1/(auth/me|system/health)$`), ""},
	{"GET", regexp.MustCompile(`^/api/v1/domains/?$`), "domains:read"},
	{"GET", regexp.MustCompile(`^/api/v1/domains/\d+/?$`), "domains:read"},
	{"GET", regexp.MustCompile(`^/api/v1/domains/\d+/dns$`), "dns:read"},
	{"POST", regexp.MustCompile(`^/api/v1/domains/\d+/dns/records$`), "dns:write"},
	{"PATCH DELETE", regexp.MustCompile(`^/api/v1/domains/\d+/dns/records/\d+$`), "dns:write"},
	{"GET", regexp.MustCompile(`^/api/v1/domains/\d+/mail$`), "mail:read"},
	{"GET", regexp.MustCompile(`^/api/v1/databases/?$`), "databases:read"},
	{"GET", regexp.MustCompile(`^/api/v1/databases/\d+$`), "databases:read"},
	{"GET", regexp.MustCompile(`^/api/v1/users/?$`), "users:read"},
	{"GET", regexp.MustCompile(`^/api/v1/users/\d+$`), "users:read"},
	{"GET", regexp.MustCompile(`^/api/v1/system/(info|stats)$`), "system:read"},
}

// extensionScope returns the permission r needs, and whether r is allowed
// to extensions at all.
func extensionScope(method, path string) (string, bool) {
	for _, s := range extensionScopes {
		if slices.Contains(strings.Fields(s.methods), method) && s.path.MatchString(path) {
			return s.scope, true
		}
	}
	return "", false
}

// ExtensionAPI is the handler of extension id's panel socket. The socket
// itself identifies the extension; it may use the API within the
// permissions it was installed with, acting either with administrator reach
// or (X-OpenPlex-User) as a given panel user.
func (s *Server) ExtensionAPI(id string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		e, err := s.db.GetExtension(ctx, id)
		if err != nil || !e.Enabled {
			writeErr(w, http.StatusForbidden, "forbidden", "this extension is not enabled")
			return
		}
		man, err := s.ext.Manifest(e)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		r.Header.Del("X-Forwarded-For")
		r.Header.Del("Authorization")
		r.RemoteAddr = "extension/" + id
		if r.URL.Path == "/api/v1/extension/edge" {
			if !slices.Contains(man.Permissions, "edge:write") {
				writeErr(w, http.StatusForbidden, "forbidden", "this extension was not given the edge:write permission")
				return
			}
			p := &Principal{User: &db.User{Username: "extension:" + id, Role: db.RoleAdmin}, Extension: id}
			s.handleExtensionEdge(w, r.WithContext(context.WithValue(ctx, principalKey, p)), id)
			return
		}
		if r.URL.Path == "/api/v1/extension" && r.Method == http.MethodGet {
			writeJSON(w, http.StatusOK, map[string]any{
				"id": e.ID, "version": e.Version, "settings": s.ext.Settings(e), "license_key": s.ext.License(e),
				"token": e.Token, "panel_version": s.version,
			})
			return
		}
		scope, known := extensionScope(r.Method, r.URL.Path)
		if !known {
			writeErr(w, http.StatusForbidden, "forbidden", "extensions can't use this endpoint")
			return
		}
		if scope != "" && !slices.Contains(man.Permissions, scope) {
			writeMsg(w, http.StatusForbidden, "forbidden", msg.New("this extension was not given the {scope} permission", "scope", scope))
			return
		}
		u := &db.User{Username: "extension:" + id, Role: db.RoleAdmin}
		if h := r.Header.Get("X-OpenPlex-User"); h != "" {
			uid, err := strconv.ParseInt(h, 10, 64)
			if err != nil {
				badRequest(w, "X-OpenPlex-User must be a user id")
				return
			}
			if u, err = s.db.GetUser(ctx, uid); err != nil || u.Disabled {
				writeErr(w, http.StatusForbidden, "forbidden", "that user is not available")
				return
			}
		}
		p := &Principal{User: u, Extension: id}
		s.root().ServeHTTP(w, r.WithContext(context.WithValue(ctx, principalKey, p)))
	})
}

// ---------- edge policy (edge:write) ----------

// Limits of an extension's edge policy.
const (
	maxEdgeNetworks = 256  // per list
	maxEdgeHeaders  = 4    // client IP headers
	maxEdgeHosts    = 2000 // restricted hosts
)

// handleExtensionEdge is GET/PUT /api/v1/extension/edge on an extension's
// socket: what it asks of the edge (trusted proxies, host restrictions).
func (s *Server) handleExtensionEdge(w http.ResponseWriter, r *http.Request, id string) {
	switch r.Method {
	case http.MethodGet:
		p, err := s.db.GetExtensionEdgePolicy(r.Context(), id)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, edgePolicyJSON(p))
	case http.MethodPut:
		var in db.EdgePolicy
		if !decode(w, r, &in) {
			return
		}
		p, err := s.validEdgePolicy(r.Context(), in)
		if err != nil {
			badRequestErr(w, err)
			return
		}
		old, err := s.db.GetExtensionEdgePolicy(r.Context(), id)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		if !reflect.DeepEqual(edgePolicyJSON(old), edgePolicyJSON(p)) {
			if err := s.db.SetExtensionEdgePolicy(r.Context(), id, p); err != nil {
				s.fail(w, r, err)
				return
			}
			hosts := make([]string, 0, len(p.Restrict))
			for _, x := range p.Restrict {
				hosts = append(hosts, x.Host)
			}
			s.audit(r, "extension.edge_updated", id, fmt.Sprintf("%d trusted proxies; restricted: %s",
				len(p.TrustedProxies), strings.Join(hosts, ", ")))
			s.applyEdgeAsync()
		}
		writeJSON(w, http.StatusOK, edgePolicyJSON(p))
	default:
		writeErr(w, http.StatusMethodNotAllowed, "method_not_allowed", "use GET or PUT")
	}
}

// edgePolicyJSON never answers null lists.
func edgePolicyJSON(p db.EdgePolicy) db.EdgePolicy {
	if p.TrustedProxies == nil {
		p.TrustedProxies = []string{}
	}
	if p.ClientIPHeaders == nil {
		p.ClientIPHeaders = []string{}
	}
	if p.Restrict == nil {
		p.Restrict = []db.EdgeRestriction{}
	}
	return p
}

// validEdgePolicy checks and normalizes a policy: networks in canonical form
// and not too broad, plain header names, and only hosts of this server's
// websites.
func (s *Server) validEdgePolicy(ctx context.Context, in db.EdgePolicy) (db.EdgePolicy, error) {
	var out db.EdgePolicy
	nets := func(list []string, what string) ([]string, error) {
		if len(list) > maxEdgeNetworks {
			return nil, fmt.Errorf("%s: at most %d networks", what, maxEdgeNetworks)
		}
		seen := map[string]bool{}
		var res []string
		for _, c := range list {
			n, err := ssl.NormalizePrefix(c)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", what, err)
			}
			if !seen[n] {
				seen[n] = true
				res = append(res, n)
			}
		}
		sort.Strings(res)
		return res, nil
	}
	var err error
	if out.TrustedProxies, err = nets(in.TrustedProxies, "trusted_proxies"); err != nil {
		return out, err
	}
	if len(in.ClientIPHeaders) > maxEdgeHeaders {
		return out, fmt.Errorf("client_ip_headers: at most %d headers", maxEdgeHeaders)
	}
	for _, h := range in.ClientIPHeaders {
		if !ssl.ValidHeaderName(h) {
			return out, fmt.Errorf("client_ip_headers: invalid header name %q", h)
		}
		if !slices.Contains(out.ClientIPHeaders, h) {
			out.ClientIPHeaders = append(out.ClientIPHeaders, h)
		}
	}
	if len(out.ClientIPHeaders) > 0 && len(out.TrustedProxies) == 0 {
		return out, errors.New("client_ip_headers need trusted_proxies")
	}
	if len(in.Restrict) > maxEdgeHosts {
		return out, fmt.Errorf("restrict: at most %d hosts", maxEdgeHosts)
	}
	if len(in.Restrict) > 0 {
		ds, err := s.db.ListDomains(ctx, db.DomainFilter{})
		if err != nil {
			return out, err
		}
		sites := map[string]bool{}
		for _, d := range ds {
			sites[d.Name] = true
		}
		seen := map[string]bool{}
		for _, x := range in.Restrict {
			h := strings.ToLower(strings.TrimSpace(x.Host))
			if !sites[h] {
				return out, fmt.Errorf("restrict: %q is not a website on this server", x.Host)
			}
			if seen[h] {
				return out, fmt.Errorf("restrict: %q is listed twice", h)
			}
			seen[h] = true
			allow, err := nets(x.Allow, "restrict "+h)
			if err != nil {
				return out, err
			}
			if len(allow) == 0 {
				return out, fmt.Errorf("restrict %s: allow at least one network", h)
			}
			out.Restrict = append(out.Restrict, db.EdgeRestriction{Host: h, Allow: allow})
		}
		sort.Slice(out.Restrict, func(i, j int) bool { return out.Restrict[i].Host < out.Restrict[j].Host })
	}
	return out, nil
}
