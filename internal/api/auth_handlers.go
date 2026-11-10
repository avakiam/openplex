package api

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"openplex.dev/openplex/internal/auth"
	"openplex.dev/openplex/internal/db"
)

const refreshCookie = "opx_refresh"

type tokenResponse struct {
	AccessToken  string    `json:"access_token"`
	ExpiresAt    time.Time `json:"expires_at"`
	RefreshToken string    `json:"refresh_token"`
	User         *db.User  `json:"user"`
}

func (s *Server) issueSession(w http.ResponseWriter, r *http.Request, u *db.User, family string, rotateFrom int64) (*tokenResponse, error) {
	access, exp, err := s.signer.Issue(u.ID, u.Role)
	if err != nil {
		return nil, err
	}
	raw := auth.NewRefreshToken()
	rt := &db.RefreshToken{
		UserID: u.ID, Family: family, TokenHash: auth.HashToken(raw),
		ExpiresAt: time.Now().Add(s.cfg.RefreshTTL), UserAgent: truncate(r.UserAgent(), 255), IP: clientIP(r),
	}
	if rotateFrom > 0 {
		err = s.db.RotateRefreshToken(r.Context(), rotateFrom, rt)
	} else {
		err = s.db.CreateRefreshToken(r.Context(), rt)
	}
	if err != nil {
		return nil, err
	}
	http.SetCookie(w, &http.Cookie{
		Name: refreshCookie, Value: raw, Path: "/api/v1/auth", HttpOnly: true,
		Secure: !s.cfg.Dev, SameSite: http.SameSiteStrictMode, Expires: rt.ExpiresAt,
	})
	return &tokenResponse{AccessToken: access, ExpiresAt: exp.UTC(), RefreshToken: raw, User: u}, nil
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Username string `json:"username"`
		Password string `json:"password"`
		TOTPCode string `json:"totp_code"`
		Captcha  string `json:"captcha_token"`
		Pass     string `json:"captcha_pass"` // from a totp_required answer
	}
	if !decode(w, r, &in) {
		return
	}
	ip := clientIP(r)
	if !s.loginLimiter.allow("ip:"+ip) || !s.loginLimiter.allow("user:"+strings.ToLower(in.Username)) {
		writeErr(w, http.StatusTooManyRequests, "rate_limited", "too many login attempts, try again later")
		return
	}
	if !s.checkLoginCaptcha(w, r, in.Username, in.Captcha, in.Pass) {
		return
	}
	u, err := s.db.GetUserByUsername(r.Context(), in.Username)
	if err != nil && !errors.Is(err, db.ErrNotFound) {
		s.fail(w, r, err)
		return
	}
	hash := ""
	if u != nil {
		hash = u.PasswordHash
	}
	if !auth.VerifyPassword(hash, in.Password) || u.Disabled {
		s.log.Warn("authentication failed", "ip", ip, "user", in.Username) // matched by the fail2ban openplex jail
		s.audit(r, "auth.login_failed", in.Username, "")
		writeErr(w, http.StatusUnauthorized, "invalid_credentials", "invalid username or password")
		return
	}
	if u.TOTPEnabled {
		if in.TOTPCode == "" {
			writeJSON(w, http.StatusUnauthorized, apiError{Error: "totp_required", Message: "two-factor code required", CaptchaPass: s.issueCaptchaPass(in.Username)})
			return
		}
		if !auth.ValidateTOTP(u.TOTPSecret, in.TOTPCode) {
			s.log.Warn("authentication failed", "ip", ip, "user", u.Username)
			s.audit(r, "auth.totp_failed", u.Username, "")
			writeJSON(w, http.StatusUnauthorized, apiError{Error: "totp_invalid", Message: "invalid two-factor code", CaptchaPass: s.issueCaptchaPass(in.Username)})
			return
		}
	}
	resp, err := s.issueSession(w, r, u, auth.NewFamily(), 0)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	r = r.WithContext(withPrincipal(r.Context(), u))
	s.audit(r, "auth.login", u.Username, "")
	writeJSON(w, http.StatusOK, resp)
}

func presentedRefresh(r *http.Request, body string) string {
	if body != "" {
		return body
	}
	if c, err := r.Cookie(refreshCookie); err == nil {
		return c.Value
	}
	return ""
}

