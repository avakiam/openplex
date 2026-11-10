package docker

import (
	"testing"
	"time"

	"openplex.dev/openplex/internal/db"
)

func TestSiteHealth(t *testing.T) {
	now := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name                string
		running, restarting bool
		started             time.Duration // ago
		status, msg         string
		ok                  bool
	}{
		{"crash loop", false, true, time.Second, db.StatusError, MsgCrashLoop, true},
		{"exited", false, false, time.Hour, db.StatusError, MsgExited, true},
		{"just started", true, false, 5 * time.Second, "", "", false},
		{"stable", true, false, time.Minute, db.StatusRunning, "", true},
	}
	for _, c := range cases {
		s, m, ok := siteHealth(c.running, c.restarting, now.Add(-c.started), now)
		if s != c.status || m != c.msg || ok != c.ok {
			t.Errorf("%s: got (%q, %q, %v)", c.name, s, m, ok)
		}
	}
}
