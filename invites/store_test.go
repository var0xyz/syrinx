package invites

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"os"
	"testing"
	"time"

	"syrinx/roles"

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
		envOr("DB_NAME", "syrinx_test"),
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
			role VARCHAR(16) NOT NULL DEFAULT 'user'
				CHECK (role IN ('root', 'admin', 'user')),
			bio TEXT,
			user_fingerprint VARCHAR(255),
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
			user_signature_id INT NOT NULL REFERENCES user_signatures(id),
			server_signature_id INT NOT NULL REFERENCES server_signatures(id),
			invited_by VARCHAR(255) REFERENCES users(id)
		)`,
		`CREATE TABLE invites (
			created_by VARCHAR(255) NOT NULL REFERENCES users(id),
			id         VARCHAR(255) NOT NULL,
			token_hash BYTEA NOT NULL UNIQUE,
			created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
			claimed_at TIMESTAMPTZ,
			claimed_by VARCHAR(255) REFERENCES users(id),
			revoked_at TIMESTAMPTZ,
			granted_role VARCHAR(16) NOT NULL DEFAULT 'user'
				CHECK (granted_role IN ('admin', 'user')),
			PRIMARY KEY (created_by, id)
		)`,
	}
	for _, s := range stmts {
		if _, err := db.Exec(s); err != nil {
			return err
		}
	}
	return nil
}

func seedUserWithRole(t *testing.T, db *sql.DB, userID, username, role string) {
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
		INSERT INTO users (id, username, role, user_signature_id, server_signature_id)
		VALUES ($1, $2, $3, $4, $5)
	`, userID, username, role, userSigID, serverSigID)
	if err != nil {
		t.Fatalf("user: %v", err)
	}
}

func seedUser(t *testing.T, db *sql.DB, userID, username string) {
	seedUserWithRole(t, db, userID, username, roles.RoleUser)
}

func TestHashSecret(t *testing.T) {
	a := HashSecret("same")
	b := HashSecret("same")
	c := HashSecret("other")
	if !bytes.Equal(a, b) {
		t.Fatal("HashSecret not stable")
	}
	if bytes.Equal(a, c) {
		t.Fatal("HashSecret collided for different inputs")
	}
	if len(a) != 32 {
		t.Fatalf("HashSecret len = %d, want 32", len(a))
	}
}

func TestNewSecret(t *testing.T) {
	raw, err := NewSecret()
	if err != nil {
		t.Fatal(err)
	}
	if raw == "" {
		t.Fatal("empty secret")
	}
	if len(HashSecret(raw)) != 32 {
		t.Fatal("bad hash")
	}
}

func TestStoreRoundTrip(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	store := &Store{DB: db}

	seedUser(t, db, "creator1", "alice")
	seedUser(t, db, "invitee1", "bob")

	raw, err := NewSecret()
	if err != nil {
		t.Fatal(err)
	}
	hash := HashSecret(raw)
	id, err := NewInviteID()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	if err := store.Insert(ctx, id, "creator1", hash, now, roles.RoleUser); err != nil {
		t.Fatalf("Insert: %v", err)
	}

	got, err := store.GetByTokenHash(ctx, hash)
	if err != nil || got == nil {
		t.Fatalf("GetByTokenHash: %v %#v", got, err)
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
	ok, err := store.MarkClaimed(ctx, tx, "creator1", id, "invitee1", now.Add(time.Minute))
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
	ok2, err := store.MarkClaimed(ctx, tx2, "creator1", id, "invitee1", now.Add(2*time.Minute))
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

	secret, err := NewSecret()
	if err != nil {
		t.Fatal(err)
	}
	hash := HashSecret(secret)
	id, err := NewInviteID()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	if err := store.Insert(ctx, id, "creator2", hash, now, roles.RoleUser); err != nil {
		t.Fatal(err)
	}

	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	ok, err := store.MarkClaimed(ctx, tx, "creator2", id, "invitee2", now)
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

	secret, err := NewSecret()
	if err != nil {
		t.Fatal(err)
	}
	hash := HashSecret(secret)
	id, err := NewInviteID()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	if err := store.Insert(ctx, id, "creator3", hash, now, roles.RoleUser); err != nil {
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

	got, err := store.GetByCreatorAndID(ctx, "creator3", id)
	if err != nil || got == nil || got.Status() != "revoked" {
		t.Fatalf("GetByCreatorAndID: %+v %v", got, err)
	}
}
