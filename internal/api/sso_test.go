package api

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"openplex.dev/openplex/internal/db"
)

// A billing system signs a customer in with a one-time link from the API.
func TestSingleSignOnLink(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	admin := h.login("admin")
	res := h.mkUser("reseller", db.RoleReseller, nil)
	cust := h.mkUser("customer", db.RoleCustomer, &res.ID)
	other := h.mkUser("other", db.RoleCustomer, nil)

	var link struct {
		URL, Path string
	}
	if code := h.do("POST", "/users/"+itoa(cust.ID)+"/login-link", admin.AccessToken, nil, &link); code != http.StatusCreated ||
		!strings.HasPrefix(link.Path, "/login/?link=") {
		t.Fatalf("create link: %d %+v", code, link)
	}
	if code := h.do("POST", "/users/"+itoa(cust.ID)+"/login-link", admin.AccessToken, map[string]int{"expires_in_seconds": 90000}, nil); code != http.StatusBadRequest {
		t.Fatalf("too long: %d", code)
	}
	// A reseller signs its own customers in, not others'.
	r := h.login("reseller")
	if code := h.do("POST", "/users/"+itoa(other.ID)+"/login-link", r.AccessToken, nil, nil); code != http.StatusNotFound {
		t.Fatalf("reseller, someone else's customer: %d", code)
	}
	if code := h.do("POST", "/users/"+itoa(cust.ID)+"/login-link", r.AccessToken, nil, nil); code != http.StatusCreated {
		t.Fatalf("reseller, own customer: %d", code)
	}

	token := strings.TrimPrefix(link.Path, "/login/?link=")
	var sess struct {
		tokenResponse
		Purpose string `json:"purpose"`
	}
	if code := h.do("POST", "/auth/link", "", map[string]string{"token": token}, &sess); code != http.StatusOK || sess.Purpose != db.LinkSSO || sess.User.ID != cust.ID {
		t.Fatalf("sign in: %d %q", code, sess.Purpose)
	}
	// It only signs in: changing the password still needs the current one.
	set := map[string]string{"current_password": "", "new_password": "a-new-pass-123"}
	if code := h.do("POST", "/auth/password", sess.AccessToken, set, nil); code != http.StatusUnauthorized {
		t.Fatalf("password without the current one after single sign-on: %d", code)
	}

	// Two-factor authentication doesn't stop it (the manager could turn it off),
	// a disabled account does.
	cust.TOTPEnabled, cust.TOTPSecret = true, "JBSWY3DPEHPK3PXP"
	_ = h.db.UpdateUser(ctx, cust)
	if code := h.do("POST", "/users/"+itoa(cust.ID)+"/login-link", admin.AccessToken, nil, &link); code != http.StatusCreated {
		t.Fatal(code)
	}
	if code := h.do("POST", "/auth/link", "", map[string]string{"token": strings.TrimPrefix(link.Path, "/login/?link=")}, nil); code != http.StatusOK {
		t.Fatalf("2fa account: %d", code)
	}
	cust.Disabled = true
	_ = h.db.UpdateUser(ctx, cust)
	if code := h.do("POST", "/users/"+itoa(cust.ID)+"/login-link", admin.AccessToken, nil, nil); code != http.StatusConflict {
		t.Fatalf("disabled account: %d", code)
	}
}

// Suspending an account takes its websites (a reseller's customers' too)
// offline; unsuspending gives them back the status they had.
func TestSuspendUser(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	admin := h.login("admin")
	res := h.mkUser("res", db.RoleReseller, nil)
	cust := h.mkUser("cust", db.RoleCustomer, &res.ID)
	mk := func(name string, owner int64, status string) *db.Domain {
		d := &db.Domain{Name: name, Kind: db.KindPrimary, OwnerID: owner, Runtime: "static", DocumentRoot: "public", Status: status}
		if err := h.db.CreateDomain(ctx, d); err != nil {
			t.Fatal(err)
		}
		return d
	}
	running := mk("running.test", cust.ID, db.StatusRunning)
	stopped := mk("stopped.test", cust.ID, db.StatusStopped)
	own := mk("reseller.test", res.ID, db.StatusRunning)
	status := func(d *db.Domain) string {
		x, err := h.db.GetDomain(ctx, d.ID)
		if err != nil {
			t.Fatal(err)
		}
		return x.Status
	}

	r := h.login("res")
	if code := h.do("POST", "/users/"+itoa(res.ID)+"/suspend", r.AccessToken, nil, nil); code != http.StatusBadRequest {
		t.Fatalf("suspending oneself: %d", code)
	}
	if code := h.do("POST", "/users/"+itoa(res.ID)+"/suspend", admin.AccessToken, nil, nil); code != http.StatusOK {
		t.Fatalf("suspend: %d", code)
	}
	for _, d := range []*db.Domain{running, stopped, own} {
		if s := status(d); s != db.StatusDisabled {
			t.Errorf("%s while suspended: %s", d.Name, s)
		}
	}
	if code := h.do("POST", "/auth/login", "", map[string]string{"username": "res", "password": "password123"}, nil); code == http.StatusOK {
		t.Error("a suspended account signs in")
	}
	if code := h.do("POST", "/users/"+itoa(res.ID)+"/unsuspend", admin.AccessToken, nil, nil); code != http.StatusOK {
		t.Fatalf("unsuspend: %d", code)
	}
	if status(running) != db.StatusRunning || status(stopped) != db.StatusStopped || status(own) != db.StatusRunning {
		t.Errorf("after unsuspending: %s %s %s", status(running), status(stopped), status(own))
	}
	h.login("res")
}

// Terminating an account deletes what it owns with ?purge=true.
func TestDeleteUserPurge(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	admin := h.login("admin")
	u := h.mkUser("leaving", db.RoleCustomer, nil)
	site := &db.Domain{Name: "leaving.test", Kind: db.KindPrimary, OwnerID: u.ID, Runtime: "static", DocumentRoot: "public", Status: db.StatusRunning}
	if err := h.db.CreateDomain(ctx, site); err != nil {
		t.Fatal(err)
	}
	sub := &db.Domain{Name: "blog.leaving.test", Kind: db.KindSubdomain, ParentID: &site.ID, OwnerID: u.ID, Runtime: "static", DocumentRoot: "public", Status: db.StatusRunning}
	if err := h.db.CreateDomain(ctx, sub); err != nil {
		t.Fatal(err)
	}

	if code := h.do("DELETE", "/users/"+itoa(u.ID), admin.AccessToken, nil, nil); code != http.StatusConflict {
		t.Fatalf("delete without purge: %d", code)
	}
	if code := h.do("DELETE", "/users/"+itoa(u.ID)+"?purge=true", admin.AccessToken, nil, nil); code != http.StatusNoContent {
		t.Fatalf("delete with purge: %d", code)
	}
	if _, err := h.db.GetUser(ctx, u.ID); err == nil {
		t.Fatal("the account is still there")
	}
	if ds, _ := h.db.ListDomains(ctx, db.DomainFilter{OwnerIDs: []int64{u.ID}}); len(ds) != 0 {
		t.Fatalf("websites left: %d", len(ds))
	}

	// A reseller with customers is refused, and keeps everything.
	res := h.mkUser("res", db.RoleReseller, nil)
	h.mkUser("rescust", db.RoleCustomer, &res.ID)
	if code := h.do("DELETE", "/users/"+itoa(res.ID)+"?purge=true", admin.AccessToken, nil, nil); code != http.StatusConflict {
		t.Fatalf("reseller with customers: %d", code)
	}
}
