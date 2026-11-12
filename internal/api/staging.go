package api

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"regexp"
	"strings"
	"time"

	"openplex.dev/openplex/internal/apps"
	"openplex.dev/openplex/internal/db"
	"openplex.dev/openplex/internal/docker"
	"openplex.dev/openplex/internal/secrets"
)

// stagingTimeout bounds making a staging copy or pushing one to the live site.
const stagingTimeout = 2 * time.Hour

var stagingLabelRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,30}[a-z0-9])?$`)

// appDatabase returns the running MariaDB database of an application.
func (s *Server) appDatabase(ctx context.Context, a *db.SiteApp) (*db.Database, error) {
	if a.DatabaseID == nil {
		return nil, errors.New("the application has no database linked in the panel")
	}
	d, err := s.db.GetDatabase(ctx, *a.DatabaseID)
	if err != nil {
		return nil, err
	}
	if d.Engine != db.EngineMariaDB || d.Status != db.StatusRunning {
		return nil, fmt.Errorf("the database %s is not a running MariaDB database", d.Name)
	}
	return d, nil
}

// handleCreateStaging copies a website with an installed application, files
// and database, to a new subdomain ("staging.<site>" by default).
func (s *Server) handleCreateStaging(w http.ResponseWriter, r *http.Request) {
	live := s.loadDomain(w, r)
	if live == nil {
		return
	}
	var in struct {
		Label string `json:"label"`
	}
	if !decode(w, r, &in) || !s.requireOrch(w) {
		return
	}
	ctx := r.Context()
	if in.Label == "" {
		in.Label = "staging"
	}
	if !stagingLabelRe.MatchString(in.Label) {
		badRequest(w, "the name of the copy must be 1-32 lowercase letters, digits and dashes")
		return
	}
	a, err := s.db.GetSiteApp(ctx, live.ID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	app, ok := apps.Get(a.App)
	switch {
	case !ok:
		writeErr(w, http.StatusConflict, "conflict", "this application is not known to this version of OpenPlex")
		return
	case a.StagingOf != nil:
		writeErr(w, http.StatusConflict, "conflict", "this website is already a staging copy; push it to the live site or copy the live site again")
		return
	case live.Status != db.StatusRunning:
		writeErr(w, http.StatusConflict, "not_running", "the website is not running; start it first")
		return
	}
	liveDB, err := s.appDatabase(ctx, a)
	if err != nil {
		writeErr(w, http.StatusConflict, "conflict", err.Error())
		return
	}
	owner, err := s.db.GetUser(ctx, live.OwnerID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	name := in.Label + "." + live.Name
	parentID := live.ID
	if live.Kind != db.KindPrimary {
		if live.ParentID == nil {
			badRequest(w, "this website has no primary domain")
			return
		}
		parentID = *live.ParentID
	}
	if msg := s.reservedName(ctx, actor(r), name); msg != "" {
		writeErr(w, http.StatusConflict, "conflict", msg)
		return
	}
	if err := s.checkNameOwnership(ctx, name, owner.ID); err != nil {
		writeErr(w, http.StatusConflict, "conflict", err.Error())
		return
	}
	for _, dbs := range []bool{false, true} {
		if msg, err := s.quotaReached(ctx, owner, dbs); err != nil {
			s.fail(w, r, err)
			return
		} else if msg != "" {
			writeErr(w, http.StatusForbidden, "quota_exceeded", msg)
			return
		}
	}
	if _, err := s.db.GetDomainByName(ctx, name); err == nil {
		writeErr(w, http.StatusConflict, "conflict", fmt.Sprintf("%s already exists; delete it or choose another name", name))
		return
	} else if !errors.Is(err, db.ErrNotFound) {
		s.fail(w, r, err)
		return
	}

	switch err := s.db.StartSiteAppTask(ctx, live.ID, db.AppTaskCopying); {
	case errors.Is(err, db.ErrConflict):
		writeErr(w, http.StatusConflict, "conflict", "the application is busy: wait for the current task to finish")
		return
	case err != nil:
		s.fail(w, r, err)
		return
	}
	stg, stgDB, password, err := s.createStagingSite(ctx, r, live, parentID, name, app, a, owner)
	if err != nil {
		_ = s.db.EndSiteAppTask(context.WithoutCancel(ctx), live.ID, "")
		if errors.Is(err, db.ErrConflict) {
			writeErr(w, http.StatusConflict, "conflict", fmt.Sprintf("%s already exists; delete it or choose another name", name))
			return
		}
		s.fail(w, r, err)
		return
	}
	s.audit(r, "app.staging_started", live.Name, stg.Name)
	liveCopy, by := *live, &actor(r).ID
	s.bg.Add(1)
	go func() {
		defer s.bg.Done()
		ctx, cancel := context.WithTimeout(s.baseCtx, stagingTimeout)
		defer cancel()
		s.copyToStaging(ctx, &liveCopy, stg, app, liveDB, stgDB, password, by)
	}()
	writeJSON(w, http.StatusAccepted, stg)
}

// createStagingSite records the staging website (same settings as live), its
// database and its application, and starts deploying the website.
func (s *Server) createStagingSite(ctx context.Context, r *http.Request, live *db.Domain, parentID int64, name string,
	app apps.App, liveApp *db.SiteApp, owner *db.User) (*db.Domain, *db.Database, string, error) {
	stg := &db.Domain{
		Name: name, Kind: db.KindSubdomain, ParentID: &parentID, OwnerID: live.OwnerID,
		Runtime: live.Runtime, RuntimeVersion: live.RuntimeVersion, DocumentRoot: live.DocumentRoot,
		StartCommand: live.StartCommand, AppPort: live.AppPort, SSLEnabled: live.SSLEnabled, ForceHTTPS: live.ForceHTTPS,
		MemoryMB: live.MemoryMB, CPUs: live.CPUs, Env: maps.Clone(live.Env),
	}
	if stg.Env == nil {
		stg.Env = map[string]string{}
	}
	if err := s.db.CreateDomain(ctx, stg); err != nil {
		return nil, nil, "", err
	}
	if err := docker.PrepareSiteDir(s.cfg, stg); err != nil {
		_ = s.db.DeleteDomain(ctx, stg.ID)
		return nil, nil, "", err
	}
	if php, err := s.db.GetPHPSettings(ctx, live.ID); err == nil && len(php) > 0 {
		if err := s.db.SavePHPSettings(ctx, stg.ID, php); err != nil {
			s.log.Warn("copy PHP settings to staging", "domain", stg.Name, "err", err)
		}
	}
	s.audit(r, "domain.created", stg.Name, stg.Kind)
	s.emit("domain.created", map[string]any{"domain_id": stg.ID, "name": stg.Name, "kind": stg.Kind, "owner_id": stg.OwnerID})
	if s.dns != nil {
		if err := s.dns.DomainCreated(ctx, stg); err != nil {
			s.log.Warn("dns records for new website", "domain", stg.Name, "err", err)
		}
	}
	s.deployAsync(stg)

	password := secrets.Password(24)
	enc, err := s.box.Seal(password)
	if err != nil {
		return nil, nil, "", err
	}
	stgDB := &db.Database{OwnerID: owner.ID, Engine: db.EngineMariaDB, PasswordEnc: enc}
	for attempt := 0; ; attempt++ {
		stgDB.Name = appDatabaseName(owner.Username, app, stg.Name, attempt)
		stgDB.Username = stgDB.Name
		err = s.db.CreateDatabase(ctx, stgDB)
		if err == nil || !errors.Is(err, db.ErrConflict) || attempt == 8 {
			break
		}
	}
	if err == nil && !docker.ValidIdentifier(stgDB.Name) {
		err = fmt.Errorf("database name %s is not valid", stgDB.Name)
	}
	if err != nil {
		return nil, nil, "", fmt.Errorf("creating the database of the copy: %w", err)
	}
	s.audit(r, "database.created", stgDB.Name, stgDB.Engine)
	sa := &db.SiteApp{DomainID: stg.ID, App: app.ID, DatabaseID: &stgDB.ID, AdminUser: liveApp.AdminUser,
		AdminEmail: liveApp.AdminEmail, Language: liveApp.Language, StagingOf: &live.ID}
	if id := actor(r).ID; id != 0 {
		sa.CreatedBy = &id
	}
	if err := s.db.StartSiteApp(ctx, sa); err != nil {
		return nil, nil, "", err
	}
	return stg, stgDB, password, nil
}

// waitRunning waits until website id is running.
func (s *Server) waitRunning(ctx context.Context, id int64) (*db.Domain, error) {
	for {
		cur, err := s.db.GetDomain(ctx, id)
		if err != nil {
			return nil, err
		}
		switch cur.Status {
		case db.StatusRunning:
			return cur, nil
		case db.StatusError:
			return nil, fmt.Errorf("the website could not start: %s", cur.StatusMessage)
		case db.StatusStopped, db.StatusDisabled:
			return nil, fmt.Errorf("the website %s is stopped", cur.Name)
		}
		select {
		case <-ctx.Done():
			return nil, errors.New("timed out waiting for the website to start")
		case <-time.After(3 * time.Second):
		}
	}
}

// runStage runs the stage script of app on website d, retrying while its
// container is still starting.
func (s *Server) runStage(ctx context.Context, d *db.Domain, app apps.App, env []string) (string, error) {
	script, err := app.StageScript()
	if err != nil {
		return "", err
	}
	for {
		cur, err := s.waitRunning(ctx, d.ID)
		if err != nil {
			return "", err
		}
		out := &tailBuffer{max: 256 << 10}
		code, err := s.orch.RunSiteScript(ctx, cur, script, env, out)
		if errors.Is(err, docker.ErrSiteNotRunning) {
			select {
			case <-ctx.Done():
				return "", errors.New("timed out waiting for the website to start")
			case <-time.After(3 * time.Second):
			}
			continue
		}
		v := apps.Version(out.String())
		if err != nil {
			return v, err
		}
		if code != 0 {
			return v, fmt.Errorf("adapting the copy stopped (exit %d): %s", code, lastLines(out.String(), 6))
		}
		return v, nil
	}
}

// copyToStaging copies the database and the files of live to stg and adapts
// the copy. A failed copy is left for the user to look at and delete.
func (s *Server) copyToStaging(ctx context.Context, live, stg *db.Domain, app apps.App, liveDB, stgDB *db.Database, password string, by *int64) {
	bg := context.WithoutCancel(ctx)
	version, err := func() (string, error) {
		if err := s.orch.ProvisionDatabase(ctx, stgDB, password); err != nil {
			_ = s.db.SetDatabaseStatus(bg, stgDB.ID, db.StatusError, err.Error())
			return "", fmt.Errorf("creating the database: %w", err)
		}
		if err := s.db.SetDatabaseStatus(ctx, stgDB.ID, db.StatusRunning, ""); err != nil {
			return "", err
		}
		if err := s.orch.CopyMariaDB(ctx, liveDB, stgDB); err != nil {
			return "", err
		}
		cur, err := s.waitRunning(ctx, stg.ID)
		if err != nil {
			return "", err
		}
		if err := s.orch.CopySiteFiles(ctx, live, cur, nil); err != nil {
			return "", err
		}
		env := apps.StageEnv(apps.StageStaging, appDocroot(cur), cur.Name, appDocroot(live), live.Name,
			apps.Database{Host: docker.DatabaseHost(stgDB), Name: stgDB.Name, User: stgDB.Username, Password: password})
		return s.runStage(ctx, cur, app, env)
	}()
	auditApp := func(action, detail string) {
		if err := s.db.Audit(bg, db.AuditEntry{UserID: by, Action: action, Target: live.Name, Detail: detail}); err != nil {
			s.log.Warn("audit write failed", "action", action, "err", err)
		}
	}
	_ = s.db.EndSiteAppTask(bg, live.ID, "")
	if err != nil {
		msg := redact(err.Error(), password)
		s.log.Warn("staging copy failed", "domain", live.Name, "staging", stg.Name, "err", msg)
		_ = s.db.FinishSiteApp(bg, stg.ID, db.AppError, "", "the copy failed: "+msg+". Delete this website and its database, then copy again")
		auditApp("app.staging_failed", stg.Name+": "+msg)
		return
	}
	_ = s.db.FinishSiteApp(bg, stg.ID, db.AppInstalled, version, "")
	s.log.Info("staging copy made", "domain", live.Name, "staging", stg.Name)
	auditApp("app.staging_created", stg.Name)
}

// handlePushStaging copies a staging website back over its live website:
// its files (except the configuration file) and/or its database.
func (s *Server) handlePushStaging(w http.ResponseWriter, r *http.Request) {
	stg := s.loadDomain(w, r)
	if stg == nil {
		return
	}
	var in struct {
		Files    bool `json:"files"`
		Database bool `json:"database"`
	}
	if !decode(w, r, &in) || !s.requireOrch(w) || !s.requireBackups(w) {
		return
	}
	if !in.Files && !in.Database {
		badRequest(w, "choose what to push: the files, the database or both")
		return
	}
	ctx := r.Context()
	sa, err := s.db.GetSiteApp(ctx, stg.ID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if sa.StagingOf == nil {
		writeErr(w, http.StatusConflict, "conflict", "this website is not a staging copy")
		return
	}
	live, err := s.db.GetDomain(ctx, *sa.StagingOf)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	la, err := s.db.GetSiteApp(ctx, live.ID)
	if errors.Is(err, db.ErrNotFound) || (err == nil && la.App != sa.App) {
		writeErr(w, http.StatusConflict, "conflict", "the live website no longer has this application")
		return
	} else if err != nil {
		s.fail(w, r, err)
		return
	}
	app, ok := apps.Get(sa.App)
	if !ok {
		writeErr(w, http.StatusConflict, "conflict", "this application is not known to this version of OpenPlex")
		return
	}
	switch {
	case live.OwnerID != stg.OwnerID:
		writeErr(w, http.StatusConflict, "conflict", "the live website belongs to another account")
		return
	case live.Status != db.StatusRunning || stg.Status != db.StatusRunning:
		writeErr(w, http.StatusConflict, "not_running", "both websites must be running")
		return
	}
	var stgDB, liveDB *db.Database
	if in.Database {
		if stgDB, err = s.appDatabase(ctx, sa); err == nil {
			liveDB, err = s.appDatabase(ctx, la)
		}
		if err != nil {
			writeErr(w, http.StatusConflict, "conflict", err.Error())
			return
		}
	}
	if err := s.db.StartSiteAppTask(ctx, stg.ID, db.AppTaskPushing); err != nil {
		if errors.Is(err, db.ErrConflict) {
			writeErr(w, http.StatusConflict, "conflict", "the application is busy: wait for the current task to finish")
			return
		}
		s.fail(w, r, err)
		return
	}
	if err := s.db.StartSiteAppTask(ctx, live.ID, db.AppTaskPushing); err != nil {
		_ = s.db.EndSiteAppTask(context.WithoutCancel(ctx), stg.ID, "")
		if errors.Is(err, db.ErrConflict) {
			writeErr(w, http.StatusConflict, "conflict", "the live website's application is busy: wait for its current task to finish")
			return
		}
		s.fail(w, r, err)
		return
	}
	s.audit(r, "app.push_started", live.Name, fmt.Sprintf("from %s files=%t database=%t", stg.Name, in.Files, in.Database))
	stgCopy, by := *stg, &actor(r).ID
	s.bg.Add(1)
	go func() {
		defer s.bg.Done()
		ctx, cancel := context.WithTimeout(s.baseCtx, stagingTimeout)
		defer cancel()
		s.pushToLive(ctx, &stgCopy, live, app, in.Files, stgDB, liveDB, by)
	}()
	writeJSON(w, http.StatusAccepted, map[string]any{"live_id": live.ID, "live_name": live.Name})
}

// pushToLive backs up the live website, copies the staging files (keeping
// the live configuration file) and/or database over it, adapts it and checks
// it still answers.
func (s *Server) pushToLive(ctx context.Context, stg, live *db.Domain, app apps.App, files bool, stgDB, liveDB *db.Database, by *int64) {
	bg := context.WithoutCancel(ctx)
	version, err := func() (string, error) {
		if _, err := s.backups.BackupNow(ctx, fmt.Sprintf("app:%d", live.ID), "staging_push", by); err != nil {
			return "", fmt.Errorf("nothing was changed: the backup of the live website failed: %w", err)
		}
		if files {
			if err := s.orch.CopySiteFiles(ctx, stg, live, []string{app.ConfigFile}); err != nil {
				return "", err
			}
		}
		if stgDB != nil {
			if err := s.orch.CopyMariaDB(ctx, stgDB, liveDB); err != nil {
				return "", err
			}
		}
		env := apps.StageEnv(apps.StageLive, appDocroot(live), live.Name, appDocroot(stg), stg.Name, apps.Database{})
		return s.runStage(ctx, live, app, env)
	}()
	auditApp := func(action, detail string) {
		if err := s.db.Audit(bg, db.AuditEntry{UserID: by, Action: action, Target: live.Name, Detail: detail}); err != nil {
			s.log.Warn("audit write failed", "action", action, "err", err)
		}
	}
	if err != nil {
		msg := err.Error()
		if !strings.HasPrefix(msg, "nothing was changed") {
			msg += ". A backup of the live website was taken just before: restore it from its Backups tab if it does not work"
		}
		s.log.Warn("staging push failed", "domain", live.Name, "staging", stg.Name, "err", err)
		_ = s.db.EndSiteAppTask(bg, stg.ID, "pushing to "+live.Name+" failed: "+msg)
		_ = s.db.FinishSiteAppUpdate(bg, live.ID, version, "pushing "+stg.Name+" failed: "+msg)
		auditApp("app.push_failed", stg.Name+": "+msg)
		return
	}
	_ = s.db.EndSiteAppTask(bg, stg.ID, "")
	_ = s.db.FinishSiteAppUpdate(bg, live.ID, version, "")
	s.log.Info("staging pushed", "domain", live.Name, "staging", stg.Name, "version", version)
	auditApp("app.pushed", stg.Name)
	if err := s.db.StartSiteAppTask(bg, live.ID, db.AppTaskChecking); err == nil {
		cctx, cancel := context.WithTimeout(bg, appCheckTimeout)
		defer cancel()
		s.checkApp(cctx, live, app)
	}
}
