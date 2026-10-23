//go:build !ops && !ripplescleanup && !challengescleanup

package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

// An account removal a peer in lockdown refuses is kept, the peer is
// suspended, and the removal goes out once the peer accepts it.
func TestOwedAccountRemovalWaitsForAPeerInLockdown(t *testing.T) {
	a := newE2EServer(t, newTestDatabase(t, InitDB), false)
	gina := a.signup(t, "gina")
	a.deleteAccount(t, gina)

	var lockdown atomic.Bool
	lockdown.Store(true)
	var calls atomic.Int32
	peer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if lockdown.Load() {
			w.Header().Set(recoveringHeader, string(recoveryModeLockdown))
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(peer.Close)
	a.h.federationHTTPClientOverride = peer.Client()
	const peerID = "peer5678"
	if _, err := a.db.Exec(`INSERT INTO servers (id, name, base_url, frontend_url) VALUES ($1, $1, $2, $2)`, peerID, peer.URL); err != nil {
		t.Fatal(err)
	}
	peerTestMesh(t, a.db, peerID)

	ctx := context.Background()
	if err := a.h.services.db.QueueAccountRemovalNotices(ctx, gina.id, []string{peerID}); err != nil {
		t.Fatal(err)
	}
	owed := func() int {
		t.Helper()
		ids, err := a.h.services.db.AccountRemovalsOwedTo(ctx, peerID)
		if err != nil {
			t.Fatal(err)
		}
		return len(ids)
	}

	a.h.sendOwedAccountRemovals(peerID)
	var down bool
	if err := a.db.QueryRow(`SELECT down_at IS NOT NULL FROM server_mesh WHERE server_id = $1`, peerID).Scan(&down); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 || owed() != 1 || !down {
		t.Fatalf("after lockdown: calls=%d owed=%d down=%v, want 1 call, still owed, peer down", calls.Load(), owed(), down)
	}

	// The peer's boot notice clears down_at before anything is resent.
	if err := a.h.services.db.SetPeerDown(ctx, peerID, false); err != nil {
		t.Fatal(err)
	}
	lockdown.Store(false)
	a.h.sendOwedAccountRemovals(peerID)
	if owed() != 0 {
		t.Fatalf("still owed %d after the peer accepted", owed())
	}
}

// Only a 503 marked as lockdown suspends a peer; an overloaded or
// unreachable one is retried as usual.
func TestPlain503DoesNotSuspendAPeer(t *testing.T) {
	db, rs, _ := newTeardownTestService(t)
	peerTestMesh(t, db, teardownPeerID)
	h := &Handlers{services: &Services{db: rs.db, log: NewLoggingService()}}
	h.suspendIfPeerInLockdown(context.Background(), teardownPeerID, &http.Response{StatusCode: http.StatusServiceUnavailable, Header: http.Header{}})
	var down bool
	if err := db.QueryRow(`SELECT down_at IS NOT NULL FROM server_mesh WHERE server_id = $1`, teardownPeerID).Scan(&down); err != nil || down {
		t.Fatalf("down = %v (%v), want a plain 503 to leave the peer up", down, err)
	}
}
