package api

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httputil"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"openplex.dev/openplex/internal/db"
	"openplex.dev/openplex/internal/docker"
	"openplex.dev/openplex/internal/secrets"
)

var dbSuffixRe = regexp.MustCompile(`^[a-z0-9][a-z0-9_]{0,29}$`)

// dbPrefix namespaces database and user names per account ("alice_shop").
func dbPrefix(username string) string {
	p := strings.NewReplacer(".", "_", "-", "_").Replace(strings.ToLower(username))
	if len(p) > 16 {
		p = p[:16]
	}
	return p
}

type databaseView struct {
	*db.Database
	Host   string `json:"host"`
	Port   int    `json:"port"`
	Label  string `json:"engine_label"`
	Shared bool   `json:"shared"` // owned by someone else, opened by a grant
	// RemotePort is the public port remote clients use (0: not reachable remotely).
	RemotePort  int      `json:"remote_port"`
	RemoteAllow []string `json:"remote_allow"`
}

func (s *Server) viewOf(d *db.Database) databaseView {
	e := docker.Engines[d.Engine]
	return databaseView{Database: d, Host: docker.DatabaseHost(d), Port: e.Port, Label: e.Label,
		RemotePort: s.remotePort(d), RemoteAllow: splitAllow(d.RemoteAllow)}
}

func (s *Server) loadDatabase(w http.ResponseWriter, r *http.Request) *db.Database {
	id, ok := idParam(w, r, "id")
	if !ok {
		return nil
	}
	d, err := s.db.GetDatabase(r.Context(), id)
	if err != nil {
		s.fail(w, r, err)
		return nil
	}
	ok, err = s.canAccessOwner(r.Context(), actor(r), d.OwnerID)
	if err != nil {
		s.fail(w, r, err)
		return nil
	}
	if ok {
		return d
	}
	// A grant opens the database (credentials, backups) but not its deletion.
	if _, err := s.db.FindGrant(r.Context(), actor(r).ID, db.GrantDatabase, d.ID); err != nil {
		if !errors.Is(err, db.ErrNotFound) {
			s.fail(w, r, err)
			return nil
		}
		writeErr(w, http.StatusNotFound, "not_found", "resource not found")
		return nil
	}
	if r.Method == http.MethodDelete {
		writeErr(w, http.StatusForbidden, "forbidden", "only the owner of this database can delete it")
		return nil
	}
	return d
}

func (s *Server) requireOrch(w http.ResponseWriter) bool {
	if s.orch == nil {
		writeErr(w, http.StatusServiceUnavailable, "docker_unavailable", "Docker is not available on this server")
		return false
	}
	return true
}

