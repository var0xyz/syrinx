//go:build !ops

package main

import (
	"context"
	"net/http"
	"testing"
)

func (f *adminFixture) reserve(t *testing.T, caller, name string, reserve bool) int {
	t.Helper()
	handler, method := f.h.ReserveUsername, http.MethodPut
	if !reserve {
		handler, method = f.h.ReleaseUsername, http.MethodDelete
	}
	return f.serve(t, handler, method, "/api/admin/reserved-usernames/"+name, caller, nil,
		map[string]string{"username": name}).Code
}

func TestReservedUsernames_RootOnlyAndLogged(t *testing.T) {
	f := newAdminFixture(t)
	f.grant(t, "a1", adminPermissions...)
	if code := f.reserve(t, "a1", "admin", true); code != http.StatusForbidden {
		t.Fatalf("admin reserving: %d, want 403", code)
	}
	if code := f.serve(t, f.h.ListReservedUsernames, http.MethodGet, "/api/admin/reserved-usernames", "a1", nil, nil).Code; code != http.StatusForbidden {
		t.Fatalf("admin listing: %d, want 403", code)
	}

	for range 2 {
		if code := f.reserve(t, "root", "Admin", true); code != http.StatusNoContent {
			t.Fatalf("root reserving: %d", code)
		}
	}
	names, err := f.ds.ListReservedUsernames(context.Background())
	if err != nil || len(names) != 1 || names[0] != "admin" {
		t.Fatalf("reserved = %v err=%v, want [admin]", names, err)
	}
	if code := f.reserve(t, "root", "admin", false); code != http.StatusNoContent {
		t.Fatalf("root releasing: %d", code)
	}
	entries := f.ledger(t)
	if len(entries) != 2 || entries[0].Message != "Username admin reserved" || entries[1].Message != "Username admin released" ||
		entries[0].UserID != f.ids["root"] {
		t.Fatalf("ledger = %+v, want one reserved and one released entry by root", entries)
	}
}

func TestReservedUsernames_Enforcement(t *testing.T) {
	f := newAdminFixture(t)
	ctx := context.Background()
	if code := f.reserve(t, "root", "admin", true); code != http.StatusNoContent {
		t.Fatal(code)
	}
	if code := f.reserve(t, "root", "u1", true); code != http.StatusNoContent {
		t.Fatal(code)
	}

	cases := []struct {
		name, username, claimant string
		taken                    bool
	}{
		{"reserved name at signup", "ADMIN", "", true},
		{"reserved name for another user", "Admin", f.ids["u2"], true},
		{"holder keeps a name reserved after they took it", "U1", f.ids["u1"], false},
		{"anyone else can't take the holder's reserved name", "u1", f.ids["u2"], true},
		{"root's name for a user", "Root", f.ids["u2"], true},
		{"root's name at signup", "ROOT", "", true},
		{"root's name for root", "root", f.ids["root"], false},
		{"a free name", "someone", f.ids["u2"], false},
	}
	for _, c := range cases {
		taken, err := f.ds.UsernameExists(ctx, c.username, c.claimant)
		if err != nil || taken != c.taken {
			t.Errorf("%s: taken=%v err=%v, want %v", c.name, taken, err, c.taken)
		}
	}
}
