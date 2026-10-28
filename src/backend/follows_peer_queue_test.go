//go:build !ops && !ripplescleanup && !challengescleanup

package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

const followPeerID, followOtherPeerID = "Pr7mN2qW", "Qx8pL3rT"

// followPeerServer is a server with two peers behind a fake that answers
// status, and a local follower of a user of the first.
func followPeerServer(t *testing.T, status *atomic.Int32) (*e2eServer, string, string) {
	t.Helper()
	s := newE2EServer(t, newTestDatabase(t, InitDB), false)
	fake := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(int(status.Load()))
	}))
	t.Cleanup(fake.Close)
	s.h.federationHTTPClientOverride = fake.Client()
	for _, id := range []string{followPeerID, followOtherPeerID} {
		if _, err := s.db.Exec(`INSERT INTO servers (id, name, base_url, frontend_url) VALUES ($1, $1, $2, $2)`, id, fake.URL); err != nil {
			t.Fatal(err)
		}
		peerTestMesh(t, s.db, id)
	}
	followee := string(canonicalID(followPeerID, "zed"))
	if err := s.h.services.db.UpsertRemoteIdentity(context.Background(), followee, followPeerID); err != nil {
		t.Fatal(err)
	}
	return s, s.signup(t, "ann").id, followee
}

// saveOwedFollow stores a follow or unfollow owed to the followee's server,
// as signFollow does when the server couldn't take it at once.
func saveOwedFollow(t *testing.T, s *e2eServer, followerID, followeeID string, follow bool, at time.Time) {
	t.Helper()
	sigs := func(kind string) string { return kind + at.Format(time.RFC3339) }
	cert := FollowCert{
		FollowerID: followerID, FolloweeID: followeeID, Follow: follow,
		UserSignature:   UserSignature{ID: followerID + "/fp", Armor: sigs("u")},
		ServerSignature: ServerSignature{ID: "sfp@" + s.h.services.db.GetServerID(), Armor: sigs("s"), SignedAt: at},
	}
	if _, err := s.db.Exec(`INSERT INTO public_keys (id, armor) VALUES ($1, 'armor') ON CONFLICT DO NOTHING`, cert.UserSignature.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.h.services.db.SaveFollowCert(context.Background(), cert, true); err != nil {
		t.Fatalf("save follow: %v", err)
	}
}

// A follow and unfollow made while the peer is down reach it as one
// unfollow, which is then dropped; no other peer hears of either.
func TestFollowWhilePeerDownSendsLatestState(t *testing.T) {
	var status atomic.Int32
	status.Store(http.StatusNoContent)
	s, ann, zed := followPeerServer(t, &status)
	ctx := context.Background()
	if err := s.h.services.db.SetPeerDown(ctx, followPeerID, true); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	saveOwedFollow(t, s, ann, zed, true, now.Add(-time.Minute))
	saveOwedFollow(t, s, ann, zed, false, now)
	if n := countRows(t, s, `SELECT COUNT(*) FROM peer_event_queue WHERE server_id = $1`, followOtherPeerID); n != 0 {
		t.Fatalf("other peer owed %d", n)
	}
	if n := countRows(t, s, `SELECT COUNT(*) FROM peer_event_queue WHERE server_id = $1 AND kind = 'follow'`, followPeerID); n != 1 {
		t.Fatalf("followee's server owed %d, want 1", n)
	}
	if err := s.h.services.db.SetPeerDown(ctx, followPeerID, false); err != nil {
		t.Fatal(err)
	}
	s.h.drainPeerEvents(followPeerID)
	if n := countRows(t, s, `SELECT COUNT(*) FROM peer_event_queue`); n != 0 {
		t.Fatalf("still owed %d", n)
	}
	if n := countRows(t, s, `SELECT COUNT(*) FROM follow_certs`); n != 0 {
		t.Fatalf("delivered unfollow kept: %d certs", n)
	}
}

// A follow the followee's server refuses is undone.
func TestRefusedFollowIsUndone(t *testing.T) {
	var status atomic.Int32
	status.Store(http.StatusForbidden)
	s, ann, zed := followPeerServer(t, &status)
	saveOwedFollow(t, s, ann, zed, true, time.Now().UTC().Truncate(time.Second))
	waitPeerIdle(t, s.db, followPeerID)
	s.h.drainPeerEvents(followPeerID)
	if n := countRows(t, s, `SELECT COUNT(*) FROM follow_certs`); n != 0 {
		t.Fatalf("refused follow kept: %d certs", n)
	}
	if n := countRows(t, s, `SELECT COUNT(*) FROM user_following WHERE user_id = $1`, ann); n != 0 {
		t.Fatalf("refused follow edge kept: %d", n)
	}
}
