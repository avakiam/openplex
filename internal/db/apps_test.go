package db

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestSiteApps(t *testing.T) {
	ctx := context.Background()
	d := OpenMemory(t)
	u := &User{Username: "ana", PasswordHash: "x", Role: RoleCustomer}
	if err := d.CreateUser(ctx, u); err != nil {
		t.Fatal(err)
	}
	dom := &Domain{OwnerID: u.ID, Name: "blog.example", Kind: KindPrimary, Runtime: "php", RuntimeVersion: "8.3", DocumentRoot: "public"}
	if err := d.CreateDomain(ctx, dom); err != nil {
		t.Fatal(err)
	}
	if _, err := d.GetSiteApp(ctx, dom.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected none, got %v", err)
	}
	dbr := &Database{OwnerID: u.ID, Engine: EngineMariaDB, Name: "ana_wp_blog", Username: "ana_wp_blog"}
	if err := d.CreateDatabase(ctx, dbr); err != nil {
		t.Fatal(err)
	}
	a := &SiteApp{DomainID: dom.ID, App: "wordpress", AdminUser: "admin", AdminEmail: "a@example.com", Language: "es", DatabaseID: &dbr.ID, CreatedBy: &u.ID}
	if err := d.StartSiteApp(ctx, a); err != nil {
		t.Fatal(err)
	}
	if err := d.StartSiteApp(ctx, &SiteApp{DomainID: dom.ID, App: "joomla", AdminUser: "x", AdminEmail: "x"}); !errors.Is(err, ErrConflict) {
		t.Fatalf("second install while installing: %v", err)
	}

	// A restart fails the running install; a failed one may be replaced.
	if err := d.StartSiteAppTask(ctx, dom.ID, AppTaskChecking); !errors.Is(err, ErrConflict) {
		t.Fatalf("check while installing: %v", err)
	}
	if n, err := d.FailInterruptedSiteApps(ctx, "interrupted", "stopped"); err != nil || n != 1 {
		t.Fatalf("interrupted: %d %v", n, err)
	}
	if err := d.StartSiteApp(ctx, &SiteApp{DomainID: dom.ID, App: "drupal", AdminUser: "root", AdminEmail: "r@example.com", Language: "en"}); err != nil {
		t.Fatalf("retry after failure: %v", err)
	}
	if err := d.FinishSiteApp(ctx, dom.ID, AppInstalled, "11.4.12", ""); err != nil {
		t.Fatal(err)
	}
	got, err := d.GetSiteApp(ctx, dom.ID)
	if err != nil || got.App != "drupal" || got.Status != AppInstalled || got.Version != "11.4.12" || got.DatabaseID != nil {
		t.Fatalf("got %+v %v", got, err)
	}

	// Update checks and updates: one task at a time; a restart ends it.
	if err := d.StartSiteAppTask(ctx, 999, AppTaskChecking); !errors.Is(err, ErrNotFound) {
		t.Fatalf("task on a website without app: %v", err)
	}
	if ids, err := d.SiteAppsToCheck(ctx, time.Now()); err != nil || len(ids) != 1 {
		t.Fatalf("to check: %v %v", ids, err)
	}
	if err := d.StartSiteAppTask(ctx, dom.ID, AppTaskChecking); err != nil {
		t.Fatal(err)
	}
	if err := d.StartSiteAppTask(ctx, dom.ID, AppTaskUpdating); !errors.Is(err, ErrConflict) {
		t.Fatalf("second task: %v", err)
	}
	if err := d.FinishSiteAppCheck(ctx, dom.ID, "11.4.12", []byte(`{"core":"11.5.0"}`), ""); err != nil {
		t.Fatal(err)
	}
	if ids, _ := d.SiteAppsToCheck(ctx, time.Now().Add(-time.Hour)); len(ids) != 0 {
		t.Fatalf("checked app listed again: %v", ids)
	}
	_ = d.StartSiteAppTask(ctx, dom.ID, AppTaskUpdating)
	if _, err := d.FailInterruptedSiteApps(ctx, "interrupted", "stopped"); err != nil {
		t.Fatal(err)
	}
	got, _ = d.GetSiteApp(ctx, dom.ID)
	if got.Task != "" || got.TaskError != "stopped" || string(got.Updates) != `{"core":"11.5.0"}` || got.CheckedAt == nil || got.Status != AppInstalled {
		t.Fatalf("after interrupted update: %+v", got)
	}
	_ = d.StartSiteAppTask(ctx, dom.ID, AppTaskUpdating)
	if err := d.FinishSiteAppUpdate(ctx, dom.ID, "11.5.0", ""); err != nil {
		t.Fatal(err)
	}
	if got, _ = d.GetSiteApp(ctx, dom.ID); got.Version != "11.5.0" || got.TaskError != "" || got.LastUpdateAt == nil {
		t.Fatalf("after update: %+v", got)
	}

	// The database link is cleared when the database goes, the app with the website.
	if err := d.SetSiteAppDatabase(ctx, dom.ID, &dbr.ID); err != nil {
		t.Fatal(err)
	}
	if err := d.DeleteDatabase(ctx, dbr.ID); err != nil {
		t.Fatal(err)
	}
	if got, _ := d.GetSiteApp(ctx, dom.ID); got.DatabaseID != nil {
		t.Fatalf("database link kept: %v", *got.DatabaseID)
	}
	if err := d.DeleteDomain(ctx, dom.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := d.GetSiteApp(ctx, dom.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("app kept after website deletion: %v", err)
	}
}
