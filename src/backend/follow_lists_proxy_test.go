//go:build !ops

package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// A foreign user's follow lists come from their home server: this one
// only holds the edges its own users are on.
func TestFollowListsOfForeignUserGoHome(t *testing.T) {
	b := newE2EServer(t, newTestDatabase(t, InitDB), false)
	viewer := b.signup(t, "viewer")
	var paths []string
	home := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path+"?"+r.URL.Query().Get("before"))
		w.WriteHeader(http.StatusOK)
	}))
	defer home.Close()
	if _, err := b.db.Exec(`INSERT INTO servers (id, name, base_url, frontend_url) VALUES ('homeAAAA', 'Home', $1, $1)`, home.URL); err != nil {
		t.Fatal(err)
	}
	peerTestMesh(t, b.db, "homeAAAA")
	b.h.cfg.FederationAllowInsecureHTTP = true

	user := "1@homeAAAA"
	vars := map[string]string{"userID": user}
	for _, tc := range []struct {
		path    string
		handler http.HandlerFunc
	}{
		{"/api/users/" + user + "/followers", b.h.GetUserFollowers},
		{"/api/users/" + user + "/following", b.h.GetUserFollowing},
	} {
		if rr := b.authed(t, viewer, http.MethodGet, tc.path+"?before=5", nil, vars, tc.handler); rr.Code != http.StatusOK {
			t.Fatalf("%s: %d %s", tc.path, rr.Code, rr.Body.String())
		}
	}
	want := []string{"/api/users/" + user + "/followers?5", "/api/users/" + user + "/following?5"}
	if len(paths) != 2 || paths[0] != want[0] || paths[1] != want[1] {
		t.Fatalf("home server got %v, want %v", paths, want)
	}
}