func (s *Server) handleListEngines(w http.ResponseWriter, r *http.Request) {
	out := make([]docker.Engine, 0, len(docker.Engines))
	for _, k := range []string{db.EngineMariaDB, db.EnginePostgres, db.EngineRedis} {
		out = append(out, docker.Engines[k])
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleListDatabases(w http.ResponseWriter, r *http.Request) {
	list, shared, err := s.visibleDatabases(r.Context(), actor(r))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	out := make([]databaseView, 0, len(list))
	for _, d := range list {
		v := s.viewOf(d)
		v.Shared = shared[d.ID]
		out = append(out, v)
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleDatabaseSizes(w http.ResponseWriter, r *http.Request) {
	list, _, err := s.visibleDatabases(r.Context(), actor(r))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	sizes := map[string]int64{}
	if s.orch != nil {
		for id, n := range s.orch.DatabaseSizes(r.Context(), list) {
			sizes[strconv.FormatInt(id, 10)] = n
		}
	}
	writeJSON(w, http.StatusOK, sizes)
}

func (s *Server) handleGetDatabase(w http.ResponseWriter, r *http.Request) {
	if d := s.loadDatabase(w, r); d != nil {
		writeJSON(w, http.StatusOK, s.viewOf(d))
	}
}

func (s *Server) handleCreateDatabase(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Engine  string `json:"engine"`
		Name    string `json:"name"`
		OwnerID *int64 `json:"owner_id"`
	}
	if !decode(w, r, &in) {
		return
	}
	if !s.requireOrch(w) {
		return
	}
	ctx := r.Context()
	a := actor(r)
	if _, ok := docker.Engines[in.Engine]; !ok {
		badRequest(w, "engine must be mariadb, postgres or redis")
		return
	}
	ownerID := a.ID
	if in.OwnerID != nil {
		ownerID = *in.OwnerID
	}
	if ok, err := s.canAccessOwner(ctx, a, ownerID); err != nil || !ok {
		forbidden(w)
		return
	}
	owner, err := s.db.GetUser(ctx, ownerID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	suffix := strings.ToLower(strings.TrimSpace(in.Name))
	if !dbSuffixRe.MatchString(suffix) {
		badRequest(w, "name must be 1-30 characters: lowercase letters, digits and underscores")
		return
	}
	full := dbPrefix(owner.Username) + "_" + suffix
	if !docker.ValidIdentifier(full) {
		badRequest(w, "resulting database name "+full+" is not valid")
		return
	}
	if msg, err := s.quotaReached(ctx, owner, true); err != nil {
		s.fail(w, r, err)
		return
	} else if msg != "" {
		writeErr(w, http.StatusForbidden, "quota_exceeded", msg)
		return
	}
	password := secrets.Password(24)
	enc, err := s.box.Seal(password)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	d := &db.Database{OwnerID: owner.ID, Engine: in.Engine, Name: full, Username: full, PasswordEnc: enc}
	if in.Engine == db.EngineRedis {
		d.Username = "default"
	}
	if err := s.db.CreateDatabase(ctx, d); err != nil {
		s.fail(w, r, err)
		return
	}
	s.audit(r, "database.created", d.Name, d.Engine)
	cp := *d
	s.background(func(ctx context.Context) error {
		err := s.orch.ProvisionDatabase(ctx, &cp, password)
		if err == nil && cp.Engine != db.EngineRedis {
			err = s.orch.EnsureAdminer(ctx)
		}
		if err != nil {
			_ = s.db.SetDatabaseStatus(ctx, cp.ID, db.StatusError, err.Error())
			return fmt.Errorf("provision database %s: %w", cp.Name, err)
		}
		return s.db.SetDatabaseStatus(ctx, cp.ID, db.StatusRunning, "")
	})
	writeJSON(w, http.StatusCreated, s.viewOf(d))
}

func (s *Server) handleDatabaseCredentials(w http.ResponseWriter, r *http.Request) {
	d := s.loadDatabase(w, r)
	if d == nil {
		return
	}
	pw, err := s.box.Open(d.PasswordEnc)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	v := s.viewOf(d)
	var uri string
	switch d.Engine {
	case db.EngineMariaDB:
		uri = fmt.Sprintf("mysql://%s:%s@%s:%d/%s", d.Username, pw, v.Host, v.Port, d.Name)
	case db.EnginePostgres:
		uri = fmt.Sprintf("postgresql://%s:%s@%s:%d/%s", d.Username, pw, v.Host, v.Port, d.Name)
	case db.EngineRedis:
		uri = fmt.Sprintf("redis://:%s@%s:%d/0", pw, v.Host, v.Port)
	}
	ticket, err := s.tickets.issueFor(actor(r).ID, 2*time.Minute) // opens Adminer (see adminerSession)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.audit(r, "database.credentials_viewed", d.Name, "")
	writeJSON(w, http.StatusOK, map[string]any{
		"host": v.Host, "port": v.Port, "database": d.Name, "username": d.Username, "password": pw, "url": uri,
		"adminer": map[string]string{"driver": docker.Engines[d.Engine].Driver, "server": v.Host, "ticket": ticket},
	})
}

func (s *Server) handleRotateDatabasePassword(w http.ResponseWriter, r *http.Request) {
	d := s.loadDatabase(w, r)
	if d == nil || !s.requireOrch(w) {
		return
	}
	if d.Status != db.StatusRunning {
		writeErr(w, http.StatusConflict, "not_ready", "the database is not ready yet")
		return
	}
	pw := secrets.Password(24)
	if err := s.orch.RotateDatabasePassword(r.Context(), d, pw); err != nil {
		s.fail(w, r, err)
		return
	}
	enc, err := s.box.Seal(pw)
	if err == nil {
		err = s.db.SetDatabasePassword(r.Context(), d.ID, enc)
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.audit(r, "database.password_rotated", d.Name, "")
	writeJSON(w, http.StatusOK, map[string]string{"password": pw})
}

func (s *Server) handleDeleteDatabase(w http.ResponseWriter, r *http.Request) {
	d := s.loadDatabase(w, r)
	if d == nil || !s.requireOrch(w) {
		return
	}
	_ = s.db.SetDatabaseStatus(r.Context(), d.ID, db.StatusPending, "deleting")
	s.audit(r, "database.deleted", d.Name, d.Engine)
	cp := *d
	s.background(func(ctx context.Context) error {
		if err := s.orch.DropDatabase(ctx, &cp); err != nil {
			_ = s.db.SetDatabaseStatus(ctx, cp.ID, db.StatusError, "delete failed: "+err.Error())
			return err
		}
		if err := s.db.DeleteDatabase(ctx, cp.ID); err != nil {
			return err
		}
		if cp.RemoteAllow != "" {
			s.syncRemote(ctx)
		}
		return nil
	})
	w.WriteHeader(http.StatusAccepted)
}

// adminerProxy serves the Adminer GUI under /tools/adminer/. Only signed-in
// panel users get there: the dashboard opens it with a one-time ticket that
// becomes an Adminer-only session cookie. Adminer itself still asks for the
// database credentials (the one-click login posts them).
func (s *Server) adminerProxy() http.Handler {
	target, _ := url.Parse(fmt.Sprintf("http://127.0.0.1:%d", docker.AdminerPort))
	rp := httputil.NewSingleHostReverseProxy(target)
	base := rp.Director
	rp.Director = func(r *http.Request) {
		base(r)
		r.URL.Path = "/" + strings.TrimPrefix(strings.TrimPrefix(r.URL.Path, "/tools/adminer"), "/")
		r.Header.Del("Authorization")
		dropCookie(r, adminerCookie)
	}
	rp.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		s.log.Warn("adminer proxy", "err", err)
		writeErr(w, http.StatusBadGateway, "adminer_unavailable", "the database manager is starting or unavailable; create a database first")
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/tools/adminer" {
			http.Redirect(w, r, "/tools/adminer/", http.StatusMovedPermanently)
			return
		}
		if !s.adminerSession(w, r) {
			http.Error(w, "Open the database manager from the OpenPlex panel.", http.StatusForbidden)
			return
		}
		if err := checkAdminerRequest(r); err != nil {
			http.Error(w, err.Error(), http.StatusForbidden)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		rp.ServeHTTP(w, r)
	})
}

const (
	adminerCookie     = "opx_adminer"
	adminerSessionTTL = 8 * time.Hour
)

// adminerSession admits r when it carries a valid Adminer session cookie, or a
// one-time ticket (?_opx=) that it turns into one.
func (s *Server) adminerSession(w http.ResponseWriter, r *http.Request) bool {
	q := r.URL.Query()
	if t := q.Get("_opx"); t != "" {
		uid, ok := s.tickets.redeem(t)
		if !ok || !s.activeUser(r, uid) {
			return false
		}
		id, err := s.adminerSessions.issueFor(uid, adminerSessionTTL)
		if err != nil {
			return false
		}
		http.SetCookie(w, &http.Cookie{Name: adminerCookie, Value: id, Path: "/tools/adminer/", HttpOnly: true,
			Secure: !s.cfg.Dev, SameSite: http.SameSiteStrictMode, MaxAge: int(adminerSessionTTL / time.Second)})
		q.Del("_opx")
		r.URL.RawQuery = q.Encode()
		return true
	}
	c, err := r.Cookie(adminerCookie)
	if err != nil {
		return false
	}
	uid, ok := s.adminerSessions.peek(c.Value)
	return ok && s.activeUser(r, uid)
}

func (s *Server) activeUser(r *http.Request, id int64) bool {
	u, err := s.db.GetUser(r.Context(), id)
	return err == nil && !u.Disabled
}

// dropCookie removes one cookie from the request's Cookie header.
func dropCookie(r *http.Request, name string) {
	cs := r.Cookies()
	r.Header.Del("Cookie")
	for _, c := range cs {
		if c.Name != name {
			r.AddCookie(c)
		}
	}
}

// checkAdminerRequest lets Adminer talk only to the OpenPlex database servers
// with the MySQL or PostgreSQL drivers: never to arbitrary hosts (SSRF) or with
// file-based drivers. PHP reads parameters differently from Go (last value
// wins, "a[]" is "a", " a" is "a"), so anything ambiguous is refused.
func checkAdminerRequest(r *http.Request) error {
	q, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		return errors.New("malformed query")
	}
	if err := checkAdminerParams(q, "server", "pgsql"); err != nil {
		return err
	}
	if r.Method == http.MethodGet || r.Method == http.MethodHead {
		return nil
	}
	mt, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil {
		if r.ContentLength == 0 {
			return nil
		}
		return errors.New("unsupported request body")
	}
	var form url.Values
	switch mt {
	case "application/x-www-form-urlencoded":
		body, err := io.ReadAll(io.LimitReader(r.Body, 32<<20))
		if err != nil {
			return errors.New("bad request")
		}
		r.Body = io.NopCloser(bytes.NewReader(body)) // restore for the proxy
		r.ContentLength = int64(len(body))
		if form, err = url.ParseQuery(string(body)); err != nil {
			return errors.New("malformed form")
		}
	case "multipart/form-data":
		body, err := io.ReadAll(io.LimitReader(r.Body, 128<<20))
		if err != nil {
			return errors.New("bad request")
		}
		r.Body = io.NopCloser(bytes.NewReader(body))
		r.ContentLength = int64(len(body))
		form = url.Values{}
		mr := multipart.NewReader(bytes.NewReader(body), params["boundary"])
		for {
			p, err := mr.NextPart()
			if err == io.EOF {
				break
			}
			if err != nil {
				return errors.New("malformed form")
			}
			if p.FileName() == "" {
				v, _ := io.ReadAll(io.LimitReader(p, 1<<20))
				form.Add(p.FormName(), string(v))
			}
		}
	default:
		return nil // PHP leaves $_POST empty for other bodies
	}
	return checkAdminerParams(form, "auth[driver]", "auth[server]")
}

// checkAdminerParams refuses driver parameters of other database kinds, any
// array form of the checked keys and any value of them that is not exactly one
// OpenPlex server (or driver).
func checkAdminerParams(v url.Values, keys ...string) error {
	count := map[string]int{}
	for k, vals := range v {
		n := phpKey(k)
		base, _, _ := strings.Cut(n, "[")
		if adminerDriverParams[base] {
			return errors.New("driver not allowed")
		}
		for _, key := range keys {
			kb, _, _ := strings.Cut(key, "[")
			if base == kb && n != key && (kb != "auth" || strings.HasPrefix(n, key+"[")) {
				return errors.New("parameter not allowed")
			}
			if n != key {
				continue
			}
			if count[key] += len(vals); count[key] > 1 {
				return errors.New("repeated parameter")
			}
			for _, val := range vals {
				if key == "auth[driver]" {
					if val != "server" && val != "pgsql" {
						return errors.New("driver not allowed")
					}
				} else if val != "" && !allowedAdminerServer(val) {
					return errors.New("server not allowed")
				}
			}
		}
	}
	return nil
}

// phpKey is the name PHP files a parameter under: leading spaces dropped,
// spaces and dots in the base name turned into underscores, and anything after
// the last complete [...] group ignored ("auth[driver]x" is auth[driver]).
func phpKey(k string) string {
	k = strings.TrimLeft(k, " ")
	fix := strings.NewReplacer(" ", "_", ".", "_").Replace
	i := strings.IndexByte(k, '[')
	if i < 0 {
		return fix(k)
	}
	out, rest := fix(k[:i]), k[i:]
	for first := true; strings.HasPrefix(rest, "["); first = false {
		j := strings.IndexByte(rest, ']')
		if j < 0 {
			if first { // an unclosed "[" is kept as "_" in the name
				return out + "_" + fix(rest[1:])
			}
			break
		}
		out += rest[:j+1]
		rest = rest[j+1:]
	}
	return out
}

var adminerDriverParams = map[string]bool{"sqlite": true, "sqlite2": true, "oracle": true, "mssql": true, "mongo": true,
	"elastic": true, "elastic5": true, "clickhouse": true, "firebird": true, "simpledb": true, "igdb": true}

func allowedAdminerServer(s string) bool {
	return slices.Contains([]string{"mariadb", "postgres"}, strings.TrimSpace(strings.ToLower(s)))
}
