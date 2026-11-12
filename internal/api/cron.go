package api

import (
	"errors"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"openplex.dev/openplex/internal/cron"
	"openplex.dev/openplex/internal/db"
)

// cronJobView adds live state to a stored job.
type cronJobView struct {
	*db.CronJob
	Running bool `json:"running"`
}

func (s *Server) cronView(j *db.CronJob) cronJobView {
	return cronJobView{CronJob: j, Running: s.cron != nil && s.cron.Running(j.ID)}
}

// loadCronSite resolves the website for cron endpoints (aliases have none).
func (s *Server) loadCronSite(w http.ResponseWriter, r *http.Request) *db.Domain {
	d := s.loadDomain(w, r)
	if d == nil {
		return nil
	}
	if d.Kind == db.KindAlias {
		badRequest(w, "aliases have no environment; open the parent website")
		return nil
	}
	return d
}

// loadCronJob resolves {jid} and checks it belongs to the website in the URL.
func (s *Server) loadCronJob(w http.ResponseWriter, r *http.Request) (*db.Domain, *db.CronJob) {
	d := s.loadCronSite(w, r)
	if d == nil {
		return nil, nil
	}
	jid, ok := idParam(w, r, "jid")
	if !ok {
		return nil, nil
	}
	j, err := s.db.GetCronJob(r.Context(), jid)
	if err != nil || j.DomainID != d.ID {
		writeErr(w, http.StatusNotFound, "not_found", "resource not found")
		return nil, nil
	}
	return d, j
}

type cronInput struct {
	Name       *string `json:"name"`
	Schedule   *string `json:"schedule"`
	Command    *string `json:"command"`
	Enabled    *bool   `json:"enabled"`
	TimeoutSec *int    `json:"timeout_sec"`
}

// apply validates in and copies it onto j; returns a user-facing message on error.
func (in cronInput) apply(j *db.CronJob) string {
	if in.Name != nil {
		j.Name = strings.TrimSpace(*in.Name)
	}
	if in.Schedule != nil {
		j.Schedule = strings.Join(strings.Fields(*in.Schedule), " ")
	}
	if in.Command != nil {
		j.Command = strings.TrimSpace(*in.Command)
	}
	if in.Enabled != nil {
		j.Enabled = *in.Enabled
	}
	if in.TimeoutSec != nil {
		j.TimeoutSec = *in.TimeoutSec
	}
	switch {
	case utf8.RuneCountInString(j.Name) > cron.MaxNameLen:
		return "the name is too long (100 characters at most)"
	case j.Command == "":
		return "enter the command to run"
	case len(j.Command) > cron.MaxCommandLen || strings.ContainsRune(j.Command, 0):
		return "the command is too long (4000 characters at most)"
	case j.TimeoutSec < cron.MinTimeout || j.TimeoutSec > cron.MaxTimeout:
		return "the time limit must be between 10 seconds and 6 hours"
	}
	if _, err := cron.Parse(j.Schedule); err != nil {
		return err.Error()
	}
	return ""
}

func (s *Server) handleListCron(w http.ResponseWriter, r *http.Request) {
	d := s.loadCronSite(w, r)
	if d == nil {
		return
	}
	jobs, err := s.db.ListCronJobs(r.Context(), d.ID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	out := make([]cronJobView, len(jobs))
	for i, j := range jobs {
		out[i] = s.cronView(j)
	}
	writeJSON(w, http.StatusOK, map[string]any{"jobs": out, "timezone": cron.TimeZone(), "max_jobs": cron.MaxJobsPerSite})
}

func (s *Server) handleCreateCron(w http.ResponseWriter, r *http.Request) {
	d := s.loadCronSite(w, r)
	if d == nil {
		return
	}
	var in cronInput
	if !decode(w, r, &in) {
		return
	}
	n, err := s.db.CountCronJobs(r.Context(), d.ID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if n >= cron.MaxJobsPerSite {
		writeErr(w, http.StatusForbidden, "quota_exceeded", "this website already has the maximum of 20 scheduled tasks")
		return
	}
	j := &db.CronJob{DomainID: d.ID, Enabled: true, TimeoutSec: cron.DefaultTimeout}
	if msg := in.apply(j); msg != "" {
		badRequest(w, msg)
		return
	}
	j.NextRunAt, _ = cron.Next(j, time.Now())
	if err := s.db.CreateCronJob(r.Context(), j); err != nil {
		s.fail(w, r, err)
		return
	}
	s.audit(r, "cron.created", d.Name, j.Schedule+" "+j.Command)
	writeJSON(w, http.StatusCreated, s.cronView(j))
}

func (s *Server) handleUpdateCron(w http.ResponseWriter, r *http.Request) {
	d, j := s.loadCronJob(w, r)
	if j == nil {
		return
	}
	var in cronInput
	if !decode(w, r, &in) {
		return
	}
	if msg := in.apply(j); msg != "" {
		badRequest(w, msg)
		return
	}
	j.NextRunAt, _ = cron.Next(j, time.Now())
	if err := s.db.UpdateCronJob(r.Context(), j); err != nil {
		s.fail(w, r, err)
		return
	}
	s.audit(r, "cron.updated", d.Name, j.Schedule+" "+j.Command)
	writeJSON(w, http.StatusOK, s.cronView(j))
}

func (s *Server) handleDeleteCron(w http.ResponseWriter, r *http.Request) {
	d, j := s.loadCronJob(w, r)
	if j == nil {
		return
	}
	if err := s.db.DeleteCronJob(r.Context(), j.ID); err != nil {
		s.fail(w, r, err)
		return
	}
	s.audit(r, "cron.deleted", d.Name, j.Schedule+" "+j.Command)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleRunCron(w http.ResponseWriter, r *http.Request) {
	d, j := s.loadCronJob(w, r)
	if j == nil {
		return
	}
	if d.Status == db.StatusDisabled && actor(r).Role != db.RoleAdmin {
		writeErr(w, http.StatusForbidden, "disabled", "this website was disabled by your provider")
		return
	}
	if !s.requireOrch(w) {
		return
	}
	if err := s.cron.RunNow(j); err != nil {
		if errors.Is(err, cron.ErrBusy) {
			writeErrFrom(w, http.StatusConflict, "busy", err)
			return
		}
		s.fail(w, r, err)
		return
	}
	s.audit(r, "cron.run", d.Name, j.Command)
	w.WriteHeader(http.StatusAccepted)
}

func (s *Server) handleListCronRuns(w http.ResponseWriter, r *http.Request) {
	_, j := s.loadCronJob(w, r)
	if j == nil {
		return
	}
	runs, err := s.db.ListCronRuns(r.Context(), j.ID, cron.KeepRuns)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, runs)
}

// handleCronPreview validates a schedule and returns its next run times so
// the UI can show what an expression means before saving it.
func (s *Server) handleCronPreview(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Schedule string `json:"schedule"`
	}
	if !decode(w, r, &in) {
		return
	}
	next, err := cron.NextRuns(strings.Join(strings.Fields(in.Schedule), " "), time.Now(), 5)
	if err != nil {
		badRequestErr(w, err)
		return
	}
	out := make([]time.Time, len(next))
	for i, t := range next {
		out[i] = t.UTC()
	}
	writeJSON(w, http.StatusOK, map[string]any{"next": out, "timezone": cron.TimeZone()})
}
