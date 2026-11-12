package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"path"
	"time"

	"openplex.dev/openplex/internal/apps"
	"openplex.dev/openplex/internal/db"
	"openplex.dev/openplex/internal/ssl"
)

const (
	// appCheckTimeout bounds one update check (it downloads WP-CLI or Composer).
	appCheckTimeout = 10 * time.Minute
	// appUpdateTimeout bounds one update, including the backup taken first.
	appUpdateTimeout = 90 * time.Minute
	// appCheckEvery is how often installed applications are checked for updates.
	appCheckEvery = 24 * time.Hour
)

// startAppTask marks task as running on the application of d, answering the
// request itself when that is not possible.
func (s *Server) startAppTask(w http.ResponseWriter, r *http.Request, d *db.Domain, task string) (*db.SiteApp, apps.App, bool) {
	a, err := s.db.GetSiteApp(r.Context(), d.ID)
	if err != nil {
		s.fail(w, r, err)
		return nil, apps.App{}, false
	}
	app, ok := apps.Get(a.App)
	if !ok {
		writeErr(w, http.StatusConflict, "conflict", "this application is not known to this version of OpenPlex")
		return nil, apps.App{}, false
	}
	if d.Status != db.StatusRunning {
		writeErr(w, http.StatusConflict, "not_running", "the website is not running; start it first")
		return nil, apps.App{}, false
	}
	switch err := s.db.StartSiteAppTask(r.Context(), d.ID, task); {
	case errors.Is(err, db.ErrConflict):
		msg := "the application is busy: wait for the current check or update to finish"
		if a.Status != db.AppInstalled {
			msg = "the application is not installed"
		}
		writeErr(w, http.StatusConflict, "conflict", msg)
		return nil, apps.App{}, false
	case err != nil:
		s.fail(w, r, err)
		return nil, apps.App{}, false
	}
	a.Task, a.TaskError = task, ""
	return a, app, true
}

func (s *Server) handleCheckSiteApp(w http.ResponseWriter, r *http.Request) {
	d := s.loadDomain(w, r)
	if d == nil || !s.requireOrch(w) {
		return
	}
	a, app, ok := s.startAppTask(w, r, d, db.AppTaskChecking)
	if !ok {
		return
	}
	site := *d
	s.bg.Add(1)
	go func() {
		defer s.bg.Done()
		ctx, cancel := context.WithTimeout(s.baseCtx, appCheckTimeout)
		defer cancel()
		s.checkApp(ctx, &site, app)
	}()
	writeJSON(w, http.StatusAccepted, a)
}

// checkApp runs the update check of app on website d and records its result.
func (s *Server) checkApp(ctx context.Context, d *db.Domain, app apps.App) *apps.Updates {
	u, err := func() (*apps.Updates, error) {
		script, err := app.CheckScript()
		if err != nil {
			return nil, err
		}
		out := &tailBuffer{max: 256 << 10}
		code, err := s.orch.RunSiteScript(ctx, d, script, apps.CheckEnv(appDocroot(d), d.Name, d.SSLEnabled), out)
		if err != nil {
			return nil, err
		}
		if code != 0 {
			return nil, fmt.Errorf("the update check stopped (exit %d): %s", code, lastLines(out.String(), 6))
		}
		return app.ParseCheck(out.String())
	}()
	bg := context.WithoutCancel(ctx)
	if err != nil {
		s.log.Warn("application update check failed", "domain", d.Name, "app", app.ID, "err", err)
		_ = s.db.FinishSiteAppCheck(bg, d.ID, "", nil, err.Error())
		return nil
	}
	body, _ := json.Marshal(u)
	if err := s.db.FinishSiteAppCheck(bg, d.ID, u.Version, body, ""); err != nil {
		s.log.Warn("record application update check", "domain", d.Name, "err", err)
	}
	return u
}

func appDocroot(d *db.Domain) string { return path.Join(ssl.EdgeSitesRoot, d.Name, d.DocumentRoot) }

