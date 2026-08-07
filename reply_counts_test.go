//go:build !ops

package main

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"testing"
	"time"

	_ "github.com/lib/pq"
)

func openReplyCountTestDB(t *testing.T) *sql.DB {
	t.Helper()
	dsn := fmt.Sprintf(
		"host=%s port=%s user=%s password=%s dbname=%s sslmode=%s",
		replyEnvOr("DB_HOST", "localhost"),
		replyEnvOr("DB_PORT", "5432"),
		replyEnvOr("DB_USER", "syrinx"),
		replyEnvOr("DB_PASSWORD", "syrinx"),
		replyEnvOr("DB_NAME", "syrinx_test"),
		replyEnvOr("DB_SSLMODE", "disable"),
	)
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Skipf("open db: %v", err)
	}
	if err := db.Ping(); err != nil {
		db.Close()
		t.Skipf("ping db: %v", err)
	}
	if err := ensureReplyCountSchema(db); err != nil {
		db.Close()
		t.Fatalf("schema: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func replyEnvOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func ensureReplyCountSchema(db *sql.DB) error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS servers (id VARCHAR(255) PRIMARY KEY, self BOOLEAN NOT NULL DEFAULT FALSE)`,
		`INSERT INTO servers (id, self) VALUES ('testserver', TRUE) ON CONFLICT (id) DO UPDATE SET self = EXCLUDED.self`,
		`CREATE TABLE IF NOT EXISTS user_signatures (id SERIAL PRIMARY KEY, fingerprint VARCHAR(255) NOT NULL, signature TEXT NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS server_signatures (id SERIAL PRIMARY KEY, fingerprint VARCHAR(255) NOT NULL, signature TEXT NOT NULL, signed_at TIMESTAMP NOT NULL)`,
		`DROP TABLE IF EXISTS reed_replies CASCADE`,
		`DROP TABLE IF EXISTS reeds CASCADE`,
		`DROP TABLE IF EXISTS users CASCADE`,
		`CREATE TABLE users (
			id VARCHAR(255) PRIMARY KEY,
			username VARCHAR(255) UNIQUE NOT NULL,
			user_fingerprint VARCHAR(255),
			user_signature_id INT NOT NULL REFERENCES user_signatures(id),
			server_signature_id INT NOT NULL REFERENCES server_signatures(id)
		)`,
		`CREATE TABLE reeds (
			id VARCHAR(255) NOT NULL,
			user_id VARCHAR(255) NOT NULL REFERENCES users(id),
			private_key_fingerprint VARCHAR(255) NOT NULL,
			signed_at TIMESTAMP NOT NULL,
			user_signature_id INT NOT NULL REFERENCES user_signatures(id),
			server_signature_id INT NOT NULL REFERENCES server_signatures(id),
			PRIMARY KEY (user_id, id)
		)`,
		`CREATE TABLE reed_replies (
			thread_id VARCHAR(255) NOT NULL,
			user_id VARCHAR(255) NOT NULL,
			reed_id VARCHAR(255) NOT NULL UNIQUE,
			parent_user_id VARCHAR(255) NOT NULL,
			parent_reed_id VARCHAR(255) NOT NULL,
			timestamp TIMESTAMP NOT NULL,
			PRIMARY KEY (user_id, reed_id)
		)`,
	}
	for _, stmt := range stmts {
		if _, err := db.Exec(stmt); err != nil {
			return err
		}
	}
	return nil
}

func seedReplyTestUser(t *testing.T, db *sql.DB, userID string) {
	t.Helper()
	var usID, ssID int
	if err := db.QueryRow(`INSERT INTO user_signatures (fingerprint, signature) VALUES ($1, 'sig') RETURNING id`, userID+"fp").Scan(&usID); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`INSERT INTO server_signatures (fingerprint, signature, signed_at) VALUES ($1, 'sig', NOW()) RETURNING id`, "srvfp").Scan(&ssID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO users (id, username, user_signature_id, server_signature_id) VALUES ($1, $2, $3, $4) ON CONFLICT DO NOTHING`,
		userID, userID+"name", usID, ssID); err != nil {
		t.Fatal(err)
	}
}

func seedReplyTestReed(t *testing.T, db *sql.DB, userID, reedID string) {
	t.Helper()
	var usID, ssID int
	if err := db.QueryRow(`INSERT INTO user_signatures (fingerprint, signature) VALUES ($1, 'sig') RETURNING id`, reedID+"fp").Scan(&usID); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`INSERT INTO server_signatures (fingerprint, signature, signed_at) VALUES ($1, 'sig', NOW()) RETURNING id`, "srvfp2").Scan(&ssID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO reeds (id, user_id, private_key_fingerprint, signed_at, user_signature_id, server_signature_id)
		VALUES ($1, $2, $3, NOW(), $4, $5)`,
		reedID, userID, userID+"fp", usID, ssID); err != nil {
		t.Fatal(err)
	}
}

func TestReplyCountsFromGraph(t *testing.T) {
	db := openReplyCountTestDB(t)
	ds := &DataService{db: db, serverID: "testserver"}
	ctx := context.Background()
	ts := time.Now().UTC().Truncate(time.Second)

	seedReplyTestUser(t, db, "alice")
	seedReplyTestReed(t, db, "alice", "root")
	seedReplyTestReed(t, db, "alice", "mid")
	seedReplyTestReed(t, db, "alice", "leaf")

	threadID := "alice@testserver/root"
	root := ReedRef{AuthorID: "alice", ReedID: "root"}

	tx1, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err := ds.insertReplyTx(ctx, tx1, threadID, root, "alice", "mid", ts); err != nil {
		t.Fatal(err)
	}
	if err := tx1.Commit(); err != nil {
		t.Fatal(err)
	}

	tx2, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err := ds.insertReplyTx(ctx, tx2, threadID, ReedRef{AuthorID: "alice", ReedID: "mid"}, "alice", "leaf", ts.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := tx2.Commit(); err != nil {
		t.Fatal(err)
	}

	threadTotal, err := ds.GetSubtreeReplyCount(context.Background(), "alice", "root")
	if err != nil {
		t.Fatal(err)
	}
	if threadTotal != 2 {
		t.Fatalf("root subtree (= thread total) = %d want 2", threadTotal)
	}

	rootSub, _ := ds.GetSubtreeReplyCount(context.Background(), "alice", "root")
	midSub, _ := ds.GetSubtreeReplyCount(context.Background(), "alice", "mid")
	leafSub, _ := ds.GetSubtreeReplyCount(context.Background(), "alice", "leaf")
	if rootSub != 2 || midSub != 1 || leafSub != 0 {
		t.Fatalf("subtree counts root=%d mid=%d leaf=%d want 2/1/0", rootSub, midSub, leafSub)
	}
}
