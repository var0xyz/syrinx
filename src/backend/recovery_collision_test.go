//go:build !ops

package main

import (
	"database/sql"
	"testing"
	"time"
)

// ensureRecoveryCollisionSchema extends the signup-test schema with the
// tables saveOwnIdentity/savePeerIdentity touch: network_stats (coverage
// counter), unclaimed_accounts, ongoing_recoveries, pending_follows, and
// user_devices (own-claim device binding).
func ensureRecoveryCollisionSchema(db *sql.DB) error {
	stmts := []string{
		`DROP TABLE IF EXISTS user_devices CASCADE`,
		`DROP TABLE IF EXISTS pending_follows CASCADE`,
		`DROP TABLE IF EXISTS ongoing_recoveries CASCADE`,
		`DROP TABLE IF EXISTS unclaimed_accounts CASCADE`,
		`DROP TABLE IF EXISTS network_stats CASCADE`,
		// public_keys/public_key_revocations are already created by
		// openSignupTestDB with the shape insertRecoveryKeys/
		// claimRecoveryUsername rely on (owner FKs identities(id) ON
		// DELETE CASCADE), so no recreation needed here.
		`CREATE TABLE network_stats (
			id BOOLEAN PRIMARY KEY DEFAULT TRUE CHECK (id),
			active_users INT NOT NULL DEFAULT 0
		)`,
		`INSERT INTO network_stats (id, active_users) VALUES (TRUE, 0)`,
		`CREATE TABLE unclaimed_accounts (
			user_id VARCHAR(255) PRIMARY KEY REFERENCES identities(id) ON DELETE CASCADE,
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE ongoing_recoveries (
			user_id VARCHAR(255) PRIMARY KEY REFERENCES identities(id) ON DELETE CASCADE,
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE pending_follows (
			follower_user_id VARCHAR(255) NOT NULL REFERENCES identities(id) ON DELETE CASCADE,
			following_user_id VARCHAR(255) NOT NULL,
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
			PRIMARY KEY (follower_user_id, following_user_id)
		)`,
		`CREATE TABLE user_devices (
			user_id VARCHAR(255) NOT NULL REFERENCES identities(id) ON DELETE CASCADE,
			device_id TEXT NOT NULL,
			linked_at TIMESTAMPTZ NOT NULL,
			revoked_at TIMESTAMPTZ NULL,
			PRIMARY KEY (user_id, device_id, linked_at)
		)`,
	}
	for _, s := range stmts {
		if _, err := db.Exec(s); err != nil {
			return err
		}
	}
	return nil
}

func openRecoveryCollisionTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db := openSignupTestDB(t)
	if err := ensureRecoveryCollisionSchema(db); err != nil {
		t.Fatalf("recovery schema: %v", err)
	}
	return db
}

func testRecoveryProfile(userID, username string, signedAt time.Time) recoveryProfile {
	return recoveryProfile{
		ID:          userID,
		Username:    username,
		Role:        "user",
		MemberSince: signedAt,
		UserSignature: recoveryUserSignature{
			KeyID: userID + "-key",
			Armor: "user-sig-" + userID,
		},
		ServerSignature: recoveryServerSignature{
			ServerID:    "test",
			Fingerprint: "server-key",
			Armor:       "server-sig-" + userID,
			Timestamp:   signedAt,
		},
	}
}

func testRecoveryFlatKey(userID string, createdAt time.Time) []recoveryFlatKey {
	return []recoveryFlatKey{{
		Key: recoveryKeyWire{
			Fingerprint: userID + "-key",
			UserID:      userID,
			Armor:       "pubkey-" + userID,
			CreatedAt:   createdAt,
			ServerSignature: recoveryServerSignature{
				ServerID:    "test",
				Fingerprint: "server-key",
				Armor:       "key-server-sig-" + userID,
				Timestamp:   createdAt,
			},
		},
	}}
}

