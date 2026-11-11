package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"

	"openplex.dev/openplex/internal/db"
)

// RBAC model:
//   admin    → every user and resource
//   reseller → itself and the customers it created (parent_id = reseller)
//   customer → itself only
// On top of that, an access grant lets a user work on one website (some of
// its areas, optionally read-only, aliases included) or one database owned by
// someone else. Deleting the resource itself stays with its owner.

// visibleOwnerIDs returns the user IDs whose resources u may access (nil = all).
func (s *Server) visibleOwnerIDs(ctx context.Context, u *db.User) ([]int64, error) {
	switch u.Role {
	case db.RoleAdmin:
		return nil, nil
	case db.RoleReseller:
		kids, err := s.db.ListUsers(ctx, db.UserFilter{ParentID: &u.ID})
		if err != nil {
			return nil, err
		}
		ids := []int64{u.ID}
		for _, k := range kids {
			ids = append(ids, k.ID)
		}
		return ids, nil
	default:
		return []int64{u.ID}, nil
	}
}

// canAccessOwner reports whether u may act on resources owned by ownerID.
func (s *Server) canAccessOwner(ctx context.Context, u *db.User, ownerID int64) (bool, error) {
	ids, err := s.visibleOwnerIDs(ctx, u)
	if err != nil {
		return false, err
	}
	return ids == nil || slices.Contains(ids, ownerID), nil
}

// canManageUser reports whether actor may view/modify target.
func canManageUser(actor, target *db.User) bool {
	switch actor.Role {
	case db.RoleAdmin:
		return true
	case db.RoleReseller:
		return target.ID == actor.ID || (target.ParentID != nil && *target.ParentID == actor.ID)
	default:
		return target.ID == actor.ID
	}
}

// loadDomain resolves {id} and enforces ownership; it writes the error response
// itself and returns nil when the caller must stop. Inaccessible domains are
// reported as 404 so IDs cannot be probed.
func (s *Server) loadDomain(w http.ResponseWriter, r *http.Request) *db.Domain {
	id, ok := idParam(w, r, "id")
	if !ok {
		return nil
	}
	d, err := s.db.GetDomain(r.Context(), id)
	if err != nil {
		s.fail(w, r, err)
		return nil
	}
	allowed, err := s.canAccessOwner(r.Context(), actor(r), d.OwnerID)
	if err != nil {
		s.fail(w, r, err)
		return nil
	}
	if allowed {
		return d
	}
	g, err := s.siteGrant(r.Context(), actor(r).ID, d)
	if err != nil {
		s.fail(w, r, err)
		return nil
	}
	if g == nil {
		writeErr(w, http.StatusNotFound, "not_found", "resource not found")
		return nil
	}
	area, write := siteArea(r)
	switch {
	case area == areaOwner:
		writeErr(w, http.StatusForbidden, "forbidden", "only the owner of this website can do this")
		return nil
	case area == areaShell && !(g.Has(db.AreaFiles) && g.Has(db.AreaSite)),
		area != "" && area != areaShell && !g.Has(area):
		writeErr(w, http.StatusForbidden, "forbidden", "you don't have access to this part of the website")
		return nil
	case write && g.ReadOnly:
		writeErr(w, http.StatusForbidden, "forbidden", "you have read-only access to this website")
		return nil
	}
	return d
}

// siteGrant is u's grant on website d, or on the website d is an alias of.
func (s *Server) siteGrant(ctx context.Context, userID int64, d *db.Domain) (*db.AccessGrant, error) {
	ids := []int64{d.ID}
	if d.Kind == db.KindAlias && d.ParentID != nil {
		ids = append(ids, *d.ParentID)
	}
	for _, id := range ids {
		g, err := s.db.FindGrant(ctx, userID, db.GrantSite, id)
		if err == nil {
			return g, nil
		}
		if !errors.Is(err, db.ErrNotFound) {
			return nil, err
		}
	}
	return nil, nil
}

// areaOwner marks requests only the owner may make through a grant (none).
const areaOwner = "owner"

// areaShell is the web terminal: a shell can change the files and the running
// app alike, so it needs both the files and the website settings areas.
const areaShell = "shell"

// siteArea maps a request under /api/v1/domains/{id} to the website area it
// touches ("" = any grant may see it) and whether it changes anything. The
// web terminal counts as a change: it is a shell.
func siteArea(r *http.Request) (area string, write bool) {
	write = r.Method != http.MethodGet && r.Method != http.MethodHead
	rest, ok := strings.CutPrefix(r.URL.Path, "/api/v1/domains/")
	if !ok {
		return areaOwner, write
	}
	parts := strings.SplitN(rest, "/", 3)
	seg := ""
	if len(parts) > 1 {
		seg = parts[1]
	}
	switch seg {
	case "":
		switch r.Method {
		case http.MethodDelete:
			return areaOwner, true
		case http.MethodGet, http.MethodHead:
			return "", false
		}
		return db.AreaSite, true
	case "stats":
		return "", write
	case "app":
		// Installing creates a database for the owner and fills the files.
		if !write {
			return "", false
		}
		return areaOwner, true
	case "actions", "logs", "redirects", "cron":
		return db.AreaSite, write
	case "files":
		return db.AreaFiles, write
	case "terminal":
		return areaShell, true
	case "dns":
		return db.AreaDNS, write
	case "mail":
		return db.AreaMail, write
	case "backups":
		return db.AreaBackups, write
	}
	return areaOwner, write
}

// loadManagedUser resolves {id} into a user the actor may manage.
func (s *Server) loadManagedUser(w http.ResponseWriter, r *http.Request) *db.User {
	id, ok := idParam(w, r, "id")
	if !ok {
		return nil
	}
	u, err := s.db.GetUser(r.Context(), id)
	if err != nil {
		s.fail(w, r, err)
		return nil
	}
	if !canManageUser(actor(r), u) {
		writeErr(w, http.StatusNotFound, "not_found", "resource not found")
		return nil
	}
	return u
}

// quotaReached says (as an error message) whether owner may not get another
// website (or database). A reseller's limit covers its customers too, so it
// can't be bypassed by creating customers without limits.
func (s *Server) quotaReached(ctx context.Context, owner *db.User, databases bool) (string, error) {
	check := []*db.User{owner}
	if owner.ParentID != nil {
		if p, err := s.db.GetUser(ctx, *owner.ParentID); err == nil && p.Role == db.RoleReseller {
			check = append(check, p)
		}
	}
	for _, u := range check {
		limit := u.MaxDomains
		if databases {
			limit = u.MaxDatabases
		}
		if limit <= 0 {
			continue
		}
		ids := []int64{u.ID}
		if u.Role == db.RoleReseller {
			var err error
			if ids, err = s.visibleOwnerIDs(ctx, u); err != nil {
				return "", err
			}
		}
		n := 0
		for _, id := range ids {
			var c int
			var err error
			if databases {
				c, err = s.db.CountDatabasesByOwner(ctx, id)
			} else {
				c, err = s.db.CountDomainsByOwner(ctx, id)
			}
			if err != nil {
				return "", err
			}
			n += c
		}
		if n < limit {
			continue
		}
		switch {
		case u.ID != owner.ID && databases:
			return fmt.Sprintf("the reseller's database quota of %d is reached", limit), nil
		case u.ID != owner.ID:
			return fmt.Sprintf("the reseller's website quota of %d is reached", limit), nil
		case databases:
			return fmt.Sprintf("database quota of %d reached", limit), nil
		default:
			return fmt.Sprintf("website quota of %d reached", limit), nil
		}
	}
	return "", nil
}
