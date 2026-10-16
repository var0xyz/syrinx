//go:build !ops

package main

import (
	"context"
	"strings"
	"testing"
)

func TestReservedUsernames_Enforcement(t *testing.T) {
	f := newAdminFixture(t)
	ctx := context.Background()
	cases := []struct {
		name, username, claimant string
		taken                    bool
	}{
		{"reserved name at signup", "ADMIN", "", true},
		{"reserved name for a rename", "Webmaster", f.ids["u2"], true},
		{"the @admins handle", "admins", f.ids["u2"], true},
		{"the server's name", strings.ToUpper(f.ds.serverName), f.ids["u2"], true},
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
