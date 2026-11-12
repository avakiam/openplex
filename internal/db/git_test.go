package db

import (
	"context"
	"errors"
	"testing"
)

func TestGitDeploys(t *testing.T) {
	ctx := context.Background()
	d := OpenMemory(t)
	u := &User{Username: "ana", PasswordHash: "x", Role: RoleCustomer}
	if err := d.CreateUser(ctx, u); err != nil {
		t.Fatal(err)
	}
	dom := &Domain{OwnerID: u.ID, Name: "app.example", Kind: KindPrimary, Runtime: "node", RuntimeVersion: "22", DocumentRoot: "public"}
	if err := d.CreateDomain(ctx, dom); err != nil {
		t.Fatal(err)
	}
	g := &GitDeploy{DomainID: dom.ID, RepoURL: "git@github.com:a/b.git", Branch: "main", AutoDeploy: true, WebhookToken: "t", KeyEnc: "k", PublicKey: "ssh-ed25519 AAA"}
	if err := d.CreateGitDeploy(ctx, g); err != nil {
		t.Fatal(err)
	}
	if err := d.CreateGitDeploy(ctx, g); !errors.Is(err, ErrConflict) {
		t.Fatalf("second: %v", err)
	}
	// A deploy asked for while one runs runs again when it ends.
	if ok, err := d.StartGitDeploy(ctx, dom.ID, "manual"); !ok || err != nil {
		t.Fatalf("start: %v %v", ok, err)
	}
	if ok, err := d.StartGitDeploy(ctx, dom.ID, "webhook"); ok || err != nil {
		t.Fatalf("second start: %v %v", ok, err)
	}
	if again, err := d.FinishGitDeploy(ctx, dom.ID, "abc123", "first", "out", ""); !again || err != nil {
		t.Fatalf("finish: %v %v", again, err)
	}
	if again, err := d.FinishGitDeploy(ctx, dom.ID, "", "", "out2", "boom"); again || err != nil {
		t.Fatalf("finish again: %v %v", again, err)
	}
	got, err := d.GetGitDeploy(ctx, dom.ID)
	if err != nil || got.Status != GitError || got.LastCommit != "abc123" || got.LastSubject != "first" || got.LastError != "boom" ||
		got.LastTrigger != "webhook" || got.LastDeployAt == nil || got.Pending {
		t.Fatalf("got %+v %v", got, err)
	}
	if _, err := d.StartGitDeploy(ctx, 999, "manual"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown site: %v", err)
	}
	_, _ = d.StartGitDeploy(ctx, dom.ID, "manual")
	if err := d.FailInterruptedGitDeploys(ctx, "restart"); err != nil {
		t.Fatal(err)
	}
	if got, _ := d.GetGitDeploy(ctx, dom.ID); got.Status != GitError || got.LastError != "restart" {
		t.Fatalf("interrupted: %+v", got)
	}
	g.Branch, g.AutoDeploy = "prod", false
	if err := d.UpdateGitDeploy(ctx, g); err != nil {
		t.Fatal(err)
	}
	if err := d.DeleteDomain(ctx, dom.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := d.GetGitDeploy(ctx, dom.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("kept after website deletion: %v", err)
	}
}
