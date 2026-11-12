package api

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"openplex.dev/openplex/internal/backup"
	"openplex.dev/openplex/internal/db"
)

func (s *Server) requireBackups(w http.ResponseWriter) bool {
	if s.backups == nil {
		writeErr(w, http.StatusServiceUnavailable, "docker_unavailable", "backups need Docker, which is not available on this server")
		return false
	}
	return true
}

func (s *Server) backupErr(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, backup.ErrBusy) {
		writeErrFrom(w, http.StatusConflict, "busy", err)
		return
	}
	s.fail(w, r, err)
}

type settingsView struct {
	*db.BackupSettings
	Location    string `json:"location"`
	S3SecretSet bool   `json:"s3_secret_set"`
	// Offsite says whether the repository would survive losing the data's disk or server.
	Offsite backup.Offsite `json:"offsite"`
}

func (s *Server) handleGetBackupSettings(w http.ResponseWriter, r *http.Request) {
	if !s.requireBackups(w) {
		return
	}
	st, err := s.backups.Settings(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, settingsView{BackupSettings: st, Location: s.backups.Location(st), S3SecretSet: st.S3SecretEnc != "", Offsite: s.backups.Offsite(r.Context(), st)})
}

func (s *Server) handlePutBackupSettings(w http.ResponseWriter, r *http.Request) {
	if !s.requireBackups(w) {
		return
	}
	var in struct {
		Target          string  `json:"target"`
		LocalPath       string  `json:"local_path"`
		S3Endpoint      string  `json:"s3_endpoint"`
		S3Bucket        string  `json:"s3_bucket"`
		S3Prefix        string  `json:"s3_prefix"`
		S3Region        string  `json:"s3_region"`
		S3AccessKey     string  `json:"s3_access_key"`
		S3Secret        *string `json:"s3_secret"` // write-only; omit to keep
		ScheduleEnabled bool    `json:"schedule_enabled"`
		ScheduleTime    string  `json:"schedule_time"`
		KeepDaily       int     `json:"keep_daily"`
		KeepWeekly      int     `json:"keep_weekly"`
		KeepMonthly     int     `json:"keep_monthly"`
	}
	if !decode(w, r, &in) {
		return
	}
	st, err := s.backups.Settings(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	st.Target, st.LocalPath = in.Target, strings.TrimSpace(in.LocalPath)
	st.S3Endpoint, st.S3Bucket = strings.TrimSpace(in.S3Endpoint), strings.TrimSpace(in.S3Bucket)
	st.S3Prefix, st.S3Region, st.S3AccessKey = strings.Trim(strings.TrimSpace(in.S3Prefix), "/"), strings.TrimSpace(in.S3Region), strings.TrimSpace(in.S3AccessKey)
	if in.S3Secret != nil && *in.S3Secret != "" {
		enc, err := s.box.Seal(*in.S3Secret)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		st.S3SecretEnc = enc
	}
	st.ScheduleEnabled, st.ScheduleTime = in.ScheduleEnabled, in.ScheduleTime
	st.KeepDaily, st.KeepWeekly, st.KeepMonthly = in.KeepDaily, in.KeepWeekly, in.KeepMonthly
	if err := s.backups.Validate(st); err != nil {
		badRequestErr(w, err)
		return
	}
	if err := s.db.SaveBackupSettings(r.Context(), st); err != nil {
		s.fail(w, r, err)
		return
	}
	s.audit(r, "backup.settings_updated", s.backups.Location(st), "")
	writeJSON(w, http.StatusOK, settingsView{BackupSettings: st, Location: s.backups.Location(st), S3SecretSet: st.S3SecretEnc != "", Offsite: s.backups.Offsite(r.Context(), st)})
}

func (s *Server) handleTestBackupRepo(w http.ResponseWriter, r *http.Request) {
	if !s.requireBackups(w) {
		return
	}
	st, err := s.backups.Settings(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if err := s.backups.EnsureRepository(r.Context(), st); err != nil {
		writeErrFrom(w, http.StatusBadGateway, "repository_error", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "location": s.backups.Location(st)})
}

func (s *Server) handleRecoveryKey(w http.ResponseWriter, r *http.Request) {
	if !s.requireBackups(w) {
		return
	}
	key, err := s.backups.RecoveryKey(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.audit(r, "backup.recovery_key_viewed", "", "")
	writeJSON(w, http.StatusOK, map[string]string{"key": key})
}

func (s *Server) handleRunBackup(w http.ResponseWriter, r *http.Request) {
	if !s.requireBackups(w) {
		return
	}
	var in struct {
		Scope string `json:"scope"`
	}
	if !decode(w, r, &in) {
		return
	}
	if in.Scope == "" {
		in.Scope = "all"
	}
	if in.Scope != "all" && in.Scope != "panel" {
		badRequest(w, "scope must be all or panel")
		return
	}
	run, err := s.backups.Backup(r.Context(), in.Scope, "manual", &actor(r).ID)
	if err != nil {
		s.backupErr(w, r, err)
		return
	}
	s.audit(r, "backup.started", in.Scope, "")
	writeJSON(w, http.StatusAccepted, run)
}

func (s *Server) handleListBackupRuns(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 || limit > 500 {
		limit = 50
	}
	runs, err := s.db.ListBackupRuns(r.Context(), nil, limit)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, runs)
}

func (s *Server) handlePanelSnapshots(w http.ResponseWriter, r *http.Request) {
	if !s.requireBackups(w) {
		return
	}
	snaps, err := s.backups.Snapshots(r.Context(), "panel")
	if err != nil {
		s.backupErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, snaps)
}

// ---------- per item (owners) ----------

// itemScope resolves {id} under /domains or /databases to its backup scope,
// enforcing ownership via the existing loaders.
func (s *Server) itemScope(w http.ResponseWriter, r *http.Request) string {
	if strings.Contains(r.URL.Path, "/mail/backups") {
		if md := s.backupMailDomain(w, r); md != nil {
			return backup.MailTag(md.DomainID)
		}
		return ""
	}
	if strings.HasPrefix(r.URL.Path, "/api/v1/databases/") {
		d := s.loadDatabase(w, r)
		if d == nil {
			return ""
		}
		if d.Status != db.StatusRunning {
			writeErr(w, http.StatusConflict, "not_ready", "the database is not ready")
			return ""
		}
		return backup.DatabaseTag(d.ID)
	}
	d := s.loadDomain(w, r)
	if d == nil {
		return ""
	}
	if d.Kind == db.KindAlias {
		badRequest(w, "aliases have no data of their own; back up the parent website")
		return ""
	}
	return backup.SiteTag(d.ID)
}

func (s *Server) handleItemBackups(w http.ResponseWriter, r *http.Request) {
	scope := s.itemScope(w, r)
	if scope == "" || !s.requireBackups(w) {
		return
	}
	snaps, err := s.backups.Snapshots(r.Context(), scope)
	if err != nil {
		s.backupErr(w, r, err)
		return
	}
	runs, err := s.db.ListBackupRuns(r.Context(), []string{scope}, 10)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"snapshots": snaps, "runs": runs})
}

