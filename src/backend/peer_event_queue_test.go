//go:build !ops && !ripplescleanup && !challengescleanup

package main

import (
	"context"
	"database/sql"
	"errors"
	"sync"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"
)

const queuePeerB = "peerb999"

// queueFixture is the delivery fixture with a second peer.
func newQueueFixture(t *testing.T) *deliveryFixture {
	t.Helper()
	f := newDeliveryFixture(t)
	if _, err := f.db.Exec(`
		INSERT INTO servers (id, name, self, base_url, frontend_url)
		VALUES ($1, $1, FALSE, 'https://b.example', 'https://b.example')`, queuePeerB); err != nil {
		t.Fatalf("insert peer b: %v", err)
	}
	peerTestMesh(t, f.db, queuePeerB)
	return f
}

// queueSender sends every event to a path naming its subject.
func queueSender(refused *[]string) func(string) (peerEventSender, bool) {
	return func(kind string) (peerEventSender, bool) {
		return peerEventSender{
			build: func(_ context.Context, ev peerEvent) (string, proto.Message, error) {
				if ev.SubjectID == "gone" {
					return "", nil, errPeerEventGone
				}
				return "/" + ev.SubjectID, nil, nil
			},
			refused: func(_ context.Context, _ string, ev peerEvent, _ proto.Message) {
				if refused != nil {
					*refused = append(*refused, ev.SubjectID)
				}
			},
		}, true
	}
}

// peerCalls answers each call with the next scripted status (0 is a
// network error) and records the paths.
type peerCalls struct {
	mu       sync.Mutex
	paths    []string
	statuses []int
	delay    time.Duration
}

func (c *peerCalls) call(_ context.Context, _, _, path string, _ proto.Message) (int, error) {
	time.Sleep(c.delay)
	c.mu.Lock()
	defer c.mu.Unlock()
	c.paths = append(c.paths, path)
	status := 204
	if len(c.statuses) > 0 {
		status, c.statuses = c.statuses[0], c.statuses[1:]
	}
	if status == 0 {
		return 0, errors.New("connection refused")
	}
	return status, nil
}

func (f *deliveryFixture) enqueue(t *testing.T, subjects ...string) {
	t.Helper()
	for _, s := range subjects {
		if err := enqueuePeerEventTx(context.Background(), f.db, peerEventServerKey, s); err != nil {
			t.Fatalf("enqueue %s: %v", s, err)
		}
	}
}

