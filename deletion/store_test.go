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
		`CREATE TABLE IF NOT EXISTS users (
			id VARCHAR(255) PRIMARY KEY,
			username VARCHAR(255) UNIQUE NOT NULL,
			user_fingerprint VARCHAR(255),
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
			user_signature TEXT NOT NULL,
			server_signature TEXT NOT NULL,
			server_signed_at TIMESTAMP NOT NULL,
			server_fingerprint VARCHAR(255) NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS user_keys (
			fingerprint VARCHAR(255) UNIQUE NOT NULL,
			owner VARCHAR(255) NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			armor TEXT NOT NULL,
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
			server_signature TEXT NOT NULL,
			server_fingerprint VARCHAR(255) NOT NULL,
			server_signed_at TIMESTAMP NOT NULL,
			PRIMARY KEY (owner, fingerprint)
		)`,
		// Drop so iterative DDL during proposal work does not leave a stale shape.
		`DROP TABLE IF EXISTS reed_removals`,
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
	}
	for _, s := range stmts {
		if _, err := db.Exec(s); err != nil {
			return err
		}
	}
	return nil
}

func TestInsertCert_IdempotentAndConflict(t *testing.T) {
	db := openTestDB(t)
	userID := fmt.Sprintf("rm-user-%d", time.Now().UnixNano())
	reedID := fmt.Sprintf("rm-reed-%d", time.Now().UnixNano())
	fp := fmt.Sprintf("rm-fp-%d", time.Now().UnixNano())

	_, err := db.Exec(`
		INSERT INTO users (id, username, user_signature, server_signature, server_signed_at, server_fingerprint)
		VALUES ($1, $2, 'u', 's', NOW(), 'sfp')
	`, userID, "u-"+userID)
	if err != nil {
		t.Fatalf("seed user: %v", err)
	}
	_, err = db.Exec(`
		INSERT INTO user_keys (fingerprint, owner, armor, server_signature, server_fingerprint, server_signed_at)
		VALUES ($1, $2, 'armor', 's', 'sfp', NOW())
	`, fp, userID)
	if err != nil {
		t.Fatalf("seed key: %v", err)
	}
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
