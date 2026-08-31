//go:build !ops

package main

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"syrinx/identity"

	"github.com/google/uuid"
)

func ensureMentionsSchema(db *sql.DB) error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS servers (id VARCHAR(255) PRIMARY KEY, name VARCHAR(255), self BOOLEAN NOT NULL DEFAULT FALSE)`,
		`INSERT INTO servers (id, name, self) VALUES ('testserver', 'Test Server', TRUE) ON CONFLICT (id) DO UPDATE SET name = EXCLUDED.name, self = EXCLUDED.self`,
		`CREATE TABLE IF NOT EXISTS user_signatures (id SERIAL PRIMARY KEY, public_key_id VARCHAR(255) NOT NULL, signature TEXT NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS server_signatures (id SERIAL PRIMARY KEY, private_key_id VARCHAR(255) NOT NULL, signature TEXT NOT NULL, signed_at TIMESTAMP NOT NULL)`,
		`DROP TABLE IF EXISTS reed_mentions CASCADE`,
		`DROP TABLE IF EXISTS reed_allocations CASCADE`,
		`DROP TABLE IF EXISTS pending_fanout CASCADE`,
		`DROP TABLE IF EXISTS reeds CASCADE`,
		`DROP TABLE IF EXISTS reed_identities CASCADE`,
		`DROP TABLE IF EXISTS account_removals CASCADE`,
		`DROP TABLE IF EXISTS users CASCADE`,
		`DROP TABLE IF EXISTS identities CASCADE`,
		// identities is the FK target for "a user" (see db.go) —
		// CreateReed/MentionTargetValid/SearchUsers all resolve through it.
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
		`CREATE TABLE account_removals (
			user_id VARCHAR(255) PRIMARY KEY REFERENCES identities(id)
		)`,
		`CREATE TABLE reed_identities (
			id VARCHAR(255) PRIMARY KEY,
			server_id VARCHAR(16) NOT NULL
		)`,
		`CREATE TABLE reeds (
			id VARCHAR(255) PRIMARY KEY REFERENCES reed_identities(id) ON DELETE CASCADE,
			user_id VARCHAR(255) NOT NULL REFERENCES identities(id),
			private_key_id VARCHAR(255) NOT NULL,
			signed_at TIMESTAMP NOT NULL,
			user_signature_id INT NOT NULL REFERENCES user_signatures(id),
			server_signature_id INT NOT NULL REFERENCES server_signatures(id)
		)`,
		`CREATE TABLE reed_allocations (
			reed_id VARCHAR(255) NOT NULL,
			holder_user_id VARCHAR(255) NOT NULL REFERENCES identities(id) ON DELETE CASCADE,
			delivered_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
			PRIMARY KEY (holder_user_id, reed_id),
			FOREIGN KEY (reed_id) REFERENCES reeds(id) ON DELETE CASCADE
		)`,
		`CREATE TABLE pending_fanout (
			reed_id VARCHAR(255) NOT NULL,
			tags TEXT[] NOT NULL DEFAULT '{}',
			PRIMARY KEY (reed_id),
			FOREIGN KEY (reed_id) REFERENCES reeds(id) ON DELETE CASCADE
		)`,
		// mentioned_user_id FKs identities(id) — backstops "only local
		// users can be indexed" (see db.go). mentioning_reed_id FKs
		// reed_identities, not reeds, so a foreign mentioning reed can be
		// represented too.
		`CREATE TABLE reed_mentions (
			mentioning_reed_id VARCHAR(255) NOT NULL REFERENCES reed_identities(id) ON DELETE CASCADE,
			mentioned_user_id VARCHAR(255) NOT NULL REFERENCES identities(id) ON DELETE CASCADE,
			PRIMARY KEY (mentioning_reed_id, mentioned_user_id)
		)`,
		`DROP TABLE IF EXISTS reed_removals CASCADE`,
		`CREATE TABLE reed_removals (
			reed_id VARCHAR(255) PRIMARY KEY
		)`,
	}
	for _, s := range stmts {
		if _, err := db.Exec(s); err != nil {
			return err
		}
	}
	return nil
}

func openMentionsTestDB(t *testing.T) *sql.DB {
	t.Helper()
	return newTestDatabase(t, ensureMentionsSchema)
}

// seedMentionUser mints an identities row (server_id = "testserver",
// matching every DataService{serverID: "testserver"} in this file) before
// the satellite users row, mirroring services.go's Signup.
func seedMentionUser(t *testing.T, db *sql.DB, userID string) {
	t.Helper()
	identityID := string(identity.CanonicalID("testserver", userID))
	if _, err := db.Exec(`INSERT INTO identities (id, server_id) VALUES ($1, $2) ON CONFLICT DO NOTHING`,
		identityID, "testserver"); err != nil {
		t.Fatal(err)
	}
	var usID, ssID int
	if err := db.QueryRow(`INSERT INTO user_signatures (public_key_id, signature) VALUES ($1, 'sig') RETURNING id`, userID+"fp").Scan(&usID); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`INSERT INTO server_signatures (fingerprint, signature, signed_at) VALUES ($1, 'sig', NOW()) RETURNING id`, "srvfp-"+userID).Scan(&ssID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO users (id, username, user_signature_id, server_signature_id) VALUES ($1, $2, $3, $4) ON CONFLICT DO NOTHING`,
		identityID, userID+"name", usID, ssID); err != nil {
		t.Fatal(err)
	}
}

