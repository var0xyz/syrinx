//go:build !ops && !ripplescleanup

package main

import (
	"context"
	"database/sql"
	"testing"
)

const (
	teardownHomeID = "home1234"
	teardownPeerID = "peer5678"
)

// newTeardownTestService runs the real schema, seeds this server and one
// peer, and marks a local viewer online.
func newTeardownTestService(t *testing.T) (*sql.DB, *realtimeService, string) {
	t.Helper()
	db := newTestDatabase(t, InitDB)
	for _, stmt := range []struct {
		id   string
		self bool
	}{{teardownHomeID, true}, {teardownPeerID, false}} {
		if _, err := db.Exec(`INSERT INTO servers (id, name, self) VALUES ($1, $1, $2)`, stmt.id, stmt.self); err != nil {
			t.Fatalf("insert server %s: %v", stmt.id, err)
		}
	}
	ds := NewDataService(db, teardownHomeID)
	ds.setServerIDForTest(teardownHomeID)
	rs := newRealtimeService(ds, newCryptoService(), "")

	viewer := string(canonicalID(teardownHomeID, "viewer"))
	insertTeardownIdentity(t, db, viewer, teardownHomeID)
	if err := ds.MarkUserOnline(context.Background(), viewer); err != nil {
		t.Fatalf("MarkUserOnline: %v", err)
	}
	return db, rs, viewer
}

func insertTeardownIdentity(t *testing.T, db *sql.DB, id, serverID string) {
	t.Helper()
	if _, err := db.Exec(`INSERT INTO identities (id, server_id) VALUES ($1, $2) ON CONFLICT DO NOTHING`, id, serverID); err != nil {
		t.Fatalf("insert identity %s: %v", id, err)
	}
}

func ageTeardownPresence(t *testing.T, db *sql.DB, userID string) {
	t.Helper()
	if _, err := db.Exec(
		`UPDATE online_users SET last_pong = CURRENT_TIMESTAMP - INTERVAL '5 minutes' WHERE user_id = $1`,
		userID,
	); err != nil {
		t.Fatalf("age last_pong: %v", err)
	}
}

func countTeardownRows(t *testing.T, db *sql.DB, table string) int {
	t.Helper()
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&n); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	return n
}

// Pending events cascade off online_users, so the peer must be told about
// an outstanding relay request before the presence row goes.
func TestDisconnectCancelsForeignRelayRequests(t *testing.T) {
	db, rs, viewer := newTeardownTestService(t)
	ctx := context.Background()

	if _, err := db.Exec(
		`INSERT INTO pending_events (event_id, request_id, requester_user_id, event_name) VALUES ('ev1', 'req1', $1, 'REQUEST_REED')`,
		viewer,
	); err != nil {
		t.Fatalf("insert pending event: %v", err)
	}
	if err := rs.db.CreateForeignPendingEvent(ctx, "ev1", teardownPeerID, "peer-ev1"); err != nil {
		t.Fatalf("CreateForeignPendingEvent: %v", err)
	}

	var cancelled []string
	rs.SetForeignCancelHook(func(_ context.Context, homeServerID, peerEventID string) error {
		cancelled = append(cancelled, homeServerID+"/"+peerEventID)
		return nil
	})

	rs.teardownUser(viewer)
	rs.peerCalls.Wait()
	if err := rs.db.MarkUserOffline(ctx, viewer); err != nil {
		t.Fatalf("MarkUserOffline: %v", err)
	}

	if len(cancelled) != 1 || cancelled[0] != teardownPeerID+"/peer-ev1" {
		t.Fatalf("cancelled = %v, want [%s/peer-ev1]", cancelled, teardownPeerID)
	}
	if n := countTeardownRows(t, db, "pending_events"); n != 0 {
		t.Fatalf("expected pending events cleared, got %d", n)
	}
}

