//go:build !ops

package main

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"syrinx/identity"

	_ "github.com/lib/pq"
)

func openReplyCountTestDB(t *testing.T) *sql.DB {
	t.Helper()
	return newTestDatabase(t, ensureReplyCountSchema)
}

func ensureReplyCountSchema(db *sql.DB) error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS servers (id VARCHAR(255) PRIMARY KEY, self BOOLEAN NOT NULL DEFAULT FALSE)`,
		`INSERT INTO servers (id, self) VALUES ('testserver', TRUE) ON CONFLICT (id) DO UPDATE SET self = EXCLUDED.self`,
		`CREATE TABLE IF NOT EXISTS user_signatures (id SERIAL PRIMARY KEY, public_key_id VARCHAR(255) NOT NULL, signature TEXT NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS server_signatures (id SERIAL PRIMARY KEY, private_key_id VARCHAR(255) NOT NULL, signature TEXT NOT NULL, signed_at TIMESTAMP NOT NULL)`,
		`DROP TABLE IF EXISTS reed_replies CASCADE`,
		`DROP TABLE IF EXISTS reeds CASCADE`,
		`DROP TABLE IF EXISTS users CASCADE`,
		`DROP TABLE IF EXISTS identities CASCADE`,
		// identities is the FK target for "a user" (see db.go).
		`CREATE TABLE identities (
			id VARCHAR(255) PRIMARY KEY,
			server_id VARCHAR(16),
			public_key_fingerprint VARCHAR(255),
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE users (
			id VARCHAR(255) PRIMARY KEY REFERENCES identities(id) ON DELETE CASCADE,
			username VARCHAR(255) UNIQUE NOT NULL,
			user_signature_id INT NOT NULL REFERENCES user_signatures(id),
			server_signature_id INT NOT NULL REFERENCES server_signatures(id)
		)`,
		`CREATE TABLE reeds (
			id VARCHAR(255) PRIMARY KEY,
			user_id VARCHAR(255) NOT NULL REFERENCES identities(id),
			signed_at TIMESTAMP NOT NULL,
			user_signature_id INT NOT NULL REFERENCES user_signatures(id),
			server_signature_id INT NOT NULL REFERENCES server_signatures(id)
		)`,
		// reed_id is canonical (embeds author) — no separate user_id column,
		// mirroring db.go.
		`CREATE TABLE reed_replies (
			thread_id VARCHAR(255) NOT NULL,
			reed_id VARCHAR(255) NOT NULL,
			parent_reed_id VARCHAR(255) NOT NULL,
			timestamp TIMESTAMP NOT NULL,
			PRIMARY KEY (reed_id)
		)`,
		`DROP TABLE IF EXISTS reed_removals CASCADE`,
		`CREATE TABLE reed_removals (
			reed_id VARCHAR(255) PRIMARY KEY
		)`,
		`DROP TABLE IF EXISTS account_removals CASCADE`,
		`CREATE TABLE account_removals (
			user_id VARCHAR(255) PRIMARY KEY REFERENCES identities(id)
		)`,
		`DROP TABLE IF EXISTS reed_stats CASCADE`,
		`CREATE TABLE reed_stats (
			reed_id VARCHAR(255) PRIMARY KEY,
			reply_count INT NOT NULL DEFAULT 0,
			echo_count INT NOT NULL DEFAULT 0,
			like_count INT NOT NULL DEFAULT 0,
			holder_count INT NOT NULL DEFAULT 0
		)`,
		`CREATE OR REPLACE FUNCTION bump_reed_stat(p_reed_id VARCHAR(255), col_name TEXT, delta INT)
		RETURNS VOID AS $$
		BEGIN
			EXECUTE format(
				'INSERT INTO reed_stats (reed_id, %1$I) VALUES ($1, GREATEST(0, $2))
				 ON CONFLICT (reed_id) DO UPDATE SET %1$I = GREATEST(0, reed_stats.%1$I + $2)',
				col_name
			) USING p_reed_id, delta;
		END;
		$$ LANGUAGE plpgsql`,
		`CREATE OR REPLACE FUNCTION bump_reply_ancestors(start_reed_id VARCHAR(255), delta INT)
		RETURNS VOID AS $$
		DECLARE
			current_id VARCHAR(255);
			next_id VARCHAR(255);
		BEGIN
			current_id := start_reed_id;
			LOOP
				SELECT parent_reed_id INTO next_id FROM reed_replies WHERE reed_id = current_id;
				EXIT WHEN next_id IS NULL;
				PERFORM bump_reed_stat(next_id, 'reply_count', delta);
				current_id := next_id;
			END LOOP;
		END;
		$$ LANGUAGE plpgsql`,
		`CREATE OR REPLACE FUNCTION reed_reply_count_trigger() RETURNS TRIGGER AS $$
		DECLARE
			author_removed BOOLEAN;
		BEGIN
			SELECT EXISTS(
				SELECT 1 FROM account_removals ar
				JOIN reeds r ON r.id = NEW.reed_id
				WHERE ar.user_id = r.user_id
			) INTO author_removed;
			IF NOT author_removed THEN
				PERFORM bump_reply_ancestors(NEW.reed_id, 1);
			END IF;
			RETURN NULL;
		END;
		$$ LANGUAGE plpgsql`,
		`DROP TRIGGER IF EXISTS reed_replies_count_insert ON reed_replies`,
		`CREATE TRIGGER reed_replies_count_insert
			AFTER INSERT ON reed_replies
			FOR EACH ROW EXECUTE FUNCTION reed_reply_count_trigger()`,
		`CREATE OR REPLACE FUNCTION reed_removal_reply_count_trigger() RETURNS TRIGGER AS $$
		BEGIN
			PERFORM bump_reply_ancestors(NEW.reed_id, -1);
			RETURN NULL;
		END;
		$$ LANGUAGE plpgsql`,
		`DROP TRIGGER IF EXISTS reed_removals_count_insert ON reed_removals`,
		`CREATE TRIGGER reed_removals_count_insert
			AFTER INSERT ON reed_removals
			FOR EACH ROW EXECUTE FUNCTION reed_removal_reply_count_trigger()`,
		`CREATE OR REPLACE FUNCTION account_removal_reply_count_trigger() RETURNS TRIGGER AS $$
		DECLARE
			reply_row RECORD;
		BEGIN
			FOR reply_row IN
				SELECT rr.reed_id
				FROM reed_replies rr
				JOIN reeds r ON r.id = rr.reed_id
				WHERE r.user_id = NEW.user_id
				AND NOT EXISTS (
					SELECT 1 FROM reed_removals rm WHERE rm.reed_id = rr.reed_id
				)
			LOOP
				PERFORM bump_reply_ancestors(reply_row.reed_id, -1);
			END LOOP;
			RETURN NULL;
		END;
		$$ LANGUAGE plpgsql`,
		`DROP TRIGGER IF EXISTS account_removals_count_insert ON account_removals`,
		`CREATE TRIGGER account_removals_count_insert
			AFTER INSERT ON account_removals
			FOR EACH ROW EXECUTE FUNCTION account_removal_reply_count_trigger()`,
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
	identityID := string(identity.CanonicalID("testserver", userID))
	if _, err := db.Exec(`INSERT INTO identities (id, server_id) VALUES ($1, $2) ON CONFLICT DO NOTHING`,
		identityID, "testserver"); err != nil {
		t.Fatal(err)
	}
	var usID, ssID int
	if err := db.QueryRow(`INSERT INTO user_signatures (public_key_id, signature) VALUES ($1, 'sig') RETURNING id`, userID+"fp").Scan(&usID); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`INSERT INTO server_signatures (private_key_id, signature, signed_at) VALUES ($1, 'sig', NOW()) RETURNING id`, "srvfp").Scan(&ssID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO users (id, username, user_signature_id, server_signature_id) VALUES ($1, $2, $3, $4) ON CONFLICT DO NOTHING`,
		identityID, userID+"name", usID, ssID); err != nil {
		t.Fatal(err)
	}
}

