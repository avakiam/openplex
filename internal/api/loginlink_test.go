package api

import (
	"context"
	"net/http"
	"testing"
	"time"

	"openplex.dev/openplex/internal/auth"
	"openplex.dev/openplex/internal/db"
)

func TestLoginLink(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	u := h.mkUser("customer", db.RoleCustomer, nil)
	link := func(uid int64, ttl time.Duration) string {
		tok := auth.NewRefreshToken()
		if err := h.db.CreateLoginLink(ctx, uid, auth.HashToken(tok), time.Now().Add(ttl)); err != nil {
			t.Fatal(err)
		}
		return tok
	}

	if code := h.do("POST", "/auth/link", "", map[string]string{"token": link(u.ID, -time.Minute)}, nil); code != http.StatusUnauthorized {
		t.Fatalf("expired link: %d", code)
	}
	tok := link(u.ID, time.Hour)
	var sess tokenResponse
	if code := h.do("POST", "/auth/link", "", map[string]string{"token": tok}, &sess); code != http.StatusOK || sess.AccessToken == "" {
		t.Fatalf("link: %d", code)
	}
	if code := h.do("POST", "/auth/link", "", map[string]string{"token": tok}, nil); code != http.StatusUnauthorized {
		t.Fatalf("second use: %d", code)
	}

	// Signed in by the link, the account sets a password without the current one, once.
	set := map[string]string{"current_password": "", "new_password": "a-new-pass-123"}
	if code := h.do("POST", "/auth/password", sess.AccessToken, set, nil); code != http.StatusNoContent {
		t.Fatalf("set password after link: %d", code)
	}
	if code := h.do("POST", "/auth/login", "", map[string]string{"username": "customer", "password": "a-new-pass-123"}, nil); code != http.StatusOK {
		t.Fatalf("sign in with the new password: %d", code)
	}
	if code := h.do("POST", "/auth/password", sess.AccessToken, set, nil); code != http.StatusUnauthorized {
		t.Fatalf("second password set without the current one: %d", code)
	}

	// Without a link sign-in the current password is required.
	h.mkUser("other", db.RoleCustomer, nil)
	o := h.login("other")
	if code := h.do("POST", "/auth/password", o.AccessToken, set, nil); code != http.StatusUnauthorized {
		t.Fatalf("no link: %d", code)
	}

	// Accounts with two-factor authentication aren't signed in by links.
	u2 := h.mkUser("with2fa", db.RoleCustomer, nil)
	u2.TOTPEnabled, u2.TOTPSecret = true, "JBSWY3DPEHPK3PXP"
	if err := h.db.UpdateUser(ctx, u2); err != nil {
		t.Fatal(err)
	}
	if code := h.do("POST", "/auth/link", "", map[string]string{"token": link(u2.ID, time.Hour)}, nil); code != http.StatusUnauthorized {
		t.Fatalf("2fa account: %d", code)
	}
}