func newTestReedID(t *testing.T) string {
	t.Helper()
	id, err := uuid.NewV7()
	if err != nil {
		t.Fatal(err)
	}
	return id.String()
}

func TestCreateReed_MentionsIndexed(t *testing.T) {
	db := openMentionsTestDB(t)
	ctx := context.Background()
	svc := &DataService{db: db, serverID: "testserver"}

	seedMentionUser(t, db, "alice")
	seedMentionUser(t, db, "bob")
	seedMentionUser(t, db, "carol")

	reedID := newTestReedID(t)
	ts := time.Now().UTC().Truncate(time.Second)
	mentions := []string{"bob@testserver", "carol@testserver"}

	_, err := svc.CreateReed(ctx, createReedParams{
		ReedID:             reedID,
		UserID:             "alice@testserver",
		UserKeyID:          "alicefp",
		UserSignatureB64:   "usersig",
		ServerFingerprint:  "srvfp-alice",
		ServerSignatureB64: "serversig",
		Timestamp:          ts,
		Mentions:           mentions,
	})
	if err != nil {
		t.Fatalf("CreateReed: %v", err)
	}

	rows, err := db.Query(`SELECT mentioned_user_id FROM reed_mentions WHERE mentioning_reed_id = $1 ORDER BY mentioned_user_id`, reedID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var got []string
	for rows.Next() {
		var u string
		if err := rows.Scan(&u); err != nil {
			t.Fatal(err)
		}
		got = append(got, u)
	}
	// reed_mentions.mentioned_user_id stores identity.CanonicalID(m.ServerID,
	// m.AuthorID), not the bare AuthorID.
	if len(got) != 2 || got[0] != "bob@testserver" || got[1] != "carol@testserver" {
		t.Fatalf("mentioned users = %v, want [bob@testserver carol@testserver]", got)
	}
}

// TestCreateReed_MentionOfNonexistentUserRejected guards the FK on
// reed_mentions.mentioned_user_id -> users(id): only local users can ever
// be indexed. This is what makes it safe to index foreign-server mentions
// as "never stored" at the handler layer — the FK is the backstop if that
// filtering is ever bypassed or wrong.
func TestCreateReed_MentionOfNonexistentUserRejected(t *testing.T) {
	db := openMentionsTestDB(t)
	ctx := context.Background()
	svc := &DataService{db: db, serverID: "testserver"}

	seedMentionUser(t, db, "alice")

	reedID := newTestReedID(t)
	ts := time.Now().UTC().Truncate(time.Second)
	_, err := svc.CreateReed(ctx, createReedParams{
		ReedID:             reedID,
		UserID:             "alice@testserver",
		UserKeyID:          "alicefp",
		UserSignatureB64:   "usersig",
		ServerFingerprint:  "srvfp-alice",
		ServerSignatureB64: "serversig",
		Timestamp:          ts,
		Mentions:           []string{"nobody-on-this-server@foreignsrv"},
	})
	if err == nil {
		t.Fatal("expected FK violation for a mention of a nonexistent local user")
	}
}

// TestInsertMentionRow_ForeignMentioningReed covers the mention-notify
// federation handler's path: the mentioning reed is authored on a peer
// (no reeds row here at all, only a reed_identities row from
// UpsertReedIdentity), and the mentioned user is local. This is the
// scenario RelayRequestFromPeer-style tests can't reach (no live DB), so
// it's exercised against the real schema instead.
func TestInsertMentionRow_ForeignMentioningReed(t *testing.T) {
	db := openMentionsTestDB(t)
	ctx := context.Background()
	svc := &DataService{db: db, serverID: "testserver"}

	seedMentionUser(t, db, "bob")

	foreignReedID := "alice@peerserver/" + newTestReedID(t)
	if err := svc.UpsertReedIdentity(ctx, foreignReedID); err != nil {
		t.Fatalf("UpsertReedIdentity: %v", err)
	}
	if err := svc.InsertMentionRow(ctx, foreignReedID, "bob@testserver"); err != nil {
		t.Fatalf("InsertMentionRow: %v", err)
	}

	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM reed_mentions WHERE mentioning_reed_id = $1 AND mentioned_user_id = 'bob@testserver'`, foreignReedID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("reed_mentions rows = %d, want 1", n)
	}
}

// TestInsertMentionRow_IdempotentOnRetry confirms a retried mention-notify
// delivery (e.g. after a timeout on the caller's side, retried) doesn't
// create a duplicate row.
func TestInsertMentionRow_IdempotentOnRetry(t *testing.T) {
	db := openMentionsTestDB(t)
	ctx := context.Background()
	svc := &DataService{db: db, serverID: "testserver"}

	seedMentionUser(t, db, "bob")

	foreignReedID := "alice@peerserver/" + newTestReedID(t)
	if err := svc.UpsertReedIdentity(ctx, foreignReedID); err != nil {
		t.Fatalf("UpsertReedIdentity: %v", err)
	}
	for i := 0; i < 2; i++ {
		if err := svc.InsertMentionRow(ctx, foreignReedID, "bob@testserver"); err != nil {
			t.Fatalf("InsertMentionRow attempt %d: %v", i, err)
		}
	}

	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM reed_mentions WHERE mentioning_reed_id = $1`, foreignReedID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("reed_mentions rows after two inserts = %d, want 1", n)
	}
}

