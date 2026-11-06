package db

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestUsersAndDomains(t *testing.T) {
	ctx := context.Background()
	d := OpenMemory(t)

	admin := &User{Username: "admin", PasswordHash: "x", Role: RoleAdmin}
	if err := d.CreateUser(ctx, admin); err != nil {
		t.Fatal(err)
	}
	if err := d.CreateUser(ctx, &User{Username: "ADMIN", PasswordHash: "x", Role: RoleAdmin}); !errors.Is(err, ErrConflict) {
		t.Fatalf("expected case-insensitive conflict, got %v", err)
	}
	res := &User{Username: "res", PasswordHash: "x", Role: RoleReseller}
	_ = d.CreateUser(ctx, res)
	cust := &User{Username: "cust", PasswordHash: "x", Role: RoleCustomer, ParentID: &res.ID}
	if err := d.CreateUser(ctx, cust); err != nil {
		t.Fatal(err)
	}
	kids, err := d.ListUsers(ctx, UserFilter{ParentID: &res.ID})
	if err != nil || len(kids) != 1 || kids[0].ID != cust.ID {
		t.Fatalf("children: %v %v", kids, err)
	}

	dom := &Domain{OwnerID: cust.ID, Name: "example.com", Kind: KindPrimary, Runtime: "php", RuntimeVersion: "8.3",
		DocumentRoot: "public", Env: map[string]string{"APP_ENV": "prod"}, SSLEnabled: true}
	if err := d.CreateDomain(ctx, dom); err != nil {
		t.Fatal(err)
	}
	got, err := d.GetDomainByName(ctx, "EXAMPLE.com")
	if err != nil || got.Env["APP_ENV"] != "prod" || !got.SSLEnabled || got.CreatedAt.IsZero() {
		t.Fatalf("get: %+v %v", got, err)
	}
	alias := &Domain{OwnerID: cust.ID, Name: "example.net", Kind: KindAlias, ParentID: &dom.ID, Env: nil}
	if err := d.CreateDomain(ctx, alias); err != nil {
		t.Fatal(err)
	}
	if n, _ := d.CountDomainsByOwner(ctx, cust.ID); n != 1 {
		t.Fatalf("count = %d, want 1 (aliases excluded)", n)
	}
	if err := d.DeleteUser(ctx, cust.ID); !errors.Is(err, ErrConflict) {
		t.Fatalf("expected FK conflict deleting owner, got %v", err)
	}
	if err := d.DeleteDomain(ctx, dom.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := d.GetDomain(ctx, alias.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("alias should cascade, got %v", err)
	}
}

func TestRefreshRotation(t *testing.T) {
	ctx := context.Background()
	d := OpenMemory(t)
	u := &User{Username: "u", PasswordHash: "x", Role: RoleCustomer}
	_ = d.CreateUser(ctx, u)
	t1 := &RefreshToken{UserID: u.ID, Family: "f", TokenHash: "h1", ExpiresAt: time.Now().Add(time.Hour)}
	if err := d.CreateRefreshToken(ctx, t1); err != nil {
		t.Fatal(err)
	}
	t2 := &RefreshToken{UserID: u.ID, Family: "f", TokenHash: "h2", ExpiresAt: time.Now().Add(time.Hour)}
	if err := d.RotateRefreshToken(ctx, t1.ID, t2); err != nil {
		t.Fatal(err)
	}
	t3 := &RefreshToken{UserID: u.ID, Family: "f", TokenHash: "h3", ExpiresAt: time.Now().Add(time.Hour)}
	if err := d.RotateRefreshToken(ctx, t1.ID, t3); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second rotation of same token must fail, got %v", err)
	}
	got, _ := d.GetRefreshTokenByHash(ctx, "h1")
	if !got.Revoked {
		t.Fatal("old token not revoked")
	}
	if got.ExpiresAt.Before(time.Now()) {
		t.Fatal("time round-trip broken")
	}
}
