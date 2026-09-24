//go:build !ops && !ripplescleanup

package main

import (
	"context"
	"database/sql"
	"testing"

	_ "github.com/lib/pq"
)

// pipeTestServerID matches the serverID passed to DataService below, so
// identityID() produces ids that match the rows these tests insert.
const pipeTestServerID = "testserver"

func openPipeTestDB(t *testing.T) *sql.DB {
	t.Helper()
	return newTestDatabase(t, ensurePipeSchema)
}

// ensurePipeSchema builds the presence + pipe-subscription slice of the
// schema, keeping the online_users FK so the cascade is exercised for real.
func ensurePipeSchema(db *sql.DB) error {
	stmts := []string{
		`DROP TABLE IF EXISTS pipe_subscriptions CASCADE`,
		`DROP TABLE IF EXISTS online_users CASCADE`,
		`DROP TABLE IF EXISTS identities CASCADE`,
		`CREATE TABLE identities (
			id VARCHAR(255) PRIMARY KEY,
			server_id VARCHAR(16),
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE UNLOGGED TABLE online_users (
			user_id VARCHAR(255) PRIMARY KEY REFERENCES identities(id) ON DELETE CASCADE,
			sync_request_id VARCHAR(255),
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
			last_pong TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE UNLOGGED TABLE pipe_subscriptions (
			user_id VARCHAR(255) NOT NULL REFERENCES online_users(user_id) ON DELETE CASCADE,
			tag VARCHAR(255) NOT NULL,
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
			PRIMARY KEY (user_id, tag)
		)`,
	}
	for _, stmt := range stmts {
		if _, err := db.Exec(stmt); err != nil {
			return err
		}
	}
	return nil
}

// markPipeUserOnline inserts the identity + presence rows a pipe
// subscription depends on.
func markPipeUserOnline(t *testing.T, db *sql.DB, svc *DataService, userID string) {
	t.Helper()
	id := string(identityID(userID))
	if _, err := db.Exec(`INSERT INTO identities (id, server_id) VALUES ($1, $2)`, id, pipeTestServerID); err != nil {
		t.Fatalf("insert identity %s: %v", id, err)
	}
	if err := svc.MarkUserOnline(context.Background(), userID); err != nil {
		t.Fatalf("mark %s online: %v", userID, err)
	}
}

