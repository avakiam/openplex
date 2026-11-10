package api

import (
	"context"
	"net/http"
	"slices"

	"openplex.dev/openplex/internal/db"
)

// accessView tells the dashboard what a grant opens; absent = full access.
type accessView struct {
	Areas    []string `json:"areas"`
	ReadOnly bool     `json:"read_only"`
}

type domainView struct {
	*db.Domain
	Access *accessView `json:"access,omitempty"`
}

// visibleDomains are the domains u owns or manages, plus the websites (and
// their aliases) shared with u through grants.
func (s *Server) visibleDomains(ctx context.Context, u *db.User, kind string) ([]domainView, error) {
	ids, err := s.visibleOwnerIDs(ctx, u)
	if err != nil {
		return nil, err
	}
	own, err := s.db.ListDomains(ctx, db.DomainFilter{OwnerIDs: ids, Kind: kind})
	if err != nil {
		return nil, err
	}
	out := make([]domainView, 0, len(own))
	seen := map[int64]bool{}
	for _, d := range own {
		out = append(out, domainView{Domain: d})
		seen[d.ID] = true
	}
	if ids == nil {
		return out, nil // admin: everything already
	}
	grants, err := s.db.ListUserGrants(ctx, u.ID)
	if err != nil {
		return nil, err
	}
	shared := map[int64]*db.AccessGrant{}
	for _, g := range grants {
		if g.Kind == db.GrantSite {
			shared[g.ResourceID] = g
		}
	}
	if len(shared) == 0 {
		return out, nil
	}
	all, err := s.db.ListDomains(ctx, db.DomainFilter{Kind: kind})
	if err != nil {
		return nil, err
	}
	for _, d := range all {
		g := shared[d.ID]
		if g == nil && d.Kind == db.KindAlias && d.ParentID != nil {
			g = shared[*d.ParentID]
		}
		if g != nil && !seen[d.ID] {
			out = append(out, domainView{Domain: d, Access: &accessView{Areas: g.Areas, ReadOnly: g.ReadOnly}})
		}
	}
	slices.SortFunc(out, func(a, b domainView) int {
		switch {
		case a.Name < b.Name:
			return -1
		case a.Name > b.Name:
			return 1
		}
		return 0
	})
	return out, nil
}

// domainAccess is the access view of d for the caller (nil = full).
func (s *Server) domainAccess(r *http.Request, d *db.Domain) *accessView {
	if ok, err := s.canAccessOwner(r.Context(), actor(r), d.OwnerID); err != nil || ok {
		return nil
	}
	if g, err := s.siteGrant(r.Context(), actor(r).ID, d); err == nil && g != nil {
		return &accessView{Areas: g.Areas, ReadOnly: g.ReadOnly}
	}
	return nil
}

// visibleDatabases are u's own (or managed) databases plus the shared ones.
func (s *Server) visibleDatabases(ctx context.Context, u *db.User) ([]*db.Database, map[int64]bool, error) {
	ids, err := s.visibleOwnerIDs(ctx, u)
	if err != nil {
		return nil, nil, err
	}
	list, err := s.db.ListDatabases(ctx, ids)
	if err != nil || ids == nil {
		return list, nil, err
	}
	grants, err := s.db.ListUserGrants(ctx, u.ID)
	if err != nil {
		return nil, nil, err
	}
	shared := map[int64]bool{}
	for _, g := range grants {
		if g.Kind != db.GrantDatabase || slices.ContainsFunc(list, func(d *db.Database) bool { return d.ID == g.ResourceID }) {
			continue
		}
		d, err := s.db.GetDatabase(ctx, g.ResourceID)
		if err != nil {
			continue
		}
		list = append(list, d)
		shared[d.ID] = true
	}
	return list, shared, nil
}

// GET /users/{id}/access: what is shared with a user.
func (s *Server) handleListAccess(w http.ResponseWriter, r *http.Request) {
	u := s.loadManagedUser(w, r)
	if u == nil {
		return
	}
	gs, err := s.db.ListUserGrants(r.Context(), u.ID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, gs)
}

// PUT /users/{id}/access: share a website (some areas) or a database with a
// user. The caller must fully manage both the user and the resource.
func (s *Server) handlePutAccess(w http.ResponseWriter, r *http.Request) {
	u := s.loadManagedUser(w, r)
	if u == nil {
		return
	}
	var in struct {
		Kind       string   `json:"kind"`
		ResourceID int64    `json:"resource_id"`
		Areas      []string `json:"areas"`
		ReadOnly   bool     `json:"read_only"`
	}
	if !decode(w, r, &in) {
		return
	}
	if u.Role == db.RoleAdmin {
		badRequest(w, "administrators already have access to everything")
		return
	}
	if u.ID == actor(r).ID {
		badRequest(w, "you cannot share something with yourself")
		return
	}
	var owner int64
	switch in.Kind {
	case db.GrantSite:
		d, err := s.db.GetDomain(r.Context(), in.ResourceID)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		if d.Kind == db.KindAlias {
			badRequest(w, "share the website the alias belongs to")
			return
		}
		owner = d.OwnerID
		var areas []string
		for _, a := range db.SiteAreas { // known areas, in order, no repeats
			if slices.Contains(in.Areas, a) {
				areas = append(areas, a)
			}
		}
		if len(areas) == 0 || len(areas) != len(in.Areas) {
			badRequest(w, "choose what the user may manage on this website")
			return
		}
		in.Areas = areas
	case db.GrantDatabase:
		d, err := s.db.GetDatabase(r.Context(), in.ResourceID)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		owner, in.Areas, in.ReadOnly = d.OwnerID, nil, false
	default:
		badRequest(w, "kind must be site or database")
		return
	}
	if ok, err := s.canAccessOwner(r.Context(), actor(r), owner); err != nil || !ok {
		writeErr(w, http.StatusNotFound, "not_found", "resource not found")
		return
	}
	if owner == u.ID {
		badRequest(w, "this user already owns it")
		return
	}
	by := actor(r).ID
	g := &db.AccessGrant{UserID: u.ID, Kind: in.Kind, ResourceID: in.ResourceID, Areas: in.Areas, ReadOnly: in.ReadOnly, CreatedBy: &by}
	if err := s.db.SaveGrant(r.Context(), g); err != nil {
		s.fail(w, r, err)
		return
	}
	saved, err := s.db.GetGrant(r.Context(), g.ID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.audit(r, "access.granted", u.Username, saved.Kind+" "+saved.ResourceName)
	writeJSON(w, http.StatusOK, saved)
}

// DELETE /users/{id}/access/{gid}
func (s *Server) handleDeleteAccess(w http.ResponseWriter, r *http.Request) {
	u := s.loadManagedUser(w, r)
	if u == nil {
		return
	}
	gid, ok := idParam(w, r, "gid")
	if !ok {
		return
	}
	g, err := s.db.GetGrant(r.Context(), gid)
	if err != nil || g.UserID != u.ID {
		writeErr(w, http.StatusNotFound, "not_found", "resource not found")
		return
	}
	if ok, err := s.canAccessOwner(r.Context(), actor(r), g.ResourceOwner); err != nil || !ok {
		writeErr(w, http.StatusNotFound, "not_found", "resource not found")
		return
	}
	if err := s.db.DeleteGrant(r.Context(), g.ID); err != nil {
		s.fail(w, r, err)
		return
	}
	s.audit(r, "access.revoked", u.Username, g.Kind+" "+g.ResourceName)
	w.WriteHeader(http.StatusNoContent)
}
