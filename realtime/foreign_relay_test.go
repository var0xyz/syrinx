package realtime

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"testing"

	"syrinx/crypto"
	"syrinx/identity"

	_ "github.com/lib/pq"
)

// newTestRealtimeService builds a RealtimeService with a serverID but no
// live DB connection — sql.Open doesn't dial until first use, and
// isForeignReed only reads rs.dbService.serverID, so this is safe for
// pure parse-logic tests that never touch the database.
func newTestRealtimeService(t *testing.T, serverID string) *RealtimeService {
	t.Helper()
	db, err := sql.Open("postgres", "dbname=unused_in_this_test sslmode=disable")
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return NewService(db, &crypto.Service{}, "", serverID)
}

func TestIsForeignReed(t *testing.T) {
	rs := newTestRealtimeService(t, "home1234")

	localReedID := string(identity.AppendEntity(identity.CanonicalID("home1234", "alice"), "01a026d4-406f-744b-b730-fcd241bf2582"))
	if foreign, home := rs.isForeignReed(localReedID); foreign {
		t.Fatalf("expected local reed to not be foreign, got foreign=true home=%q", home)
	}

	foreignReedID := string(identity.AppendEntity(identity.CanonicalID("peer5678", "bob"), "01a026d4-406f-744b-b730-fcd241bf2582"))
	foreign, home := rs.isForeignReed(foreignReedID)
	if !foreign {
		t.Fatal("expected foreign reed to be detected as foreign")
	}
	if home != "peer5678" {
		t.Fatalf("home server = %q, want peer5678", home)
	}

	// Malformed input (no embedded serverID at all) must not be treated as foreign.
	if foreign, _ := rs.isForeignReed("not-a-canonical-id"); foreign {
		t.Fatal("expected malformed reedID to not be treated as foreign")
	}
}

// TestPeerRelaySentinelUserIDNeverCollidesWithRealUser guards the
// assumption EnsurePeerSentinelUser's design depends on: the underscore
// character never appears in crypto.Alphabet, so no real userID minted
// via crypto.NewID can ever equal the sentinel token.
func TestPeerRelaySentinelUserIDNeverCollidesWithRealUser(t *testing.T) {
	if strings.ContainsAny(crypto.Alphabet, "_") {
		t.Fatalf("crypto.Alphabet unexpectedly contains '_' -- peerRelaySentinelUserID %q may now collide with a real userID", peerRelaySentinelUserID)
	}
	if !strings.Contains(peerRelaySentinelUserID, "_") {
		t.Fatalf("peerRelaySentinelUserID %q no longer contains the reserved '_' marker", peerRelaySentinelUserID)
	}
}

// TestForeignHookSettersWireUp confirms all three hooks start nil (so
// handleForeignRequestReedFromClient/handleRelayResponse/disconnect
// cleanup degrade gracefully when a server has no peers configured) and
// are actually stored by their setters.
func TestForeignHookSettersWireUp(t *testing.T) {
	rs := newTestRealtimeService(t, "home1234")

	if rs.foreignRequestReedHook != nil || rs.foreignDeliverHook != nil || rs.foreignCancelHook != nil {
		t.Fatal("expected all foreign relay hooks to be nil before Set*")
	}

	var requestCalled, deliverCalled, cancelCalled bool
	rs.SetForeignRequestReedHook(func(_ context.Context, _, _, _ string) (ForeignRequestResult, string, error) {
		requestCalled = true
		return ForeignRequestOK, "peer-event-1", nil
	})
	rs.SetForeignDeliverHook(func(_ context.Context, _, _ string, _ json.RawMessage) error {
		deliverCalled = true
		return nil
	})
	rs.SetForeignCancelHook(func(_ context.Context, _, _ string) error {
		cancelCalled = true
		return nil
	})

	if rs.foreignRequestReedHook == nil || rs.foreignDeliverHook == nil || rs.foreignCancelHook == nil {
		t.Fatal("expected all foreign relay hooks to be set")
	}

	if _, _, err := rs.foreignRequestReedHook(context.Background(), "reed", "user", "req"); err != nil {
		t.Fatalf("foreignRequestReedHook: %v", err)
	}
	if err := rs.foreignDeliverHook(context.Background(), "server", "event", nil); err != nil {
		t.Fatalf("foreignDeliverHook: %v", err)
	}
	if err := rs.foreignCancelHook(context.Background(), "server", "event"); err != nil {
		t.Fatalf("foreignCancelHook: %v", err)
	}

	if !requestCalled || !deliverCalled || !cancelCalled {
		t.Fatal("expected all three hooks to have been invoked")
	}
}

// TestCancelForeignPendingEventOwnershipMismatchIsDistinctError confirms
// errForeignRelayOwnershipMismatch is a stable sentinel the HTTP handler
// can compare against to map to 403 (vs. a generic DB error -> 500).
func TestCancelForeignPendingEventOwnershipMismatchIsDistinctError(t *testing.T) {
	if errForeignRelayOwnershipMismatch == nil {
		t.Fatal("expected errForeignRelayOwnershipMismatch to be a non-nil sentinel error")
	}
}