// seedReplyTestReed writes reeds.user_id using "testserver", matching
// TestReplyCountsFromGraph's DataService.serverID.
func seedReplyTestReed(t *testing.T, db *sql.DB, userID, reedID string) {
	t.Helper()
	identityID := string(identity.CanonicalID("testserver", userID))
	var usID, ssID int
	if err := db.QueryRow(`INSERT INTO user_signatures (public_key_id, signature) VALUES ($1, 'sig') RETURNING id`, reedID+"fp").Scan(&usID); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`INSERT INTO server_signatures (private_key_id, signature, signed_at) VALUES ($1, 'sig', NOW()) RETURNING id`, "srvfp2").Scan(&ssID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO reeds (id, user_id, signed_at, user_signature_id, server_signature_id)
		VALUES ($1, $2, NOW(), $3, $4)`,
		reedID, identityID, usID, ssID); err != nil {
		t.Fatal(err)
	}
}

func TestReplyCountsFromGraph(t *testing.T) {
	db := openReplyCountTestDB(t)
	ds := &DataService{db: db, serverID: "testserver"}
	ctx := context.Background()
	ts := time.Now().UTC().Truncate(time.Second)

	rootID := "alice@testserver/root"
	midID := "alice@testserver/mid"
	leafID := "alice@testserver/leaf"

	seedReplyTestUser(t, db, "alice")
	seedReplyTestReed(t, db, "alice", rootID)
	seedReplyTestReed(t, db, "alice", midID)
	seedReplyTestReed(t, db, "alice", leafID)

	// root.ServerID must be set to "testserver", or insertReplyTx builds
	// the malformed "alice@" instead of "alice@testserver".
	threadID := "alice@testserver/root"
	root := ReedRef{AuthorID: "alice", ServerID: "testserver", ReedID: "root"}

	tx1, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err := ds.insertReplyTx(ctx, tx1, threadID, root, midID, ts); err != nil {
		t.Fatal(err)
	}
	if err := tx1.Commit(); err != nil {
		t.Fatal(err)
	}

	tx2, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	mid := ReedRef{AuthorID: "alice", ServerID: "testserver", ReedID: "mid"}
	if err := ds.insertReplyTx(ctx, tx2, threadID, mid, leafID, ts.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := tx2.Commit(); err != nil {
		t.Fatal(err)
	}

	threadTotal, err := ds.GetSubtreeReplyCount(context.Background(), rootID)
	if err != nil {
		t.Fatal(err)
	}
	if threadTotal != 2 {
		t.Fatalf("root subtree (= thread total) = %d want 2", threadTotal)
	}

	rootSub, _ := ds.GetSubtreeReplyCount(context.Background(), rootID)
	midSub, _ := ds.GetSubtreeReplyCount(context.Background(), midID)
	leafSub, _ := ds.GetSubtreeReplyCount(context.Background(), leafID)
	if rootSub != 2 || midSub != 1 || leafSub != 0 {
		t.Fatalf("subtree counts root=%d mid=%d leaf=%d want 2/1/0", rootSub, midSub, leafSub)
	}
}
