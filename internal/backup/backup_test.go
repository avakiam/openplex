package backup

import (
	"testing"
	"time"

	"openplex.dev/openplex/internal/config"
	"openplex.dev/openplex/internal/db"
)

func TestDue(t *testing.T) {
	loc := time.FixedZone("CET", 3600)
	at := func(h, m int) time.Time { return time.Date(2026, 11, 6, h, m, 0, 0, loc) }
	ptr := func(t time.Time) *time.Time { return &t }
	cases := []struct {
		now  time.Time
		last *time.Time
		want bool
	}{
		{at(2, 59), nil, false},                              // before the slot
		{at(3, 0), nil, true},                                // at the slot, never ran
		{at(9, 0), ptr(at(3, 1)), false},                     // already ran today
		{at(9, 0), ptr(at(3, 0).Add(-24 * time.Hour)), true}, // last ran yesterday (missed today)
	}
	for i, c := range cases {
		if got := Due("03:00", c.last, c.now); got != c.want {
			t.Errorf("case %d: Due=%v want %v", i, got, c.want)
		}
	}
	if Due("25:00", nil, at(12, 0)) {
		t.Error("invalid time must never be due")
	}
}

func TestValidate(t *testing.T) {
	m := &Manager{cfg: &config.Config{DataDir: "/var/lib/openplex"}}
	ok := db.BackupSettings{Target: "local", ScheduleTime: "03:00", KeepDaily: 7}
	if err := m.Validate(&ok); err != nil {
		t.Fatal(err)
	}
	bad := []db.BackupSettings{
		{Target: "local", LocalPath: "relative/path", ScheduleTime: "03:00", KeepDaily: 1},
		{Target: "local", LocalPath: "/var/lib/openplex/sites/x", ScheduleTime: "03:00", KeepDaily: 1},
		{Target: "local", ScheduleTime: "3am", KeepDaily: 1},
		{Target: "local", ScheduleTime: "03:00"},
		{Target: "s3", S3Endpoint: "ftp://x", S3Bucket: "b-ok", S3AccessKey: "a", S3SecretEnc: "s", ScheduleTime: "03:00", KeepDaily: 1},
		{Target: "s3", S3Endpoint: "https://s3.example.com", S3Bucket: "Bad_Bucket", S3AccessKey: "a", S3SecretEnc: "s", ScheduleTime: "03:00", KeepDaily: 1},
		{Target: "s3", S3Endpoint: "https://s3.example.com", S3Bucket: "bucket", S3Prefix: "../x", S3AccessKey: "a", S3SecretEnc: "s", ScheduleTime: "03:00", KeepDaily: 1},
		{Target: "nfs", ScheduleTime: "03:00", KeepDaily: 1},
	}
	for i := range bad {
		if err := m.Validate(&bad[i]); err == nil {
			t.Errorf("case %d accepted: %+v", i, bad[i])
		}
	}
}
