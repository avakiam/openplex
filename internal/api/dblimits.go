package api

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"openplex.dev/openplex/internal/db"
	"openplex.dev/openplex/internal/msg"
)

// dbLimitEvery is how often database sizes are checked against their limits.
const dbLimitEvery = 5 * time.Minute

// limitable reports whether d's engine supports a size limit.
func limitable(d *db.Database) bool {
	return d.Engine == db.EngineMariaDB || d.Engine == db.EnginePostgres
}

// dbSizeCap checks a database size limit (MB) a can set: a reseller with a
// limit of its own cannot give more, nor take the limit away.
func dbSizeCap(a *db.User, mb int) *msg.Error {
	switch {
	case mb < 0:
		return msg.New("the database size limit cannot be negative")
	case mb > 1<<20:
		return msg.New("the database size limit is at most 1048576 MB")
	case a.Role == db.RoleReseller && a.MaxDBSizeMB > 0 && (mb == 0 || mb > a.MaxDBSizeMB):
		return msg.New("the database size limit must be between 1 and {limit} MB (your own limit)", "limit", a.MaxDBSizeMB)
	}
	return nil
}

// canLimitDatabase reports whether a may change the size limit of a database
// owned by owner: administrators, and resellers for their customers.
func canLimitDatabase(a, owner *db.User) bool {
	switch a.Role {
	case db.RoleAdmin:
		return true
	case db.RoleReseller:
		return owner.ParentID != nil && *owner.ParentID == a.ID
	}
	return false
}

func (s *Server) handleSetDatabaseLimit(w http.ResponseWriter, r *http.Request) {
	d := s.loadDatabase(w, r)
	if d == nil {
		return
	}
	owner, err := s.db.GetUser(r.Context(), d.OwnerID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	a := actor(r)
	if !canLimitDatabase(a, owner) {
		writeErr(w, http.StatusForbidden, "forbidden", "only an administrator, or the reseller of this account, can change the size limit")
		return
	}
	if !limitable(d) {
		badRequest(w, "Redis instances have a fixed memory limit")
		return
	}
	var in struct {
		MaxSizeMB int `json:"max_size_mb"`
	}
	if !decode(w, r, &in) {
		return
	}
	eff := in.MaxSizeMB
	if eff == 0 {
		eff = owner.MaxDBSizeMB
	}
	if m := dbSizeCap(a, eff); m != nil {
		writeMsg(w, http.StatusBadRequest, "bad_request", m)
		return
	}
	if err := s.db.SetDatabaseLimit(r.Context(), d.ID, in.MaxSizeMB); err != nil {
		s.fail(w, r, err)
		return
	}
	s.audit(r, "database.size_limit", d.Name, fmt.Sprintf("%d MB", in.MaxSizeMB))
	d.MaxSizeMB = in.MaxSizeMB
	s.kickDBLimits()
	writeJSON(w, http.StatusOK, s.viewOf(r, d))
}

// kickDBLimits asks for a check of the database limits soon.
func (s *Server) kickDBLimits() {
	select {
	case s.dbLimits <- struct{}{}:
	default:
	}
}

// RunDatabaseLimits checks the size of the databases with a limit every few
// minutes (and soon after a limit changes): a database over its limit becomes
// read-only for its user, and writable again once back under it.
func (s *Server) RunDatabaseLimits(ctx context.Context) {
	if s.orch == nil {
		return
	}
	t := time.NewTimer(time.Minute)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		case <-s.dbLimits:
			if !t.Stop() {
				select {
				case <-t.C:
				default:
				}
			}
		}
		if err := s.enforceDBLimits(ctx); err != nil && ctx.Err() == nil {
			s.log.Warn("database size limits", "err", err)
		}
		t.Reset(dbLimitEvery)
	}
}

func (s *Server) enforceDBLimits(ctx context.Context) error {
	all, err := s.db.ListDatabases(ctx, nil)
	if err != nil {
		return err
	}
	owners := map[int64]*db.User{}
	type item struct {
		d     *db.Database
		limit int
	}
	var items []item
	var check []*db.Database
	for _, d := range all {
		if !limitable(d) || d.Status != db.StatusRunning {
			continue
		}
		o, ok := owners[d.OwnerID]
		if !ok {
			o, _ = s.db.GetUser(ctx, d.OwnerID)
			owners[d.OwnerID] = o
		}
		limit := d.LimitMB(o)
		if limit == 0 && !d.SizeLocked {
			continue
		}
		if d.SizeLocked {
			// Deleted rows only lower the measured size once statistics are refreshed.
			if err := s.orch.AnalyzeDatabase(ctx, d); err != nil {
				s.log.Warn("analyze database", "database", d.Name, "err", err)
			}
		}
		items = append(items, item{d, limit})
		check = append(check, d)
	}
	if len(items) == 0 {
		return nil
	}
	sizes := s.orch.DatabaseSizes(ctx, check)
	for _, it := range items {
		size, ok := sizes[it.d.ID]
		if !ok {
			continue // the server did not answer: keep the current state
		}
		over := it.limit > 0 && size > int64(it.limit)<<20
		if over == it.d.SizeLocked {
			continue
		}
		if err := s.orch.SetDatabaseReadOnly(ctx, it.d, over); err != nil {
			s.log.Warn("database size limit", "database", it.d.Name, "err", err)
			continue
		}
		if err := s.db.SetDatabaseLocked(ctx, it.d.ID, over); err != nil {
			return err
		}
		s.log.Info("database size limit", "database", it.d.Name, "read_only", over, "size", size, "limit_mb", it.limit)
	}
	return nil
}