func TestPipeSubscriptionBookkeeping(t *testing.T) {
	db := openPipeTestDB(t)
	svc := NewDataService(db, pipeTestServerID)
	ctx := context.Background()

	viewer := "viewer@" + pipeTestServerID
	markPipeUserOnline(t, db, svc, viewer)

	// Tags are normalized before they reach SQL, never inside it.
	if err := svc.SubscribePipe(ctx, viewer, normalizePipeTag("#Climate")); err != nil {
		t.Fatalf("SubscribePipe: %v", err)
	}

	live, err := svc.GetTagsWithListeners(ctx, normalizePipeTags([]string{"Climate", "other"}))
	if err != nil {
		t.Fatalf("GetTagsWithListeners: %v", err)
	}
	if len(live) != 1 || live[0] != "climate" {
		t.Fatalf("GetTagsWithListeners = %v, want [climate]", live)
	}

	ids, err := svc.GetPipeListeners(ctx, []string{"climate"}, "author@"+pipeTestServerID)
	if err != nil {
		t.Fatalf("GetPipeListeners: %v", err)
	}
	if len(ids) != 1 || ids[0] != viewer {
		t.Fatalf("GetPipeListeners = %v, want [%s]", ids, viewer)
	}

	got, err := svc.GetPipeListeners(ctx, []string{"climate"}, viewer)
	if err != nil {
		t.Fatalf("GetPipeListeners exclude self: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("exclude self: got %v", got)
	}

	if err := svc.UnsubscribePipe(ctx, viewer, "climate"); err != nil {
		t.Fatalf("UnsubscribePipe: %v", err)
	}
	live, err = svc.GetTagsWithListeners(ctx, []string{"climate"})
	if err != nil {
		t.Fatalf("GetTagsWithListeners after unsubscribe: %v", err)
	}
	if len(live) != 0 {
		t.Fatalf("expected no listeners after unsubscribe, got %v", live)
	}
}

// Going offline must clear pipe subscriptions through the online_users FK
// cascade, with no explicit teardown call.
func TestPipeSubscriptionsCascadeOnOffline(t *testing.T) {
	db := openPipeTestDB(t)
	svc := NewDataService(db, pipeTestServerID)
	ctx := context.Background()

	viewer := "viewer@" + pipeTestServerID
	markPipeUserOnline(t, db, svc, viewer)

	for _, tag := range []string{"tag1", "tag2"} {
		if err := svc.SubscribePipe(ctx, viewer, tag); err != nil {
			t.Fatalf("SubscribePipe %s: %v", tag, err)
		}
	}

	if err := svc.MarkUserOffline(ctx, viewer); err != nil {
		t.Fatalf("MarkUserOffline: %v", err)
	}

	var remaining int
	if err := db.QueryRow(`SELECT COUNT(*) FROM pipe_subscriptions`).Scan(&remaining); err != nil {
		t.Fatalf("count pipe_subscriptions: %v", err)
	}
	if remaining != 0 {
		t.Fatalf("expected pipe subscriptions cleared by cascade, got %d", remaining)
	}
}

// A lapsed PONG heartbeat evicts the presence row, taking its
// subscriptions with it; a fresh one is left alone.
func TestReapStalePresence(t *testing.T) {
	db := openPipeTestDB(t)
	svc := NewDataService(db, pipeTestServerID)
	ctx := context.Background()

	stale := "stale@" + pipeTestServerID
	fresh := "fresh@" + pipeTestServerID
	markPipeUserOnline(t, db, svc, stale)
	markPipeUserOnline(t, db, svc, fresh)

	if err := svc.SubscribePipe(ctx, stale, "climate"); err != nil {
		t.Fatalf("SubscribePipe: %v", err)
	}

	// Age the heartbeat past the TTL.
	if _, err := db.Exec(
		`UPDATE online_users SET last_pong = CURRENT_TIMESTAMP - INTERVAL '5 minutes' WHERE user_id = $1`,
		string(identityID(stale)),
	); err != nil {
		t.Fatalf("age last_pong: %v", err)
	}

	evicted, err := svc.ReapStalePresence(ctx, realtimePresenceTTL)
	if err != nil {
		t.Fatalf("ReapStalePresence: %v", err)
	}
	if len(evicted) != 1 || evicted[0] != stale {
		t.Fatalf("evicted = %v, want [%s]", evicted, stale)
	}

	online, err := svc.IsUserOnline(ctx, fresh)
	if err != nil {
		t.Fatalf("IsUserOnline: %v", err)
	}
	if !online {
		t.Fatal("expected fresh user to survive the reaper")
	}

	var remaining int
	if err := db.QueryRow(`SELECT COUNT(*) FROM pipe_subscriptions`).Scan(&remaining); err != nil {
		t.Fatalf("count pipe_subscriptions: %v", err)
	}
	if remaining != 0 {
		t.Fatalf("expected evicted user's subscriptions cleared, got %d", remaining)
	}
}

// RecordPong refreshes the heartbeat, which is what keeps a live client
// from being reaped.
func TestRecordPongRefreshesHeartbeat(t *testing.T) {
	db := openPipeTestDB(t)
	svc := NewDataService(db, pipeTestServerID)
	ctx := context.Background()

	viewer := "viewer@" + pipeTestServerID
	markPipeUserOnline(t, db, svc, viewer)

	if _, err := db.Exec(
		`UPDATE online_users SET last_pong = CURRENT_TIMESTAMP - INTERVAL '5 minutes' WHERE user_id = $1`,
		string(identityID(viewer)),
	); err != nil {
		t.Fatalf("age last_pong: %v", err)
	}

	if err := svc.RecordPong(ctx, viewer); err != nil {
		t.Fatalf("RecordPong: %v", err)
	}

	evicted, err := svc.ReapStalePresence(ctx, realtimePresenceTTL)
	if err != nil {
		t.Fatalf("ReapStalePresence: %v", err)
	}
	if len(evicted) != 0 {
		t.Fatalf("expected no eviction after PONG, got %v", evicted)
	}
}

func TestUnionAndSubtractUserIDs(t *testing.T) {
	got := unionUserIDs([]string{"a", "b"}, []string{"b", "c"})
	if len(got) != 3 {
		t.Fatalf("union len = %d want 3: %v", len(got), got)
	}
	sub := subtractUserIDs([]string{"a", "b", "c"}, []string{"b"})
	if len(sub) != 2 || sub[0] != "a" || sub[1] != "c" {
		t.Fatalf("subtract = %v want [a c]", sub)
	}
}

func TestNormalizePipeTag(t *testing.T) {
	if got := normalizePipeTag(" #Foo "); got != "foo" {
		t.Fatalf("got %q want foo", got)
	}
	if got := normalizePipeTag(""); got != "" {
		t.Fatalf("empty: got %q", got)
	}
}

func TestNormalizePipeTags(t *testing.T) {
	got := normalizePipeTags([]string{" #Foo ", "", "BAR"})
	if len(got) != 2 || got[0] != "foo" || got[1] != "bar" {
		t.Fatalf("got %v want [foo bar]", got)
	}
	if normalizePipeTags(nil) != nil {
		t.Fatal("expected nil for empty input")
	}
}