func (f *deliveryFixture) queued(t *testing.T, peerID string) int {
	t.Helper()
	n, err := f.h.services.db.CountPeerEvents(context.Background(), peerID)
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	return n
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// Every peer gets each event once, oldest first, and an answered event
// leaves the queue.
func TestPeerEventQueue_DrainsInOrderToEveryPeer(t *testing.T) {
	f := newQueueFixture(t)
	f.enqueue(t, "one", "two", "three")
	for _, peer := range []string{deliveryPeerID, queuePeerB} {
		calls := &peerCalls{}
		f.h.drainPeerEventsWith(peer, queueSender(nil), calls.call)
		if !equalStrings(calls.paths, []string{"/one", "/two", "/three"}) {
			t.Fatalf("%s got %v", peer, calls.paths)
		}
		if n := f.queued(t, peer); n != 0 {
			t.Fatalf("%s still owed %d", peer, n)
		}
	}
}

// A 5xx, a 429 or a network error stops the drain and keeps the event; a
// refusal drops it and runs the kind's refusal step.
func TestPeerEventQueue_RetriesFailuresDropsRefusals(t *testing.T) {
	for _, status := range []int{0, 503, 429} {
		f := newDeliveryFixture(t)
		f.enqueue(t, "one", "two")
		calls := &peerCalls{statuses: []int{status}}
		f.h.drainPeerEventsWith(deliveryPeerID, queueSender(nil), calls.call)
		if len(calls.paths) != 1 || f.queued(t, deliveryPeerID) != 2 {
			t.Fatalf("status %d: sent %v, %d left", status, calls.paths, f.queued(t, deliveryPeerID))
		}
	}

	f := newDeliveryFixture(t)
	f.enqueue(t, "one", "two")
	var refused []string
	calls := &peerCalls{statuses: []int{400}}
	f.h.drainPeerEventsWith(deliveryPeerID, queueSender(&refused), calls.call)
	if len(calls.paths) != 2 || f.queued(t, deliveryPeerID) != 0 || !equalStrings(refused, []string{"one"}) {
		t.Fatalf("refusal: sent %v, refused %v, %d left", calls.paths, refused, f.queued(t, deliveryPeerID))
	}
}

// An event whose subject is gone is dropped without a call.
func TestPeerEventQueue_DropsGoneSubjects(t *testing.T) {
	f := newDeliveryFixture(t)
	f.enqueue(t, "gone", "one")
	calls := &peerCalls{}
	f.h.drainPeerEventsWith(deliveryPeerID, queueSender(nil), calls.call)
	if !equalStrings(calls.paths, []string{"/one"}) || f.queued(t, deliveryPeerID) != 0 {
		t.Fatalf("sent %v, %d left", calls.paths, f.queued(t, deliveryPeerID))
	}
}

// A down peer gets nothing until its boot notice clears down_at.
func TestPeerEventQueue_WaitsForDownPeer(t *testing.T) {
	f := newDeliveryFixture(t)
	f.enqueue(t, "one")
	if _, err := f.db.Exec(`UPDATE server_mesh SET down_at = NOW() WHERE server_id = $1`, deliveryPeerID); err != nil {
		t.Fatal(err)
	}
	calls := &peerCalls{}
	f.h.drainPeerEventsWith(deliveryPeerID, queueSender(nil), calls.call)
	if len(calls.paths) != 0 || f.queued(t, deliveryPeerID) != 1 {
		t.Fatalf("down peer sent %v", calls.paths)
	}
	if _, err := f.db.Exec(`UPDATE server_mesh SET down_at = NULL WHERE server_id = $1`, deliveryPeerID); err != nil {
		t.Fatal(err)
	}
	f.h.drainPeerEventsWith(deliveryPeerID, queueSender(nil), calls.call)
	if len(calls.paths) != 1 {
		t.Fatalf("up peer sent %v", calls.paths)
	}
}

// Two drains of one peer send each event once.
func TestPeerEventQueue_OneDrainPerPeer(t *testing.T) {
	f := newDeliveryFixture(t)
	f.enqueue(t, "one", "two")
	calls := &peerCalls{delay: 50 * time.Millisecond}
	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			f.h.drainPeerEventsWith(deliveryPeerID, queueSender(nil), calls.call)
		}()
	}
	wg.Wait()
	if len(calls.paths) != 2 {
		t.Fatalf("sent %v, want each event once", calls.paths)
	}
}

// A slow peer doesn't delay another peer's drain.
func TestPeerEventQueue_SlowPeerDelaysOnlyItself(t *testing.T) {
	f := newQueueFixture(t)
	f.enqueue(t, "one")
	slow := &peerCalls{delay: 2 * time.Second}
	go f.h.drainPeerEventsWith(deliveryPeerID, queueSender(nil), slow.call)
	start := time.Now()
	fast := &peerCalls{}
	f.h.drainPeerEventsWith(queuePeerB, queueSender(nil), fast.call)
	if len(fast.paths) != 1 || time.Since(start) > time.Second {
		t.Fatalf("fast peer sent %v after %v", fast.paths, time.Since(start))
	}
}

// Revoking a peer empties its queue, and nothing is queued for it after;
// a purge cascades.
func TestPeerEventQueue_RevokeAndPurgeEmptyIt(t *testing.T) {
	f := newQueueFixture(t)
	f.enqueue(t, "one")
	if err := f.h.services.db.RevokeFederationServer(context.Background(), deliveryPeerID, nil, "bye", time.Now()); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	f.enqueue(t, "two")
	if n := f.queued(t, deliveryPeerID); n != 0 {
		t.Fatalf("revoked peer owed %d", n)
	}
	if n := f.queued(t, queuePeerB); n != 2 {
		t.Fatalf("other peer owed %d, want 2", n)
	}
	if _, err := f.db.Exec(`DELETE FROM servers WHERE id = $1`, queuePeerB); err != nil {
		t.Fatal(err)
	}
	if n := f.queued(t, queuePeerB); n != 0 {
		t.Fatalf("purged peer owed %d", n)
	}
}

// waitPeerIdle waits for a drain started in the background to release
// peerID's queue, so a test's own drain isn't skipped by its claim.
func waitPeerIdle(t *testing.T, db interface {
	QueryRow(string, ...any) *sql.Row
}, peerID string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		var free bool
		if err := db.QueryRow(`SELECT delivery_claimed_at IS NULL FROM server_mesh WHERE server_id = $1`, peerID).Scan(&free); err != nil || free {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("peer %s's queue still claimed", peerID)
}
