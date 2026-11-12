package api

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"strings"

	"openplex.dev/openplex/internal/apps"
	"openplex.dev/openplex/internal/db"
)

// siteChecks are the security checks of a website's own settings: HTTPS, a
// supported PHP version, PHP errors hidden from visitors.
func (s *Server) siteChecks(ctx context.Context, d *db.Domain) []apps.SecurityCheck {
	checks := []apps.SecurityCheck{apps.NewCheck("site_https", d.SSLEnabled && d.ForceHTTPS, "")}
	if d.Runtime == "php" {
		checks = append(checks, apps.NewCheck("site_php_eol", !slices.Contains(apps.UnsupportedPHP, d.RuntimeVersion), d.RuntimeVersion))
		display := ""
		if php, err := s.db.GetPHPSettings(ctx, d.ID); err == nil {
			display = strings.ToLower(php["display_errors"])
		}
		checks = append(checks, apps.NewCheck("site_display_errors", display != "on" && display != "1", ""))
	}
	return checks
}

// siteHosts lists the names a website answers to: its own, then its aliases'.
func (s *Server) siteHosts(ctx context.Context, d *db.Domain) ([]string, error) {
	hosts := []string{d.Name}
	aliases, err := s.db.ListDomains(ctx, db.DomainFilter{ParentID: &d.ID, Kind: db.KindAlias})
	if err != nil {
		return nil, err
	}
	for _, a := range aliases {
		hosts = append(hosts, a.Name)
	}
	return hosts, nil
}

// handleHardenSiteApp applies security fixes to the application of a website,
// then checks it again.
func (s *Server) handleHardenSiteApp(w http.ResponseWriter, r *http.Request) {
	d := s.loadDomain(w, r)
	if d == nil {
		return
	}
	var in struct {
		Fixes []string `json:"fixes"`
	}
	if !decode(w, r, &in) || !s.requireOrch(w) {
		return
	}
	if cur, err := s.db.GetSiteApp(r.Context(), d.ID); err == nil {
		if app, ok := apps.Get(cur.App); ok {
			if err := app.ValidFixes(in.Fixes); err != nil {
				badRequestErr(w, err)
				return
			}
		}
	}
	a, app, ok := s.startAppTask(w, r, d, db.AppTaskSecuring)
	if !ok {
		return
	}
	hosts, err := s.siteHosts(r.Context(), d)
	if err != nil {
		_ = s.db.EndSiteAppTask(context.WithoutCancel(r.Context()), d.ID, "")
		s.fail(w, r, err)
		return
	}
	fixes := slices.Compact(slices.Sorted(slices.Values(in.Fixes)))
	s.audit(r, "app.harden_started", d.Name, strings.Join(fixes, " "))
	site, by := *d, &actor(r).ID
	s.bg.Add(1)
	go func() {
		defer s.bg.Done()
		ctx, cancel := context.WithTimeout(s.baseCtx, appCheckTimeout)
		defer cancel()
		s.hardenApp(ctx, &site, app, hosts, fixes, by)
	}()
	writeJSON(w, http.StatusAccepted, a)
}

func (s *Server) hardenApp(ctx context.Context, d *db.Domain, app apps.App, hosts, fixes []string, by *int64) {
	bg := context.WithoutCancel(ctx)
	err := func() error {
		script, err := app.HardenScript()
		if err != nil {
			return err
		}
		out := &tailBuffer{max: 64 << 10}
		code, err := s.orch.RunSiteScript(ctx, d, script, apps.HardenEnv(appDocroot(d), hosts, fixes), out)
		if err != nil {
			return err
		}
		if code != 0 {
			return fmt.Errorf("the fixes stopped (exit %d): %s", code, lastLines(out.String(), 6))
		}
		return nil
	}()
	audit := func(action, detail string) {
		if err := s.db.Audit(bg, db.AuditEntry{UserID: by, Action: action, Target: d.Name, Detail: detail}); err != nil {
			s.log.Warn("audit write failed", "action", action, "err", err)
		}
	}
	if err != nil {
		s.log.Warn("security fixes failed", "domain", d.Name, "err", err)
		_ = s.db.EndSiteAppTask(bg, d.ID, err.Error())
		audit("app.harden_failed", err.Error())
		return
	}
	audit("app.hardened", strings.Join(fixes, " "))
	_ = s.db.EndSiteAppTask(bg, d.ID, "")
	if err := s.db.StartSiteAppTask(bg, d.ID, db.AppTaskChecking); err != nil {
		return
	}
	cctx, cancel := context.WithTimeout(bg, appCheckTimeout)
	defer cancel()
	s.checkApp(cctx, d, app)
}
