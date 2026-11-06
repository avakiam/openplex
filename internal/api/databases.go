package api

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httputil"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"

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
	Host  string `json:"host"`
	Port  int    `json:"port"`
	Label string `json:"engine_label"`
}

func viewOf(d *db.Database) databaseView {
	e := docker.Engines[d.Engine]
	return databaseView{Database: d, Host: docker.DatabaseHost(d), Port: e.Port, Label: e.Label}
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
	if ok, err := s.canAccessOwner(r.Context(), actor(r), d.OwnerID); err != nil || !ok {
		writeErr(w, http.StatusNotFound, "not_found", "resource not found")
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
	ids, err := s.visibleOwnerIDs(r.Context(), actor(r))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	list, err := s.db.ListDatabases(r.Context(), ids)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	out := make([]databaseView, 0, len(list))
	for _, d := range list {
		out = append(out, viewOf(d))
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleDatabaseSizes(w http.ResponseWriter, r *http.Request) {
	ids, err := s.visibleOwnerIDs(r.Context(), actor(r))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	list, err := s.db.ListDatabases(r.Context(), ids)
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
		writeJSON(w, http.StatusOK, viewOf(d))
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
	if owner.MaxDatabases > 0 {
		n, err := s.db.CountDatabasesByOwner(ctx, owner.ID)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		if n >= owner.MaxDatabases {
			writeErr(w, http.StatusForbidden, "quota_exceeded", fmt.Sprintf("database quota of %d reached", owner.MaxDatabases))
			return
		}
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
	writeJSON(w, http.StatusCreated, viewOf(d))
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
	v := viewOf(d)
	var uri string
	switch d.Engine {
	case db.EngineMariaDB:
		uri = fmt.Sprintf("mysql://%s:%s@%s:%d/%s", d.Username, pw, v.Host, v.Port, d.Name)
	case db.EnginePostgres:
		uri = fmt.Sprintf("postgresql://%s:%s@%s:%d/%s", d.Username, pw, v.Host, v.Port, d.Name)
	case db.EngineRedis:
		uri = fmt.Sprintf("redis://:%s@%s:%d/0", pw, v.Host, v.Port)
	}
	s.audit(r, "database.credentials_viewed", d.Name, "")
	writeJSON(w, http.StatusOK, map[string]any{
		"host": v.Host, "port": v.Port, "database": d.Name, "username": d.Username, "password": pw, "url": uri,
		"adminer": map[string]string{"driver": docker.Engines[d.Engine].Driver, "server": v.Host},
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
		return s.db.DeleteDatabase(ctx, cp.ID)
	})
	w.WriteHeader(http.StatusAccepted)
}

// adminerProxy serves the Adminer GUI under /tools/adminer/. Adminer itself
// requires database credentials; the panel's one-click login posts them.
func (s *Server) adminerProxy() http.Handler {
	target, _ := url.Parse(fmt.Sprintf("http://127.0.0.1:%d", docker.AdminerPort))
	rp := httputil.NewSingleHostReverseProxy(target)
	base := rp.Director
	rp.Director = func(r *http.Request) {
		base(r)
		r.URL.Path = "/" + strings.TrimPrefix(strings.TrimPrefix(r.URL.Path, "/tools/adminer"), "/")
		r.Header.Del("Authorization")
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
		// Adminer may only talk to OpenPlex database servers with the MySQL or
		// PostgreSQL drivers — never arbitrary hosts (SSRF) or file-based drivers.
		q := r.URL.Query()
		for k := range q {
			if adminerDriverParams[k] {
				http.Error(w, "driver not allowed", http.StatusForbidden)
				return
			}
		}
		for _, k := range []string{"server", "pgsql"} {
			if v := q.Get(k); v != "" && !allowedAdminerServer(v) {
				http.Error(w, "server not allowed", http.StatusForbidden)
				return
			}
		}
		if r.Method == http.MethodPost && strings.HasPrefix(r.Header.Get("Content-Type"), "application/x-www-form-urlencoded") {
			body, err := io.ReadAll(io.LimitReader(r.Body, 32<<20))
			if err != nil {
				http.Error(w, "bad request", http.StatusBadRequest)
				return
			}
			r.Body = io.NopCloser(bytes.NewReader(body)) // restore for the proxy
			r.ContentLength = int64(len(body))
			if form, err := url.ParseQuery(string(body)); err == nil {
				if drv := form.Get("auth[driver]"); drv != "" && drv != "server" && drv != "pgsql" {
					http.Error(w, "driver not allowed", http.StatusForbidden)
					return
				}
				if srv, ok := form["auth[server]"]; ok && !allowedAdminerServer(srv[0]) {
					http.Error(w, "server not allowed", http.StatusForbidden)
					return
				}
			}
		}
		w.Header().Set("Cache-Control", "no-store")
		rp.ServeHTTP(w, r)
	})
}

var adminerDriverParams = map[string]bool{"sqlite": true, "sqlite2": true, "oracle": true, "mssql": true, "mongo": true,
	"elastic": true, "clickhouse": true, "firebird": true, "simpledb": true}

func allowedAdminerServer(s string) bool {
	return slices.Contains([]string{"mariadb", "postgres"}, strings.TrimSpace(strings.ToLower(s)))
}
