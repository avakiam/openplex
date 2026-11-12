package api

import (
	"context"
	"errors"
	"net/http"

	"openplex.dev/openplex/internal/db"
	"openplex.dev/openplex/internal/update"
)

func (s *Server) updateStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.updates.Status(r.Context()))
}

func (s *Server) noUpdates(w http.ResponseWriter) bool {
	if s.updates == nil {
		writeErr(w, http.StatusServiceUnavailable, "unavailable", "updates are not available on this server")
		return true
	}
	return false
}

func (s *Server) handleGetUpdate(w http.ResponseWriter, r *http.Request) {
	if s.noUpdates(w) {
		return
	}
	s.updateStatus(w, r)
}

func (s *Server) handleCheckUpdate(w http.ResponseWriter, r *http.Request) {
	if s.noUpdates(w) {
		return
	}
	if err := s.updates.Check(r.Context()); err != nil {
		s.log.Warn("update check", "err", err)
	}
	s.updateStatus(w, r)
}

func (s *Server) handleInstallUpdate(w http.ResponseWriter, r *http.Request) {
	if s.noUpdates(w) {
		return
	}
	var in struct {
		Version string `json:"version"`
	}
	if !decode(w, r, &in) {
		return
	}
	if err := s.updates.Install(r.Context(), in.Version); err != nil {
		s.audit(r, "system.update_failed", "openplexd", s.version+" → "+in.Version+": "+err.Error())
		s.updates.MarkAudited()
		switch {
		case errors.Is(err, update.ErrBusy):
			writeErrFrom(w, http.StatusConflict, "busy", err)
		case errors.Is(err, update.ErrNoUpdate), errors.Is(err, update.ErrDevBuild), errors.Is(err, update.ErrUnsupported):
			writeErrFrom(w, http.StatusConflict, "not_installable", err)
		case errors.Is(err, update.ErrBadRelease):
			writeErrFrom(w, http.StatusBadGateway, "bad_release", err)
		default:
			writeErrFrom(w, http.StatusBadGateway, "update_failed", err)
		}
		return
	}
	s.audit(r, "system.update_started", "openplexd", s.version+" → "+in.Version)
	writeJSON(w, http.StatusAccepted, s.updates.Status(r.Context()))
}

func (s *Server) handlePutUpdateSettings(w http.ResponseWriter, r *http.Request) {
	if s.noUpdates(w) {
		return
	}
	var in struct {
		CheckEnabled bool `json:"check_enabled"`
	}
	if !decode(w, r, &in) {
		return
	}
	if err := s.db.SaveUpdateSettings(r.Context(), &db.UpdateSettings{CheckEnabled: in.CheckEnabled}); err != nil {
		s.fail(w, r, err)
		return
	}
	s.audit(r, "system.update_settings", "openplexd", map[bool]string{true: "automatic checks on", false: "automatic checks off"}[in.CheckEnabled])
	if in.CheckEnabled {
		_ = s.updates.Check(r.Context())
	}
	s.updateStatus(w, r)
}

// AuditUpdateOutcome writes how the last update ended to the audit log,
// once (the daemon calls it at start, after an update restarted it).
func (s *Server) AuditUpdateOutcome(ctx context.Context) {
	if s.updates == nil {
		return
	}
	o := s.updates.LastOutcome()
	if o == nil || o.Audited {
		return
	}
	action := map[string]string{"updated": "system.updated", "rolled_back": "system.update_rolled_back"}[o.Result]
	if action == "" {
		action = "system.update_failed"
	}
	detail := o.From + " → " + o.To
	if o.Message != "" {
		detail += ": " + o.Message
	}
	if err := s.db.Audit(ctx, db.AuditEntry{Action: action, Target: "openplexd", Detail: detail}); err != nil {
		s.log.Warn("audit update outcome", "err", err)
		return
	}
	s.updates.MarkAudited()
}
