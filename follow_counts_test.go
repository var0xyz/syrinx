//go:build !ops

package main

import (
	"context"
	"database/sql"
	"testing"

	"syrinx/identity"

	_ "github.com/lib/pq"
)

// followCountsTestServerID matches the serverID passed to DataService in
// this file's tests, so identities.id values written here match what
// GetUserInfo/ListFollowers/ListFollowing resolve internally.
const followCountsTestServerID = "testserver"

func openFollowCountTestDB(t *testing.T) *sql.DB {
	t.Helper()
	return newTestDatabase(t, ensureFollowCountSchema)
}

func ensureFollowCountSchema(db *sql.DB) error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS servers (id VARCHAR(255) PRIMARY KEY, self BOOLEAN NOT NULL DEFAULT FALSE)`,
		`INSERT INTO servers (id, self) VALUES ('testserver', TRUE) ON CONFLICT (id) DO UPDATE SET self = EXCLUDED.self`,
		`CREATE TABLE IF NOT EXISTS user_signatures (id SERIAL PRIMARY KEY, public_key_id VARCHAR(255) NOT NULL, signature TEXT NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS server_signatures (id SERIAL PRIMARY KEY, fingerprint VARCHAR(255) NOT NULL, signature TEXT NOT NULL, signed_at TIMESTAMP NOT NULL)`,
		`DROP TABLE IF EXISTS user_followers CASCADE`,
		`DROP TABLE IF EXISTS user_following CASCADE`,
		`DROP TABLE IF EXISTS reeds CASCADE`,
		`DROP TABLE IF EXISTS users CASCADE`,
		`DROP TABLE IF EXISTS identities CASCADE`,
		// identities is the FK target for "a user" (see db.go).
		`CREATE TABLE identities (
			id VARCHAR(255) PRIMARY KEY,
			server_id VARCHAR(16),
			public_key_fingerprint VARCHAR(255),
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE users (
			id VARCHAR(255) PRIMARY KEY REFERENCES identities(id) ON DELETE CASCADE,
			username VARCHAR(255) UNIQUE NOT NULL,
			user_fingerprint VARCHAR(255),
			user_signature_id INT NOT NULL REFERENCES user_signatures(id),
			server_signature_id INT NOT NULL REFERENCES server_signatures(id)
		)`,
		`CREATE TABLE reeds (
			id VARCHAR(255) NOT NULL,
			user_id VARCHAR(255) NOT NULL REFERENCES identities(id),
			private_key_id VARCHAR(255) NOT NULL,
			signed_at TIMESTAMP NOT NULL,
			user_signature_id INT NOT NULL REFERENCES user_signatures(id),
			server_signature_id INT NOT NULL REFERENCES server_signatures(id),
			PRIMARY KEY (user_id, id)
		)`,
		`DROP TABLE IF EXISTS reed_removals CASCADE`,
		`CREATE TABLE reed_removals (
			reed_id VARCHAR(255) NOT NULL,
			user_id VARCHAR(255) NOT NULL REFERENCES identities(id),
			PRIMARY KEY (user_id, reed_id)
		)`,
		`DROP TABLE IF EXISTS account_removals CASCADE`,
		`CREATE TABLE account_removals (
			user_id VARCHAR(255) PRIMARY KEY REFERENCES identities(id)
		)`,
		`CREATE TABLE user_followers (
			user_id VARCHAR(255) NOT NULL REFERENCES identities(id) ON DELETE CASCADE,
			follower_user_id VARCHAR(255) NOT NULL REFERENCES identities(id) ON DELETE CASCADE,
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
			PRIMARY KEY (user_id, follower_user_id)
		)`,
		`CREATE TABLE user_following (
			user_id VARCHAR(255) NOT NULL REFERENCES identities(id) ON DELETE CASCADE,
			following_user_id VARCHAR(255) NOT NULL REFERENCES identities(id) ON DELETE CASCADE,
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
			PRIMARY KEY (user_id, following_user_id)
		)`,
	}
	for _, stmt := range stmts {
		if _, err := db.Exec(stmt); err != nil {
			return err
		}
	}
	return nil
}

// insertFollowCountTestUser creates a minimal user row (plus its required
// signature FKs and matching identities row) for follow-count fixtures.
func insertFollowCountTestUser(t *testing.T, db *sql.DB, userID, username string) {
	t.Helper()
	identityID := string(identity.CanonicalID(followCountsTestServerID, userID))
	if _, err := db.Exec(
		`INSERT INTO identities (id, server_id) VALUES ($1, $2)`,
		identityID, followCountsTestServerID,
	); err != nil {
		t.Fatalf("insert identities for %s: %v", userID, err)
	}
	var userSigID, serverSigID int
	if err := db.QueryRow(
		`INSERT INTO user_signatures (public_key_id, signature) VALUES ($1, 'sig') RETURNING id`,
		"fp-"+userID,
	).Scan(&userSigID); err != nil {
		t.Fatalf("insert user_signatures for %s: %v", userID, err)
	}
	if err := db.QueryRow(
		`INSERT INTO server_signatures (fingerprint, signature, signed_at) VALUES ($1, 'sig', now()) RETURNING id`,
		"server-fp-"+userID,
	).Scan(&serverSigID); err != nil {
		t.Fatalf("insert server_signatures for %s: %v", userID, err)
	}
	if _, err := db.Exec(
		`INSERT INTO users (id, username, user_fingerprint, user_signature_id, server_signature_id)
		 VALUES ($1, $2, $3, $4, $5)`,
		identityID, username, "fp-"+userID, userSigID, serverSigID,
	); err != nil {
		t.Fatalf("insert user %s: %v", userID, err)
	}
}

// TestGetUserInfo_FollowerCountExcludesRemovedAccounts is a regression test
// for a bug reported live: a profile showed "2 Followers" in the header
// (from GetUserInfo's COUNT) but only 1 entry in the followers list dropdown
// (from ListFollowers, which already excludes followers with an
// account_removals row). The count query had no such exclusion, so a
// follower who deleted their account was still counted but never listed.
// GetUserInfo must agree with ListFollowers/ListFollowing on what counts as
// an active follow relationship.
func TestGetUserInfo_FollowerCountExcludesRemovedAccounts(t *testing.T) {
	db := openFollowCountTestDB(t)
	svc := &DataService{db: db, serverID: "testserver"}
	ctx := context.Background()

	insertFollowCountTestUser(t, db, "author1", "author")
	insertFollowCountTestUser(t, db, "active-follower", "activeFollower")
	insertFollowCountTestUser(t, db, "removed-follower", "removedFollower")

	author1 := string(identity.CanonicalID(followCountsTestServerID, "author1"))
	activeFollower := string(identity.CanonicalID(followCountsTestServerID, "active-follower"))
	removedFollower := string(identity.CanonicalID(followCountsTestServerID, "removed-follower"))

	if _, err := db.Exec(
		`INSERT INTO user_followers (user_id, follower_user_id) VALUES ($1, $2), ($1, $3)`,
		author1, activeFollower, removedFollower,
	); err != nil {
		t.Fatalf("insert user_followers: %v", err)
	}
	if _, err := db.Exec(
		`INSERT INTO account_removals (user_id) VALUES ($1)`,
		removedFollower,
	); err != nil {
		t.Fatalf("insert account_removals: %v", err)
	}

	info, err := svc.GetUserInfo(ctx, author1)
	if err != nil {
		t.Fatalf("GetUserInfo: %v", err)
	}
	if info == nil {
		t.Fatal("GetUserInfo returned nil info for existing user")
	}
	if info.FollowersCount != 1 {
		t.Errorf("FollowersCount = %d, want 1 (removed-follower must not be counted)", info.FollowersCount)
	}

	list, err := svc.ListFollowers(ctx, author1, 50, nil)
	if err != nil {
		t.Fatalf("ListFollowers: %v", err)
	}
	if len(list.Users) != info.FollowersCount {
		t.Errorf("ListFollowers returned %d users but GetUserInfo counted %d — count and list must agree",
			len(list.Users), info.FollowersCount)
	}
}

// TestGetUserInfo_FollowingCountExcludesRemovedAccounts mirrors the
// followers-side regression for the "following" direction: a user who
// deleted their account must not still be counted among who this user
// follows, matching ListFollowing's own exclusion.
func TestGetUserInfo_FollowingCountExcludesRemovedAccounts(t *testing.T) {
	db := openFollowCountTestDB(t)
	svc := &DataService{db: db, serverID: "testserver"}
	ctx := context.Background()

	insertFollowCountTestUser(t, db, "viewer1", "viewer")
	insertFollowCountTestUser(t, db, "active-followed", "activeFollowed")
	insertFollowCountTestUser(t, db, "removed-followed", "removedFollowed")

	viewer1 := string(identity.CanonicalID(followCountsTestServerID, "viewer1"))
	activeFollowed := string(identity.CanonicalID(followCountsTestServerID, "active-followed"))
	removedFollowed := string(identity.CanonicalID(followCountsTestServerID, "removed-followed"))

	if _, err := db.Exec(
		`INSERT INTO user_following (user_id, following_user_id) VALUES ($1, $2), ($1, $3)`,
		viewer1, activeFollowed, removedFollowed,
	); err != nil {
		t.Fatalf("insert user_following: %v", err)
	}
	if _, err := db.Exec(
		`INSERT INTO account_removals (user_id) VALUES ($1)`,
		removedFollowed,
	); err != nil {
		t.Fatalf("insert account_removals: %v", err)
	}

	info, err := svc.GetUserInfo(ctx, viewer1)
	if err != nil {
		t.Fatalf("GetUserInfo: %v", err)
	}
	if info == nil {
		t.Fatal("GetUserInfo returned nil info for existing user")
	}
	if info.FollowingCount != 1 {
		t.Errorf("FollowingCount = %d, want 1 (removed-followed must not be counted)", info.FollowingCount)
	}

	list, err := svc.ListFollowing(ctx, viewer1, 50, nil)
	if err != nil {
		t.Fatalf("ListFollowing: %v", err)
	}
	if len(list.Users) != info.FollowingCount {
		t.Errorf("ListFollowing returned %d users but GetUserInfo counted %d — count and list must agree",
			len(list.Users), info.FollowingCount)
	}
}
