package cron

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"openplex.dev/openplex/internal/db"
)

func TestParse(t *testing.T) {
	cases := []struct {
		expr string
		ok   bool
	}{
		{"*/5 * * * *", true},
		{"0 3 * * 1-5", true},
		{"@daily", true},
		{"@hourly", true},
		{"* * * *", false},     // 4 fields
		{"0 0 * * * *", false}, // seconds field not allowed
		{"@every 1s", false},   // sub-minute schedules
		{"CRON_TZ=UTC * * * * *", false},
		{"61 * * * *", false},
		{"", false},
	}
	for _, c := range cases {
		if _, err := Parse(c.expr); (err == nil) != c.ok {
			t.Errorf("Parse(%q) err=%v, want ok=%v", c.expr, err, c.ok)
		}
	}
	from := time.Date(2026, 11, 10, 10, 2, 0, 0, time.Local)
	next, err := NextRuns("*/15 * * * *", from, 3)
	if err != nil || len(next) != 3 || next[0].Minute() != 15 || next[2].Minute() != 45 {
		t.Fatalf("NextRuns = %v, %v", next, err)
	}
}

func TestTail(t *testing.T) {
	tl := &tail{max: 10}
	_, _ = tl.Write([]byte("hello "))
	_, _ = tl.Write([]byte("world, long line"))
	if got := tl.String(); !strings.HasSuffix(got, " long line") || !strings.HasPrefix(got, "[…") {
		t.Fatalf("tail = %q", got)
	}
}

type fakeRunner struct {
	out   string
	code  int
	block bool
}

func (f fakeRunner) RunSiteCommand(ctx context.Context, d *db.Domain, cmd string, w io.Writer) (int, error) {
	_, _ = io.WriteString(w, f.out+cmd)
	if f.block {
		<-ctx.Done()
		return -1, ctx.Err()
	}
	return f.code, nil
}

func setup(t *testing.T, status string) (*db.DB, *db.CronJob) {
	t.Helper()
	ctx := context.Background()
	store := db.OpenMemory(t)
	u := &db.User{Username: "owner", PasswordHash: "x", Role: db.RoleCustomer}
	if err := store.CreateUser(ctx, u); err != nil {
		t.Fatal(err)
	}
	d := &db.Domain{OwnerID: u.ID, Name: "cron.test", Kind: db.KindPrimary, Runtime: "php", Status: status}
	if err := store.CreateDomain(ctx, d); err != nil {
		t.Fatal(err)
	}
	j := &db.CronJob{DomainID: d.ID, Schedule: "* * * * *", Command: "echo hi", Enabled: true, TimeoutSec: MinTimeout}
	if err := store.CreateCronJob(ctx, j); err != nil {
		t.Fatal(err)
	}
	return store, j
}

func lastRun(t *testing.T, store *db.DB, jobID int64) *db.CronRun {
	t.Helper()
	runs, err := store.ListCronRuns(context.Background(), jobID, 10)
	if err != nil || len(runs) == 0 {
		t.Fatalf("runs: %v %v", runs, err)
	}
	return runs[0]
}

func TestExecute(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	cases := []struct {
		name, domainStatus string
		runner             Runner
		status, output     string
	}{
		{"success", db.StatusRunning, fakeRunner{out: "ran: "}, db.CronSuccess, "ran: echo hi"},
		{"non-zero exit", db.StatusRunning, fakeRunner{code: 2}, db.CronFailed, "echo hi"},
		{"stopped site", db.StatusStopped, fakeRunner{}, db.CronSkipped, "stopped"},
		{"no docker", db.StatusRunning, nil, db.CronFailed, "Docker"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			store, j := setup(t, c.domainStatus)
			m := New(store, c.runner, log)
			m.execute(j, "manual")
			r := lastRun(t, store, j.ID)
			if r.Status != c.status || !strings.Contains(r.Output, c.output) || r.FinishedAt == nil {
				t.Errorf("run = %+v", r)
			}
			got, _ := store.GetCronJob(context.Background(), j.ID)
			if got.LastStatus != c.status || got.LastRunAt == nil {
				t.Errorf("job summary = %q %v", got.LastStatus, got.LastRunAt)
			}
		})
	}
}

func TestTimeoutAndOverlap(t *testing.T) {
	if testing.Short() {
		t.Skip("waits for the minimum timeout")
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	store, j := setup(t, db.StatusRunning)
	m := New(store, fakeRunner{block: true}, log)
	if err := m.RunNow(j); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for !m.Running(j.ID) && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if err := m.RunNow(j); err != ErrBusy {
		t.Fatalf("overlapping run: %v", err)
	}
	m.wg.Wait()
	if r := lastRun(t, store, j.ID); r.Status != db.CronTimeout {
		t.Fatalf("status = %s", r.Status)
	}
}

func TestTickSchedulesAndSkipsMissed(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	store, j := setup(t, db.StatusRunning)
	ctx := context.Background()
	now := time.Now()
	old := now.Add(-time.Hour)
	_ = store.SetCronNextRun(ctx, j.ID, &old)
	m := New(store, fakeRunner{}, log)
	m.tick(ctx, now)
	m.wg.Wait()
	got, _ := store.GetCronJob(ctx, j.ID)
	if got.NextRunAt == nil || !got.NextRunAt.After(now) {
		t.Fatalf("next run not moved forward: %v", got.NextRunAt)
	}
	if runs, _ := store.ListCronRuns(ctx, j.ID, 10); len(runs) != 0 {
		t.Fatalf("missed run executed: %d runs", len(runs))
	}
	due := now.Add(-30 * time.Second)
	_ = store.SetCronNextRun(ctx, j.ID, &due)
	m.tick(ctx, now)
	m.wg.Wait()
	if r := lastRun(t, store, j.ID); r.Status != db.CronSuccess || r.Trigger != "schedule" {
		t.Fatalf("due run = %+v", r)
	}
}
