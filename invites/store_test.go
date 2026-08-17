package invites

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"syrinx/roles"

	_ "github.com/lib/pq"
)

func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	return newTestDatabase(t, ensureInviteSchema)
}

func testDSN(dbName string) string {
	return fmt.Sprintf(
		"host=%s port=%s user=%s password=%s dbname=%s sslmode=%s",
		envOr("DB_HOST", "localhost"),
		envOr("DB_PORT", "5432"),
		envOr("DB_USER", "syrinx"),
		envOr("DB_PASSWORD", "syrinx"),
		dbName,
		envOr("DB_SSLMODE", "disable"),
	)
}

// newTestDatabase creates a fresh, uniquely-named database for the
// duration of one test and drops it on cleanup, so this package's tests
// never race a shared syrinx_test database against other packages' (or
// their own) concurrently-run tests.
func newTestDatabase(t *testing.T, ensureSchema func(*sql.DB) error) *sql.DB {
	t.Helper()

	admin, err := sql.Open("postgres", testDSN(envOr("DB_MAINTENANCE_NAME", "postgres")))
	if err != nil {
		t.Skipf("open admin db: %v", err)
	}
	defer admin.Close()
	if err := admin.Ping(); err != nil {
		t.Skipf("ping admin db: %v", err)
	}

	n := atomic.AddInt64(&testDBCounter, 1)
	dbName := fmt.Sprintf("syrinx_test_%d_%d", time.Now().UnixNano(), n)

	if _, err := admin.Exec(fmt.Sprintf(`CREATE DATABASE %s`, dbName)); err != nil {
		t.Fatalf("create test db %s: %v", dbName, err)
	}
	t.Cleanup(func() {
		cleanupAdmin, err := sql.Open("postgres", testDSN(envOr("DB_MAINTENANCE_NAME", "postgres")))
		if err != nil {
			return
		}
		defer cleanupAdmin.Close()
		_, _ = cleanupAdmin.Exec(fmt.Sprintf(
			`SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname = '%s' AND pid <> pg_backend_pid()`,
			dbName,
		))
		_, _ = cleanupAdmin.Exec(fmt.Sprintf(`DROP DATABASE IF EXISTS %s`, dbName))
	})

	db, err := sql.Open("postgres", testDSN(dbName))
	if err != nil {
		t.Fatalf("open test db %s: %v", dbName, err)
	}
	if err := db.Ping(); err != nil {
		db.Close()
		t.Fatalf("ping test db %s: %v", dbName, err)
	}
	t.Cleanup(func() { db.Close() })

	if err := ensureSchema(db); err != nil {
		t.Fatalf("schema for %s: %v", dbName, err)
	}
	return db
}

var testDBCounter int64

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
		// local to testServerID below.
		`CREATE TABLE servers (
			id VARCHAR(16) UNIQUE,
			name VARCHAR(255) PRIMARY KEY,
			self BOOLEAN NOT NULL DEFAULT FALSE
		)`,
		`CREATE TABLE identities (
			id VARCHAR(255) PRIMARY KEY,
			server_id VARCHAR(16) REFERENCES servers(id)
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
			invited_by VARCHAR(255) REFERENCES identities(id)
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
				CHECK (granted_role IN ('admin', 'user'))
		)`,
		fmt.Sprintf(`INSERT INTO servers (id, name, self) VALUES ('%s', 'test', TRUE)`, testServerID),
	}
	for _, s := range stmts {
		if _, err := db.Exec(s); err != nil {
			return err
		}
	}
	return nil
}

// testServerID is the fixed self serverID seeded by ensureInviteSchema and
// used by every Store constructed in this package's tests — must match
// whatever serverID a test passes to &Store{ServerID: ...}.
const testServerID = "testsrv1"

func seedUserWithRole(t *testing.T, db *sql.DB, userID, username, role string) {
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
	identityID := userID + "@" + testServerID
	if _, err := db.Exec(`
		INSERT INTO identities (id, server_id)
		VALUES ($1, $2)
	`, identityID, testServerID); err != nil {
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
	store := &Store{DB: db, ServerID: testServerID}

	seedUser(t, db, "creator1", "alice")
	seedUser(t, db, "invitee1", "bob")

	raw, err := NewSecret()
	if err != nil {
		t.Fatal(err)
	}
	hash := HashSecret(raw)
	creator := "creator1@" + testServerID
	rawID, err := NewInviteID()
	if err != nil {
		t.Fatal(err)
	}
	id := creator + "/" + rawID
	now := time.Now().UTC().Truncate(time.Second)
	if err := store.Insert(ctx, id, creator, hash, now, roles.RoleUser); err != nil {
		t.Fatalf("Insert: %v", err)
	}

	got, err := store.GetByTokenHash(ctx, hash)
	if err != nil || got == nil {
		t.Fatalf("GetByTokenHash: %v %#v", got, err)
	}
	if got.ID != id || got.CreatedBy != creator || got.Status() != "pending" {
		t.Fatalf("unexpected invite: %+v", got)
	}
	_ = raw

	n, err := store.CountByCreator(ctx, creator)
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

	n, err = store.CountByCreator(ctx, creator)
	if err != nil || n != 1 {
		t.Fatalf("CountByCreator after claim = %d, %v", n, err)
	}
}

func TestRevokeDistinguishesClaimed(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	store := &Store{DB: db, ServerID: testServerID}

	seedUser(t, db, "creator2", "carol")
	seedUser(t, db, "invitee2", "dave")

	creator := "creator2@" + testServerID
	secret, err := NewSecret()
	if err != nil {
		t.Fatal(err)
	}
	hash := HashSecret(secret)
	rawID, err := NewInviteID()
	if err != nil {
		t.Fatal(err)
	}
	id := creator + "/" + rawID
	now := time.Now().UTC().Truncate(time.Second)
	if err := store.Insert(ctx, id, creator, hash, now, roles.RoleUser); err != nil {
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

	err = store.Revoke(ctx, id, creator, now.Add(time.Minute))
	if err != ErrInviteAlreadyClaimed {
		t.Fatalf("Revoke claimed = %v, want ErrInviteAlreadyClaimed", err)
	}

	err = store.Revoke(ctx, "missing", creator, now)
	if err != ErrInviteNotFound {
		t.Fatalf("Revoke missing = %v, want ErrInviteNotFound", err)
	}
}

func TestRevokeAndCountIncludesRevoked(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	store := &Store{DB: db, ServerID: testServerID}

	seedUser(t, db, "creator3", "erin")

	creator := "creator3@" + testServerID
	secret, err := NewSecret()
	if err != nil {
		t.Fatal(err)
	}
	hash := HashSecret(secret)
	rawID, err := NewInviteID()
	if err != nil {
		t.Fatal(err)
	}
	id := creator + "/" + rawID
	now := time.Now().UTC().Truncate(time.Second)
	if err := store.Insert(ctx, id, creator, hash, now, roles.RoleUser); err != nil {
		t.Fatal(err)
	}
	if err := store.Revoke(ctx, id, creator, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := store.Revoke(ctx, id, creator, now.Add(2*time.Minute)); err != ErrInviteAlreadyRevoked {
		t.Fatalf("second revoke = %v, want ErrInviteAlreadyRevoked", err)
	}

	n, err := store.CountByCreator(ctx, creator)
	if err != nil || n != 1 {
		t.Fatalf("CountByCreator should include revoked: %d %v", n, err)
	}

	got, err := store.GetByID(ctx, id)
	if err != nil || got == nil || got.Status() != "revoked" {
		t.Fatalf("GetByID: %+v %v", got, err)
	}
}