// A stale user with no socket here never runs the socket's close path, so
// the reaper itself must notify peers and clear every subscription. Profile
// subscriptions are local only: their authors' servers never hear of them.
func TestReaperTearsDownUserWithoutLocalSocket(t *testing.T) {
	db, rs, viewer := newTeardownTestService(t)
	ctx := context.Background()

	localAuthor := string(canonicalID(teardownHomeID, "alice"))
	foreignAuthor := string(canonicalID(teardownPeerID, "bob"))
	insertTeardownIdentity(t, db, localAuthor, teardownHomeID)
	insertTeardownIdentity(t, db, foreignAuthor, teardownPeerID)
	foreignReed := string(appendEntity(identityID(foreignAuthor), "01a026d4-406f-744b-b730-fcd241bf2582"))
	if err := rs.db.UpsertReedIdentity(ctx, foreignReed); err != nil {
		t.Fatalf("insert reed identity: %v", err)
	}

	if _, err := rs.db.CreateProfileSubscription(ctx, "psub1", viewer, localAuthor); err != nil {
		t.Fatalf("CreateProfileSubscription: %v", err)
	}
	if _, err := rs.db.CreateProfileSubscription(ctx, "psub2", viewer, foreignAuthor); err != nil {
		t.Fatalf("CreateProfileSubscription foreign: %v", err)
	}
	if err := rs.db.CreateReedSubscription(ctx, "rsub1", viewer, foreignReed); err != nil {
		t.Fatalf("CreateReedSubscription: %v", err)
	}

	var reedUnsubs []string
	rs.SetForeignUnsubscribeReedHook(func(_ context.Context, reedID, _ string) error {
		reedUnsubs = append(reedUnsubs, reedID)
		return nil
	})

	ageTeardownPresence(t, db, viewer)
	rs.reapStalePresence()
	rs.peerCalls.Wait()

	if len(reedUnsubs) != 1 || reedUnsubs[0] != foreignReed {
		t.Fatalf("reed unsubscribes = %v, want [%s]", reedUnsubs, foreignReed)
	}
	for _, table := range []string{"profile_subscriptions", "reed_subscriptions", "online_users"} {
		if n := countTeardownRows(t, db, table); n != 0 {
			t.Fatalf("expected %s cleared, got %d", table, n)
		}
	}
}

// A heartbeat that lands between listing and evicting keeps the row.
func TestEvictStalePresenceSparesRefreshedRow(t *testing.T) {
	db, rs, viewer := newTeardownTestService(t)
	ctx := context.Background()

	ageTeardownPresence(t, db, viewer)
	stale, err := rs.db.StalePresence(ctx, realtimePresenceTTL)
	if err != nil {
		t.Fatalf("StalePresence: %v", err)
	}
	if len(stale) != 1 || stale[0] != viewer {
		t.Fatalf("stale = %v, want [%s]", stale, viewer)
	}

	if _, err := rs.db.RecordPong(ctx, viewer); err != nil {
		t.Fatalf("RecordPong: %v", err)
	}
	gone, err := rs.db.EvictStalePresence(ctx, viewer, realtimePresenceTTL)
	if err != nil {
		t.Fatalf("EvictStalePresence: %v", err)
	}
	if gone {
		t.Fatal("expected a refreshed row to survive eviction")
	}
}

// Reconnecting must count as a heartbeat, or a returning user whose old
// row went stale is reaped moments after connecting.
func TestMarkUserOnlineRefreshesHeartbeat(t *testing.T) {
	db, rs, viewer := newTeardownTestService(t)
	ctx := context.Background()

	ageTeardownPresence(t, db, viewer)
	if err := rs.db.MarkUserOnline(ctx, viewer); err != nil {
		t.Fatalf("MarkUserOnline: %v", err)
	}
	stale, err := rs.db.StalePresence(ctx, realtimePresenceTTL)
	if err != nil {
		t.Fatalf("StalePresence: %v", err)
	}
	if len(stale) != 0 {
		t.Fatalf("expected reconnect to refresh the heartbeat, still stale: %v", stale)
	}
}

// A PONG for a reaped row reports it, so the socket can be closed.
func TestRecordPongReportsMissingRow(t *testing.T) {
	_, rs, viewer := newTeardownTestService(t)
	ctx := context.Background()

	if err := rs.db.MarkUserOffline(ctx, viewer); err != nil {
		t.Fatalf("MarkUserOffline: %v", err)
	}
	live, err := rs.db.RecordPong(ctx, viewer)
	if err != nil {
		t.Fatalf("RecordPong: %v", err)
	}
	if live {
		t.Fatal("expected RecordPong to report no presence row")
	}
}