func (s *Server) handleItemBackupNow(w http.ResponseWriter, r *http.Request) {
	scope := s.itemScope(w, r)
	if scope == "" || !s.requireBackups(w) {
		return
	}
	run, err := s.backups.Backup(r.Context(), scope, "manual", &actor(r).ID)
	if err != nil {
		s.backupErr(w, r, err)
		return
	}
	s.audit(r, "backup.started", scope, "")
	writeJSON(w, http.StatusAccepted, run)
}

func (s *Server) handleItemRestore(w http.ResponseWriter, r *http.Request) {
	scope := s.itemScope(w, r)
	if scope == "" || !s.requireBackups(w) {
		return
	}
	snap := chi.URLParam(r, "snap")
	run, err := s.backups.Restore(r.Context(), scope, snap, &actor(r).ID)
	if err != nil {
		if err.Error() == "invalid snapshot id" {
			badRequestErr(w, err)
			return
		}
		s.backupErr(w, r, err)
		return
	}
	s.audit(r, "backup.restore_started", scope, snap)
	writeJSON(w, http.StatusAccepted, run)
}

// backupMailDomain is the mail domain of the website in the URL (nil and an
// error response when mail is off for it).
func (s *Server) backupMailDomain(w http.ResponseWriter, r *http.Request) *db.MailDomain {
	_, md, ok := s.loadMailDomain(w, r)
	if !ok {
		return nil
	}
	if md == nil {
		badRequest(w, "mail is off for this website")
		return nil
	}
	return md
}

// POST /domains/{id}/mail/backups/{snap}/restore {"mailbox": "info"}: puts back
// the messages missing from the domain's mailboxes (or one mailbox).
func (s *Server) handleMailRestore(w http.ResponseWriter, r *http.Request) {
	md := s.backupMailDomain(w, r)
	if md == nil || !s.requireBackups(w) {
		return
	}
	var in struct {
		Mailbox string `json:"mailbox"`
	}
	if r.ContentLength != 0 && !decode(w, r, &in) {
		return
	}
	snap := chi.URLParam(r, "snap")
	run, err := s.backups.RestoreMail(r.Context(), md, in.Mailbox, snap, &actor(r).ID)
	if err != nil {
		if msg := err.Error(); msg == "invalid snapshot id" || msg == "invalid mailbox" {
			badRequest(w, msg)
			return
		}
		s.backupErr(w, r, err)
		return
	}
	target := md.Name
	if in.Mailbox != "" {
		target = in.Mailbox + "@" + md.Name
	}
	s.audit(r, "backup.restore_started", backup.MailTag(md.DomainID), snap+" "+target)
	writeJSON(w, http.StatusAccepted, run)
}