// TestInsertMentionRow_RejectsUnknownMentionedUser confirms the FK backstop
// on mentioned_user_id still holds when inserting via this path directly
// (not just through CreateReed's transaction) — MentionNotifyFromPeer's own
// MentionTargetValid check is the primary guard, but this is the same
// belt-and-suspenders property TestCreateReed_MentionOfNonexistentUserRejected
// verifies for the local insert path.
func TestInsertMentionRow_RejectsUnknownMentionedUser(t *testing.T) {
	db := openMentionsTestDB(t)
	ctx := context.Background()
	svc := &DataService{db: db, serverID: "testserver"}

	foreignReedID := "alice@peerserver/" + newTestReedID(t)
	if err := svc.UpsertReedIdentity(ctx, foreignReedID); err != nil {
		t.Fatalf("UpsertReedIdentity: %v", err)
	}
	if err := svc.InsertMentionRow(ctx, foreignReedID, "nobody@testserver"); err == nil {
		t.Fatal("expected FK violation for a mention of a nonexistent local user")
	}
}

func TestDeleteMentionsForReed_ClearsRows(t *testing.T) {
	db := openMentionsTestDB(t)
	ctx := context.Background()
	svc := &DataService{db: db, serverID: "testserver"}

	seedMentionUser(t, db, "alice")
	seedMentionUser(t, db, "bob")

	bareReedID := newTestReedID(t)
	reedID := string(identity.AppendEntity(identity.IdentityID("alice@testserver"), bareReedID))
	ts := time.Now().UTC().Truncate(time.Second)
	_, err := svc.CreateReed(ctx, createReedParams{
		ReedID:             reedID,
		UserID:             "alice@testserver",
		UserKeyID:          "alicefp",
		UserSignatureB64:   "usersig",
		ServerFingerprint:  "srvfp-alice",
		ServerSignatureB64: "serversig",
		Timestamp:          ts,
		Mentions:           []string{"bob@testserver"},
	})
	if err != nil {
		t.Fatalf("CreateReed: %v", err)
	}

	if err := svc.DeleteMentionsForReed(ctx, reedID); err != nil {
		t.Fatalf("DeleteMentionsForReed: %v", err)
	}

	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM reed_mentions WHERE mentioning_reed_id = $1`, reedID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("expected 0 mention rows after delete, got %d", n)
	}
}

func TestDeleteMentionsByAuthor_ClearsBothSides(t *testing.T) {
	db := openMentionsTestDB(t)
	ctx := context.Background()
	svc := &DataService{db: db, serverID: "testserver"}

	seedMentionUser(t, db, "alice")
	seedMentionUser(t, db, "bob")
	seedMentionUser(t, db, "carol")

	// alice mentions bob (reed authored by alice)
	reed1 := newTestReedID(t)
	ts := time.Now().UTC().Truncate(time.Second)
	if _, err := svc.CreateReed(ctx, createReedParams{
		ReedID: reed1, UserID: "alice@testserver", UserKeyID: "alicefp",
		UserSignatureB64: "sig", ServerFingerprint: "srvfp-alice", ServerSignatureB64: "sig",
		Timestamp: ts, Mentions: []string{"bob@testserver"},
	}); err != nil {
		t.Fatalf("CreateReed 1: %v", err)
	}

	// carol mentions bob (a second, independent mention of bob — proves
	// DeleteMentionsByAuthor(bob) clears mentions of bob regardless of author)
	reed2 := newTestReedID(t)
	if _, err := svc.CreateReed(ctx, createReedParams{
		ReedID: reed2, UserID: "carol@testserver", UserKeyID: "carolfp",
		UserSignatureB64: "sig", ServerFingerprint: "srvfp-carol", ServerSignatureB64: "sig",
		Timestamp: ts, Mentions: []string{"bob@testserver"},
	}); err != nil {
		t.Fatalf("CreateReed 2: %v", err)
	}

	if err := svc.DeleteMentionsByAuthor(ctx, "bob@testserver"); err != nil {
		t.Fatalf("DeleteMentionsByAuthor: %v", err)
	}

	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM reed_mentions WHERE mentioned_user_id = 'bob@testserver'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("expected all mentions of bob cleared, got %d", n)
	}
}

func TestMentionTargetValid(t *testing.T) {
	db := openMentionsTestDB(t)
	ctx := context.Background()
	svc := &DataService{db: db, serverID: "testserver"}

	seedMentionUser(t, db, "alice")

	valid, err := svc.MentionTargetValid(ctx, "alice", "testserver")
	if err != nil {
		t.Fatal(err)
	}
	if !valid {
		t.Fatal("expected alice to be a valid mention target")
	}

	valid, err = svc.MentionTargetValid(ctx, "nobody", "testserver")
	if err != nil {
		t.Fatal(err)
	}
	if valid {
		t.Fatal("expected nonexistent user to be invalid mention target")
	}

	valid, err = svc.MentionTargetValid(ctx, "alice", "unknown-server")
	if err != nil {
		t.Fatal(err)
	}
	if valid {
		t.Fatal("expected unknown serverID to be invalid mention target")
	}

	// account_removals.user_id joins against u.identity_id, so this must
	// be the full id, not the bare username.
	if _, err := db.Exec(`INSERT INTO account_removals (user_id) VALUES ('alice@testserver')`); err != nil {
		t.Fatal(err)
	}
	valid, err = svc.MentionTargetValid(ctx, "alice", "testserver")
	if err != nil {
		t.Fatal(err)
	}
	if valid {
		t.Fatal("expected account-removed user to be invalid mention target")
	}
}

func TestSearchUsers(t *testing.T) {
	db := openMentionsTestDB(t)
	ctx := context.Background()
	svc := &DataService{db: db, serverID: "testserver"}

	seedMentionUser(t, db, "alice")
	seedMentionUser(t, db, "bob")
	// account_removals.user_id joins against u.identity_id.
	if _, err := db.Exec(`INSERT INTO account_removals (user_id) VALUES ('bob@testserver')`); err != nil {
		t.Fatal(err)
	}

	results, err := svc.SearchUsers(ctx, "alice", 20)
	if err != nil {
		t.Fatal(err)
	}
	// UserSearchResult.ID holds u.id directly (which IS identities.id).
	if len(results) != 1 || results[0].ID != "alice@testserver" || results[0].ServerName != "Test Server" {
		t.Fatalf("results = %+v", results)
	}

	results, err = svc.SearchUsers(ctx, "bobname", 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 0 {
		t.Fatalf("expected account-removed user excluded, got %+v", results)
	}
}
