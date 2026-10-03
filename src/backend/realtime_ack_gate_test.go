//go:build !ops && !ripplescleanup

package main

import (
	"context"
	"database/sql"
	"testing"
)

func TestAckAcceptable(t *testing.T) {
	reader := "reader@home"
	cases := []struct {
		name string
		pe   pendingEvent
		user string
		want bool
	}{
		{"requester after relay", pendingEvent{RequesterUserID: reader, EventName: string(followReedEvent), Dispatched: true, Relayed: true}, reader, true},
		{"requester before relay", pendingEvent{RequesterUserID: reader, EventName: string(followReedEvent), Dispatched: true}, reader, false},
		{"someone else", pendingEvent{RequesterUserID: reader, EventName: string(followReedEvent), Dispatched: true, Relayed: true}, "holder@home", false},
		{"foreign-attributed", pendingEvent{EventName: string(followReedEvent), Relayed: true}, "", false},
		{"removal after send", pendingEvent{RequesterUserID: reader, EventName: string(reedRemovedEvent), Dispatched: true}, reader, true},
		{"removal before send", pendingEvent{RequesterUserID: reader, EventName: string(accountRemovedEvent)}, reader, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := ackAcceptable(&pendingSubject{pendingEvent: c.pe}, c.user); got != c.want {
				t.Fatalf("ackAcceptable = %v, want %v", got, c.want)
			}
		})
	}
}

// ackFixture is a fold fixture with one reader whose request crossed to the peer.
func newAckFixture(t *testing.T) (*foreignFoldFixture, string, string) {
	t.Helper()
	f := newForeignFoldFixture(t)
	reader := f.onlineUser(t, "reader")
	f.request(t, reader)
	var eventID string
	if err := f.db.QueryRow(`SELECT event_id FROM pending_events WHERE requester_user_id = $1`, reader).Scan(&eventID); err != nil {
		t.Fatalf("find reader event: %v", err)
	}
	return f, reader, eventID
}

func (f *foreignFoldFixture) allocated(t *testing.T, userID string) bool {
	t.Helper()
	var n int
	if err := f.db.QueryRow(`SELECT COUNT(*) FROM reed_allocations WHERE reed_id = $1 AND holder_user_id = $2`, f.reedID, userID).Scan(&n); err != nil {
		t.Fatalf("count allocations: %v", err)
	}
	return n > 0
}

func TestDataAckBeforeRelayIgnored(t *testing.T) {
	f, reader, eventID := newAckFixture(t)

	f.rs.handleDataAck(&realtimeClient{userID: reader}, eventID)

	if f.allocated(t, reader) {
		t.Fatal("reader allocated without anything relayed")
	}
	if f.pendingFor(t, reader) != 1 {
		t.Fatal("pending event must survive an early ack")
	}
}

func TestDataAckFromOtherUserIgnored(t *testing.T) {
	f, reader, eventID := newAckFixture(t)
	other := f.onlineUser(t, "other")
	if err := f.rs.db.MarkEventRelayed(context.Background(), eventID); err != nil {
		t.Fatalf("MarkEventRelayed: %v", err)
	}

	f.rs.handleDataAck(&realtimeClient{userID: other}, eventID)

	if f.allocated(t, other) {
		t.Fatal("a user who didn't request the reed was allocated it")
	}
	if f.pendingFor(t, reader) != 1 {
		t.Fatal("another user's ack must not resolve the reader's event")
	}
}

func TestDataAckAfterRelayAllocates(t *testing.T) {
	f, reader, eventID := newAckFixture(t)
	if err := f.rs.db.MarkEventRelayed(context.Background(), eventID); err != nil {
		t.Fatalf("MarkEventRelayed: %v", err)
	}

	f.rs.handleDataAck(&realtimeClient{userID: reader}, eventID)

	if !f.allocated(t, reader) {
		t.Fatal("reader not allocated after a relayed delivery was acked")
	}
	if f.pendingFor(t, reader) != 0 {
		t.Fatal("acked event should be deleted")
	}
}

func TestDataInvalidFromOtherUserIgnored(t *testing.T) {
	f, reader, eventID := newAckFixture(t)
	other := f.onlineUser(t, "other")

	f.rs.handleDataInvalid(&realtimeClient{userID: other}, eventID)

	if f.pendingFor(t, reader) != 1 {
		t.Fatal("another user's DATA_INVALID must not cancel the reader's delivery")
	}
}

