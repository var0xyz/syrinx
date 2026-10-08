//go:build !ops && !ripplescleanup && !challengescleanup

package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"syrinx/observability/metrics"
)

func newReedTestHandlers(f *foreignFoldFixture) *Handlers {
	return &Handlers{
		services:      &Services{db: f.rs.db, log: NewLoggingService()},
		metrics:       metrics.Noop{},
		realtimeRelay: f.rs,
		broadcastChan: make(chan realtimeBroadcastMessage, 16),
	}
}

func postNewReed(t *testing.T, h *Handlers, peerServerID string, payload relayNewReedPayload) int {
	t.Helper()
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/federation/relay/new-reed", strings.NewReader(string(body)))
	rr := httptest.NewRecorder()
	h.NewReedFromPeer(rr, withPeer(req, peerServerID))
	return rr.Code
}

func (f *foreignFoldFixture) eventNameFor(t *testing.T, userID string) string {
	t.Helper()
	var name string
	err := f.db.QueryRow(`
		SELECT pe.event_name FROM pending_events pe
		JOIN pending_reed_events pre ON pre.event_id = pe.event_id
		WHERE pe.requester_user_id = $1 AND pre.reed_id = $2`, userID, f.reedID).Scan(&name)
	if err != nil {
		return ""
	}
	return name
}

// A mention is stored for later catch-up and dispatched to an online user.
func TestNewReedFromPeerRecordsAndDispatchesMention(t *testing.T) {
	f := newForeignFoldFixture(t)
	h := newReedTestHandlers(f)
	mentioned := f.onlineUser(t, "carol")
	if _, err := f.db.Exec(`UPDATE users SET username = 'carol' WHERE id = $1`, mentioned); err != nil {
		t.Fatalf("set username: %v", err)
	}

	code := postNewReed(t, h, teardownPeerID, relayNewReedPayload{
		ReedID:   f.reedID,
		AuthorID: string(canonicalID(teardownPeerID, "bob")),
		Mentions: []string{mentioned, string(canonicalID(teardownOtherPeerID, "dave"))},
	})
	if code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", code)
	}

	var rows int
	if err := f.db.QueryRow(`SELECT COUNT(*) FROM reed_mentions WHERE mentioning_reed_id = $1`, f.reedID).Scan(&rows); err != nil {
		t.Fatalf("count mentions: %v", err)
	}
	if rows != 1 {
		t.Fatalf("mention rows = %d, want only the local user's", rows)
	}
	if name := f.eventNameFor(t, mentioned); name != string(mentionEvent) {
		t.Fatalf("mentioned user's event = %q, want %q", name, mentionEvent)
	}
}

// Followers of the foreign author get it once, and not also as broadcast.
func TestNewReedFromPeerDispatchesToFollowers(t *testing.T) {
	f := newForeignFoldFixture(t)
	h := newReedTestHandlers(f)
	author := string(canonicalID(teardownPeerID, "bob"))
	follower := f.onlineUser(t, "erin")
	if _, err := f.db.Exec(`INSERT INTO user_following (user_id, following_user_id) VALUES ($1, $2)`, follower, author); err != nil {
		t.Fatalf("follow: %v", err)
	}

	if code := postNewReed(t, h, teardownPeerID, relayNewReedPayload{ReedID: f.reedID, AuthorID: author}); code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", code)
	}
	if name := f.eventNameFor(t, follower); name != string(followReedEvent) {
		t.Fatalf("follower's event = %q, want %q", name, followReedEvent)
	}
	if n := f.pendingFor(t, follower); n != 1 {
		t.Fatalf("follower has %d events, want 1", n)
	}
}

// A peer may only announce its own users' reeds.
func TestNewReedFromPeerRejectsOtherServersReed(t *testing.T) {
	f := newForeignFoldFixture(t)
	h := newReedTestHandlers(f)
	code := postNewReed(t, h, teardownOtherPeerID, relayNewReedPayload{
		ReedID:   f.reedID,
		AuthorID: string(canonicalID(teardownPeerID, "bob")),
	})
	if code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", code)
	}
}

