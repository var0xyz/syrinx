//go:build !ops && !ripplescleanup && !challengescleanup

package main

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
)

// foreignFoldFixture is a server with a foreign reed from teardownPeerID
// and a stub home server that counts crossings instead of calling out.
type foreignFoldFixture struct {
	db        *sql.DB
	rs        *realtimeService
	reedID    string
	crossings []string
}

func newForeignFoldFixture(t *testing.T) *foreignFoldFixture {
	t.Helper()
	db, rs, _ := newTeardownTestService(t)
	author := string(canonicalID(teardownPeerID, "bob"))
	insertTeardownIdentity(t, db, author, teardownPeerID)
	f := &foreignFoldFixture{
		db:     db,
		rs:     rs,
		reedID: string(appendEntity(identityID(author), "01a026d4-406f-744b-b730-fcd241bf2590")),
	}
	rs.SetForeignRequestReedHook(func(_ context.Context, reedID, requesterUserID, _ string) (realtimeForeignRequestResult, string, error) {
		f.crossings = append(f.crossings, requesterUserID)
		return realtimeForeignRequestOK, fmt.Sprintf("peer-ev-%d", len(f.crossings)), nil
	})
	return f
}

// onlineUser registers a local user who is connected, as the dispatch
// queries see it.
func (f *foreignFoldFixture) onlineUser(t *testing.T, name string) string {
	t.Helper()
	id := string(canonicalID(teardownHomeID, name))
	insertTeardownIdentity(t, f.db, id, teardownHomeID)
	if _, err := f.db.Exec(`INSERT INTO users (id) VALUES ($1) ON CONFLICT DO NOTHING`, id); err != nil {
		t.Fatalf("insert user %s: %v", name, err)
	}
	if err := f.rs.db.MarkUserOnline(context.Background(), id); err != nil {
		t.Fatalf("MarkUserOnline %s: %v", name, err)
	}
	return id
}

func (f *foreignFoldFixture) request(t *testing.T, userID string) {
	t.Helper()
	client := &realtimeClient{userID: userID}
	f.rs.handleForeignRequestReedFromClient(client, generateRealtimeEventID(userID), f.reedID, teardownPeerID)
}

func (f *foreignFoldFixture) pendingFor(t *testing.T, userID string) int {
	t.Helper()
	var n int
	if err := f.db.QueryRow(`
		SELECT COUNT(*) FROM pending_events pe
		JOIN pending_reed_events pre ON pre.event_id = pe.event_id
		WHERE pe.requester_user_id = $1 AND pre.reed_id = $2`, userID, f.reedID).Scan(&n); err != nil {
		t.Fatalf("count pending events: %v", err)
	}
	return n
}

// Once someone here holds the reed, a new request is served locally.
func TestForeignRequestUsesLocalHolder(t *testing.T) {
	f := newForeignFoldFixture(t)
	holder := f.onlineUser(t, "holder")
	if err := f.rs.db.UpsertReedIdentity(context.Background(), f.reedID); err != nil {
		t.Fatalf("UpsertReedIdentity: %v", err)
	}
	if _, err := f.rs.db.AllocateReed(context.Background(), f.reedID, holder); err != nil {
		t.Fatalf("AllocateReed: %v", err)
	}

	requester := f.onlineUser(t, "reader")
	f.request(t, requester)

	if len(f.crossings) != 0 {
		t.Fatalf("crossings = %v, want none while a local holder is online", f.crossings)
	}
	if n := f.pendingFor(t, requester); n != 1 {
		t.Fatalf("expected one local event for the requester, got %d", n)
	}
}

// With nobody holding it here, at most three copies cross at once.
func TestForeignRequestCapsCrossings(t *testing.T) {
	f := newForeignFoldFixture(t)
	var readers []string
	for i := 0; i < 5; i++ {
		r := f.onlineUser(t, fmt.Sprintf("reader%d", i))
		readers = append(readers, r)
		f.request(t, r)
	}

	if len(f.crossings) != maxForeignCrossings {
		t.Fatalf("crossings = %d, want %d", len(f.crossings), maxForeignCrossings)
	}
	for i, r := range readers {
		if n := f.pendingFor(t, r); n != 1 {
			t.Fatalf("reader%d has %d events, want 1 (crossing or waiting)", i, n)
		}
	}
}

// A crossing that ends without an ack hands its slot to the oldest
// waiting requester.
func TestForeignCrossingFailurePromotesWaiting(t *testing.T) {
	f := newForeignFoldFixture(t)
	for i := 0; i < 4; i++ {
		f.request(t, f.onlineUser(t, fmt.Sprintf("reader%d", i)))
	}
	if len(f.crossings) != maxForeignCrossings {
		t.Fatalf("crossings = %d, want %d", len(f.crossings), maxForeignCrossings)
	}

	if _, err := f.rs.HandleForeignRelayNotHeld(context.Background(), "peer-ev-1", teardownPeerID); err != nil {
		t.Fatalf("HandleForeignRelayNotHeld: %v", err)
	}

	waiting := string(canonicalID(teardownHomeID, "reader3"))
	if len(f.crossings) != maxForeignCrossings+1 || f.crossings[maxForeignCrossings] != waiting {
		t.Fatalf("crossings = %v, want the waiting reader3 promoted", f.crossings)
	}
}