// TestSaveOwnIdentity_UsernameCollision_IncomingLoses guards against a
// regression to the old rename-in-place behavior: when a claim's username
// collides with an existing holder whose server_signed_at is newer or
// equal, the claim must be rejected wholesale (no row written, no keys,
// no unclaimed/ongoing/follow bookkeeping) rather than stored under a
// renamed username that no longer matches what the claimant signed.
func TestSaveOwnIdentity_UsernameCollision_IncomingLoses(t *testing.T) {
	db := openRecoveryCollisionTestDB(t)
	ctx := t.Context()

	holderSignedAt := time.Now().UTC().Truncate(time.Second)
	holder := testRecoveryProfile("holder1", "bob", holderSignedAt)
	if _, err := savePeerIdentity(ctx, db, "test", holder, testRecoveryFlatKey("holder1", holderSignedAt)); err != nil {
		t.Fatalf("seed holder: %v", err)
	}

	olderSignedAt := holderSignedAt.Add(-time.Hour)
	claimant := testRecoveryProfile("claimant1", "bob", olderSignedAt)
	res, err := saveOwnIdentity(ctx, db, "test", claimant, testRecoveryFlatKey("claimant1", olderSignedAt), "")
	if err != nil {
		t.Fatalf("saveOwnIdentity: %v", err)
	}
	if !res.Rejected {
		t.Fatalf("expected Rejected=true, got %+v", res)
	}

	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM users WHERE id = $1`, "claimant1@test").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("rejected claim must not create a row, found %d", count)
	}

	var holderUsername string
	if err := db.QueryRow(`SELECT username FROM users WHERE id = $1`, "holder1@test").Scan(&holderUsername); err != nil {
		t.Fatal(err)
	}
	if holderUsername != "bob" {
		t.Fatalf("holder must be untouched, got username=%q", holderUsername)
	}
}

// TestSavePeerIdentity_UsernameCollision_IncomingWins verifies the "nuke
// the loser" side: when the incoming report is newer, the existing holder
// row is hard-deleted (cascading to its keys) rather than renamed, so no
// stored profile ever carries a username its own signature doesn't match.
func TestSavePeerIdentity_UsernameCollision_IncomingWins(t *testing.T) {
	db := openRecoveryCollisionTestDB(t)
	ctx := t.Context()

	holderSignedAt := time.Now().UTC().Add(-time.Hour).Truncate(time.Second)
	holder := testRecoveryProfile("holder2", "carol", holderSignedAt)
	if _, err := savePeerIdentity(ctx, db, "test", holder, testRecoveryFlatKey("holder2", holderSignedAt)); err != nil {
		t.Fatalf("seed holder: %v", err)
	}

	newerSignedAt := time.Now().UTC().Truncate(time.Second)
	winner := testRecoveryProfile("winner2", "carol", newerSignedAt)
	res, err := savePeerIdentity(ctx, db, "test", winner, testRecoveryFlatKey("winner2", newerSignedAt))
	if err != nil {
		t.Fatalf("savePeerIdentity: %v", err)
	}
	if res.Rejected || !res.Created {
		t.Fatalf("expected winner to be created, got %+v", res)
	}

	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM users WHERE id = $1`, "holder2@test").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("collision loser must be hard-deleted, found %d rows", count)
	}

	var keyCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM public_keys WHERE owner = $1`, "holder2@test").Scan(&keyCount); err != nil {
		t.Fatal(err)
	}
	if keyCount != 0 {
		t.Fatalf("collision loser's keys must cascade-delete, found %d", keyCount)
	}

	var winnerUsername string
	if err := db.QueryRow(`SELECT username FROM users WHERE id = $1`, "winner2@test").Scan(&winnerUsername); err != nil {
		t.Fatal(err)
	}
	if winnerUsername != "carol" {
		t.Fatalf("winner must keep its signed username, got %q", winnerUsername)
	}
}

// TestSaveOwnIdentity_UsernameCollision_TieGoesToHolder covers the
// equal-timestamp edge: !incoming.After(holder) means the holder wins ties,
// matching the existing newest-wins comparator used elsewhere in recovery.
func TestSaveOwnIdentity_UsernameCollision_TieGoesToHolder(t *testing.T) {
	db := openRecoveryCollisionTestDB(t)
	ctx := t.Context()

	tie := time.Now().UTC().Truncate(time.Second)
	holder := testRecoveryProfile("holder3", "dave", tie)
	if _, err := savePeerIdentity(ctx, db, "test", holder, testRecoveryFlatKey("holder3", tie)); err != nil {
		t.Fatalf("seed holder: %v", err)
	}

	claimant := testRecoveryProfile("claimant3", "dave", tie)
	res, err := saveOwnIdentity(ctx, db, "test", claimant, testRecoveryFlatKey("claimant3", tie), "")
	if err != nil {
		t.Fatalf("saveOwnIdentity: %v", err)
	}
	if !res.Rejected {
		t.Fatalf("expected tie to reject the incoming claim, got %+v", res)
	}
}
