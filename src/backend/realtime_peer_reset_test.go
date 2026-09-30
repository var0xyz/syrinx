//go:build !ops && !ripplescleanup

package main

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"syrinx/observability/metrics"
)

const teardownOtherPeerID = "othr9012"

// seedPeerResetState builds realtime state in both directions with
// teardownPeerID, plus one row tied to another peer that must survive.
func seedPeerResetState(t *testing.T, db *sql.DB, rs *realtimeService, viewer string) {
	t.Helper()
	ctx := context.Background()
	if _, err := db.Exec(`INSERT INTO servers (id, name, self) VALUES ($1, $1, FALSE)`, teardownOtherPeerID); err != nil {
		t.Fatalf("insert other peer: %v", err)
	}

	localAuthor := string(canonicalID(teardownHomeID, "alice"))
	peerAuthor := string(canonicalID(teardownPeerID, "bob"))
	peerViewer := string(canonicalID(teardownPeerID, "carol"))
	otherViewer := string(canonicalID(teardownOtherPeerID, "dave"))
	insertTeardownIdentity(t, db, localAuthor, teardownHomeID)
	insertTeardownIdentity(t, db, peerAuthor, teardownPeerID)
	insertTeardownIdentity(t, db, peerViewer, teardownPeerID)
	insertTeardownIdentity(t, db, otherViewer, teardownOtherPeerID)

	localReed := string(appendEntity(identityID(localAuthor), "01a026d4-406f-744b-b730-fcd241bf2582"))
	peerReed := string(appendEntity(identityID(peerAuthor), "01a026d4-406f-744b-b730-fcd241bf2583"))
	for id, server := range map[string]string{localReed: teardownHomeID, peerReed: teardownPeerID} {
		if _, err := db.Exec(`INSERT INTO reed_identities (id, server_id) VALUES ($1, $2)`, id, server); err != nil {
			t.Fatalf("insert reed identity: %v", err)
		}
	}

	// Our viewer's subscriptions to the peer's resources.
	if _, err := rs.db.CreateProfileSubscription(ctx, "p-out", viewer, peerAuthor); err != nil {
		t.Fatalf("subscribe local viewer to peer profile: %v", err)
	}
	if err := rs.db.CreateReedSubscription(ctx, "r-out", viewer, peerReed); err != nil {
		t.Fatalf("subscribe local viewer to peer reed: %v", err)
	}
	// The peer's viewer subscribed to our resources.
	if _, err := rs.db.CreateProfileSubscription(ctx, "p-in", peerViewer, localAuthor); err != nil {
		t.Fatalf("subscribe peer viewer to local profile: %v", err)
	}
	if err := rs.db.CreateReedSubscription(ctx, "r-in", peerViewer, localReed); err != nil {
		t.Fatalf("subscribe peer viewer to local reed: %v", err)
	}
	// Another peer's viewer, untouched by this reset.
	if err := rs.db.CreateReedSubscription(ctx, "r-other", otherViewer, localReed); err != nil {
		t.Fatalf("subscribe other peer viewer: %v", err)
	}

	// A relay request the peer's user has pending here.
	if _, err := db.Exec(`INSERT INTO pending_events (event_id, request_id, requester_user_id, event_name) VALUES ('ev-in', 'req-in', NULL, 'REQUEST_REED')`); err != nil {
		t.Fatalf("insert foreign pending event: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO foreign_relay_requests (event_id, requesting_server_id, requesting_user_id) VALUES ('ev-in', $1, $2)`, teardownPeerID, peerViewer); err != nil {
		t.Fatalf("insert foreign relay request: %v", err)
	}

	// Our viewer's relay request still waiting on the peer.
	if _, err := db.Exec(`INSERT INTO pending_events (event_id, request_id, requester_user_id, event_name) VALUES ('ev-out', 'req-out', $1, 'REQUEST_REED')`, viewer); err != nil {
		t.Fatalf("insert local pending event: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO pending_reed_events (event_id, reed_id) VALUES ('ev-out', $1)`, peerReed); err != nil {
		t.Fatalf("insert pending reed event: %v", err)
	}
	if err := rs.db.CreateForeignPendingEvent(ctx, "ev-out", teardownPeerID, "peer-ev-out"); err != nil {
		t.Fatalf("CreateForeignPendingEvent: %v", err)
	}
}

func TestForgetPeerDropsStateInBothDirections(t *testing.T) {
	db, rs, viewer := newTeardownTestService(t)
	seedPeerResetState(t, db, rs, viewer)

	rs.forgetPeer(context.Background(), teardownPeerID)

	if n := countTeardownRows(t, db, "profile_subscriptions"); n != 0 {
		t.Fatalf("expected profile subscriptions to and from the peer gone, got %d", n)
	}
	var reedSubs []string
	rows, err := db.Query(`SELECT subscription_id FROM reed_subscriptions`)
	if err != nil {
		t.Fatalf("list reed subscriptions: %v", err)
	}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatalf("scan: %v", err)
		}
		reedSubs = append(reedSubs, id)
	}
	rows.Close()
	if len(reedSubs) != 1 || reedSubs[0] != "r-other" {
		t.Fatalf("reed subscriptions = %v, want only the other peer's [r-other]", reedSubs)
	}
	if n := countTeardownRows(t, db, "foreign_relay_requests"); n != 0 {
		t.Fatalf("expected the peer's relay requests gone, got %d", n)
	}
	if n := countTeardownRows(t, db, "pending_events"); n != 0 {
		t.Fatalf("expected pending events in both directions gone, got %d", n)
	}
}

