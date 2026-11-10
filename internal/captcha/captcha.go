// Package captcha verifies login captchas: Cloudflare Turnstile, hCaptcha and
// Google reCAPTCHA v2. The three share the same siteverify protocol (a form
// POST of secret, response and remoteip answered with {"success": bool}).
package captcha

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

// Provider is one captcha service.
type Provider struct {
	ID        string
	Name      string
	VerifyURL string
}

// Providers by ID.
var Providers = map[string]Provider{
	"turnstile": {ID: "turnstile", Name: "Cloudflare Turnstile", VerifyURL: "https://challenges.cloudflare.com/turnstile/v0/siteverify"},
	"hcaptcha":  {ID: "hcaptcha", Name: "hCaptcha", VerifyURL: "https://api.hcaptcha.com/siteverify"},
	"recaptcha": {ID: "recaptcha", Name: "Google reCAPTCHA v2", VerifyURL: "https://www.google.com/recaptcha/api/siteverify"},
}

var keyRe = regexp.MustCompile(`^[A-Za-z0-9_\-.]{8,128}$`)

// ValidKey reports whether k looks like a site key or secret of any provider
// (they are also written into the webmail's PHP configuration).
func ValidKey(k string) bool { return keyRe.MatchString(k) }

// ErrFailed means the provider did not accept the token.
var ErrFailed = errors.New("captcha check failed")

// Verifier checks tokens against the providers.
type Verifier struct {
	Client *http.Client
	// URLs overrides the verify endpoints (tests).
	URLs map[string]string
}

// NewVerifier returns a Verifier with a 10 s timeout.
func NewVerifier() *Verifier { return &Verifier{Client: &http.Client{Timeout: 10 * time.Second}} }

// Verify asks provider whether token is a solved challenge. It returns
// ErrFailed when the provider says no, another error when it can't be asked.
func (v *Verifier) Verify(ctx context.Context, provider, secret, token, remoteIP string) error {
	p, ok := Providers[provider]
	if !ok {
		return fmt.Errorf("unknown captcha provider %q", provider)
	}
	if token == "" || len(token) > 4096 {
		return ErrFailed
	}
	endpoint := p.VerifyURL
	if u := v.URLs[provider]; u != "" {
		endpoint = u
	}
	form := url.Values{"secret": {secret}, "response": {token}}
	if remoteIP != "" {
		form.Set("remoteip", remoteIP)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	res, err := v.Client.Do(req)
	if err != nil {
		return fmt.Errorf("ask %s: %w", p.Name, err)
	}
	defer res.Body.Close()
	var out struct {
		Success bool     `json:"success"`
		Codes   []string `json:"error-codes"`
	}
	if err := json.NewDecoder(io.LimitReader(res.Body, 64<<10)).Decode(&out); err != nil {
		return fmt.Errorf("read %s answer (HTTP %d): %w", p.Name, res.StatusCode, err)
	}
	if !out.Success {
		for _, c := range out.Codes {
			if strings.Contains(c, "secret") {
				return fmt.Errorf("%s rejected the secret key (%s)", p.Name, c)
			}
		}
		return ErrFailed
	}
	return nil
}
