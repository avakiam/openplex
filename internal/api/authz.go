package api

import (
	"context"
	"net/http"
	"slices"

	"openplex.dev/openplex/internal/db"
)

// RBAC model:
//   admin    → every user and resource
//   reseller → itself and the customers it created (parent_id = reseller)
//   customer → itself only

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
	if !allowed {
		writeErr(w, http.StatusNotFound, "not_found", "resource not found")
		return nil
	}
	return d
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
