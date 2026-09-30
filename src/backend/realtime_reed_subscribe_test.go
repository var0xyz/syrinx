//go:build !ops && !ripplescleanup

package main

import (
	"context"
	"database/sql"
	"testing"

	_ "github.com/lib/pq"
)

const reedSubTestServerID = "testserver"

// ensureReedSubSchema mirrors reed_subscriptions' real FKs, so a foreign
// viewer with no presence row is exercised against the actual constraint.
func ensureReedSubSchema(db *sql.DB) error {
	stmts := []string{
		`DROP TABLE IF EXISTS reed_subscriptions CASCADE`,
		`DROP TABLE IF EXISTS online_users CASCADE`,
		`DROP TABLE IF EXISTS reed_identities CASCADE`,
		`DROP TABLE IF EXISTS identities CASCADE`,
		`CREATE TABLE identities (
			id VARCHAR(255) PRIMARY KEY,
			server_id VARCHAR(16),
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE reed_identities (
			id VARCHAR(255) PRIMARY KEY,
			author_id VARCHAR(255),
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE UNLOGGED TABLE online_users (
			user_id VARCHAR(255) PRIMARY KEY REFERENCES identities(id) ON DELETE CASCADE,
			sync_request_id VARCHAR(255),
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
			last_pong TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE UNLOGGED TABLE reed_subscriptions (
			subscription_id VARCHAR(255) PRIMARY KEY,
			viewer_user_id VARCHAR(255) NOT NULL REFERENCES identities(id) ON DELETE CASCADE,
			reed_id VARCHAR(255) NOT NULL REFERENCES reed_identities(id) ON DELETE CASCADE,
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
			UNIQUE (viewer_user_id, reed_id)
		)`,
	}
	for _, stmt := range stmts {
		if _, err := db.Exec(stmt); err != nil {
			return err
		}
	}
	return nil
}

