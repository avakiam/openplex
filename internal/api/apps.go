package api

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"openplex.dev/openplex/internal/apps"
	"openplex.dev/openplex/internal/db"
	"openplex.dev/openplex/internal/docker"
	"openplex.dev/openplex/internal/secrets"
	"openplex.dev/openplex/internal/ssl"
)

// appInstallTimeout bounds one install, including a first build of the PHP
// runtime image and the creation of the database.
const appInstallTimeout = 30 * time.Minute

type siteAppView struct {
	*db.SiteApp
	Name         string `json:"name"`
	AdminURL     string `json:"admin_url"`
	DatabaseName string `json:"database_name,omitempty"`
	// StagingOfName is the live website of a staging copy.
	StagingOfName string `json:"staging_of_name,omitempty"`
	// StagingCopies are the staging copies of this website.
	StagingCopies []siteRef `json:"staging_copies"`
	// SiteChecks are the security checks of the website's own settings.
	SiteChecks []apps.SecurityCheck `json:"site_checks"`
}

type siteRef struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

func (s *Server) handleListApps(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, apps.Catalog)
}

func siteURL(d *db.Domain) string {
	if d.SSLEnabled {
		return "https://" + d.Name
	}
	return "http://" + d.Name
}

func (s *Server) handleGetSiteApp(w http.ResponseWriter, r *http.Request) {
	d := s.loadDomain(w, r)
	if d == nil {
		return
	}
	a, err := s.db.GetSiteApp(r.Context(), d.ID)
	if errors.Is(err, db.ErrNotFound) {
		writeJSON(w, http.StatusOK, nil)
		return
	} else if err != nil {
		s.fail(w, r, err)
		return
	}
	v := siteAppView{SiteApp: a}
	if app, ok := apps.Get(a.App); ok {
		v.Name, v.AdminURL = app.Name, siteURL(d)+app.AdminPath
	}
	if a.DatabaseID != nil {
		if dbr, err := s.db.GetDatabase(r.Context(), *a.DatabaseID); err == nil {
			v.DatabaseName = dbr.Name
		}
	}
	if a.StagingOf != nil {
		if l, err := s.db.GetDomain(r.Context(), *a.StagingOf); err == nil {
			v.StagingOfName = l.Name
		}
	}
	v.SiteChecks = s.siteChecks(r.Context(), d)
	v.StagingCopies = []siteRef{}
	if ids, err := s.db.StagingCopies(r.Context(), d.ID); err == nil {
		for _, id := range ids {
			if c, err := s.db.GetDomain(r.Context(), id); err == nil {
				v.StagingCopies = append(v.StagingCopies, siteRef{ID: c.ID, Name: c.Name})
			}
		}
	}
	writeJSON(w, http.StatusOK, v)
}

