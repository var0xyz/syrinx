//go:build !ops

package main

import (
	"context"
	"sync"
	"testing"

	"syrinx/observability/metrics"
)

// lagRecorder sums the relay and federation lag counters as they move.
type lagRecorder struct {
	metrics.Noop
	mu         sync.Mutex
	relay      int64
	federation map[string]int64
}

func (r *lagRecorder) RelayEvent(_ context.Context, lifecycle metrics.RelayLifecycle, _ string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if lifecycle == metrics.RelayEventDeleted {
		r.relay--
	} else {
		r.relay++
	}
}

func (r *lagRecorder) FederationPending(_ context.Context, direction metrics.Direction, peer string, delta int64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.federation[string(direction)+" "+peer] += delta
}

func (r *lagRecorder) check(t *testing.T, relay, out, in int64) {
	t.Helper()
	r.mu.Lock()
	defer r.mu.Unlock()
	gotOut := r.federation["out "+teardownPeerID]
	gotIn := r.federation["in "+teardownPeerID]
	if r.relay != relay || gotOut != out || gotIn != in {
		t.Fatalf("relay=%d out=%d in=%d, want %d %d %d", r.relay, gotOut, gotIn, relay, out, in)
	}
}

// Going offline and forgetting a peer, which used to drop pending events
// by cascade, bring both lag counts back to zero.
func TestRelayAndFederationLagBalance(t *testing.T) {
	db, rs, viewer := newTeardownTestService(t)
	rec := &lagRecorder{federation: map[string]int64{}}
	rs.SetMetrics(rec)
	ctx := context.Background()

	// Our viewer's request, crossed to the peer.
	if err := rs.createPendingAccountEvent(ctx, "ev-out", "req-out", viewer, viewer); err != nil {
		t.Fatalf("createPendingAccountEvent: %v", err)
	}
	if err := rs.createForeignPendingEvent(ctx, "ev-out", teardownPeerID, "peer-ev-out"); err != nil {
		t.Fatalf("createForeignPendingEvent: %v", err)
	}
	// The peer's request, waiting on us.
	if _, err := db.Exec(`INSERT INTO pending_events (event_id, request_id, requester_user_id, event_name) VALUES ('ev-in', 'req-in', NULL, 'request_reed')`); err != nil {
		t.Fatalf("insert foreign pending event: %v", err)
	}
	rec.RelayEvent(ctx, metrics.RelayEventCreated, "request_reed")
	if err := rs.recordForeignRelayRequest(ctx, "ev-in", teardownPeerID, "peer-user@"+teardownPeerID, "k1"); err != nil {
		t.Fatalf("recordForeignRelayRequest: %v", err)
	}
	rec.check(t, 2, 1, 1)

	deleted, err := rs.db.MarkUserOffline(ctx, viewer)
	if err != nil {
		t.Fatalf("MarkUserOffline: %v", err)
	}
	rs.recordPendingDeleted(ctx, deleted)
	rec.check(t, 1, 0, 1)

	rs.forgetPeer(ctx, teardownPeerID)
	rec.check(t, 0, 0, 0)
}