// A reed relayed by a local holder is reset and asked for again, not dropped.
func TestDataInvalidResetsLocalRelay(t *testing.T) {
	f := newForeignFoldFixture(t)
	ctx := context.Background()
	holder := f.onlineUser(t, "holder")
	if err := f.rs.db.UpsertReedIdentity(ctx, f.reedID); err != nil {
		t.Fatalf("UpsertReedIdentity: %v", err)
	}
	if _, err := f.rs.db.AllocateReed(ctx, f.reedID, holder); err != nil {
		t.Fatalf("AllocateReed: %v", err)
	}
	reader := f.onlineUser(t, "reader")
	f.request(t, reader)
	var eventID string
	if err := f.db.QueryRow(`SELECT event_id FROM pending_events WHERE requester_user_id = $1`, reader).Scan(&eventID); err != nil {
		t.Fatalf("find reader event: %v", err)
	}
	if _, err := f.db.Exec(`UPDATE pending_events SET dispatched_at = NOW(), relayed_at = NOW() WHERE event_id = $1`, eventID); err != nil {
		t.Fatalf("mark delivered: %v", err)
	}

	f.rs.handleDataInvalid(&realtimeClient{userID: reader}, eventID)

	var dispatched, relayed bool
	if err := f.db.QueryRow(`SELECT dispatched_at IS NOT NULL, relayed_at IS NOT NULL FROM pending_events WHERE event_id = $1`, eventID).Scan(&dispatched, &relayed); err != nil {
		t.Fatalf("event should survive DATA_INVALID: %v", err)
	}
	if dispatched || relayed {
		t.Fatalf("dispatched=%v relayed=%v, want both cleared", dispatched, relayed)
	}
	if f.allocated(t, reader) {
		t.Fatal("DATA_INVALID must never allocate")
	}
}

// newRelayDispatchFixture has a local holder with an in-flight relay request
// for the reader's event.
func newRelayDispatchFixture(t *testing.T) (*foreignFoldFixture, string, string) {
	t.Helper()
	f := newForeignFoldFixture(t)
	ctx := context.Background()
	holder := f.onlineUser(t, "holder")
	if err := f.rs.db.UpsertReedIdentity(ctx, f.reedID); err != nil {
		t.Fatalf("UpsertReedIdentity: %v", err)
	}
	if _, err := f.rs.db.AllocateReed(ctx, f.reedID, holder); err != nil {
		t.Fatalf("AllocateReed: %v", err)
	}
	reader := f.onlineUser(t, "reader")
	f.request(t, reader)
	var eventID string
	if err := f.db.QueryRow(`SELECT event_id FROM pending_events WHERE requester_user_id = $1`, reader).Scan(&eventID); err != nil {
		t.Fatalf("find reader event: %v", err)
	}
	if _, err := f.db.Exec(`UPDATE pending_events SET dispatched_at = NOW(), dispatched_to = $2 WHERE event_id = $1`, eventID, holder); err != nil {
		t.Fatalf("mark dispatched: %v", err)
	}
	return f, holder, eventID
}

func (f *foreignFoldFixture) dispatchState(t *testing.T, eventID string) (dispatchedTo string, relayed bool) {
	t.Helper()
	var to sql.NullString
	if err := f.db.QueryRow(`SELECT dispatched_to, relayed_at IS NOT NULL FROM pending_events WHERE event_id = $1`, eventID).Scan(&to, &relayed); err != nil {
		t.Fatalf("read dispatch state: %v", err)
	}
	return to.String, relayed
}

func TestRelayResponseFromOtherUserIgnored(t *testing.T) {
	f, holder, eventID := newRelayDispatchFixture(t)
	other := f.onlineUser(t, "other")

	f.rs.handleRelayResponse(&realtimeClient{userID: other}, eventID, "ciphertext")

	if to, relayed := f.dispatchState(t, eventID); relayed || to != holder {
		t.Fatalf("dispatched_to=%q relayed=%v: a non-holder's response was accepted", to, relayed)
	}
}

func TestRelayMissFromOtherUserIgnored(t *testing.T) {
	f, holder, eventID := newRelayDispatchFixture(t)
	other := f.onlineUser(t, "other")

	f.rs.handleRelayMiss(other, eventID)

	if to, _ := f.dispatchState(t, eventID); to != holder {
		t.Fatalf("dispatched_to=%q: a non-holder's miss reset the dispatch", to)
	}
	if !f.allocated(t, holder) {
		t.Fatal("a non-holder's miss dropped the holder's allocation")
	}
}

func TestRelayMissFromHolderResetsDispatch(t *testing.T) {
	f, holder, eventID := newRelayDispatchFixture(t)

	f.rs.handleRelayMiss(holder, eventID)

	if f.allocated(t, holder) {
		t.Fatal("holder's miss should drop their allocation")
	}
}