// handleDeleteSiteApp forgets the application of a website; its files and
// database stay where they are.
func (s *Server) handleDeleteSiteApp(w http.ResponseWriter, r *http.Request) {
	d := s.loadDomain(w, r)
	if d == nil {
		return
	}
	a, err := s.db.GetSiteApp(r.Context(), d.ID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if a.Status == db.AppInstalling {
		writeErr(w, http.StatusConflict, "conflict", "the application is still being installed")
		return
	}
	if err := s.db.DeleteSiteApp(r.Context(), d.ID); err != nil {
		s.fail(w, r, err)
		return
	}
	s.audit(r, "app.removed", d.Name, a.App)
	w.WriteHeader(http.StatusNoContent)
}

// docrootFree reports whether d's document root is empty apart from the
// placeholder page OpenPlex creates with a website.
func (s *Server) docrootFree(d *db.Domain) (bool, error) {
	root, err := os.OpenRoot(filepath.Join(s.cfg.SitesDir(), d.Name))
	if err != nil {
		return false, err
	}
	defer root.Close()
	dir, err := root.Open(filepath.FromSlash(d.DocumentRoot))
	if errors.Is(err, os.ErrNotExist) {
		return true, nil
	} else if err != nil {
		return false, err
	}
	names, err := dir.Readdirnames(3)
	dir.Close()
	if err != nil && !errors.Is(err, io.EOF) {
		return false, err
	}
	return apps.EmptyDocroot(names, func() ([]byte, error) {
		return root.ReadFile(filepath.Join(filepath.FromSlash(d.DocumentRoot), "index.html"))
	}), nil
}

var slugRe = regexp.MustCompile(`[^a-z0-9]+`)

// appDatabaseName picks "<owner>_<app>_<site>" within the identifier limits.
func appDatabaseName(owner string, app apps.App, site string, attempt int) string {
	suffix := app.DBPrefix + "_" + strings.Trim(slugRe.ReplaceAllString(site, "_"), "_")
	if len(suffix) > 27 {
		suffix = strings.TrimRight(suffix[:27], "_")
	}
	if attempt > 0 {
		suffix = fmt.Sprintf("%s_%d", suffix, attempt+1)
	}
	return dbPrefix(owner) + "_" + suffix
}

func (s *Server) handleInstallSiteApp(w http.ResponseWriter, r *http.Request) {
	d := s.loadDomain(w, r)
	if d == nil {
		return
	}
	var in struct {
		App           string `json:"app"`
		Title         string `json:"title"`
		AdminUser     string `json:"admin_user"`
		AdminEmail    string `json:"admin_email"`
		AdminPassword string `json:"admin_password"`
		Language      string `json:"language"`
	}
	if !decode(w, r, &in) || !s.requireOrch(w) {
		return
	}
	ctx := r.Context()
	app, ok := apps.Get(in.App)
	if !ok {
		badRequest(w, "unknown application")
		return
	}
	p := apps.Params{Title: in.Title, AdminUser: in.AdminUser, AdminEmail: in.AdminEmail, AdminPassword: in.AdminPassword, Language: in.Language}
	if err := app.Validate(&p); err != nil {
		badRequest(w, err.Error())
		return
	}
	switch {
	case d.Kind == db.KindAlias:
		badRequest(w, "aliases have no files of their own; install on the website")
		return
	case d.Runtime != "php":
		writeErr(w, http.StatusConflict, "needs_php", "applications need a PHP website; change the runtime to PHP first")
		return
	case !app.SupportsPHP(d.RuntimeVersion):
		writeErr(w, http.StatusConflict, "needs_php", fmt.Sprintf("%s needs PHP %s or later; change the website's PHP version first", app.Name, app.MinPHP))
		return
	}
	if cur, err := s.db.GetSiteApp(ctx, d.ID); err == nil && cur.Status != db.AppError {
		writeErr(w, http.StatusConflict, "conflict", "this website already has an application")
		return
	} else if err != nil && !errors.Is(err, db.ErrNotFound) {
		s.fail(w, r, err)
		return
	}
	if free, err := s.docrootFree(d); err != nil {
		s.fail(w, r, err)
		return
	} else if !free {
		writeErr(w, http.StatusConflict, "not_empty", "the website's document root is not empty; delete its files first")
		return
	}
	owner, err := s.db.GetUser(ctx, d.OwnerID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if msg, err := s.quotaReached(ctx, owner, true); err != nil {
		s.fail(w, r, err)
		return
	} else if msg != "" {
		writeErr(w, http.StatusForbidden, "quota_exceeded", msg)
		return
	}

	a := &db.SiteApp{DomainID: d.ID, App: app.ID, AdminUser: p.AdminUser, AdminEmail: p.AdminEmail, Language: p.Language}
	if id := actor(r).ID; id != 0 {
		a.CreatedBy = &id
	}
	if err := s.db.StartSiteApp(ctx, a); err != nil {
		if errors.Is(err, db.ErrConflict) {
			writeErr(w, http.StatusConflict, "conflict", "this website already has an application")
			return
		}
		s.fail(w, r, err)
		return
	}
	password := secrets.Password(24)
	enc, err := s.box.Seal(password)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	dbr := &db.Database{OwnerID: owner.ID, Engine: db.EngineMariaDB, PasswordEnc: enc}
	for attempt := 0; ; attempt++ {
		dbr.Name = appDatabaseName(owner.Username, app, d.Name, attempt)
		dbr.Username = dbr.Name
		err = s.db.CreateDatabase(ctx, dbr)
		if err == nil || !errors.Is(err, db.ErrConflict) || attempt == 8 {
			break
		}
	}
	if err == nil && !docker.ValidIdentifier(dbr.Name) {
		err = fmt.Errorf("database name %s is not valid", dbr.Name)
	}
	if err != nil {
		_ = s.db.FinishSiteApp(ctx, d.ID, db.AppError, "", "could not create its database")
		s.fail(w, r, err)
		return
	}
	_ = s.db.SetSiteAppDatabase(ctx, d.ID, &dbr.ID)
	a.DatabaseID = &dbr.ID
	s.audit(r, "app.install_started", d.Name, app.ID)
	s.audit(r, "database.created", dbr.Name, dbr.Engine)

	site, dbCopy := *d, *dbr
	s.bg.Add(1)
	go func() {
		defer s.bg.Done()
		ctx, cancel := context.WithTimeout(s.baseCtx, appInstallTimeout)
		defer cancel()
		s.installApp(ctx, &site, app, p, &dbCopy, password, a.CreatedBy)
	}()
	writeJSON(w, http.StatusAccepted, siteAppView{SiteApp: a, Name: app.Name, AdminURL: siteURL(d) + app.AdminPath, DatabaseName: dbr.Name})
}

// installApp provisions the database, waits for the website's container and
// runs the install script in it. A failed install leaves nothing behind: the
// files it wrote and its database are removed.
func (s *Server) installApp(ctx context.Context, d *db.Domain, app apps.App, p apps.Params, dbr *db.Database, dbPassword string, by *int64) {
	out := &tailBuffer{max: 64 << 10}
	auditApp := func(action, detail string) {
		if err := s.db.Audit(context.WithoutCancel(ctx), db.AuditEntry{UserID: by, Action: action, Target: d.Name, Detail: detail}); err != nil {
			s.log.Warn("audit write failed", "action", action, "err", err)
		}
	}
	err := func() error {
		if err := s.orch.ProvisionDatabase(ctx, dbr, dbPassword); err != nil {
			_ = s.db.SetDatabaseStatus(ctx, dbr.ID, db.StatusError, err.Error())
			return fmt.Errorf("creating the database: %w", err)
		}
		if err := s.db.SetDatabaseStatus(ctx, dbr.ID, db.StatusRunning, ""); err != nil {
			return err
		}
		if err := s.orch.EnsureAdminer(ctx); err != nil {
			s.log.Warn("adminer", "err", err)
		}
		script, err := app.Script()
		if err != nil {
			return err
		}
		docroot := path.Join(ssl.EdgeSitesRoot, d.Name, d.DocumentRoot)
		env := apps.Env(p, docroot, siteURL(d), apps.Database{Host: docker.DatabaseHost(dbr), Name: dbr.Name, User: dbr.Username, Password: dbPassword})
		for {
			cur, err := s.db.GetDomain(ctx, d.ID)
			if err != nil {
				return err
			}
			switch cur.Status {
			case db.StatusError:
				return fmt.Errorf("the website could not start: %s", cur.StatusMessage)
			case db.StatusStopped, db.StatusDisabled:
				return errors.New("the website is stopped")
			case db.StatusRunning:
				code, err := s.orch.RunSiteScript(ctx, cur, script, env, out)
				if errors.Is(err, docker.ErrSiteNotRunning) {
					break // container still starting
				} else if err != nil {
					return err
				} else if code != 0 {
					return fmt.Errorf("the installer stopped (exit %d): %s", code, lastLines(out.String(), 6))
				}
				return nil
			}
			select {
			case <-ctx.Done():
				return errors.New("timed out waiting for the website to start")
			case <-time.After(3 * time.Second):
			}
		}
	}()
	bg := context.WithoutCancel(ctx)
	if err == nil {
		v := apps.Version(out.String())
		_ = s.db.FinishSiteApp(bg, d.ID, db.AppInstalled, v, "")
		s.log.Info("application installed", "domain", d.Name, "app", app.ID, "version", v)
		auditApp("app.installed", strings.TrimSpace(app.ID+" "+v))
		return
	}
	msg := redact(err.Error(), p.AdminPassword, dbPassword)
	s.log.Warn("application install failed", "domain", d.Name, "app", app.ID, "err", msg)
	// Undo: the document root was empty before, and the database is the app's own.
	cleanup, ccancel := context.WithTimeout(bg, 2*time.Minute)
	defer ccancel()
	if cur, gerr := s.db.GetDomain(cleanup, d.ID); gerr == nil && cur.Status == db.StatusRunning {
		docroot := path.Join(ssl.EdgeSitesRoot, d.Name, d.DocumentRoot)
		if _, cerr := s.orch.RunSiteScript(cleanup, cur, `find "$OPX_DOCROOT" -mindepth 1 -delete`, []string{"OPX_DOCROOT=" + docroot}, &tailBuffer{max: 4096}); cerr != nil {
			s.log.Warn("clean up after failed install", "domain", d.Name, "err", cerr)
		}
	}
	if derr := s.orch.DropDatabase(cleanup, dbr); derr != nil {
		s.log.Warn("drop database after failed install", "database", dbr.Name, "err", derr)
	} else {
		_ = s.db.DeleteDatabase(cleanup, dbr.ID)
	}
	_ = s.db.FinishSiteApp(bg, d.ID, db.AppError, "", msg)
	auditApp("app.install_failed", app.ID+": "+msg)
}

// tailBuffer keeps the last max bytes written to it.
type tailBuffer struct {
	max int
	b   []byte
}

func (t *tailBuffer) Write(p []byte) (int, error) {
	t.b = append(t.b, p...)
	if len(t.b) > t.max {
		t.b = append(t.b[:0], t.b[len(t.b)-t.max:]...)
	}
	return len(p), nil
}

func (t *tailBuffer) String() string { return string(t.b) }

func lastLines(s string, n int) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	var keep []string
	for _, l := range lines {
		if l = strings.TrimSpace(l); l != "" && !strings.HasPrefix(l, "==> ") && !strings.HasPrefix(l, "OPX_") {
			keep = append(keep, l)
		}
	}
	if len(keep) > n {
		keep = keep[len(keep)-n:]
	}
	out := strings.Join(keep, " / ")
	if len(out) > 600 {
		out = out[len(out)-600:]
	}
	return out
}

func redact(s string, hide ...string) string {
	for _, x := range hide {
		if x != "" {
			s = strings.ReplaceAll(s, x, "***")
		}
	}
	return s
}
