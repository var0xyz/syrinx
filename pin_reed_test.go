//go:build !ops

package main

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"syrinx/identity"

	_ "github.com/lib/pq"
)

const pinReedTestServerID = "testserver"

func openPinReedTestDB(t *testing.T) *sql.DB {
	t.Helper()
	return newTestDatabase(t, ensurePinReedSchema)
}

func ensurePinReedSchema(db *sql.DB) error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS servers (id VARCHAR(255) PRIMARY KEY, self BOOLEAN NOT NULL DEFAULT FALSE)`,
		`INSERT INTO servers (id, self) VALUES ('testserver', TRUE) ON CONFLICT (id) DO UPDATE SET self = EXCLUDED.self`,
		`CREATE TABLE IF NOT EXISTS user_signatures (id SERIAL PRIMARY KEY, public_key_id VARCHAR(255) NOT NULL, signature TEXT NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS server_signatures (id SERIAL PRIMARY KEY, private_key_id VARCHAR(255) NOT NULL, signature TEXT NOT NULL, signed_at TIMESTAMP NOT NULL)`,
		`DROP TABLE IF EXISTS pinned_reeds CASCADE`,
		`DROP TABLE IF EXISTS reed_removals CASCADE`,
		`DROP TABLE IF EXISTS reeds CASCADE`,
		`DROP TABLE IF EXISTS reed_identities CASCADE`,
		`DROP TABLE IF EXISTS user_followers CASCADE`,
		`DROP TABLE IF EXISTS user_following CASCADE`,
		`DROP TABLE IF EXISTS users CASCADE`,
		`DROP TABLE IF EXISTS identities CASCADE`,
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
		`CREATE TABLE reed_identities (
			id VARCHAR(255) PRIMARY KEY,
			server_id VARCHAR(16) NOT NULL,
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE reeds (
			id VARCHAR(255) PRIMARY KEY REFERENCES reed_identities(id) ON DELETE CASCADE,
			user_id VARCHAR(255) NOT NULL REFERENCES identities(id),
			signed_at TIMESTAMP NOT NULL,
			user_signature_id INT NOT NULL REFERENCES user_signatures(id),
			server_signature_id INT NOT NULL REFERENCES server_signatures(id)
		)`,
		`CREATE TABLE reed_removals (
			reed_id VARCHAR(255) NOT NULL,
			user_id VARCHAR(255) NOT NULL REFERENCES identities(id),
			PRIMARY KEY (user_id, reed_id)
		)`,
		`DROP TABLE IF EXISTS account_removals CASCADE`,
		`CREATE TABLE account_removals (
			user_id VARCHAR(255) PRIMARY KEY REFERENCES identities(id)
		)`,
		`CREATE TABLE pinned_reeds (
			user_id VARCHAR(255) NOT NULL REFERENCES identities(id) ON DELETE CASCADE,
			reed_id VARCHAR(255) NOT NULL REFERENCES reed_identities(id) ON DELETE CASCADE,
			pinned_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
			PRIMARY KEY (user_id, reed_id)
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

func insertPinReedTestUser(t *testing.T, db *sql.DB, userID, username string) string {
	t.Helper()
	identityID := string(identity.CanonicalID(pinReedTestServerID, userID))
	if _, err := db.Exec(
		`INSERT INTO identities (id, server_id) VALUES ($1, $2)`,
		identityID, pinReedTestServerID,
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
	return identityID
}

// insertPinReedTestReed creates a reed owned by ownerIdentityID and returns
// its id.
func insertPinReedTestReed(t *testing.T, db *sql.DB, ownerIdentityID, reedID string, userSigID, serverSigID int) string {
	t.Helper()
	if _, err := db.Exec(
		`INSERT INTO reed_identities (id, server_id) VALUES ($1, $2)`,
		reedID, pinReedTestServerID,
	); err != nil {
		t.Fatalf("insert reed_identities for %s: %v", reedID, err)
	}
	if _, err := db.Exec(
		`INSERT INTO reeds (id, user_id, signed_at, user_signature_id, server_signature_id)
		 VALUES ($1, $2, now(), $3, $4)`,
		reedID, ownerIdentityID, userSigID, serverSigID,
	); err != nil {
		t.Fatalf("insert reed %s: %v", reedID, err)
	}
	return reedID
}

func firstSigPair(t *testing.T, db *sql.DB) (int, int) {
	t.Helper()
	var userSigID, serverSigID int
	if err := db.QueryRow(`SELECT id FROM user_signatures LIMIT 1`).Scan(&userSigID); err != nil {
		t.Fatalf("select user_signatures: %v", err)
	}
	if err := db.QueryRow(`SELECT id FROM server_signatures LIMIT 1`).Scan(&serverSigID); err != nil {
		t.Fatalf("select server_signatures: %v", err)
	}
	return userSigID, serverSigID
}

func TestPinReed(t *testing.T) {
	db := openPinReedTestDB(t)
	svc := &DataService{db: db, serverID: pinReedTestServerID}
	ctx := context.Background()

	owner := insertPinReedTestUser(t, db, "owner1", "owner")
	other := insertPinReedTestUser(t, db, "other1", "other")
	userSigID, serverSigID := firstSigPair(t, db)

	ownReed := insertPinReedTestReed(t, db, owner, owner+"/reed1", userSigID, serverSigID)
	otherReed := insertPinReedTestReed(t, db, other, other+"/reed1", userSigID, serverSigID)

	if err := svc.PinReed(ctx, owner, ownReed); err != nil {
		t.Fatalf("pin own reed: %v", err)
	}

	if err := svc.PinReed(ctx, owner, otherReed); !errors.Is(err, ErrPinTargetNotFound) {
		t.Fatalf("pin other's reed: got %v, want ErrPinTargetNotFound", err)
	}

	if err := svc.PinReed(ctx, owner, owner+"/nonexistent"); !errors.Is(err, ErrPinTargetNotFound) {
		t.Fatalf("pin nonexistent reed: got %v, want ErrPinTargetNotFound", err)
	}

	// Fill the cap, then pin a 4th.
	for i := 2; i <= 3; i++ {
		reedID := insertPinReedTestReed(t, db, owner, owner+"/reedN"+string(rune('0'+i)), userSigID, serverSigID)
		if err := svc.PinReed(ctx, owner, reedID); err != nil {
			t.Fatalf("pin reed %d: %v", i, err)
		}
	}
	fourth := insertPinReedTestReed(t, db, owner, owner+"/reed4", userSigID, serverSigID)
	if err := svc.PinReed(ctx, owner, fourth); !errors.Is(err, ErrPinLimitReached) {
		t.Fatalf("pin 4th reed: got %v, want ErrPinLimitReached", err)
	}

	// Re-pinning an already-pinned reed is idempotent, not counted against the cap.
	if err := svc.PinReed(ctx, owner, ownReed); err != nil {
		t.Fatalf("re-pin own reed: %v", err)
	}

	if err := svc.UnpinReed(ctx, owner, ownReed); err != nil {
		t.Fatalf("unpin own reed: %v", err)
	}
	if err := svc.UnpinReed(ctx, owner, ownReed); err != nil {
		t.Fatalf("unpin already-unpinned reed should be a no-op: %v", err)
	}
}

func TestGetUserInfo_PinnedReedIDs(t *testing.T) {
	db := openPinReedTestDB(t)
	svc := &DataService{db: db, serverID: pinReedTestServerID}
	ctx := context.Background()

	owner := insertPinReedTestUser(t, db, "owner2", "owner2")
	userSigID, serverSigID := firstSigPair(t, db)

	info, err := svc.GetUserInfo(ctx, owner)
	if err != nil {
		t.Fatalf("GetUserInfo: %v", err)
	}
	if len(info.PinnedReedIDs) != 0 {
		t.Errorf("PinnedReedIDs = %v, want empty before any pins", info.PinnedReedIDs)
	}

	reedA := insertPinReedTestReed(t, db, owner, owner+"/a", userSigID, serverSigID)
	reedB := insertPinReedTestReed(t, db, owner, owner+"/b", userSigID, serverSigID)
	reedC := insertPinReedTestReed(t, db, owner, owner+"/c", userSigID, serverSigID)

	if err := svc.PinReed(ctx, owner, reedA); err != nil {
		t.Fatalf("pin a: %v", err)
	}
	if err := svc.PinReed(ctx, owner, reedB); err != nil {
		t.Fatalf("pin b: %v", err)
	}
	if err := svc.PinReed(ctx, owner, reedC); err != nil {
		t.Fatalf("pin c: %v", err)
	}

	info, err = svc.GetUserInfo(ctx, owner)
	if err != nil {
		t.Fatalf("GetUserInfo: %v", err)
	}
	want := []string{reedC, reedB, reedA}
	if len(info.PinnedReedIDs) != len(want) {
		t.Fatalf("PinnedReedIDs = %v, want %v", info.PinnedReedIDs, want)
	}
	for i, id := range want {
		if info.PinnedReedIDs[i] != id {
			t.Errorf("PinnedReedIDs[%d] = %s, want %s", i, info.PinnedReedIDs[i], id)
		}
	}

	if _, err := db.Exec(`INSERT INTO reed_removals (reed_id, user_id) VALUES ($1, $2)`, reedC, owner); err != nil {
		t.Fatalf("insert reed_removals: %v", err)
	}
	info, err = svc.GetUserInfo(ctx, owner)
	if err != nil {
		t.Fatalf("GetUserInfo: %v", err)
	}
	for _, id := range info.PinnedReedIDs {
		if id == reedC {
			t.Errorf("PinnedReedIDs still contains removed reed %s", reedC)
		}
	}
}
