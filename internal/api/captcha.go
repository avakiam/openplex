package api

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"openplex.dev/openplex/internal/captcha"
	"openplex.dev/openplex/internal/db"
)

// captchaPassTTL is how long the two-factor step may skip the captcha.
const captchaPassTTL = 5 * time.Minute

// loginCaptcha is the captcha the panel login asks for, or nil.
func (s *Server) loginCaptcha(ctx context.Context) (*db.CaptchaSettings, string, error) {
	if !s.cfg.Captcha {
		return nil, "", nil
	}
	c, err := s.db.GetCaptchaSettings(ctx)
	if err != nil || !c.Panel || c.Provider == "" {
		return nil, "", err
	}
	secret, err := s.box.Open(c.SecretEnc)
	if err != nil {
		return nil, "", err
	}
	return c, secret, nil
}

// GET /auth/captcha (public): what the login page must show.
func (s *Server) handleLoginCaptcha(w http.ResponseWriter, r *http.Request) {
	out := map[string]string{"provider": "", "site_key": ""}
	if c, _, err := s.loginCaptcha(r.Context()); err != nil {
		s.log.Error("captcha settings", "err", err)
	} else if c != nil {
		out["provider"], out["site_key"] = c.Provider, c.SiteKey
	}
	writeJSON(w, http.StatusOK, out)
}

// checkLoginCaptcha enforces the captcha on a login attempt. It writes the
// error response and returns false when the attempt must stop.
func (s *Server) checkLoginCaptcha(w http.ResponseWriter, r *http.Request, user, token, pass string) bool {
	c, secret, err := s.loginCaptcha(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return false
	}
	if c == nil {
		return true
	}
	if pass != "" {
		if id, ok := s.captchaPasses.redeem(pass); ok && id == passKey(user) {
			return true
		}
	}
	switch err := s.captcha.Verify(r.Context(), c.Provider, secret, token, clientIP(r)); {
	case err == nil:
		return true
	case errors.Is(err, captcha.ErrFailed):
		writeErr(w, http.StatusBadRequest, "captcha_failed", "please confirm that you are not a robot")
	default:
		s.log.Error("captcha check", "provider", c.Provider, "err", err)
		writeErr(w, http.StatusServiceUnavailable, "captcha_unavailable", "the captcha could not be checked, try again")
	}
	return false
}

// passKey binds a captcha pass to a user name (the store keys passes by int64).
func passKey(user string) int64 {
	var h int64 = 1469598103934665603
	for _, c := range []byte(strings.ToLower(user)) {
		h = (h ^ int64(c)) * 1099511628211
	}
	return h
}

func (s *Server) issueCaptchaPass(user string) string {
	if c, _, err := s.loginCaptcha(s.baseCtx); err != nil || c == nil {
		return ""
	}
	p, err := s.captchaPasses.issueFor(passKey(user), captchaPassTTL)
	if err != nil {
		return ""
	}
	return p
}

type captchaView struct {
	Provider  string `json:"provider"`
	SiteKey   string `json:"site_key"`
	SecretSet bool   `json:"secret_set"`
	Panel     bool   `json:"panel"`
	Webmail   bool   `json:"webmail"`
	Disabled  bool   `json:"disabled_by_config"` // OPENPLEX_CAPTCHA=false
}

// GET /captcha (admin)
func (s *Server) handleGetCaptcha(w http.ResponseWriter, r *http.Request) {
	c, err := s.db.GetCaptchaSettings(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, captchaView{Provider: c.Provider, SiteKey: c.SiteKey, SecretSet: c.SecretEnc != "", Panel: c.Panel, Webmail: c.Webmail, Disabled: !s.cfg.Captcha})
}

// PUT /captcha (admin). Turning the captcha on needs a token solved with the
// new keys, so a wrong key can't lock everyone out of the login page.
func (s *Server) handlePutCaptcha(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Provider  string `json:"provider"`
		SiteKey   string `json:"site_key"`
		Secret    string `json:"secret"`
		Panel     bool   `json:"panel"`
		Webmail   bool   `json:"webmail"`
		TestToken string `json:"test_token"`
	}
	if !decode(w, r, &in) {
		return
	}
	old, err := s.db.GetCaptchaSettings(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	next := &db.CaptchaSettings{}
	if in.Provider != "" {
		if _, ok := captcha.Providers[in.Provider]; !ok {
			writeErr(w, http.StatusBadRequest, "invalid", "choose Cloudflare Turnstile, hCaptcha or Google reCAPTCHA")
			return
		}
		in.SiteKey, in.Secret = strings.TrimSpace(in.SiteKey), strings.TrimSpace(in.Secret)
		if !captcha.ValidKey(in.SiteKey) {
			writeErr(w, http.StatusBadRequest, "invalid", "enter the site key from the captcha provider")
			return
		}
		secretEnc := old.SecretEnc
		secret := ""
		switch {
		case in.Secret != "":
			if !captcha.ValidKey(in.Secret) {
				writeErr(w, http.StatusBadRequest, "invalid", "enter the secret key from the captcha provider")
				return
			}
			secret = in.Secret
			if secretEnc, err = s.box.Seal(in.Secret); err != nil {
				s.fail(w, r, err)
				return
			}
		case old.SecretEnc != "" && old.Provider == in.Provider:
			if secret, err = s.box.Open(old.SecretEnc); err != nil {
				s.fail(w, r, err)
				return
			}
		default:
			writeErr(w, http.StatusBadRequest, "invalid", "enter the secret key from the captcha provider")
			return
		}
		next = &db.CaptchaSettings{Provider: in.Provider, SiteKey: in.SiteKey, SecretEnc: secretEnc, Panel: in.Panel, Webmail: in.Webmail}
		changed := in.Provider != old.Provider || in.SiteKey != old.SiteKey || in.Secret != "" || (in.Panel && !old.Panel) || (in.Webmail && !old.Webmail)
		if (in.Panel || in.Webmail) && changed {
			switch err := s.captcha.Verify(r.Context(), in.Provider, secret, in.TestToken, clientIP(r)); {
			case errors.Is(err, captcha.ErrFailed):
				writeErr(w, http.StatusBadRequest, "captcha_test_failed", "solve the captcha shown here with the new keys before turning it on")
				return
			case err != nil:
				writeErrFrom(w, http.StatusBadRequest, "captcha_test_failed", err)
				return
			}
		}
	}
	if err := s.db.SaveCaptchaSettings(r.Context(), next); err != nil {
		s.fail(w, r, err)
		return
	}
	if s.mail != nil {
		s.mail.Changed() // the webmail's configuration
	}
	s.audit(r, "captcha.update", next.Provider, "")
	s.handleGetCaptcha(w, r)
}
