package deletion

import (
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
	if err := ensureTestSchema(db); err != nil {
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

func ensureTestSchema(db *sql.DB) error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS user_signatures (
			id             SERIAL PRIMARY KEY,
			fingerprint    VARCHAR(255) NOT NULL,
			signature      TEXT NOT NULL,
			algorithm      TEXT NOT NULL DEFAULT 'PGP+base64',
			signed_fields  TEXT[] NOT NULL DEFAULT '{}'
		)`,
		`CREATE TABLE IF NOT EXISTS server_signatures (
			id             SERIAL PRIMARY KEY,
			fingerprint    VARCHAR(255) NOT NULL,
			signature      TEXT NOT NULL,
			signed_at      TIMESTAMP NOT NULL,
			algorithm      TEXT NOT NULL DEFAULT 'PGP+base64',
			signed_fields  TEXT[] NOT NULL DEFAULT '{}'
		)`,
		// Blank-slate reshape: drop dependents then users so CREATE matches
		// current DDL (IF NOT EXISTS would leave a stale inline-column table).
		`DROP TABLE IF EXISTS account_removals`,
		`DROP TABLE IF EXISTS reed_removals`,
		`DROP TABLE IF EXISTS user_key_revocations`,
		`DROP TABLE IF EXISTS user_keys CASCADE`,
		`DROP TABLE IF EXISTS users CASCADE`,
		`CREATE TABLE users (
			id VARCHAR(255) PRIMARY KEY,
			username VARCHAR(255) UNIQUE NOT NULL,
			user_fingerprint VARCHAR(255),
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
			user_signature_id INT NOT NULL REFERENCES user_signatures(id),
			server_signature_id INT NOT NULL REFERENCES server_signatures(id)
		)`,
		`CREATE TABLE user_keys (
			fingerprint VARCHAR(255) UNIQUE NOT NULL,
			owner VARCHAR(255) NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			armor TEXT NOT NULL,
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
			server_signature_id INT NOT NULL REFERENCES server_signatures(id),
			PRIMARY KEY (owner, fingerprint)
		)`,
		`CREATE TABLE reed_removals (
			reed_id VARCHAR(255) UNIQUE NOT NULL,
			user_id VARCHAR(255) NOT NULL REFERENCES users(id),
			user_signature TEXT NOT NULL,
			user_fingerprint VARCHAR(255) NOT NULL,
			server_signature TEXT NOT NULL,
			server_fingerprint VARCHAR(255) NOT NULL,
			server_signed_at TIMESTAMP NOT NULL,
			PRIMARY KEY (user_id, reed_id),
			FOREIGN KEY (user_id, user_fingerprint)
				REFERENCES user_keys(owner, fingerprint)
				ON DELETE CASCADE
		)`,
		`CREATE TABLE account_removals (
			user_id VARCHAR(255) PRIMARY KEY REFERENCES users(id),
			note VARCHAR(140) NOT NULL DEFAULT '',
			user_signature TEXT NOT NULL,
			user_fingerprint VARCHAR(255) NOT NULL,
			server_signature TEXT NOT NULL,
			server_fingerprint VARCHAR(255) NOT NULL,
			server_signed_at TIMESTAMP NOT NULL,
			FOREIGN KEY (user_id, user_fingerprint)
				REFERENCES user_keys(owner, fingerprint)
				ON DELETE CASCADE,
			CONSTRAINT account_removals_note_len CHECK (char_length(note) <= 140)
		)`,
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
	`, userID, username, userSigID, serverSigID)
	if err != nil {
		t.Fatalf("seed user: %v", err)
	}
}

func seedUserKey(t *testing.T, db *sql.DB, userID, fingerprint string) {
	t.Helper()
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
	`, fingerprint, userID, serverSigID)
	if err != nil {
		t.Fatalf("seed key: %v", err)
	}
}

func TestInsertCert_IdempotentAndConflict(t *testing.T) {
	db := openTestDB(t)
	userID := fmt.Sprintf("rm-user-%d", time.Now().UnixNano())
	reedID := fmt.Sprintf("rm-reed-%d", time.Now().UnixNano())
	fp := fmt.Sprintf("rm-fp-%d", time.Now().UnixNano())

	seedUser(t, db, userID, "u-"+userID)
	seedUserKey(t, db, userID, fp)
	t.Cleanup(func() {
		_, _ = db.Exec(`DELETE FROM reed_removals WHERE user_id = $1`, userID)
		_, _ = db.Exec(`DELETE FROM user_keys WHERE owner = $1`, userID)
		_, _ = db.Exec(`DELETE FROM users WHERE id = $1`, userID)
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
	if err := InsertCert(db, cert); err != nil {
		t.Fatalf("first insert: %v", err)
	}
	if err := InsertCert(db, cert); err != nil {
		t.Fatalf("identical replay: %v", err)
	}

	got, err := GetCert(db, userID, reedID)
	if err != nil || got == nil {
		t.Fatalf("get: got=%v err=%v", got, err)
	}
	if got.UserFingerprint != fp {
		t.Fatalf("fingerprint=%q", got.UserFingerprint)
	}

	conflict := cert
	conflict.UserSignature = "other"
	if err := InsertCert(db, conflict); err != ErrConflict {
		t.Fatalf("want ErrConflict, got %v", err)
	}
}

func TestInsertAccountCert_IdempotentConflictAndNote(t *testing.T) {
	db := openTestDB(t)
	userID := fmt.Sprintf("ar-user-%d", time.Now().UnixNano())
	fp := fmt.Sprintf("ar-fp-%d", time.Now().UnixNano())

	seedUser(t, db, userID, "u-"+userID)
	seedUserKey(t, db, userID, fp)
	t.Cleanup(func() {
		_, _ = db.Exec(`DELETE FROM account_removals WHERE user_id = $1`, userID)
		_, _ = db.Exec(`DELETE FROM user_keys WHERE owner = $1`, userID)
		_, _ = db.Exec(`DELETE FROM users WHERE id = $1`, userID)
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
	if err := InsertAccountCert(db, cert); err != nil {
		t.Fatalf("first insert: %v", err)
	}
	if err := InsertAccountCert(db, cert); err != nil {
		t.Fatalf("identical replay: %v", err)
	}

	got, err := GetAccountCert(db, userID)
	if err != nil || got == nil {
		t.Fatalf("get: got=%v err=%v", got, err)
	}
	if got.Note != "goodbye" {
		t.Fatalf("note=%q", got.Note)
	}
	ok, err := HasAccountRemoval(db, userID)
	if err != nil || !ok {
		t.Fatalf("has: ok=%v err=%v", ok, err)
	}

	conflict := cert
	conflict.UserSignature = "other"
	if err := InsertAccountCert(db, conflict); err != ErrConflict {
		t.Fatalf("want ErrConflict, got %v", err)
	}

	long := cert
	long.Note = string(make([]rune, MaxAccountNoteLen+1))
	if err := InsertAccountCert(db, long); err == nil {
		t.Fatal("expected note length rejection")
	}
}