func (s *Server) handleUpdateSiteApp(w http.ResponseWriter, r *http.Request) {
	d := s.loadDomain(w, r)
	if d == nil {
		return
	}
	var in struct {
		Core       bool `json:"core"`
		Extensions bool `json:"extensions"`
	}
	if !decode(w, r, &in) || !s.requireOrch(w) || !s.requireBackups(w) {
		return
	}
	if !in.Core && !in.Extensions {
		badRequest(w, "choose what to update")
		return
	}
	if cur, err := s.db.GetSiteApp(r.Context(), d.ID); err == nil {
		if app, ok := apps.Get(cur.App); ok && app.ManualExtensions && !in.Core {
			badRequest(w, fmt.Sprintf("%s extensions are updated from its own administration", app.Name))
			return
		}
	}
	a, app, ok := s.startAppTask(w, r, d, db.AppTaskUpdating)
	if !ok {
		return
	}
	s.audit(r, "app.update_started", d.Name, fmt.Sprintf("%s core=%t extensions=%t", app.ID, in.Core, in.Extensions))
	site, by := *d, &actor(r).ID
	s.bg.Add(1)
	go func() {
		defer s.bg.Done()
		ctx, cancel := context.WithTimeout(s.baseCtx, appUpdateTimeout)
		defer cancel()
		s.updateApp(ctx, &site, app, in.Core, in.Extensions, by)
	}()
	writeJSON(w, http.StatusAccepted, a)
}

// updateApp backs up the website and its database, runs the update script and
// checks again for updates. Nothing is updated when the backup fails; when
// the update fails, the backup is there to restore.
func (s *Server) updateApp(ctx context.Context, d *db.Domain, app apps.App, core, extensions bool, by *int64) {
	bg := context.WithoutCancel(ctx)
	auditApp := func(action, detail string) {
		if err := s.db.Audit(bg, db.AuditEntry{UserID: by, Action: action, Target: d.Name, Detail: detail}); err != nil {
			s.log.Warn("audit write failed", "action", action, "err", err)
		}
	}
	version, err := func() (string, error) {
		if _, err := s.backups.BackupNow(ctx, fmt.Sprintf("app:%d", d.ID), "app_update", by); err != nil {
			return "", fmt.Errorf("nothing was updated: the backup before the update failed: %w", err)
		}
		script, err := app.UpdateScript()
		if err != nil {
			return "", err
		}
		cur, err := s.db.GetDomain(ctx, d.ID)
		if err != nil {
			return "", err
		}
		out := &tailBuffer{max: 256 << 10}
		code, err := s.orch.RunSiteScript(ctx, cur, script, apps.UpdateEnv(appDocroot(cur), cur.Name, core, extensions), out)
		v := apps.Version(out.String())
		if err != nil {
			return v, err
		}
		if code != 0 {
			return v, fmt.Errorf("the update stopped (exit %d): %s. A backup was taken just before it: restore it from the website's Backups if the site does not work", code, lastLines(out.String(), 6))
		}
		return v, nil
	}()
	if err != nil {
		s.log.Warn("application update failed", "domain", d.Name, "app", app.ID, "err", err)
		_ = s.db.FinishSiteAppUpdate(bg, d.ID, version, err.Error())
		auditApp("app.update_failed", app.ID+": "+err.Error())
		return
	}
	_ = s.db.FinishSiteAppUpdate(bg, d.ID, version, "")
	s.log.Info("application updated", "domain", d.Name, "app", app.ID, "version", version)
	auditApp("app.updated", app.ID+" "+version)
	// What is left to update.
	if err := s.db.StartSiteAppTask(bg, d.ID, db.AppTaskChecking); err == nil {
		cctx, cancel := context.WithTimeout(bg, appCheckTimeout)
		defer cancel()
		s.checkApp(cctx, d, app)
	}
}

// RunAppUpdateChecks checks the installed applications for updates once a
// day, one website at a time, until ctx ends.
func (s *Server) RunAppUpdateChecks(ctx context.Context) {
	if s.orch == nil {
		return
	}
	t := time.NewTimer(5 * time.Minute)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		ids, err := s.db.SiteAppsToCheck(ctx, time.Now().Add(-appCheckEvery))
		if err != nil {
			s.log.Warn("list applications to check", "err", err)
		}
		for _, id := range ids {
			if ctx.Err() != nil {
				return
			}
			d, err := s.db.GetDomain(ctx, id)
			if err != nil || d.Status != db.StatusRunning {
				continue
			}
			a, err := s.db.GetSiteApp(ctx, id)
			if err != nil {
				continue
			}
			app, ok := apps.Get(a.App)
			if !ok || s.db.StartSiteAppTask(ctx, id, db.AppTaskChecking) != nil {
				continue
			}
			cctx, cancel := context.WithTimeout(ctx, appCheckTimeout)
			s.checkApp(cctx, d, app)
			cancel()
		}
		t.Reset(time.Hour)
	}
}
