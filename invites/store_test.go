package invites

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"os"
	"testing"
	"time"

	_ "github.com/lib/pq"
)

func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	dsn := fmt.Sprintf(
		"host=%s port=%s user=%s password=%s dbname=%s sslmode=%s",
		envOr("DB_HOST", "localhost"),
		envOr("DB_PORT", "5432"),
		envOr("DB_USER", "syrinx"),
		envOr("DB_PASSWORD", "syrinx"),
		envOr("DB_NAME", "syrinx"),
		envOr("DB_SSLMODE", "disable"),
	)
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Skipf("open db: %v", err)
	}
	if err := db.Ping(); err != nil {
		db.Close()
		t.Skipf("ping db: %v", err)
	}
	if err := ensureInviteSchema(db); err != nil {
		db.Close()
		t.Fatalf("schema: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func ensureInviteSchema(db *sql.DB) error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS user_signatures (
			id SERIAL PRIMARY KEY,
			fingerprint VARCHAR(255) NOT NULL,
			signature TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS server_signatures (
			id SERIAL PRIMARY KEY,
			fingerprint VARCHAR(255) NOT NULL,
			signature TEXT NOT NULL,
			signed_at TIMESTAMP NOT NULL
		)`,
		`DROP TABLE IF EXISTS invites`,
		`DROP TABLE IF EXISTS users CASCADE`,
		`CREATE TABLE users (
			id VARCHAR(255) PRIMARY KEY,
			username VARCHAR(255) UNIQUE NOT NULL,
			user_fingerprint VARCHAR(255),
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
			user_signature_id INT NOT NULL REFERENCES user_signatures(id),
			server_signature_id INT NOT NULL REFERENCES server_signatures(id),
			invited_by VARCHAR(255) REFERENCES users(id)
		)`,
		`CREATE TABLE invites (
			id         VARCHAR(255) PRIMARY KEY,
			token_hash BYTEA NOT NULL UNIQUE,
			created_by VARCHAR(255) NOT NULL REFERENCES users(id),
			created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
			claimed_at TIMESTAMPTZ,
			claimed_by VARCHAR(255) REFERENCES users(id),
			revoked_at TIMESTAMPTZ
		)`,
		`CREATE INDEX IF NOT EXISTS idx_invites_created_by ON invites(created_by)`,
	}
	for _, s := range stmts {
		if _, err := db.Exec(s); err != nil {
			return err
		}
	}
	return nil
}

func seedUser(t *testing.T, db *sql.DB, userID, username string) {
	t.Helper()
	var userSigID, serverSigID int64
	err := db.QueryRow(`
		INSERT INTO user_signatures (fingerprint, signature)
		VALUES ('seed-ufp', 'u') RETURNING id
	`).Scan(&userSigID)
	if err != nil {
		t.Fatalf("user sig: %v", err)
	}
	err = db.QueryRow(`
		INSERT INTO server_signatures (fingerprint, signature, signed_at)
		VALUES ('seed-sfp', 's', NOW()) RETURNING id
	`).Scan(&serverSigID)
	if err != nil {
		t.Fatalf("server sig: %v", err)
	}
	_, err = db.Exec(`
		INSERT INTO users (id, username, user_signature_id, server_signature_id)
		VALUES ($1, $2, $3, $4)
	`, userID, username, userSigID, serverSigID)
	if err != nil {
		t.Fatalf("user: %v", err)
	}
}

func TestHashToken(t *testing.T) {
	a := HashToken("same")
	b := HashToken("same")
	c := HashToken("other")
	if !bytes.Equal(a, b) {
		t.Fatal("HashToken not stable")
	}
	if bytes.Equal(a, c) {
		t.Fatal("HashToken collided for different inputs")
	}
	if len(a) != 32 {
		t.Fatalf("HashToken len = %d, want 32", len(a))
	}
}

func TestNewToken(t *testing.T) {
	raw, hash, err := NewToken()
	if err != nil {
		t.Fatal(err)
	}
	if raw == "" || len(hash) != 32 {
		t.Fatalf("empty token or bad hash")
	}
	if !bytes.Equal(HashToken(raw), hash) {
		t.Fatal("NewToken hash mismatch")
	}
}

func TestStoreRoundTrip(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	store := &Store{DB: db}

	seedUser(t, db, "creator1", "alice")
	seedUser(t, db, "invitee1", "bob")

	raw, hash, err := NewToken()
	if err != nil {
		t.Fatal(err)
	}
	id, err := NewInviteID()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	if err := store.Insert(ctx, id, "creator1", hash, now); err != nil {
		t.Fatalf("Insert: %v", err)
	}

	got, err := store.GetByTokenHash(ctx, hash)
	if err != nil || got == nil {
		t.Fatalf("GetByTokenHash: %v %#v", err, got)
	}
	if got.ID != id || got.CreatedBy != "creator1" || got.Status() != "pending" {
		t.Fatalf("unexpected invite: %+v", got)
	}
	_ = raw

	n, err := store.CountByCreator(ctx, "creator1")
	if err != nil || n != 1 {
		t.Fatalf("CountByCreator = %d, %v", n, err)
	}

	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	ok, err := store.MarkClaimed(ctx, tx, id, "invitee1", now.Add(time.Minute))
	if err != nil || !ok {
		tx.Rollback()
		t.Fatalf("MarkClaimed: ok=%v err=%v", ok, err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	tx2, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	ok2, err := store.MarkClaimed(ctx, tx2, id, "invitee1", now.Add(2*time.Minute))
	tx2.Rollback()
	if err != nil {
		t.Fatalf("second MarkClaimed err: %v", err)
	}
	if ok2 {
		t.Fatal("second MarkClaimed should not update")
	}

	claimed, err := store.GetByTokenHash(ctx, hash)
	if err != nil || claimed.Status() != "claimed" {
		t.Fatalf("expected claimed: %+v %v", claimed, err)
	}

	n, err = store.CountByCreator(ctx, "creator1")
	if err != nil || n != 1 {
		t.Fatalf("CountByCreator after claim = %d, %v", n, err)
	}
}

func TestRevokeDistinguishesClaimed(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	store := &Store{DB: db}

	seedUser(t, db, "creator2", "carol")
	seedUser(t, db, "invitee2", "dave")

	_, hash, err := NewToken()
	if err != nil {
		t.Fatal(err)
	}
	id, err := NewInviteID()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	if err := store.Insert(ctx, id, "creator2", hash, now); err != nil {
		t.Fatal(err)
	}

	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	ok, err := store.MarkClaimed(ctx, tx, id, "invitee2", now)
	if err != nil || !ok {
		tx.Rollback()
		t.Fatalf("MarkClaimed: %v %v", ok, err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	err = store.Revoke(ctx, id, "creator2", now.Add(time.Minute))
	if err != ErrInviteAlreadyClaimed {
		t.Fatalf("Revoke claimed = %v, want ErrInviteAlreadyClaimed", err)
	}

	err = store.Revoke(ctx, "missing", "creator2", now)
	if err != ErrInviteNotFound {
		t.Fatalf("Revoke missing = %v, want ErrInviteNotFound", err)
	}
}

func TestRevokeAndCountIncludesRevoked(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	store := &Store{DB: db}

	seedUser(t, db, "creator3", "erin")

	_, hash, err := NewToken()
	if err != nil {
		t.Fatal(err)
	}
	id, err := NewInviteID()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	if err := store.Insert(ctx, id, "creator3", hash, now); err != nil {
		t.Fatal(err)
	}
	if err := store.Revoke(ctx, id, "creator3", now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := store.Revoke(ctx, id, "creator3", now.Add(2*time.Minute)); err != ErrInviteAlreadyRevoked {
		t.Fatalf("second revoke = %v, want ErrInviteAlreadyRevoked", err)
	}

	n, err := store.CountByCreator(ctx, "creator3")
	if err != nil || n != 1 {
		t.Fatalf("CountByCreator should include revoked: %d %v", n, err)
	}

	list, err := store.ListByCreator(ctx, "creator3")
	if err != nil || len(list) != 1 || list[0].Status() != "revoked" {
		t.Fatalf("ListByCreator: %+v %v", list, err)
	}
}