func TestForgetPeerReportsLocalViewers(t *testing.T) {
	db, rs, viewer := newTeardownTestService(t)
	seedPeerResetState(t, db, rs, viewer)

	viewers, err := rs.db.ForgetPeerRealtimeState(context.Background(), teardownPeerID)
	if err != nil {
		t.Fatalf("ForgetPeerRealtimeState: %v", err)
	}
	if len(viewers) != 1 || viewers[0] != viewer {
		t.Fatalf("viewers = %v, want [%s]", viewers, viewer)
	}
}

func postRealtimeReset(t *testing.T, h *Handlers, peerServerID, reason string) int {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/federation/relay/realtime-reset", strings.NewReader(`{"reason":"`+reason+`"}`))
	rr := httptest.NewRecorder()
	h.RealtimeResetFromPeer(rr, withPeer(req, peerServerID))
	return rr.Code
}

func peerDown(t *testing.T, db *sql.DB, serverID string) bool {
	t.Helper()
	var down bool
	if err := db.QueryRow(`SELECT down_at IS NOT NULL FROM servers WHERE id = $1`, serverID).Scan(&down); err != nil {
		t.Fatalf("read down_at: %v", err)
	}
	return down
}

// Shutdown marks the peer down and forgets it; boot brings it back up.
func TestRealtimeResetFromPeer(t *testing.T) {
	db, rs, viewer := newTeardownTestService(t)
	seedPeerResetState(t, db, rs, viewer)
	h := &Handlers{
		services:      &Services{db: rs.db, log: NewLoggingService()},
		metrics:       metrics.Noop{},
		realtimeRelay: rs,
	}

	if code := postRealtimeReset(t, h, teardownPeerID, realtimeResetShutdown); code != http.StatusNoContent {
		t.Fatalf("shutdown status = %d, want 204", code)
	}
	if !peerDown(t, db, teardownPeerID) {
		t.Fatal("expected shutdown to mark the peer down")
	}
	if n := countTeardownRows(t, db, "profile_subscriptions"); n != 0 {
		t.Fatalf("expected shutdown to forget the peer's subscriptions, got %d", n)
	}

	if code := postRealtimeReset(t, h, teardownPeerID, realtimeResetBoot); code != http.StatusNoContent {
		t.Fatalf("boot status = %d, want 204", code)
	}
	if peerDown(t, db, teardownPeerID) {
		t.Fatal("expected boot to mark the peer up")
	}

	if code := postRealtimeReset(t, h, teardownPeerID, "reboot"); code != http.StatusBadRequest {
		t.Fatalf("unknown reason status = %d, want 400", code)
	}
}

// Boot starts from nothing, foreign viewers included.
func TestClearRealtimeState(t *testing.T) {
	db, rs, viewer := newTeardownTestService(t)
	seedPeerResetState(t, db, rs, viewer)

	if err := rs.db.ClearRealtimeState(context.Background()); err != nil {
		t.Fatalf("ClearRealtimeState: %v", err)
	}
	for _, table := range []string{"online_users", "profile_subscriptions", "reed_subscriptions", "pending_events", "foreign_relay_requests", "foreign_pending_events"} {
		if n := countTeardownRows(t, db, table); n != 0 {
			t.Fatalf("expected %s empty after boot clear, got %d", table, n)
		}
	}
}
