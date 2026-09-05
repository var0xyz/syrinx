//go:build !ops

package main

import (
	"context"
	"database/sql"
	"testing"

	"syrinx/recovery"

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
	if _, err := db.Exec(`INSERT INTO servers (id, name, self) VALUES ($1, 'test', TRUE)`, serverID); err != nil {
		t.Fatalf("insert server: %v", err)
	}

	follower := "follower1@" + serverID
	existingTarget := "target1@" + serverID
	pendingTarget := "target2@" + serverID

	insertFollowingTestIdentity(t, db, follower, serverID)
	insertFollowingTestIdentity(t, db, existingTarget, serverID)
	// pendingTarget deliberately has no identities row yet.

	err := recovery.SaveFollowing(context.Background(), db, serverID, follower, []string{existingTarget, pendingTarget})
	if err != nil {
		t.Fatalf("SaveFollowing: %v", err)
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

	// pendingTarget has no identities row, so it must land in
	// pending_follows keyed by its BARE userID (matching
	// drainPendingFollows' lookup), not the canonical form.
	var pendingBare, pendingCanonical int
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM pending_follows WHERE follower_user_id = $1 AND following_user_id = 'target2'`,
		follower,
	).Scan(&pendingBare); err != nil {
		t.Fatalf("query pending_follows (bare): %v", err)
	}
	if pendingBare != 1 {
		t.Errorf("pending_follows rows keyed by bare target = %d, want 1", pendingBare)
	}
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM pending_follows WHERE follower_user_id = $1 AND following_user_id = $2`,
		follower, pendingTarget,
	).Scan(&pendingCanonical); err != nil {
		t.Fatalf("query pending_follows (canonical): %v", err)
	}
	if pendingCanonical != 0 {
		t.Errorf("pending_follows rows keyed by canonical target = %d, want 0", pendingCanonical)
	}
}
