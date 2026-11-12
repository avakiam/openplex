package api

import (
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"openplex.dev/openplex/internal/db"
	"openplex.dev/openplex/internal/msg"
)

type apiError struct {
	Error   string `json:"error"`
	Message string `json:"message"`
	// Template and Params let the dashboard translate a message with values
	// (msg.Error): Message is Template with Params filled in.
	Template string            `json:"template,omitempty"`
	Params   map[string]string `json:"params,omitempty"`
	// CaptchaPass lets the two-factor step skip a second captcha.
	CaptchaPass string `json:"captcha_pass,omitempty"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	if v != nil {
		_ = json.NewEncoder(w).Encode(v)
	}
}

func writeErr(w http.ResponseWriter, status int, code, msg string) {
	writeJSON(w, status, apiError{Error: code, Message: msg})
}

// writeMsg writes a message with values, translatable by the dashboard.
func writeMsg(w http.ResponseWriter, status int, code string, m *msg.Error) {
	writeJSON(w, status, apiError{Error: code, Message: m.Error(), Template: m.Template, Params: m.Params})
}

// writeErrFrom writes err as the message, keeping its template when it is a msg.Error.
func writeErrFrom(w http.ResponseWriter, status int, code string, err error) {
	if m, ok := msg.From(err); ok {
		writeMsg(w, status, code, m)
		return
	}
	writeErr(w, status, code, err.Error())
}

// badRequestErr is badRequest for a validation error.
func badRequestErr(w http.ResponseWriter, err error) {
	writeErrFrom(w, http.StatusBadRequest, "bad_request", err)
}

// fail maps well-known errors to HTTP responses; anything else is a logged 500.
func (s *Server) fail(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, db.ErrNotFound), errors.Is(err, fs.ErrNotExist):
		writeErr(w, http.StatusNotFound, "not_found", "resource not found")
	case errors.Is(err, db.ErrConflict), errors.Is(err, fs.ErrExist):
		if m, ok := msg.From(err); ok {
			writeMsg(w, http.StatusConflict, "conflict", m)
		} else {
			writeErr(w, http.StatusConflict, "conflict", "already exists")
		}
	case errors.Is(err, fs.ErrPermission):
		writeErr(w, http.StatusForbidden, "forbidden", "permission denied")
	default:
		s.log.Error("request failed", "method", r.Method, "path", r.URL.Path, "err", err)
		writeErr(w, http.StatusInternalServerError, "internal", "internal server error")
	}
}

func badRequest(w http.ResponseWriter, msg string) {
	writeErr(w, http.StatusBadRequest, "bad_request", msg)
}

func forbidden(w http.ResponseWriter) {
	writeErr(w, http.StatusForbidden, "forbidden", "you are not allowed to do this")
}

// decode reads a JSON body (max 1 MiB) into v, rejecting unknown fields.
func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	return decodeLarge(w, r, v)
}

// decodeLarge decodes without imposing the 1 MiB cap (caller sets its own).
func decodeLarge(w http.ResponseWriter, r *http.Request, v any) bool {
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil && !errors.Is(err, io.EOF) {
		writeMsg(w, http.StatusBadRequest, "bad_request", msg.New("invalid JSON body: {detail}", "detail", strings.TrimPrefix(err.Error(), "json: ")))
		return false
	}
	return true
}

func idParam(w http.ResponseWriter, r *http.Request, name string) (int64, bool) {
	id, err := strconv.ParseInt(chi.URLParam(r, name), 10, 64)
	if err != nil || id <= 0 {
		writeMsg(w, http.StatusBadRequest, "bad_request", msg.New("invalid {name}", "name", name))
		return 0, false
	}
	return id, true
}

func (s *Server) audit(r *http.Request, action, target, detail string) {
	e := db.AuditEntry{Action: action, Target: target, Detail: detail, IP: clientIP(r)}
	if p := principalFrom(r.Context()); p != nil && p.User.ID != 0 {
		e.UserID = &p.User.ID
	}
	if p := principalFrom(r.Context()); p != nil && p.Extension != "" {
		e.Detail = strings.TrimSpace(e.Detail + " (by extension " + p.Extension + ")")
	}
	if err := s.db.Audit(r.Context(), e); err != nil {
		s.log.Warn("audit write failed", "action", action, "err", err)
	}
}