// Removing a foreign reed drops the reply and echo references it left.
func TestDropForeignReedReferences(t *testing.T) {
	f := newForeignFoldFixture(t)
	h := newReedTestHandlers(f)
	ctx := context.Background()
	author := string(canonicalID(teardownPeerID, "bob"))
	if err := f.rs.db.UpsertReedIdentity(ctx, f.reedID); err != nil {
		t.Fatalf("UpsertReedIdentity: %v", err)
	}
	local := string(canonicalID(teardownHomeID, "alice"))
	insertTeardownIdentity(t, f.db, local, teardownHomeID)
	parent := string(appendEntity(identityID(local), "01a026d4-406f-744b-b730-fcd241bf2591"))
	echoed := string(appendEntity(identityID(local), "01a026d4-406f-744b-b730-fcd241bf2592"))
	for _, id := range []string{parent, echoed} {
		if _, err := f.db.Exec(`INSERT INTO reed_identities (id, server_id, author_id) VALUES ($1, $2, $3)`, id, teardownHomeID, local); err != nil {
			t.Fatalf("insert reed identity: %v", err)
		}
	}
	if _, err := f.db.Exec(`INSERT INTO reed_replies (root_id, reed_id, parent_reed_id, timestamp) VALUES ($1, $2, $1, NOW())`, parent, f.reedID); err != nil {
		t.Fatalf("insert reply: %v", err)
	}
	if err := f.rs.db.InsertForeignEcho(ctx, f.reedID, echoed, author, local, false, time.Now().UTC()); err != nil {
		t.Fatalf("InsertForeignEcho: %v", err)
	}

	if err := h.dropForeignReedReferences(ctx, teardownPeerID, f.reedID, "", reedRemovalCert{ReedID: f.reedID}); err != nil {
		t.Fatalf("dropForeignReedReferences: %v", err)
	}
	for _, table := range []string{"reed_replies", "reed_echoes"} {
		if n := countTeardownRows(t, f.db, table); n != 0 {
			t.Fatalf("expected %s cleared, got %d", table, n)
		}
	}
}

// An offline user mentioned in a peer's reed gets it on their next SYNC,
// and so does an offline follower of its author.
func TestForeignReedCaughtUpOnSync(t *testing.T) {
	f := newForeignFoldFixture(t)
	h := newReedTestHandlers(f)
	ctx := context.Background()
	author := string(canonicalID(teardownPeerID, "bob"))

	mentioned := f.onlineUser(t, "carol")
	follower := f.onlineUser(t, "erin")
	if _, err := f.db.Exec(`INSERT INTO user_following (user_id, following_user_id) VALUES ($1, $2)`, follower, author); err != nil {
		t.Fatalf("follow: %v", err)
	}
	for _, u := range []string{mentioned, follower} {
		if err := f.rs.db.MarkUserOffline(ctx, u); err != nil {
			t.Fatalf("MarkUserOffline: %v", err)
		}
	}

	if code := postNewReed(t, h, teardownPeerID, relayNewReedPayload{ReedID: f.reedID, AuthorID: author, Mentions: []string{mentioned}}); code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", code)
	}
	if len(f.crossings) != 0 {
		t.Fatalf("crossings = %v, want none while both are offline", f.crossings)
	}

	for _, u := range []string{mentioned, follower} {
		if err := f.rs.db.MarkUserOnline(ctx, u); err != nil {
			t.Fatalf("MarkUserOnline: %v", err)
		}
		f.rs.catchUp(u, generateRealtimeEventID(u))
	}
	if name := f.eventNameFor(t, mentioned); name != string(mentionEvent) {
		t.Fatalf("mentioned user's catch-up event = %q, want %q", name, mentionEvent)
	}
	if name := f.eventNameFor(t, follower); name != string(followReedEvent) {
		t.Fatalf("follower's catch-up event = %q, want %q", name, followReedEvent)
	}
}
