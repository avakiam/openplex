package captcha

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestVerify(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		switch {
		case r.Form.Get("secret") != "good-secret":
			_, _ = w.Write([]byte(`{"success":false,"error-codes":["invalid-input-secret"]}`))
		case r.Form.Get("response") == "solved" && r.Form.Get("remoteip") == "203.0.113.7":
			_, _ = w.Write([]byte(`{"success":true}`))
		default:
			_, _ = w.Write([]byte(`{"success":false,"error-codes":["invalid-input-response"]}`))
		}
	}))
	defer srv.Close()
	v := NewVerifier()
	v.URLs = map[string]string{"turnstile": srv.URL, "hcaptcha": srv.URL, "recaptcha": srv.URL}
	ctx := context.Background()
	for _, p := range []string{"turnstile", "hcaptcha", "recaptcha"} {
		if err := v.Verify(ctx, p, "good-secret", "solved", "203.0.113.7"); err != nil {
			t.Errorf("%s: %v", p, err)
		}
	}
	if err := v.Verify(ctx, "turnstile", "good-secret", "robot", "203.0.113.7"); !errors.Is(err, ErrFailed) {
		t.Errorf("unsolved token: %v", err)
	}
	if err := v.Verify(ctx, "turnstile", "good-secret", "", ""); !errors.Is(err, ErrFailed) {
		t.Errorf("empty token: %v", err)
	}
	if err := v.Verify(ctx, "turnstile", "bad", "solved", "203.0.113.7"); err == nil || errors.Is(err, ErrFailed) {
		t.Errorf("wrong secret should be a configuration error: %v", err)
	}
	if err := v.Verify(ctx, "other", "good-secret", "solved", ""); err == nil {
		t.Error("unknown provider accepted")
	}
	for k, ok := range map[string]bool{"1x00000000000000000000AA": true, "6LeIxAcTAAAAAJcZVRqyHh6xMfMuIGTXiXwXrfDO": true, "a'b": false, "short": false} {
		if ValidKey(k) != ok {
			t.Errorf("ValidKey(%q) != %v", k, ok)
		}
	}
}
