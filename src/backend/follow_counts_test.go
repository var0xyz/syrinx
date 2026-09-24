//go:build !ops

package main

import (
	"context"
	"database/sql"
	"testing"


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
		`CREATE TABLE IF NOT EXISTS server_signatures (id SERIAL PRIMARY KEY, private_key_id VARCHAR(255) NOT NULL, signature TEXT NOT NULL, signed_at TIMESTAMP NOT NULL)`,
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
			active_key_id VARCHAR(255),
			user_signature_id INT NOT NULL REFERENCES user_signatures(id),
			server_signature_id INT NOT NULL REFERENCES server_signatures(id)
		)`,
		`CREATE TABLE reeds (
			id VARCHAR(255) NOT NULL,
			user_id VARCHAR(255) NOT NULL REFERENCES identities(id),
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
		`DROP TABLE IF EXISTS pinned_reeds CASCADE`,
		`DROP TABLE IF EXISTS reed_identities CASCADE`,
		`CREATE TABLE reed_identities (
			id VARCHAR(255) PRIMARY KEY,
			server_id VARCHAR(16) NOT NULL,
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE pinned_reeds (
			user_id VARCHAR(255) NOT NULL REFERENCES identities(id) ON DELETE CASCADE,
			reed_id VARCHAR(255) NOT NULL REFERENCES reed_identities(id) ON DELETE CASCADE,
			pinned_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
			PRIMARY KEY (user_id, reed_id)
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
	identityID := string(canonicalID(followCountsTestServerID, userID))
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
		`INSERT INTO server_signatures (private_key_id, signature, signed_at) VALUES ($1, 'sig', now()) RETURNING id`,
		"server-fp-"+userID,
	).Scan(&serverSigID); err != nil {
		t.Fatalf("insert server_signatures for %s: %v", userID, err)
	}
	if _, err := db.Exec(
		`INSERT INTO users (id, username, active_key_id, user_signature_id, server_signature_id)
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

	author1 := string(canonicalID(followCountsTestServerID, "author1"))
	activeFollower := string(canonicalID(followCountsTestServerID, "active-follower"))
	removedFollower := string(canonicalID(followCountsTestServerID, "removed-follower"))

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

	viewer1 := string(canonicalID(followCountsTestServerID, "viewer1"))
	activeFollowed := string(canonicalID(followCountsTestServerID, "active-followed"))
	removedFollowed := string(canonicalID(followCountsTestServerID, "removed-followed"))

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

// insertFollowCountTestReed adds one reed for author, with an explicit id so
// tests control which is oldest (FirstReedID is MIN(id), matching
// GetAuthorReedPage's `ORDER BY r.id DESC` walk).
func insertFollowCountTestReed(t *testing.T, db *sql.DB, authorIdentity, reedID string) {
	t.Helper()
	var userSigID, serverSigID int
	if err := db.QueryRow(
		`INSERT INTO user_signatures (public_key_id, signature) VALUES ($1, 'sig') RETURNING id`,
		"fp-"+reedID,
	).Scan(&userSigID); err != nil {
		t.Fatalf("insert user_signatures for %s: %v", reedID, err)
	}
	if err := db.QueryRow(
		`INSERT INTO server_signatures (private_key_id, signature, signed_at) VALUES ($1, 'sig', now()) RETURNING id`,
		"server-fp-"+reedID,
	).Scan(&serverSigID); err != nil {
		t.Fatalf("insert server_signatures for %s: %v", reedID, err)
	}
	if _, err := db.Exec(
		`INSERT INTO reeds (id, user_id, signed_at, user_signature_id, server_signature_id)
		 VALUES ($1, $2, now(), $3, $4)`,
		reedID, authorIdentity, userSigID, serverSigID,
	); err != nil {
		t.Fatalf("insert reed %s: %v", reedID, err)
	}
}

// An author with no reeds reports nil, which is the client's "this profile
// is empty" signal.
func TestGetUserInfo_FirstReedIDNilWhenNoReeds(t *testing.T) {
	db := openFollowCountTestDB(t)
	svc := &DataService{db: db, serverID: "testserver"}
	ctx := context.Background()

	insertFollowCountTestUser(t, db, "author1", "author")
	author1 := string(canonicalID(followCountsTestServerID, "author1"))

	info, err := svc.GetUserInfo(ctx, author1)
	if err != nil {
		t.Fatalf("GetUserInfo: %v", err)
	}
	if info == nil {
		t.Fatal("GetUserInfo returned nil info for existing user")
	}
	if info.FirstReedID != nil {
		t.Errorf("FirstReedID = %q, want nil for an author with no reeds", *info.FirstReedID)
	}
}

// FirstReedID is the oldest reed — the last one a newest-first walk reaches,
// which is what lets the client retire "Load more" without a page ack.
func TestGetUserInfo_FirstReedIDIsOldestReed(t *testing.T) {
	db := openFollowCountTestDB(t)
	svc := &DataService{db: db, serverID: "testserver"}
	ctx := context.Background()

	insertFollowCountTestUser(t, db, "author1", "author")
	author1 := string(canonicalID(followCountsTestServerID, "author1"))

	insertFollowCountTestReed(t, db, author1, author1+"/0001")
	insertFollowCountTestReed(t, db, author1, author1+"/0002")
	insertFollowCountTestReed(t, db, author1, author1+"/0003")

	info, err := svc.GetUserInfo(ctx, author1)
	if err != nil {
		t.Fatalf("GetUserInfo: %v", err)
	}
	if info.FirstReedID == nil {
		t.Fatal("FirstReedID = nil, want the oldest reed id")
	}
	if want := author1 + "/0001"; *info.FirstReedID != want {
		t.Errorf("FirstReedID = %q, want %q", *info.FirstReedID, want)
	}
}

// A removed oldest reed must hand the slot to the next surviving one —
// otherwise the client waits for a reed that will never arrive.
func TestGetUserInfo_FirstReedIDSkipsRemovedReeds(t *testing.T) {
	db := openFollowCountTestDB(t)
	svc := &DataService{db: db, serverID: "testserver"}
	ctx := context.Background()

	insertFollowCountTestUser(t, db, "author1", "author")
	author1 := string(canonicalID(followCountsTestServerID, "author1"))

	insertFollowCountTestReed(t, db, author1, author1+"/0001")
	insertFollowCountTestReed(t, db, author1, author1+"/0002")

	if _, err := db.Exec(
		`INSERT INTO reed_removals (reed_id, user_id) VALUES ($1, $2)`,
		author1+"/0001", author1,
	); err != nil {
		t.Fatalf("insert reed_removals: %v", err)
	}

	info, err := svc.GetUserInfo(ctx, author1)
	if err != nil {
		t.Fatalf("GetUserInfo: %v", err)
	}
	if info.FirstReedID == nil {
		t.Fatal("FirstReedID = nil, want the oldest surviving reed id")
	}
	if want := author1 + "/0002"; *info.FirstReedID != want {
		t.Errorf("FirstReedID = %q, want %q (removed reed must not be reported)", *info.FirstReedID, want)
	}
}
