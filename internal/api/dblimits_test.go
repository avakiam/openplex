package api

import (
	"testing"

	"openplex.dev/openplex/internal/db"
)

func TestDatabaseLimitRules(t *testing.T) {
	r := int64(7)
	admin := &db.User{ID: 1, Role: db.RoleAdmin}
	reseller := &db.User{ID: 7, Role: db.RoleReseller, MaxDBSizeMB: 500}
	freeReseller := &db.User{ID: 8, Role: db.RoleReseller}
	customer := &db.User{ID: 9, Role: db.RoleCustomer, ParentID: &r}
	other := &db.User{ID: 10, Role: db.RoleCustomer}

	for _, tt := range []struct {
		actor, owner *db.User
		want         bool
	}{
		{admin, customer, true}, {admin, admin, true},
		{reseller, customer, true}, {reseller, other, false}, {reseller, reseller, false},
		{customer, customer, false},
	} {
		if got := canLimitDatabase(tt.actor, tt.owner); got != tt.want {
			t.Errorf("canLimitDatabase(%d, %d) = %v", tt.actor.ID, tt.owner.ID, got)
		}
	}
	for _, tt := range []struct {
		actor *db.User
		mb    int
		ok    bool
	}{
		{admin, 0, true}, {admin, 100000, true}, {admin, -1, false},
		{reseller, 500, true}, {reseller, 1, true}, {reseller, 0, false}, {reseller, 501, false},
		{freeReseller, 0, true},
	} {
		if got := dbSizeCap(tt.actor, tt.mb) == nil; got != tt.ok {
			t.Errorf("dbSizeCap(%s %d, %d) ok = %v", tt.actor.Role, tt.actor.MaxDBSizeMB, tt.mb, got)
		}
	}
}
