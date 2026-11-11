package api

import (
	"context"
	"net/http"

	"openplex.dev/openplex/internal/alerts"
	"openplex.dev/openplex/internal/db"
)

type alertsView struct {
	Settings        *db.AlertSettings `json:"settings"`
	PasswordSet     bool              `json:"smtp_password_set"`
	Kinds           []string          `json:"kinds"`
	MailRunning     bool              `json:"mail_running"` // this server's mail service can send
	DefaultFrom     string            `json:"default_from"`
	AdminEmails     []string          `json:"admin_emails"` // suggested recipients
	Recent          []*db.Alert       `json:"recent"`
	AlertsAvailable bool              `json:"available"`
}

// alertsInput is the settings as the dashboard sends them; SMTPPassword nil
// keeps the stored password, "" removes it.
type alertsInput struct {
	db.AlertSettings
	SMTPPassword *string `json:"smtp_password"`
}

func (s *Server) alertsView(ctx context.Context) (*alertsView, error) {
	st, err := s.db.GetAlertSettings(ctx)
	if err != nil {
		return nil, err
	}
	recent, err := s.db.RecentAlerts(ctx, 50)
	if err != nil {
		return nil, err
	}
	v := &alertsView{Settings: st, PasswordSet: st.SMTPPasswordEnc != "", Kinds: alerts.Kinds, Recent: recent, AlertsAvailable: s.alerts != nil, AdminEmails: []string{}}
	if s.alerts != nil {
		v.DefaultFrom = s.alerts.DefaultFrom()
	}
	if s.orch != nil {
		v.MailRunning, _ = s.orch.MailStatus(ctx)
	}
	users, err := s.db.ListUsers(ctx, db.UserFilter{})
	if err != nil {
		return nil, err
	}
	for _, u := range users {
		if u.Role == db.RoleAdmin && !u.Disabled && u.Email != "" {
			v.AdminEmails = append(v.AdminEmails, u.Email)
		}
	}
	return v, nil
}

func (s *Server) handleGetAlerts(w http.ResponseWriter, r *http.Request) {
	v, err := s.alertsView(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

// readAlertSettings decodes and validates the settings in the request,
// keeping the stored SMTP password unless a new one is given.
func (s *Server) readAlertSettings(w http.ResponseWriter, r *http.Request) *db.AlertSettings {
	var in alertsInput
	if !decode(w, r, &in) {
		return nil
	}
	cur, err := s.db.GetAlertSettings(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return nil
	}
	st := in.AlertSettings
	st.SMTPPasswordEnc = cur.SMTPPasswordEnc
	if in.SMTPPassword != nil {
		st.SMTPPasswordEnc = ""
		if *in.SMTPPassword != "" {
			if len(*in.SMTPPassword) > 512 {
				badRequest(w, "the SMTP password is too long")
				return nil
			}
			if st.SMTPPasswordEnc, err = s.box.Seal(*in.SMTPPassword); err != nil {
				s.fail(w, r, err)
				return nil
			}
		}
	}
	if err := alerts.Validate(&st); err != nil {
		badRequest(w, err.Error())
		return nil
	}
	return &st
}

func (s *Server) handlePutAlerts(w http.ResponseWriter, r *http.Request) {
	st := s.readAlertSettings(w, r)
	if st == nil {
		return
	}
	if err := s.db.SaveAlertSettings(r.Context(), st); err != nil {
		s.fail(w, r, err)
		return
	}
	detail := "off"
	if st.Enabled {
		detail = "on"
	}
	s.audit(r, "alerts.settings_updated", "", detail)
	s.handleGetAlerts(w, r)
}

// handleTestAlert sends a test e-mail with the settings in the request (saved or not).
func (s *Server) handleTestAlert(w http.ResponseWriter, r *http.Request) {
	if s.alerts == nil {
		writeErr(w, http.StatusServiceUnavailable, "unavailable", "alerts are not available on this server")
		return
	}
	st := s.readAlertSettings(w, r)
	if st == nil {
		return
	}
	if err := s.alerts.Test(r.Context(), st); err != nil {
		writeErr(w, http.StatusBadGateway, "send_failed", "the test e-mail could not be sent: "+err.Error())
		return
	}
	s.audit(r, "alerts.test_sent", "", "")
	writeJSON(w, http.StatusOK, map[string]any{"sent_to": st.Recipients})
}
