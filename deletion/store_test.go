package deletion

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"syrinx/identity"

	_ "github.com/lib/pq"
)

func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	return newTestDatabase(t, ensureTestSchema)
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
// never race a shared syrinx_test database against other packages'
// (or their own) concurrently-run tests.
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

func ensureTestSchema(db *sql.DB) error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS user_signatures (
			id             SERIAL PRIMARY KEY,
			fingerprint    VARCHAR(255) NOT NULL,
			signature      TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS server_signatures (
			id             SERIAL PRIMARY KEY,
			fingerprint    VARCHAR(255) NOT NULL,
			signature      TEXT NOT NULL,
			signed_at      TIMESTAMP NOT NULL
		)`,
		// Blank-slate reshape: drop dependents then users so CREATE matches
		// current DDL (IF NOT EXISTS would leave a stale inline-column table).
		`DROP TABLE IF EXISTS account_removals`,
		`DROP TABLE IF EXISTS reed_removals`,
		`DROP TABLE IF EXISTS user_key_revocations`,
		`DROP TABLE IF EXISTS user_keys CASCADE`,
		`DROP TABLE IF EXISTS users CASCADE`,
		`DROP TABLE IF EXISTS identities CASCADE`,
		// identities is the FK target for "a user" (see db.go).
		`CREATE TABLE identities (
			id VARCHAR(255) PRIMARY KEY,
			remote_user_id VARCHAR(255) NOT NULL,
			server_id VARCHAR(16),
			public_key_fingerprint VARCHAR(255),
			verified BOOLEAN NOT NULL DEFAULT FALSE,
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
			UNIQUE (remote_user_id, server_id)
		)`,
		`CREATE TABLE users (
			id VARCHAR(255) PRIMARY KEY REFERENCES identities(id) ON DELETE CASCADE,
			username VARCHAR(255) UNIQUE,
			user_fingerprint VARCHAR(255),
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
			user_signature_id INT REFERENCES user_signatures(id),
			server_signature_id INT REFERENCES server_signatures(id)
		)`,
		`CREATE TABLE user_keys (
			fingerprint VARCHAR(255) UNIQUE NOT NULL,
			owner VARCHAR(255) NOT NULL REFERENCES identities(id) ON DELETE CASCADE,
			armor TEXT NOT NULL,
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
			server_signature_id INT NOT NULL REFERENCES server_signatures(id),
			PRIMARY KEY (owner, fingerprint)
		)`,
		`CREATE TABLE reed_removals (
			reed_id VARCHAR(255) NOT NULL,
			user_id VARCHAR(255) NOT NULL REFERENCES identities(id),
			user_fingerprint VARCHAR(255) NOT NULL,
			user_signature_id INT NOT NULL REFERENCES user_signatures(id),
			server_signature_id INT NOT NULL REFERENCES server_signatures(id),
			PRIMARY KEY (user_id, reed_id),
			FOREIGN KEY (user_id, user_fingerprint)
				REFERENCES user_keys(owner, fingerprint)
				ON DELETE CASCADE
		)`,
		`CREATE TABLE account_removals (
			user_id VARCHAR(255) PRIMARY KEY REFERENCES identities(id),
			note VARCHAR(140) NOT NULL DEFAULT '',
			user_fingerprint VARCHAR(255) NOT NULL,
			user_signature_id INT NOT NULL REFERENCES user_signatures(id),
			server_signature_id INT NOT NULL REFERENCES server_signatures(id),
			FOREIGN KEY (user_id, user_fingerprint)
				REFERENCES user_keys(owner, fingerprint)
				ON DELETE CASCADE,
			CONSTRAINT account_removals_note_len CHECK (char_length(note) <= 140)
		)`,
		`CREATE TABLE IF NOT EXISTS network_stats (
			id BOOLEAN PRIMARY KEY DEFAULT TRUE CHECK (id),
			active_users INT NOT NULL DEFAULT 0
		)`,
		`INSERT INTO network_stats (id, active_users) VALUES (TRUE, 0)
			ON CONFLICT (id) DO NOTHING`,
	}
	for _, s := range stmts {
		if _, err := db.Exec(s); err != nil {
			return err
		}
	}
	return nil
}

// testServerID is the serverID used to mint identities.id values here;
// must match the serverID passed to InsertCert/GetCert/etc.
const testServerID = "test-srv"

func seedUser(t *testing.T, db *sql.DB, userID, username string) {
	t.Helper()
	identityID := string(identity.LocalID(userID, testServerID))
	if _, err := db.Exec(`
		INSERT INTO identities (id, remote_user_id, server_id, verified)
		VALUES ($1, $2, $3, TRUE)
	`, identityID, userID, testServerID); err != nil {
		t.Fatalf("seed identities: %v", err)
	}
	var userSigID, serverSigID int64
	err := db.QueryRow(`
		INSERT INTO user_signatures (fingerprint, signature)
		VALUES ('seed-ufp', 'u') RETURNING id
	`).Scan(&userSigID)
	if err != nil {
		t.Fatalf("seed user_signatures: %v", err)
	}
	err = db.QueryRow(`
		INSERT INTO server_signatures (fingerprint, signature, signed_at)
		VALUES ('seed-sfp', 's', NOW()) RETURNING id
	`).Scan(&serverSigID)
	if err != nil {
		t.Fatalf("seed server_signatures: %v", err)
	}
	_, err = db.Exec(`
		INSERT INTO users (id, username, user_signature_id, server_signature_id)
		VALUES ($1, $2, $3, $4)
	`, identityID, username, userSigID, serverSigID)
	if err != nil {
		t.Fatalf("seed user: %v", err)
	}
}

func seedUserKey(t *testing.T, db *sql.DB, userID, fingerprint string) {
	t.Helper()
	identityID := string(identity.LocalID(userID, testServerID))
	var serverSigID int64
	err := db.QueryRow(`
		INSERT INTO server_signatures (fingerprint, signature, signed_at)
		VALUES ('key-sfp', 's', NOW()) RETURNING id
	`).Scan(&serverSigID)
	if err != nil {
		t.Fatalf("seed key server_signatures: %v", err)
	}
	_, err = db.Exec(`
		INSERT INTO user_keys (fingerprint, owner, armor, server_signature_id)
		VALUES ($1, $2, 'armor', $3)
	`, fingerprint, identityID, serverSigID)
	if err != nil {
		t.Fatalf("seed key: %v", err)
	}
}

func TestInsertCert_IdempotentAndConflict(t *testing.T) {
	db := openTestDB(t)
	userID := fmt.Sprintf("rm-user-%d", time.Now().UnixNano())
	reedID := fmt.Sprintf("rm-reed-%d", time.Now().UnixNano())
	fp := fmt.Sprintf("rm-fp-%d", time.Now().UnixNano())
	identityID := string(identity.LocalID(userID, testServerID))

	seedUser(t, db, userID, "u-"+userID)
	seedUserKey(t, db, userID, fp)
	t.Cleanup(func() {
		_, _ = db.Exec(`DELETE FROM reed_removals WHERE user_id = $1`, identityID)
		_, _ = db.Exec(`DELETE FROM user_keys WHERE owner = $1`, identityID)
		_, _ = db.Exec(`DELETE FROM users WHERE id = $1`, identityID)
		_, _ = db.Exec(`DELETE FROM identities WHERE id = $1`, identityID)
	})

	cert := Cert{
		ReedID:            reedID,
		UserID:            userID,
		UserSignature:     "user-sig",
		UserFingerprint:   fp,
		ServerSignature:   "server-sig",
		ServerFingerprint: "server-fp",
		ServerSignedAt:    time.Now().UTC(),
	}
	if err := InsertCert(context.Background(), db, cert, testServerID); err != nil {
		t.Fatalf("first insert: %v", err)
	}
	if err := InsertCert(context.Background(), db, cert, testServerID); err != nil {
		t.Fatalf("identical replay: %v", err)
	}

	got, err := GetCert(context.Background(), db, userID, reedID, testServerID)
	if err != nil || got == nil {
		t.Fatalf("get: got=%v err=%v", got, err)
	}
	if got.UserFingerprint != fp {
		t.Fatalf("fingerprint=%q", got.UserFingerprint)
	}

	conflict := cert
	conflict.UserSignature = "other"
	if err := InsertCert(context.Background(), db, conflict, testServerID); err != ErrConflict {
		t.Fatalf("want ErrConflict, got %v", err)
	}
}

func TestInsertAccountCert_IdempotentConflictAndNote(t *testing.T) {
	db := openTestDB(t)
	userID := fmt.Sprintf("ar-user-%d", time.Now().UnixNano())
	fp := fmt.Sprintf("ar-fp-%d", time.Now().UnixNano())
	identityID := string(identity.LocalID(userID, testServerID))

	seedUser(t, db, userID, "u-"+userID)
	seedUserKey(t, db, userID, fp)
	t.Cleanup(func() {
		_, _ = db.Exec(`DELETE FROM account_removals WHERE user_id = $1`, identityID)
		_, _ = db.Exec(`DELETE FROM user_keys WHERE owner = $1`, identityID)
		_, _ = db.Exec(`DELETE FROM users WHERE id = $1`, identityID)
		_, _ = db.Exec(`DELETE FROM identities WHERE id = $1`, identityID)
	})

	if err := ValidateAccountNote(string(make([]rune, MaxAccountNoteLen+1))); err == nil {
		t.Fatal("expected note length error")
	}

	cert := AccountCert{
		UserID:            userID,
		Note:              "goodbye",
		UserSignature:     "user-sig",
		UserFingerprint:   fp,
		ServerSignature:   "server-sig",
		ServerFingerprint: "server-fp",
		ServerSignedAt:    time.Now().UTC(),
	}
	if err := InsertAccountCert(context.Background(), db, cert, testServerID); err != nil {
		t.Fatalf("first insert: %v", err)
	}
	if err := InsertAccountCert(context.Background(), db, cert, testServerID); err != nil {
		t.Fatalf("identical replay: %v", err)
	}

	got, err := GetAccountCert(context.Background(), db, userID, testServerID)
	if err != nil || got == nil {
		t.Fatalf("get: got=%v err=%v", got, err)
	}
	if got.Note != "goodbye" {
		t.Fatalf("note=%q", got.Note)
	}
	ok, err := HasAccountRemoval(context.Background(), db, userID, testServerID)
	if err != nil || !ok {
		t.Fatalf("has: ok=%v err=%v", ok, err)
	}

	conflict := cert
	conflict.UserSignature = "other"
	if err := InsertAccountCert(context.Background(), db, conflict, testServerID); err != ErrConflict {
		t.Fatalf("want ErrConflict, got %v", err)
	}

	long := cert
	long.Note = string(make([]rune, MaxAccountNoteLen+1))
	if err := InsertAccountCert(context.Background(), db, long, testServerID); err == nil {
		t.Fatal("expected note length rejection")
	}
}
