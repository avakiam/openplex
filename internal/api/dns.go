package api

import (
	"errors"
	"net/http"
	"sync"

	"openplex.dev/openplex/internal/db"
	"openplex.dev/openplex/internal/dns"
)

type zoneView struct {
	*db.DNSZone
	Records    int            `json:"records"`
	Delegation dns.Delegation `json:"delegation"`
}

// handleGetDNS returns the DNS service status and every zone (admins).
func (s *Server) handleGetDNS(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	st, err := s.dns.Status(ctx)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	zones, err := s.db.ListDNSZones(ctx)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	out := make([]zoneView, len(zones))
	var wg sync.WaitGroup
	sem := make(chan struct{}, 8)
	for i, z := range zones {
		n, err := s.db.CountDNSRecords(ctx, z.ID)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		out[i] = zoneView{DNSZone: z, Records: n}
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			out[i].Delegation = s.dns.Delegation(ctx, z.Name)
		}()
	}
	wg.Wait()
	writeJSON(w, http.StatusOK, map[string]any{"status": st, "zones": out})
}

func (s *Server) handlePutDNSSettings(w http.ResponseWriter, r *http.Request) {
	var in db.DNSSettings
	if !decode(w, r, &in) {
		return
	}
	if err := s.dns.UpdateSettings(r.Context(), in); err != nil {
		badRequestErr(w, err)
		return
	}
	s.audit(r, "dns.settings_updated", "dns", in.NS1+" "+in.NS2)
	st, err := s.dns.Status(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, st)
}

// loadZone resolves the website in the URL to its own zone.
func (s *Server) loadZone(w http.ResponseWriter, r *http.Request) (*db.Domain, *db.DNSZone) {
	d := s.loadDomain(w, r)
	if d == nil {
		return nil, nil
	}
	if d.Kind == db.KindSubdomain {
		writeErr(w, http.StatusBadRequest, "parent_zone", "a subdomain's records are in the DNS zone of its main domain")
		return nil, nil
	}
	z, err := s.db.GetDNSZoneByDomain(r.Context(), d.ID)
	if errors.Is(err, db.ErrNotFound) {
		if err = s.dns.EnsureZones(r.Context()); err == nil {
			z, err = s.db.GetDNSZoneByDomain(r.Context(), d.ID)
		}
	}
	if err != nil {
		s.fail(w, r, err)
		return nil, nil
	}
	return d, z
}

func (s *Server) handleGetZone(w http.ResponseWriter, r *http.Request) {
	_, z := s.loadZone(w, r)
	if z == nil {
		return
	}
	recs, err := s.db.ListDNSRecords(r.Context(), z.ID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	st, err := s.dns.Status(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"zone": z, "records": recs, "delegation": s.dns.Delegation(r.Context(), z.Name),
		"nameservers": []string{st.NS1, st.NS2}, "server_enabled": st.Enabled, "server_running": st.Running,
		"types": dns.Types, "max_records": dns.MaxRecordsPerZone,
	})
}

type recordInput struct {
	Name     string `json:"name"`
	Type     string `json:"type"`
	Content  string `json:"content"`
	Priority int    `json:"priority"`
	TTL      int    `json:"ttl"`
}

// saveRecord validates r against its zone and stores it (create when ID is 0).
func (s *Server) saveRecord(w http.ResponseWriter, r *http.Request, d *db.Domain, z *db.DNSZone, rec *db.DNSRecord) bool {
	if err := dns.NormalizeRecord(rec, z.Name); err != nil {
		badRequestErr(w, err)
		return false
	}
	others, err := s.db.ListDNSRecords(r.Context(), z.ID)
	if err != nil {
		s.fail(w, r, err)
		return false
	}
	if err := dns.CheckConflicts(*rec, others); err != nil {
		badRequestErr(w, err)
		return false
	}
	if rec.ID == 0 {
		if len(others) >= dns.MaxRecordsPerZone {
			writeErr(w, http.StatusForbidden, "quota_exceeded", "this zone already has the maximum of 500 records")
			return false
		}
		rec.ZoneID = z.ID
		recs := []db.DNSRecord{*rec}
		if err := s.db.CreateDNSRecords(r.Context(), recs); err != nil {
			s.fail(w, r, err)
			return false
		}
		*rec = recs[0]
	} else if err := s.db.UpdateDNSRecord(r.Context(), rec); err != nil {
		s.fail(w, r, err)
		return false
	}
	_ = s.db.TouchDNSZone(r.Context(), z.ID)
	s.dns.Changed()
	s.applyEdgeAsync() // a www record decides whether the edge serves www.<domain>
	return true
}

func (s *Server) handleCreateRecord(w http.ResponseWriter, r *http.Request) {
	d, z := s.loadZone(w, r)
	if z == nil {
		return
	}
	var in recordInput
	if !decode(w, r, &in) {
		return
	}
	rec := &db.DNSRecord{Name: in.Name, Type: in.Type, Content: in.Content, Priority: in.Priority, TTL: in.TTL}
	if !s.saveRecord(w, r, d, z, rec) {
		return
	}
	s.audit(r, "dns.record_created", z.Name, rec.Name+" "+rec.Type+" "+rec.Content)
	s.emit("dns.changed", map[string]any{"domain_id": z.DomainID, "zone": z.Name, "change": "created", "record_id": rec.ID})
	writeJSON(w, http.StatusCreated, rec)
}

func (s *Server) loadRecord(w http.ResponseWriter, r *http.Request) (*db.Domain, *db.DNSZone, *db.DNSRecord) {
	d, z := s.loadZone(w, r)
	if z == nil {
		return nil, nil, nil
	}
	rid, ok := idParam(w, r, "rid")
	if !ok {
		return nil, nil, nil
	}
	rec, err := s.db.GetDNSRecord(r.Context(), rid)
	if err != nil || rec.ZoneID != z.ID {
		writeErr(w, http.StatusNotFound, "not_found", "resource not found")
		return nil, nil, nil
	}
	return d, z, rec
}

func (s *Server) handleUpdateRecord(w http.ResponseWriter, r *http.Request) {
	d, z, rec := s.loadRecord(w, r)
	if rec == nil {
		return
	}
	var in recordInput
	if !decode(w, r, &in) {
		return
	}
	rec.Name, rec.Type, rec.Content, rec.Priority, rec.TTL = in.Name, in.Type, in.Content, in.Priority, in.TTL
	if !s.saveRecord(w, r, d, z, rec) {
		return
	}
	s.audit(r, "dns.record_updated", z.Name, rec.Name+" "+rec.Type+" "+rec.Content)
	s.emit("dns.changed", map[string]any{"domain_id": z.DomainID, "zone": z.Name, "change": "updated", "record_id": rec.ID})
	writeJSON(w, http.StatusOK, rec)
}

func (s *Server) handleDeleteRecord(w http.ResponseWriter, r *http.Request) {
	_, z, rec := s.loadRecord(w, r)
	if rec == nil {
		return
	}
	if err := s.db.DeleteDNSRecord(r.Context(), rec.ID); err != nil {
		s.fail(w, r, err)
		return
	}
	_ = s.db.TouchDNSZone(r.Context(), z.ID)
	s.dns.Changed()
	s.applyEdgeAsync() // a www record decides whether the edge serves www.<domain>
	s.audit(r, "dns.record_deleted", z.Name, rec.Name+" "+rec.Type+" "+rec.Content)
	s.emit("dns.changed", map[string]any{"domain_id": z.DomainID, "zone": z.Name, "change": "deleted", "record_id": rec.ID})
	w.WriteHeader(http.StatusNoContent)
}
