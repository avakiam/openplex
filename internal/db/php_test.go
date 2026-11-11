package db

import (
	"context"
	"testing"
)

func TestPHPSettings(t *testing.T) {
	ctx := context.Background()
	d := OpenMemory(t)
	u := &User{Username: "ana", PasswordHash: "x", Role: RoleCustomer}
	if err := d.CreateUser(ctx, u); err != nil {
		t.Fatal(err)
	}
	dom := &Domain{OwnerID: u.ID, Name: "a.example", Kind: KindPrimary, Runtime: "php", RuntimeVersion: "8.3", DocumentRoot: "public"}
	if err := d.CreateDomain(ctx, dom); err != nil {
		t.Fatal(err)
	}
	if s, err := d.GetPHPSettings(ctx, dom.ID); err != nil || len(s) != 0 {
		t.Fatalf("none: %v %v", s, err)
	}
	for _, want := range []map[string]string{{"memory_limit": "512M"}, {"memory_limit": "1G", "display_errors": "On"}} {
		if err := d.SavePHPSettings(ctx, dom.ID, want); err != nil {
			t.Fatal(err)
		}
		got, err := d.GetPHPSettings(ctx, dom.ID)
		if err != nil || len(got) != len(want) || got["memory_limit"] != want["memory_limit"] {
			t.Fatalf("got %v %v, want %v", got, err, want)
		}
	}
	if err := d.SavePHPSettings(ctx, dom.ID, map[string]string{}); err != nil {
		t.Fatal(err)
	}
	if s, _ := d.GetPHPSettings(ctx, dom.ID); len(s) != 0 {
		t.Fatalf("not cleared: %v", s)
	}
	_ = d.SavePHPSettings(ctx, dom.ID, map[string]string{"memory_limit": "512M"})
	if err := d.DeleteDomain(ctx, dom.ID); err != nil {
		t.Fatal(err)
	}
	if s, _ := d.GetPHPSettings(ctx, dom.ID); len(s) != 0 {
		t.Fatal("settings kept after the website was deleted")
	}
}
