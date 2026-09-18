//go:build !ops

package main

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"

	_ "github.com/lib/pq"
)

func openInviteStoreTestDB(t *testing.T) *sql.DB {
	t.Helper()
	return newTestDatabase(t, ensureInviteStoreSchema)
}

func ensureInviteStoreSchema(db *sql.DB) error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS user_signatures (
			id SERIAL PRIMARY KEY,
			public_key_id VARCHAR(255) NOT NULL,
			signature TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS server_signatures (
			id SERIAL PRIMARY KEY,
			private_key_id VARCHAR(255) NOT NULL,
			signature TEXT NOT NULL,
			signed_at TIMESTAMP NOT NULL
		)`,
		`DROP TABLE IF EXISTS invites`,
		`DROP TABLE IF EXISTS users CASCADE`,
		`DROP TABLE IF EXISTS identities CASCADE`,
		`DROP TABLE IF EXISTS servers CASCADE`,
		// Minimal servers/identities fixture mirroring db.go's real schema
		// (identities.id = "userID@serverID"). Every seeded user here is
		// local to inviteStoreTestServerID below.
		`CREATE TABLE servers (
			id VARCHAR(16) UNIQUE,
			name VARCHAR(255) PRIMARY KEY,
			self BOOLEAN NOT NULL DEFAULT FALSE
		)`,
		`CREATE TABLE identities (
			id VARCHAR(255) PRIMARY KEY,
			server_id VARCHAR(16) REFERENCES servers(id)
		)`,
		`CREATE TABLE invites (
			id         VARCHAR(255) PRIMARY KEY,
			created_by VARCHAR(255) NOT NULL REFERENCES identities(id),
			token_hash BYTEA NOT NULL UNIQUE,
			created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
			claimed_at TIMESTAMPTZ,
			claimed_by VARCHAR(255) REFERENCES identities(id),
			revoked_at TIMESTAMPTZ,
			granted_role VARCHAR(16) NOT NULL DEFAULT 'user'
				CHECK (granted_role IN ('admin', 'user')),
			user_signature_id INT NOT NULL REFERENCES user_signatures(id)
		)`,
		`CREATE TABLE users (
			id VARCHAR(255) PRIMARY KEY REFERENCES identities(id),
			username VARCHAR(255) UNIQUE NOT NULL,
			role VARCHAR(16) NOT NULL DEFAULT 'user'
				CHECK (role IN ('root', 'admin', 'user')),
			bio TEXT,
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
			user_signature_id INT NOT NULL REFERENCES user_signatures(id),
			server_signature_id INT NOT NULL REFERENCES server_signatures(id),
			invite_id VARCHAR(255) REFERENCES invites(id)
		)`,
		fmt.Sprintf(`INSERT INTO servers (id, name, self) VALUES ('%s', 'test', TRUE)`, inviteStoreTestServerID),
	}
	for _, s := range stmts {
		if _, err := db.Exec(s); err != nil {
			return err
		}
	}
	return nil
}

// inviteStoreTestServerID is the fixed self serverID seeded by
// ensureInviteStoreSchema and used by every DataService constructed in
// this file's tests — must match whatever serverID a test sets via
// setServerIDForTest.
const inviteStoreTestServerID = "testsrv1"

func seedInviteStoreUserWithRole(t *testing.T, db *sql.DB, userID, username, role string) {
	t.Helper()
	var userSigID, serverSigID int64
	err := db.QueryRow(`
		INSERT INTO user_signatures (public_key_id, signature)
		VALUES ('seed-ufp', 'u') RETURNING id
	`).Scan(&userSigID)
	if err != nil {
		t.Fatalf("user sig: %v", err)
	}
	err = db.QueryRow(`
		INSERT INTO server_signatures (private_key_id, signature, signed_at)
		VALUES ('seed-sfp', 's', NOW()) RETURNING id
	`).Scan(&serverSigID)
	if err != nil {
		t.Fatalf("server sig: %v", err)
	}
	identityID := userID + "@" + inviteStoreTestServerID
	if _, err := db.Exec(`
		INSERT INTO identities (id, server_id)
		VALUES ($1, $2)
	`, identityID, inviteStoreTestServerID); err != nil {
		t.Fatalf("identity: %v", err)
	}
	_, err = db.Exec(`
		INSERT INTO users (id, username, role, user_signature_id, server_signature_id)
		VALUES ($1, $2, $3, $4, $5)
	`, identityID, username, role, userSigID, serverSigID)
	if err != nil {
		t.Fatalf("user: %v", err)
	}
}

func seedInviteStoreUser(t *testing.T, db *sql.DB, userID, username string) {
	seedInviteStoreUserWithRole(t, db, userID, username, roleUser)
}

func TestHashSecret(t *testing.T) {
	a := hashSecret("same")
	b := hashSecret("same")
	c := hashSecret("other")
	if !bytes.Equal(a, b) {
		t.Fatal("hashSecret not stable")
	}
	if bytes.Equal(a, c) {
		t.Fatal("hashSecret collided for different inputs")
	}
	if len(a) != 32 {
		t.Fatalf("hashSecret len = %d, want 32", len(a))
	}
}

func TestNewSecret(t *testing.T) {
	raw, err := newInviteSecret()
	if err != nil {
		t.Fatal(err)
	}
	if raw == "" {
		t.Fatal("empty secret")
	}
	if len(hashSecret(raw)) != 32 {
		t.Fatal("bad hash")
	}
}

func TestStoreRoundTrip(t *testing.T) {
	db := openInviteStoreTestDB(t)
	svc := NewDataService(db, "test")
	svc.setServerIDForTest(inviteStoreTestServerID)
	ctx := context.Background()

	seedInviteStoreUser(t, db, "creator1", "alice")
	seedInviteStoreUser(t, db, "invitee1", "bob")

	raw, err := newInviteSecret()
	if err != nil {
		t.Fatal(err)
	}
	hash := hashSecret(raw)
	creator := "creator1@" + inviteStoreTestServerID
	rawID, err := newInviteID()
	if err != nil {
		t.Fatal(err)
	}
	id := creator + "/" + rawID
	now := time.Now().UTC().Truncate(time.Second)
	if err := svc.insertInvite(ctx, id, creator, hash, now, roleUser, "seed-ufp", "sig"); err != nil {
		t.Fatalf("insertInvite: %v", err)
	}

	got, err := svc.getInviteByTokenHash(ctx, hash)
	if err != nil || got == nil {
		t.Fatalf("getInviteByTokenHash: %v %#v", got, err)
	}
	if got.ID != id || got.CreatedBy != creator || got.Status() != "pending" {
		t.Fatalf("unexpected invite: %+v", got)
	}
	_ = raw

	n, err := svc.countInvitesByCreator(ctx, creator)
	if err != nil || n != 1 {
		t.Fatalf("countInvitesByCreator = %d, %v", n, err)
	}

	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	ok, err := svc.markInviteClaimed(ctx, tx, id, "invitee1", now.Add(time.Minute))
	if err != nil || !ok {
		tx.Rollback()
		t.Fatalf("markInviteClaimed: ok=%v err=%v", ok, err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	tx2, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	ok2, err := svc.markInviteClaimed(ctx, tx2, id, "invitee1", now.Add(2*time.Minute))
	tx2.Rollback()
	if err != nil {
		t.Fatalf("second markInviteClaimed err: %v", err)
	}
	if ok2 {
		t.Fatal("second markInviteClaimed should not update")
	}

	claimed, err := svc.getInviteByTokenHash(ctx, hash)
	if err != nil || claimed.Status() != "claimed" {
		t.Fatalf("expected claimed: %+v %v", claimed, err)
	}

	n, err = svc.countInvitesByCreator(ctx, creator)
	if err != nil || n != 1 {
		t.Fatalf("countInvitesByCreator after claim = %d, %v", n, err)
	}
}

func TestRevokeDistinguishesClaimed(t *testing.T) {
	db := openInviteStoreTestDB(t)
	svc := NewDataService(db, "test")
	svc.setServerIDForTest(inviteStoreTestServerID)
	ctx := context.Background()

	seedInviteStoreUser(t, db, "creator2", "carol")
	seedInviteStoreUser(t, db, "invitee2", "dave")

	creator := "creator2@" + inviteStoreTestServerID
	secret, err := newInviteSecret()
	if err != nil {
		t.Fatal(err)
	}
	hash := hashSecret(secret)
	rawID, err := newInviteID()
	if err != nil {
		t.Fatal(err)
	}
	id := creator + "/" + rawID
	now := time.Now().UTC().Truncate(time.Second)
	if err := svc.insertInvite(ctx, id, creator, hash, now, roleUser, "seed-ufp", "sig"); err != nil {
		t.Fatal(err)
	}

	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	ok, err := svc.markInviteClaimed(ctx, tx, id, "invitee2", now)
	if err != nil || !ok {
		tx.Rollback()
		t.Fatalf("markInviteClaimed: %v %v", ok, err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	err = svc.revokeInvite(ctx, id, creator, now.Add(time.Minute))
	if err != errInviteAlreadyClaimed {
		t.Fatalf("revokeInvite claimed = %v, want errInviteAlreadyClaimed", err)
	}

	err = svc.revokeInvite(ctx, "missing", creator, now)
	if err != errInviteNotFound {
		t.Fatalf("revokeInvite missing = %v, want errInviteNotFound", err)
	}
}

func TestRevokeAndCountIncludesRevoked(t *testing.T) {
	db := openInviteStoreTestDB(t)
	svc := NewDataService(db, "test")
	svc.setServerIDForTest(inviteStoreTestServerID)
	ctx := context.Background()

	seedInviteStoreUser(t, db, "creator3", "erin")

	creator := "creator3@" + inviteStoreTestServerID
	secret, err := newInviteSecret()
	if err != nil {
		t.Fatal(err)
	}
	hash := hashSecret(secret)
	rawID, err := newInviteID()
	if err != nil {
		t.Fatal(err)
	}
	id := creator + "/" + rawID
	now := time.Now().UTC().Truncate(time.Second)
	if err := svc.insertInvite(ctx, id, creator, hash, now, roleUser, "seed-ufp", "sig"); err != nil {
		t.Fatal(err)
	}
	if err := svc.revokeInvite(ctx, id, creator, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := svc.revokeInvite(ctx, id, creator, now.Add(2*time.Minute)); err != errInviteAlreadyRevoked {
		t.Fatalf("second revoke = %v, want errInviteAlreadyRevoked", err)
	}

	n, err := svc.countInvitesByCreator(ctx, creator)
	if err != nil || n != 1 {
		t.Fatalf("countInvitesByCreator should include revoked: %d %v", n, err)
	}

	got, err := svc.getInviteByID(ctx, id)
	if err != nil || got == nil || got.Status() != "revoked" {
		t.Fatalf("getInviteByID: %+v %v", got, err)
	}
}
