package docker

import (
	"context"
	"strconv"
	"time"

	"openplex.dev/openplex/internal/db"
)

// Health messages are fixed strings so the dashboard can translate them.
const (
	MsgCrashLoop  = "The app keeps crashing and restarting. Check the website's logs."
	MsgExited     = "The app stopped unexpectedly. Check the website's logs, then restart it."
	healthyAfter  = 20 * time.Second
	healthTimeout = 10 * time.Second
)

// siteHealth classifies a site container: status and message, or ok=false
// when its state is still settling (just started).
func siteHealth(running, restarting bool, startedAt time.Time, now time.Time) (status, message string, ok bool) {
	switch {
	case restarting:
		return db.StatusError, MsgCrashLoop, true
	case !running:
		return db.StatusError, MsgExited, true
	case now.Sub(startedAt) >= healthyAfter:
		return db.StatusRunning, "", true
	default:
		return "", "", false
	}
}

// WatchHealth keeps each website's status in line with its container: a
// crash-looping or exited app shows as an error, and recovers to running on
// its own once it has stayed up for a while. It never touches websites that
// are being deployed, stopped or disabled.
func (o *Orchestrator) WatchHealth(ctx context.Context, every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			o.checkHealth(ctx)
		}
	}
}

func (o *Orchestrator) checkHealth(ctx context.Context) {
	ctx, cancel := context.WithTimeout(ctx, healthTimeout)
	defer cancel()
	list, err := o.listManaged(ctx, "site")
	if err != nil {
		return
	}
	now := time.Now()
	for _, c := range list {
		id, err := strconv.ParseInt(c.Labels[LabelDomainID], 10, 64)
		if err != nil {
			continue
		}
		insp, err := o.cli.ContainerInspect(ctx, c.ID)
		if err != nil || insp.State == nil {
			continue
		}
		started, _ := time.Parse(time.RFC3339Nano, insp.State.StartedAt)
		status, msg, ok := siteHealth(insp.State.Running, insp.State.Restarting, started, now)
		if !ok {
			continue
		}
		changed, err := o.db.SetDomainHealth(ctx, id, status, msg)
		if err == nil && changed {
			o.log.Info("website health changed", "domain_id", id, "status", status, "restarts", insp.RestartCount)
		}
	}
}
