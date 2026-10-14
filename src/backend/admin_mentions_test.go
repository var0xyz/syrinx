//go:build !ops && !ripplescleanup && !challengescleanup

package main

import (
	"context"
	"slices"
	"testing"

	"github.com/google/uuid"
)

func TestAdminsMentionServer(t *testing.T) {
	cases := []struct {
		claim  string
		server string
		ok     bool
	}{
		{"!admins@home1234", "home1234", true},
		{" !admins@peer5678 ", "peer5678", true},
		{"admins@home1234", "", false},
		{"!admins@", "", false},
		{"abcdefgh@home1234", "", false},
	}
	for _, c := range cases {
		server, ok := adminsMentionServer(c.claim)
		if ok != c.ok || (ok && server != c.server) {
			t.Errorf("adminsMentionServer(%q) = %q, %v; want %q, %v", c.claim, server, ok, c.server, c.ok)
		}
	}
}

func TestMissingAdminMentions(t *testing.T) {
	db, rs, _ := newTeardownTestService(t)
	ds := rs.db
	ctx := context.Background()
	users := map[string]string{"author": roleUser, "admin": roleAdmin, "root": roleRoot, "member": roleUser}
	ids := map[string]string{}
	for name, role := range users {
		id := string(canonicalID(teardownHomeID, name))
		ids[name] = id
		insertTeardownIdentity(t, db, id, teardownHomeID)
		if _, err := db.Exec(`INSERT INTO users (id, role) VALUES ($1, $2)`, id, role); err != nil {
			t.Fatalf("insert user %s: %v", name, err)
		}
	}
	here := ids["author"] + "/" + uuid.NewString()
	there := ids["author"] + "/" + uuid.NewString()
	for _, reedID := range []string{here, there} {
		if err := ds.UpsertReedIdentity(ctx, reedID); err != nil {
			t.Fatalf("UpsertReedIdentity: %v", err)
		}
	}
	if err := ds.InsertAdminMentionRow(ctx, here, teardownHomeID); err != nil {
		t.Fatalf("InsertAdminMentionRow: %v", err)
	}
	if err := ds.InsertAdminMentionRow(ctx, there, teardownPeerID); err != nil {
		t.Fatalf("InsertAdminMentionRow: %v", err)
	}

	if servers, err := ds.GetReedAdminMentions(ctx, here); err != nil || !slices.Equal(servers, []string{teardownHomeID}) {
		t.Fatalf("GetReedAdminMentions = %v %v, want this server", servers, err)
	}
	for name, want := range map[string]int{"admin": 1, "root": 1, "member": 0, "author": 0} {
		got, err := ds.GetMissingAdminMentions(ctx, ids[name])
		if err != nil {
			t.Fatalf("GetMissingAdminMentions(%s): %v", name, err)
		}
		if len(got) != want || (want == 1 && got[0].ReedID != here) {
			t.Errorf("%s: missing admin mentions = %+v, want %d of %s", name, got, want, here)
		}
	}
}

func TestAdminsUsernameIsReserved(t *testing.T) {
	_, rs, _ := newTeardownTestService(t)
	for _, name := range []string{"admins", "Admins"} {
		if taken, err := rs.db.UsernameExists(context.Background(), name, "someone@"+teardownHomeID); err != nil || !taken {
			t.Errorf("UsernameExists(%q) = %v %v, want taken", name, taken, err)
		}
	}
}
