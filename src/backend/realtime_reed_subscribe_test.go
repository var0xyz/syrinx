//go:build !ops && !ripplescleanup

package main

import (
	"context"
	"database/sql"
	"testing"

	_ "github.com/lib/pq"
)

const reedSubTestServerID = "testserver"

// ensureReedSubSchema keeps reed_subscriptions' real FK to online_users so
// the disconnect cascade is exercised rather than assumed.
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
			viewer_user_id VARCHAR(255) NOT NULL REFERENCES online_users(user_id) ON DELETE CASCADE,
			reed_id VARCHAR(255) NOT NULL REFERENCES reed_identities(id) ON DELETE CASCADE,
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
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

// Disconnect must clear reed subscriptions via the online_users cascade.
func TestReedSubscriptionsCascadeOnOffline(t *testing.T) {
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

	var remaining int
	if err := db.QueryRow(`SELECT COUNT(*) FROM reed_subscriptions`).Scan(&remaining); err != nil {
		t.Fatalf("count reed_subscriptions: %v", err)
	}
	if remaining != 0 {
		t.Fatalf("expected reed subscriptions cleared by cascade, got %d", remaining)
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
