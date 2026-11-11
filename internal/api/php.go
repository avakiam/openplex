package api

import (
	"net/http"

	"openplex.dev/openplex/internal/db"
	"openplex.dev/openplex/internal/docker"
)

type phpSettingsView struct {
	Settings map[string]string  `json:"settings"` // only the values that differ from the defaults
	Options  []docker.PHPOption `json:"options"`
	MemoryMB int                `json:"memory_mb"` // upper bound of memory_limit
}

func (s *Server) phpView(r *http.Request, d *db.Domain) (*phpSettingsView, error) {
	settings, err := s.db.GetPHPSettings(r.Context(), d.ID)
	if err != nil {
		return nil, err
	}
	return &phpSettingsView{Settings: settings, Options: docker.PHPOptions, MemoryMB: d.MemoryMB}, nil
}

func (s *Server) handleGetPHPSettings(w http.ResponseWriter, r *http.Request) {
	d := s.loadDomain(w, r)
	if d == nil {
		return
	}
	if d.Runtime != "php" {
		writeErr(w, http.StatusConflict, "needs_php", "this website does not run PHP")
		return
	}
	v, err := s.phpView(r, d)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

// handlePutPHPSettings replaces the website's PHP settings (missing keys go
// back to their defaults) and restarts its container with them.
func (s *Server) handlePutPHPSettings(w http.ResponseWriter, r *http.Request) {
	d := s.loadDomain(w, r)
	if d == nil {
		return
	}
	var in struct {
		Settings map[string]string `json:"settings"`
	}
	if !decode(w, r, &in) {
		return
	}
	if d.Runtime != "php" {
		writeErr(w, http.StatusConflict, "needs_php", "this website does not run PHP")
		return
	}
	settings, err := docker.ValidatePHPSettings(in.Settings, d.MemoryMB)
	if err != nil {
		badRequest(w, err.Error())
		return
	}
	if err := s.db.SavePHPSettings(r.Context(), d.ID, settings); err != nil {
		s.fail(w, r, err)
		return
	}
	s.audit(r, "domain.php_settings_updated", d.Name, formatSettings(settings))
	if d.Status != db.StatusStopped && d.Status != db.StatusDisabled {
		s.deployAsync(d)
	}
	v, err := s.phpView(r, d)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

func formatSettings(m map[string]string) string {
	out := ""
	for _, o := range docker.PHPOptions {
		if v, ok := m[o.Key]; ok {
			if out != "" {
				out += ", "
			}
			out += o.Key + "=" + v
		}
	}
	if out == "" {
		return "defaults"
	}
	return out
}