func TestReedSubscriptionBookkeeping(t *testing.T) {
	db := newTestDatabase(t, ensureReedSubSchema)
	svc := NewDataService(db, reedSubTestServerID)
	ctx := context.Background()

	viewer := "viewer@" + reedSubTestServerID
	if _, err := db.Exec(`INSERT INTO identities (id, server_id) VALUES ($1, $2)`, string(identityID(viewer)), reedSubTestServerID); err != nil {
		t.Fatalf("insert identity: %v", err)
	}
	if err := svc.MarkUserOnline(ctx, viewer); err != nil {
		t.Fatalf("MarkUserOnline: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO reed_identities (id) VALUES ($1)`, "reed1"); err != nil {
		t.Fatalf("insert reed identity: %v", err)
	}

	if err := svc.CreateReedSubscription(ctx, "sub1", viewer, "reed1"); err != nil {
		t.Fatalf("CreateReedSubscription: %v", err)
	}

	subscribers, err := svc.GetReedSubscriberUserIDs(ctx, "reed1", "")
	if err != nil {
		t.Fatalf("GetReedSubscriberUserIDs: %v", err)
	}
	if len(subscribers) != 1 || subscribers[0] != viewer {
		t.Fatalf("subscribers = %v, want [%s]", subscribers, viewer)
	}

	excluded, err := svc.GetReedSubscriberUserIDs(ctx, "reed1", viewer)
	if err != nil {
		t.Fatalf("GetReedSubscriberUserIDs exclude: %v", err)
	}
	if len(excluded) != 0 {
		t.Fatalf("exclude self: got %v", excluded)
	}

	subscriptionID, err := svc.GetReedSubscription(ctx, viewer, "reed1")
	if err != nil {
		t.Fatalf("GetReedSubscription: %v", err)
	}
	if subscriptionID != "sub1" {
		t.Fatalf("subscriptionID = %q, want sub1", subscriptionID)
	}

	if err := svc.DeleteReedSubscription(ctx, subscriptionID); err != nil {
		t.Fatalf("DeleteReedSubscription: %v", err)
	}
	subscribers, err = svc.GetReedSubscriberUserIDs(ctx, "reed1", "")
	if err != nil {
		t.Fatalf("GetReedSubscriberUserIDs after delete: %v", err)
	}
	if len(subscribers) != 0 {
		t.Fatalf("expected no subscribers after delete, got %v", subscribers)
	}
}

// A peer's viewer never has a presence row here, yet must be able to
// subscribe to a local reed's stats.
func TestReedSubscriptionForeignViewer(t *testing.T) {
	db := newTestDatabase(t, ensureReedSubSchema)
	svc := NewDataService(db, reedSubTestServerID)
	ctx := context.Background()

	viewer := "viewer@peerserver"
	if _, err := db.Exec(`INSERT INTO identities (id, server_id) VALUES ($1, $2)`, string(identityID(viewer)), "peerserver"); err != nil {
		t.Fatalf("insert identity: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO reed_identities (id) VALUES ($1)`, "reed1"); err != nil {
		t.Fatalf("insert reed identity: %v", err)
	}

	if err := svc.CreateReedSubscription(ctx, "sub1", viewer, "reed1"); err != nil {
		t.Fatalf("CreateReedSubscription for foreign viewer: %v", err)
	}
	subscribers, err := svc.GetReedSubscriberUserIDs(ctx, "reed1", "")
	if err != nil {
		t.Fatalf("GetReedSubscriberUserIDs: %v", err)
	}
	if len(subscribers) != 1 || subscribers[0] != viewer {
		t.Fatalf("subscribers = %v, want [%s]", subscribers, viewer)
	}
}

// A retried subscribe must not add a second row, or every stat change
// would be delivered twice.
func TestReedSubscriptionResubscribeIsNoOp(t *testing.T) {
	db := newTestDatabase(t, ensureReedSubSchema)
	svc := NewDataService(db, reedSubTestServerID)
	ctx := context.Background()

	viewer := "viewer@" + reedSubTestServerID
	if _, err := db.Exec(`INSERT INTO identities (id, server_id) VALUES ($1, $2)`, string(identityID(viewer)), reedSubTestServerID); err != nil {
		t.Fatalf("insert identity: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO reed_identities (id) VALUES ($1)`, "reed1"); err != nil {
		t.Fatalf("insert reed identity: %v", err)
	}

	for _, id := range []string{"sub1", "sub2"} {
		if err := svc.CreateReedSubscription(ctx, id, viewer, "reed1"); err != nil {
			t.Fatalf("CreateReedSubscription %s: %v", id, err)
		}
	}

	var rows int
	if err := db.QueryRow(`SELECT COUNT(*) FROM reed_subscriptions`).Scan(&rows); err != nil {
		t.Fatalf("count reed_subscriptions: %v", err)
	}
	if rows != 1 {
		t.Fatalf("expected one row after resubscribe, got %d", rows)
	}
	subscriptionID, err := svc.GetReedSubscription(ctx, viewer, "reed1")
	if err != nil {
		t.Fatalf("GetReedSubscription: %v", err)
	}
	if subscriptionID != "sub1" {
		t.Fatalf("subscriptionID = %q, want the original sub1", subscriptionID)
	}
}

// Presence no longer owns reed subscriptions: going offline leaves them,
// and the explicit disconnect teardown is what clears them.
func TestReedSubscriptionsClearedByTeardownNotPresence(t *testing.T) {
	db := newTestDatabase(t, ensureReedSubSchema)
	svc := NewDataService(db, reedSubTestServerID)
	ctx := context.Background()

	viewer := "viewer@" + reedSubTestServerID
	if _, err := db.Exec(`INSERT INTO identities (id, server_id) VALUES ($1, $2)`, string(identityID(viewer)), reedSubTestServerID); err != nil {
		t.Fatalf("insert identity: %v", err)
	}
	if err := svc.MarkUserOnline(ctx, viewer); err != nil {
		t.Fatalf("MarkUserOnline: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO reed_identities (id) VALUES ($1)`, "reed1"); err != nil {
		t.Fatalf("insert reed identity: %v", err)
	}
	if err := svc.CreateReedSubscription(ctx, "sub1", viewer, "reed1"); err != nil {
		t.Fatalf("CreateReedSubscription: %v", err)
	}

	if err := svc.MarkUserOffline(ctx, viewer); err != nil {
		t.Fatalf("MarkUserOffline: %v", err)
	}
	countRows := func() int {
		var n int
		if err := db.QueryRow(`SELECT COUNT(*) FROM reed_subscriptions`).Scan(&n); err != nil {
			t.Fatalf("count reed_subscriptions: %v", err)
		}
		return n
	}
	if n := countRows(); n != 1 {
		t.Fatalf("expected the subscription to outlive presence, got %d rows", n)
	}

	if err := svc.DeleteReedSubscriptionsByViewer(ctx, viewer); err != nil {
		t.Fatalf("DeleteReedSubscriptionsByViewer: %v", err)
	}
	if n := countRows(); n != 0 {
		t.Fatalf("expected teardown to clear subscriptions, got %d rows", n)
	}
}

func TestRealtimeEchoCountChangedString(t *testing.T) {
	if got := realtimeEchoCountChanged.String(); got != "EchoCountChanged" {
		t.Fatalf("String() = %q, want EchoCountChanged", got)
	}
}

func TestRealtimeReplyCountChangedString(t *testing.T) {
	if got := realtimeReplyCountChanged.String(); got != "ReplyCountChanged" {
		t.Fatalf("String() = %q, want ReplyCountChanged", got)
	}
}