func (s *Server) handleRefresh(w http.ResponseWriter, r *http.Request) {
	var in struct {
		RefreshToken string `json:"refresh_token"`
	}
	if !decode(w, r, &in) {
		return
	}
	raw := presentedRefresh(r, in.RefreshToken)
	if raw == "" {
		writeErr(w, http.StatusUnauthorized, "unauthorized", "refresh token required")
		return
	}
	rt, err := s.db.GetRefreshTokenByHash(r.Context(), auth.HashToken(raw))
	if err != nil {
		writeErr(w, http.StatusUnauthorized, "unauthorized", "invalid refresh token")
		return
	}
	if rt.Revoked {
		// Reuse of a rotated token means it leaked: kill the whole family.
		_ = s.db.RevokeRefreshFamily(r.Context(), rt.Family)
		s.audit(r, "auth.refresh_reuse", "", "family revoked")
		writeErr(w, http.StatusUnauthorized, "unauthorized", "refresh token reuse detected; please sign in again")
		return
	}
	if time.Now().After(rt.ExpiresAt) {
		writeErr(w, http.StatusUnauthorized, "unauthorized", "refresh token expired")
		return
	}
	u, err := s.db.GetUser(r.Context(), rt.UserID)
	if err != nil || u.Disabled {
		writeErr(w, http.StatusUnauthorized, "unauthorized", "account unavailable")
		return
	}
	resp, err := s.issueSession(w, r, u, rt.Family, rt.ID)
	if errors.Is(err, db.ErrNotFound) {
		_ = s.db.RevokeRefreshFamily(r.Context(), rt.Family)
		writeErr(w, http.StatusUnauthorized, "unauthorized", "refresh token reuse detected; please sign in again")
		return
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	var in struct {
		RefreshToken string `json:"refresh_token"`
	}
	if !decode(w, r, &in) {
		return
	}
	if raw := presentedRefresh(r, in.RefreshToken); raw != "" {
		if rt, err := s.db.GetRefreshTokenByHash(r.Context(), auth.HashToken(raw)); err == nil {
			_ = s.db.RevokeRefreshFamily(r.Context(), rt.Family)
		}
	}
	http.SetCookie(w, &http.Cookie{Name: refreshCookie, Value: "", Path: "/api/v1/auth", MaxAge: -1, HttpOnly: true, Secure: !s.cfg.Dev, SameSite: http.SameSiteStrictMode})
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, actor(r))
}

// handleUpdateMe changes the caller's own preferences (any role).
func (s *Server) handleUpdateMe(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Locale *string `json:"locale"`
	}
	if !decode(w, r, &in) {
		return
	}
	u, err := s.db.GetUser(r.Context(), actor(r).ID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if in.Locale != nil {
		if !db.ValidLocale(*in.Locale) {
			badRequest(w, "unsupported language")
			return
		}
		u.Locale = *in.Locale
	}
	if err := s.db.UpdateUser(r.Context(), u); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, u)
}

func (s *Server) handleChangeOwnPassword(w http.ResponseWriter, r *http.Request) {
	var in struct {
		CurrentPassword string `json:"current_password"`
		NewPassword     string `json:"new_password"`
	}
	if !decode(w, r, &in) {
		return
	}
	u := actor(r)
	if !auth.VerifyPassword(u.PasswordHash, in.CurrentPassword) {
		writeErr(w, http.StatusUnauthorized, "invalid_credentials", "current password is wrong")
		return
	}
	if err := s.setPassword(r, u, in.NewPassword); err != nil {
		badRequest(w, err.Error())
		return
	}
	s.audit(r, "user.password_changed", u.Username, "self")
	w.WriteHeader(http.StatusNoContent)
}

// setPassword validates, stores and revokes all sessions.
func (s *Server) setPassword(r *http.Request, u *db.User, pw string) error {
	if err := auth.CheckPolicy(pw); err != nil {
		return err
	}
	h, err := auth.HashPassword(pw)
	if err != nil {
		return err
	}
	u.PasswordHash = h
	if err := s.db.UpdateUser(r.Context(), u); err != nil {
		return err
	}
	return s.db.RevokeUserRefreshTokens(r.Context(), u.ID)
}

func (s *Server) handleTOTPSetup(w http.ResponseWriter, r *http.Request) {
	u := actor(r)
	if u.TOTPEnabled {
		writeErr(w, http.StatusConflict, "conflict", "two-factor authentication is already enabled")
		return
	}
	key, err := auth.NewTOTP(u.Username)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	u.TOTPSecret = key.Secret()
	if err := s.db.UpdateUser(r.Context(), u); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"secret": key.Secret(), "otpauth_url": key.URL()})
}

func (s *Server) handleTOTPEnable(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Code string `json:"code"`
	}
	if !decode(w, r, &in) {
		return
	}
	u := actor(r)
	if u.TOTPSecret == "" {
		badRequest(w, "call /auth/2fa/setup first")
		return
	}
	if !auth.ValidateTOTP(u.TOTPSecret, in.Code) {
		writeErr(w, http.StatusBadRequest, "totp_invalid", "invalid two-factor code")
		return
	}
	u.TOTPEnabled = true
	if err := s.db.UpdateUser(r.Context(), u); err != nil {
		s.fail(w, r, err)
		return
	}
	s.audit(r, "auth.2fa_enabled", u.Username, "")
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleTOTPDisable(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Password string `json:"password"`
		Code     string `json:"code"`
	}
	if !decode(w, r, &in) {
		return
	}
	u := actor(r)
	if !u.TOTPEnabled {
		badRequest(w, "two-factor authentication is not enabled")
		return
	}
	if !auth.VerifyPassword(u.PasswordHash, in.Password) || !auth.ValidateTOTP(u.TOTPSecret, in.Code) {
		writeErr(w, http.StatusUnauthorized, "invalid_credentials", "password or code is wrong")
		return
	}
	u.TOTPEnabled, u.TOTPSecret = false, ""
	if err := s.db.UpdateUser(r.Context(), u); err != nil {
		s.fail(w, r, err)
		return
	}
	s.audit(r, "auth.2fa_disabled", u.Username, "")
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleWSTicket(w http.ResponseWriter, r *http.Request) {
	t, err := s.tickets.issue(actor(r).ID)
	if err != nil {
		writeErr(w, http.StatusServiceUnavailable, "unavailable", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"ticket": t})
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}
