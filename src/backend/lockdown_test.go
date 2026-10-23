//go:build !ops && !ripplescleanup && !challengescleanup

package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// In lockdown every peer is told to come back later, before peer auth can
// answer a final 401; users and our own server key go through.
func TestLockdownPeersMiddleware(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) })
	mw := lockdownPeersMiddleware("/api", "home1234")(next)
	for _, c := range []struct {
		path, keyID string
		want        int
	}{
		{"/api/federation/relay/new-reed", "abcdef@peer5678", http.StatusServiceUnavailable},
		{"/api/keys/x", "abcdef@unknown1", http.StatusServiceUnavailable},
		{"/api/federation/connect/inv1", "", http.StatusServiceUnavailable},
		{"/api/users/me", "1@home1234/abcdef", http.StatusNoContent},
		{"/api/server/info", "abcdef@home1234", http.StatusNoContent},
		{"/api/server/info", "", http.StatusNoContent},
	} {
		req := httptest.NewRequest(http.MethodPost, c.path, nil)
		if c.keyID != "" {
			req.Header.Set("X-Syrinx-Public-Key-Id", c.keyID)
		}
		rr := httptest.NewRecorder()
		mw.ServeHTTP(rr, req)
		if rr.Code != c.want {
			t.Errorf("%s as %q: %d, want %d", c.path, c.keyID, rr.Code, c.want)
		}
	}
}