// An event already crossing must not also be handed to a local holder.
func TestHolderQueueSkipsCrossingEvents(t *testing.T) {
	f := newForeignFoldFixture(t)
	requester := f.onlineUser(t, "reader")
	f.request(t, requester)
	if len(f.crossings) != 1 {
		t.Fatalf("crossings = %d, want 1", len(f.crossings))
	}

	holder := f.onlineUser(t, "holder")
	if _, err := f.rs.db.AllocateReed(context.Background(), f.reedID, holder); err != nil {
		t.Fatalf("AllocateReed: %v", err)
	}
	pe, err := f.rs.db.GetNextPendingForHolder(context.Background(), holder)
	if err != nil {
		t.Fatalf("GetNextPendingForHolder: %v", err)
	}
	if pe != nil {
		t.Fatalf("holder was handed crossing event %s", pe.EventID)
	}
}

// fillCrossings sends four requests: three cross and reader3 waits.
func (f *foreignFoldFixture) fillCrossings(t *testing.T) (carrier, waiting string) {
	t.Helper()
	for i := 0; i < 4; i++ {
		f.request(t, f.onlineUser(t, fmt.Sprintf("reader%d", i)))
	}
	if len(f.crossings) != maxForeignCrossings {
		t.Fatalf("crossings = %d, want %d", len(f.crossings), maxForeignCrossings)
	}
	return f.crossings[0], string(canonicalID(teardownHomeID, "reader3"))
}

// A carrier who disconnects hands their slot to the next waiting reader.
func TestForeignCarrierDisconnectPromotesWaiting(t *testing.T) {
	f := newForeignFoldFixture(t)
	carrier, waiting := f.fillCrossings(t)

	f.rs.teardownUser(carrier)
	f.rs.peerCalls.Wait()
	if err := f.rs.db.MarkUserOffline(context.Background(), carrier); err != nil {
		t.Fatalf("MarkUserOffline: %v", err)
	}

	if len(f.crossings) != maxForeignCrossings+1 || f.crossings[maxForeignCrossings] != waiting {
		t.Fatalf("crossings = %v, want %s promoted", f.crossings, waiting)
	}
}

// A crossed copy that fails verification frees its slot too.
func TestForeignInvalidCopyPromotesWaiting(t *testing.T) {
	f := newForeignFoldFixture(t)
	carrier, waiting := f.fillCrossings(t)

	var eventID string
	if err := f.db.QueryRow(`SELECT event_id FROM pending_events WHERE requester_user_id = $1`, carrier).Scan(&eventID); err != nil {
		t.Fatalf("find carrier event: %v", err)
	}
	f.rs.handleDataInvalid(&realtimeClient{userID: carrier}, eventID)

	if len(f.crossings) != maxForeignCrossings+1 || f.crossings[maxForeignCrossings] != waiting {
		t.Fatalf("crossings = %v, want %s promoted", f.crossings, waiting)
	}
}

// A foreign profile page is folded like any other request: reeds the
// viewer holds are skipped, and one with a local holder doesn't cross.
func TestForeignProfilePageFolds(t *testing.T) {
	f := newForeignFoldFixture(t)
	ctx := context.Background()
	author := string(canonicalID(teardownPeerID, "bob"))
	reeds := []string{f.reedID}
	for _, suffix := range []string{"2593", "2594"} {
		reeds = append(reeds, string(appendEntity(identityID(author), "01a026d4-406f-744b-b730-fcd241bf"+suffix)))
	}
	for _, id := range reeds {
		if err := f.rs.db.UpsertReedIdentity(ctx, id); err != nil {
			t.Fatalf("UpsertReedIdentity: %v", err)
		}
	}
	f.rs.SetForeignProfilePageHook(func(context.Context, string, string, int) ([]string, int, bool, error) {
		return reeds, len(reeds), false, nil
	})

	viewer := f.onlineUser(t, "viewer2")
	holder := f.onlineUser(t, "holder")
	if _, err := f.rs.db.AllocateReed(ctx, reeds[0], viewer); err != nil {
		t.Fatalf("AllocateReed viewer: %v", err)
	}
	if _, err := f.rs.db.AllocateReed(ctx, reeds[1], holder); err != nil {
		t.Fatalf("AllocateReed holder: %v", err)
	}

	f.rs.handleForeignProfilePageFromClient(&realtimeClient{userID: viewer}, author, teardownPeerID, 1)

	if len(f.crossings) != 1 {
		t.Fatalf("crossings = %v, want only the reed nobody here holds", f.crossings)
	}
}
