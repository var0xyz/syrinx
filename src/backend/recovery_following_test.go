//go:build !ops

package main

import (
	"context"
	"database/sql"
	"testing"

	_ "github.com/lib/pq"
)

func openFollowingTestDB(t *testing.T) *sql.DB {
	t.Helper()
	return newTestDatabase(t, InitDB)
}

func insertFollowingTestIdentity(t *testing.T, db *sql.DB, id, serverID string) {
	t.Helper()
	if _, err := db.Exec(
		`INSERT INTO identities (id, server_id) VALUES ($1, $2) ON CONFLICT (id) DO NOTHING`,
		id, serverID,
	); err != nil {
		t.Fatalf("insert identity %s: %v", id, err)
	}
}

// TestSaveFollowing_CanonicalIDs: SaveFollowing used to double-canonicalize
// followerUserID/targetIDs, which the real client already sends canonical.
func TestSaveFollowing_CanonicalIDs(t *testing.T) {
	db := openFollowingTestDB(t)
	serverID := "srv1"
	if _, err := db.Exec(`INSERT INTO servers (id, name, self, frontend_url) VALUES ($1, 'test', TRUE, 'https://test.example')`, serverID); err != nil {
		t.Fatalf("insert server: %v", err)
	}

	follower := "follower1@" + serverID
	existingTarget := "target1@" + serverID
	pendingTarget := "target2@" + serverID

	insertFollowingTestIdentity(t, db, follower, serverID)
	insertFollowingTestIdentity(t, db, existingTarget, serverID)
	// pendingTarget deliberately has no identities row yet.

	err := saveRecoveryFollowing(context.Background(), db, serverID, follower, []string{existingTarget, pendingTarget})
	if err != nil {
		t.Fatalf("saveRecoveryFollowing: %v", err)
	}

	var followingCount int
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM user_following WHERE user_id = $1 AND following_user_id = $2`,
		follower, existingTarget,
	).Scan(&followingCount); err != nil {
		t.Fatalf("query user_following: %v", err)
	}
	if followingCount != 1 {
		t.Errorf("user_following rows = %d, want 1 (follower=%q target=%q)", followingCount, follower, existingTarget)
	}

	var followersCount int
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM user_followers WHERE user_id = $1 AND follower_user_id = $2`,
		existingTarget, follower,
	).Scan(&followersCount); err != nil {
		t.Fatalf("query user_followers: %v", err)
	}
	if followersCount != 1 {
		t.Errorf("user_followers rows = %d, want 1", followersCount)
	}

	// pendingTarget has no identities row, so it waits in pending_follows
	// under its full id, the one its own claim drains by.
	var pending int
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM pending_follows WHERE follower_user_id = $1 AND following_user_id = $2`,
		follower, pendingTarget,
	).Scan(&pending); err != nil {
		t.Fatalf("query pending_follows: %v", err)
	}
	if pending != 1 {
		t.Errorf("pending_follows rows for %q = %d, want 1", pendingTarget, pending)
	}
}
