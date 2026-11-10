package api

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strings"

	"openplex.dev/openplex/internal/db"
	"openplex.dev/openplex/internal/mail"
)

// handleGetMail returns the mail service status and every mail domain (admins).
func (s *Server) handleGetMail(w http.ResponseWriter, r *http.Request) {
	st, err := s.mail.Status(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	doms, err := s.db.ListMailDomains(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	type row struct {
		*db.MailDomain
		Mailboxes int `json:"mailboxes"`
		Aliases   int `json:"aliases"`
	}
	out := make([]row, len(doms))
	for i, d := range doms {
		mbs, _ := s.db.ListMailboxes(r.Context(), d.ID)
		als, _ := s.db.ListMailAliases(r.Context(), d.ID)
		out[i] = row{MailDomain: d, Mailboxes: len(mbs), Aliases: len(als)}
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": st, "domains": out})
}

func (s *Server) handlePutMailSettings(w http.ResponseWriter, r *http.Request) {
	var in db.MailSettings
	if !decode(w, r, &in) {
		return
	}
	if err := s.mail.UpdateSettings(r.Context(), in); err != nil {
		badRequest(w, err.Error())
		return
	}
	s.audit(r, "mail.settings_updated", "mail", in.Hostname)
	st, err := s.mail.Status(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, st)
}

// loadMailDomain resolves the website in the URL and its mail domain (nil
// when mail is off for it).
func (s *Server) loadMailDomain(w http.ResponseWriter, r *http.Request) (*db.Domain, *db.MailDomain, bool) {
	d := s.loadDomain(w, r)
	if d == nil {
		return nil, nil, false
	}
	md, err := s.db.GetMailDomainByDomain(r.Context(), d.ID)
	if errors.Is(err, db.ErrNotFound) {
		return d, nil, true
	}
	if err != nil {
		s.fail(w, r, err)
		return nil, nil, false
	}
	return d, md, true
}

func (s *Server) requireMailDomain(w http.ResponseWriter, r *http.Request) (*db.Domain, *db.MailDomain) {
	d, md, ok := s.loadMailDomain(w, r)
	if !ok {
		return nil, nil
	}
	if md == nil {
		writeErr(w, http.StatusConflict, "mail_off", "mail is not enabled for this domain")
		return nil, nil
	}
	return d, md
}

type mailboxView struct {
	*db.Mailbox
	Address string  `json:"address"`
	UsedMB  float64 `json:"used_mb"`
}

func (s *Server) handleGetDomainMail(w http.ResponseWriter, r *http.Request) {
	d, md, ok := s.loadMailDomain(w, r)
	if !ok {
		return
	}
	st, err := s.db.GetMailSettings(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	out := map[string]any{"enabled": md != nil, "domain": d.Name, "hostname": st.Hostname, "server_enabled": st.Enabled,
		"webmail_url":   mail.WebmailURL(st),
		"max_mailboxes": mail.MaxMailboxesPerDomain, "mailboxes": []mailboxView{}, "aliases": []*db.MailAlias{}}
	if md != nil {
		mbs, err := s.db.ListMailboxes(r.Context(), md.ID)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		usage := s.mail.Usage(r.Context())
		views := make([]mailboxView, len(mbs))
		for i, b := range mbs {
			addr := b.LocalPart + "@" + md.Name
			views[i] = mailboxView{Mailbox: b, Address: addr, UsedMB: usage[addr]}
		}
		als, err := s.db.ListMailAliases(r.Context(), md.ID)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		out["mailboxes"], out["aliases"] = views, als
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleEnableMail(w http.ResponseWriter, r *http.Request) {
	d, md, ok := s.loadMailDomain(w, r)
	if !ok {
		return
	}
	if md != nil {
		writeErr(w, http.StatusConflict, "conflict", "mail is already enabled for this domain")
		return
	}
	md, err := s.mail.EnableDomain(r.Context(), d)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.applyEdgeAsync()
	s.audit(r, "mail.enabled", d.Name, "")
	writeJSON(w, http.StatusCreated, md)
}

func (s *Server) handleDisableMail(w http.ResponseWriter, r *http.Request) {
	d, md := s.requireMailDomain(w, r)
	if md == nil {
		return
	}
	if err := s.mail.DisableDomain(r.Context(), md); err != nil {
		s.fail(w, r, err)
		return
	}
	s.applyEdgeAsync()
	s.audit(r, "mail.disabled", d.Name, "")
	w.WriteHeader(http.StatusNoContent)
}

// addressTaken reports whether local is already a mailbox or an alias.
func (s *Server) addressTaken(r *http.Request, md *db.MailDomain, local string, skipAlias int64) (bool, error) {
	mbs, err := s.db.ListMailboxes(r.Context(), md.ID)
	if err != nil {
		return false, err
	}
	als, err := s.db.ListMailAliases(r.Context(), md.ID)
	if err != nil {
		return false, err
	}
	return slices.ContainsFunc(mbs, func(b *db.Mailbox) bool { return b.LocalPart == local }) ||
		slices.ContainsFunc(als, func(a *db.MailAlias) bool { return a.LocalPart == local && a.ID != skipAlias }), nil
}

func (s *Server) handleCreateMailbox(w http.ResponseWriter, r *http.Request) {
	d, md := s.requireMailDomain(w, r)
	if md == nil {
		return
	}
	var in struct {
		LocalPart string `json:"local_part"`
		Password  string `json:"password"`
		QuotaMB   int    `json:"quota_mb"`
	}
	if !decode(w, r, &in) {
		return
	}
	local, err := mail.NormalizeLocal(in.LocalPart)
	if err != nil {
		badRequest(w, err.Error())
		return
	}
	if err := mail.CheckPassword(in.Password); err != nil {
		badRequest(w, err.Error())
		return
	}
	if in.QuotaMB == 0 {
		in.QuotaMB = mail.DefaultQuotaMB
	}
	if in.QuotaMB < 10 || in.QuotaMB > mail.MaxQuotaMB {
		badRequest(w, "the mailbox size must be between 10 MB and 100 GB")
		return
	}
	mbs, err := s.db.ListMailboxes(r.Context(), md.ID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if len(mbs) >= mail.MaxMailboxesPerDomain {
		writeErr(w, http.StatusForbidden, "quota_exceeded", "this domain already has the maximum of 200 mailboxes")
		return
	}
	if taken, err := s.addressTaken(r, md, local, 0); err != nil || taken {
		if err != nil {
			s.fail(w, r, err)
			return
		}
		writeErr(w, http.StatusConflict, "conflict", "this address already exists")
		return
	}
	b := &db.Mailbox{MailDomainID: md.ID, LocalPart: local, PasswordHash: mail.HashPassword(in.Password), QuotaMB: in.QuotaMB, Enabled: true}
	if err := s.db.CreateMailbox(r.Context(), b); err != nil {
		s.fail(w, r, err)
		return
	}
	s.mail.Changed()
	s.audit(r, "mail.mailbox_created", local+"@"+d.Name, "")
	writeJSON(w, http.StatusCreated, mailboxView{Mailbox: b, Address: local + "@" + d.Name})
}

func (s *Server) loadMailbox(w http.ResponseWriter, r *http.Request) (*db.MailDomain, *db.Mailbox) {
	_, md := s.requireMailDomain(w, r)
	if md == nil {
		return nil, nil
	}
	id, ok := idParam(w, r, "mid")
	if !ok {
		return nil, nil
	}
	b, err := s.db.GetMailbox(r.Context(), id)
	if err != nil || b.MailDomainID != md.ID {
		writeErr(w, http.StatusNotFound, "not_found", "resource not found")
		return nil, nil
	}
	return md, b
}

func (s *Server) handleUpdateMailbox(w http.ResponseWriter, r *http.Request) {
	md, b := s.loadMailbox(w, r)
	if b == nil {
		return
	}
	var in struct {
		Password *string `json:"password"`
		QuotaMB  *int    `json:"quota_mb"`
		Enabled  *bool   `json:"enabled"`
	}
	if !decode(w, r, &in) {
		return
	}
	detail := ""
	if in.Password != nil {
		if err := mail.CheckPassword(*in.Password); err != nil {
			badRequest(w, err.Error())
			return
		}
		b.PasswordHash = mail.HashPassword(*in.Password)
		detail = "password changed"
	}
	if in.QuotaMB != nil {
		if *in.QuotaMB < 10 || *in.QuotaMB > mail.MaxQuotaMB {
			badRequest(w, "the mailbox size must be between 10 MB and 100 GB")
			return
		}
		b.QuotaMB = *in.QuotaMB
	}
	if in.Enabled != nil {
		b.Enabled = *in.Enabled
	}
	if err := s.db.UpdateMailbox(r.Context(), b); err != nil {
		s.fail(w, r, err)
		return
	}
	s.mail.Changed()
	s.audit(r, "mail.mailbox_updated", b.LocalPart+"@"+md.Name, detail)
	writeJSON(w, http.StatusOK, mailboxView{Mailbox: b, Address: b.LocalPart + "@" + md.Name})
}

func (s *Server) handleDeleteMailbox(w http.ResponseWriter, r *http.Request) {
	md, b := s.loadMailbox(w, r)
	if b == nil {
		return
	}
	if err := s.db.DeleteMailbox(r.Context(), b.ID); err != nil {
		s.fail(w, r, err)
		return
	}
	s.audit(r, "mail.mailbox_deleted", b.LocalPart+"@"+md.Name, "")
	s.background(func(ctx context.Context) error { s.mail.MailboxDeleted(ctx, md.Name, b.LocalPart); return nil })
	w.WriteHeader(http.StatusNoContent)
}

type aliasInput struct {
	LocalPart    string   `json:"local_part"`
	Destinations []string `json:"destinations"`
}

func normalizeDestinations(in []string) ([]string, error) {
	if len(in) == 0 || len(in) > mail.MaxDestinations {
		return nil, errors.New("enter between 1 and 50 destination addresses")
	}
	out := make([]string, 0, len(in))
	for _, d := range in {
		a, err := mail.NormalizeAddress(d)
		if err != nil {
			return nil, err
		}
		if !slices.Contains(out, a) {
			out = append(out, a)
		}
	}
	return out, nil
}

func (s *Server) handleCreateAlias(w http.ResponseWriter, r *http.Request) {
	d, md := s.requireMailDomain(w, r)
	if md == nil {
		return
	}
	var in aliasInput
	if !decode(w, r, &in) {
		return
	}
	local, err := mail.NormalizeLocal(in.LocalPart)
	if err != nil {
		badRequest(w, err.Error())
		return
	}
	dests, err := normalizeDestinations(in.Destinations)
	if err != nil {
		badRequest(w, err.Error())
		return
	}
	if slices.Contains(dests, local+"@"+d.Name) {
		badRequest(w, "an address cannot forward to itself")
		return
	}
	als, err := s.db.ListMailAliases(r.Context(), md.ID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if len(als) >= mail.MaxAliasesPerDomain {
		writeErr(w, http.StatusForbidden, "quota_exceeded", "this domain already has the maximum of 200 aliases")
		return
	}
	if taken, err := s.addressTaken(r, md, local, 0); err != nil || taken {
		if err != nil {
			s.fail(w, r, err)
			return
		}
		writeErr(w, http.StatusConflict, "conflict", "this address already exists")
		return
	}
	a := &db.MailAlias{MailDomainID: md.ID, LocalPart: local, Destinations: dests}
	if err := s.db.CreateMailAlias(r.Context(), a); err != nil {
		s.fail(w, r, err)
		return
	}
	s.mail.Changed()
	s.audit(r, "mail.alias_created", local+"@"+d.Name, strings.Join(dests, ", "))
	writeJSON(w, http.StatusCreated, a)
}

func (s *Server) loadAlias(w http.ResponseWriter, r *http.Request) (*db.Domain, *db.MailDomain, *db.MailAlias) {
	d, md := s.requireMailDomain(w, r)
	if md == nil {
		return nil, nil, nil
	}
	id, ok := idParam(w, r, "aid")
	if !ok {
		return nil, nil, nil
	}
	a, err := s.db.GetMailAlias(r.Context(), id)
	if err != nil || a.MailDomainID != md.ID {
		writeErr(w, http.StatusNotFound, "not_found", "resource not found")
		return nil, nil, nil
	}
	return d, md, a
}

func (s *Server) handleUpdateAlias(w http.ResponseWriter, r *http.Request) {
	d, _, a := s.loadAlias(w, r)
	if a == nil {
		return
	}
	var in aliasInput
	if !decode(w, r, &in) {
		return
	}
	dests, err := normalizeDestinations(in.Destinations)
	if err != nil {
		badRequest(w, err.Error())
		return
	}
	if slices.Contains(dests, a.LocalPart+"@"+d.Name) {
		badRequest(w, "an address cannot forward to itself")
		return
	}
	a.Destinations = dests
	if err := s.db.UpdateMailAlias(r.Context(), a); err != nil {
		s.fail(w, r, err)
		return
	}
	s.mail.Changed()
	s.audit(r, "mail.alias_updated", a.LocalPart+"@"+d.Name, strings.Join(dests, ", "))
	writeJSON(w, http.StatusOK, a)
}

func (s *Server) handleDeleteAlias(w http.ResponseWriter, r *http.Request) {
	d, _, a := s.loadAlias(w, r)
	if a == nil {
		return
	}
	if err := s.db.DeleteMailAlias(r.Context(), a.ID); err != nil {
		s.fail(w, r, err)
		return
	}
	s.mail.Changed()
	s.audit(r, "mail.alias_deleted", a.LocalPart+"@"+d.Name, "")
	w.WriteHeader(http.StatusNoContent)
}

// POST /domains/{id}/mail/mailboxes/{mid}/webmail: a one-click webmail link
// (single use, valid for a couple of minutes) for a mailbox the caller owns.
func (s *Server) handleWebmailLogin(w http.ResponseWriter, r *http.Request) {
	md, b := s.loadMailbox(w, r)
	if b == nil {
		return
	}
	if !b.Enabled {
		badRequest(w, "turn the mailbox on to open its webmail")
		return
	}
	addr := b.LocalPart + "@" + md.Name
	url, err := s.mail.WebmailLogin(r.Context(), addr)
	if err != nil {
		badRequest(w, err.Error())
		return
	}
	s.audit(r, "mail.webmail_login", addr, "")
	writeJSON(w, http.StatusOK, map[string]string{"url": url})
}
