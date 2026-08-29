//go:build !ops

package main

import (
	"context"
	"database/sql"
	"testing"

	_ "github.com/lib/pq"
)

func openReedStatsTestDB(t *testing.T) *sql.DB {
	t.Helper()
	return newTestDatabase(t, InitDB)
}

// seedReedStatsServer creates the self server row InitDB's own schema
// expects, plus one reusable private/public key pair and signature rows
// every reed/removal insert below FKs into.
func seedReedStatsServer(t *testing.T, db *sql.DB, serverID string) (userSigID, serverSigID int64, pubKeyID string) {
	t.Helper()
	if _, err := db.Exec(`INSERT INTO servers (id, name, self) VALUES ($1, $2, TRUE)`, serverID, serverID); err != nil {
		t.Fatal(err)
	}
	pubKeyID = "pk1@" + serverID
	if _, err := db.Exec(`INSERT INTO private_keys (id, armor) VALUES ($1, 'armor')`, pubKeyID); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`INSERT INTO user_signatures (public_key_id, signature) VALUES ($1, 'sig') RETURNING id`, "sig1").Scan(&userSigID); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`INSERT INTO server_signatures (fingerprint, signature, signed_at) VALUES ($1, 'sig', NOW()) RETURNING id`, "sfp1").Scan(&serverSigID); err != nil {
		t.Fatal(err)
	}
	var pkServerSigID int64
	if err := db.QueryRow(`INSERT INTO server_signatures (fingerprint, signature, signed_at) VALUES ($1, 'sig', NOW()) RETURNING id`, "sfp-pk").Scan(&pkServerSigID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO public_keys (id, armor, server_signature_id) VALUES ($1, 'pubarmor', $2)`, pubKeyID, pkServerSigID); err != nil {
		t.Fatal(err)
	}
	return userSigID, serverSigID, pubKeyID
}

func seedReedStatsIdentity(t *testing.T, db *sql.DB, userID, serverID string) {
	t.Helper()
	if _, err := db.Exec(`INSERT INTO identities (id, server_id) VALUES ($1, $2)`, userID+"@"+serverID, serverID); err != nil {
		t.Fatal(err)
	}
}

func seedReedStatsReed(t *testing.T, db *sql.DB, reedID, userID, pubKeyID string, userSigID, serverSigID int64) {
	t.Helper()
	if _, err := db.Exec(`INSERT INTO reed_identities (id, server_id) VALUES ($1, $2)`,
		reedID, "testserver"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		INSERT INTO reeds (id, user_id, private_key_id, signed_at, user_signature_id, server_signature_id)
		VALUES ($1, $2, $3, NOW(), $4, $5)
	`, reedID, userID, pubKeyID, userSigID, serverSigID); err != nil {
		t.Fatal(err)
	}
}

func seedReedStatsReply(t *testing.T, db *sql.DB, threadID, reedID, parentReedID string) {
	t.Helper()
	if _, err := db.Exec(`
		INSERT INTO reed_replies (thread_id, reed_id, parent_reed_id, timestamp)
		VALUES ($1, $2, $3, NOW())
	`, threadID, reedID, parentReedID); err != nil {
		t.Fatal(err)
	}
}

// TestReedStatsReplyTriggers verifies the reed_replies/reed_removals
// insert triggers maintain reply_count exactly like the old recursive
// GetSubtreeReplyCount CTE would have computed: a 3-level chain increments
// every ancestor, and removing the leaf decrements them back down.
func TestReedStatsReplyTriggers(t *testing.T) {
	db := openReedStatsTestDB(t)
	ds := &DataService{db: db, serverID: "testserver"}
	ctx := context.Background()

	userSigID, serverSigID, pubKeyID := seedReedStatsServer(t, db, "testserver")
	seedReedStatsIdentity(t, db, "alice", "testserver")
	seedReedStatsIdentity(t, db, "bob", "testserver")

	root := "alice@testserver/root"
	r1 := "bob@testserver/r1"
	r2 := "alice@testserver/r2"

	seedReedStatsReed(t, db, root, "alice@testserver", pubKeyID, userSigID, serverSigID)
	seedReedStatsReed(t, db, r1, "bob@testserver", pubKeyID, userSigID, serverSigID)
	seedReedStatsReed(t, db, r2, "alice@testserver", pubKeyID, userSigID, serverSigID)

	seedReedStatsReply(t, db, root, r1, root)
	seedReedStatsReply(t, db, root, r2, r1)

	rootCount, err := ds.GetSubtreeReplyCount(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	if rootCount != 2 {
		t.Fatalf("root reply_count = %d, want 2", rootCount)
	}
	r1Count, err := ds.GetSubtreeReplyCount(ctx, r1)
	if err != nil {
		t.Fatal(err)
	}
	if r1Count != 1 {
		t.Fatalf("r1 reply_count = %d, want 1", r1Count)
	}

	// Remove r2 (the leaf) — both ancestors decrement.
	if _, err := db.Exec(`
		INSERT INTO reed_removals (reed_id, public_key_id, user_signature_id, server_signature_id)
		VALUES ($1, $2, $3, $4)
	`, r2, pubKeyID, userSigID, serverSigID); err != nil {
		t.Fatal(err)
	}

	rootCount, err = ds.GetSubtreeReplyCount(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	if rootCount != 1 {
		t.Fatalf("root reply_count after removal = %d, want 1", rootCount)
	}
	r1Count, err = ds.GetSubtreeReplyCount(ctx, r1)
	if err != nil {
		t.Fatal(err)
	}
	if r1Count != 0 {
		t.Fatalf("r1 reply_count after removal = %d, want 0", r1Count)
	}
}

// TestReedStatsForeignReplyDeleteTrigger verifies the reed_replies AFTER
// DELETE trigger — the path DeleteForeignReplyReference uses (a hard
// DELETE, not an INSERT into reed_removals) when a peer notifies this
// server that one of its replies to a locally-hosted reed was removed.
// Regression test: this trigger was originally missing, so a foreign
// reply removal never decremented the parent's reply_count at all.
func TestReedStatsForeignReplyDeleteTrigger(t *testing.T) {
	db := openReedStatsTestDB(t)
	ds := &DataService{db: db, serverID: "testserver"}
	ctx := context.Background()

	userSigID, serverSigID, pubKeyID := seedReedStatsServer(t, db, "testserver")
	seedReedStatsIdentity(t, db, "alice", "testserver")
	seedReedStatsIdentity(t, db, "bob", "testserver")
	seedReedStatsIdentity(t, db, "carol", "testserver")

	root := "alice@testserver/root"
	mid := "bob@testserver/mid"
	leaf := "carol@testserver/leaf"

	seedReedStatsReed(t, db, root, "alice@testserver", pubKeyID, userSigID, serverSigID)
	seedReedStatsReed(t, db, mid, "bob@testserver", pubKeyID, userSigID, serverSigID)
	seedReedStatsReed(t, db, leaf, "carol@testserver", pubKeyID, userSigID, serverSigID)

	seedReedStatsReply(t, db, root, mid, root)
	seedReedStatsReply(t, db, root, leaf, mid)

	rootBefore, err := ds.GetSubtreeReplyCount(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	midBefore, err := ds.GetSubtreeReplyCount(ctx, mid)
	if err != nil {
		t.Fatal(err)
	}
	if rootBefore != 2 || midBefore != 1 {
		t.Fatalf("before delete: root=%d mid=%d, want root=2 mid=1", rootBefore, midBefore)
	}

	deleted, err := ds.DeleteForeignReplyReference(ctx, leaf)
	if err != nil {
		t.Fatal(err)
	}
	if !deleted {
		t.Fatal("expected deleted=true")
	}

	rootAfter, err := ds.GetSubtreeReplyCount(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	midAfter, err := ds.GetSubtreeReplyCount(ctx, mid)
	if err != nil {
		t.Fatal(err)
	}
	if rootAfter != 1 {
		t.Fatalf("root reply_count after foreign reply delete = %d, want 1", rootAfter)
	}
	if midAfter != 0 {
		t.Fatalf("mid reply_count after foreign reply delete = %d, want 0", midAfter)
	}
}

// TestReedStatsAccountRemovalTrigger verifies that removing an account
// decrements ancestor reply_count for every reply by that author, walking
// each one's ancestor chain — the one genuinely bulk trigger in this
// design.
func TestReedStatsAccountRemovalTrigger(t *testing.T) {
	db := openReedStatsTestDB(t)
	ds := &DataService{db: db, serverID: "testserver"}
	ctx := context.Background()

	userSigID, serverSigID, pubKeyID := seedReedStatsServer(t, db, "testserver")
	seedReedStatsIdentity(t, db, "alice", "testserver")
	seedReedStatsIdentity(t, db, "bob", "testserver")

	root := "alice@testserver/root"
	bobReply1 := "bob@testserver/r1"
	bobReply2 := "bob@testserver/r2"

	seedReedStatsReed(t, db, root, "alice@testserver", pubKeyID, userSigID, serverSigID)
	seedReedStatsReed(t, db, bobReply1, "bob@testserver", pubKeyID, userSigID, serverSigID)
	seedReedStatsReed(t, db, bobReply2, "bob@testserver", pubKeyID, userSigID, serverSigID)

	seedReedStatsReply(t, db, root, bobReply1, root)
	seedReedStatsReply(t, db, root, bobReply2, root)

	rootCount, err := ds.GetSubtreeReplyCount(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	if rootCount != 2 {
		t.Fatalf("root reply_count before account removal = %d, want 2", rootCount)
	}

	if _, err := db.Exec(`
		INSERT INTO account_removals (user_id, note, public_key_id, user_signature_id, server_signature_id)
		VALUES ($1, '', $2, $3, $4)
	`, "bob@testserver", pubKeyID, userSigID, serverSigID); err != nil {
		t.Fatal(err)
	}

	rootCount, err = ds.GetSubtreeReplyCount(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	if rootCount != 0 {
		t.Fatalf("root reply_count after account removal = %d, want 0", rootCount)
	}
}

// TestReedStatsEchoLikeHolderTriggers verifies the flat insert/delete
// triggers for echoes, likes, and holder allocations.
func TestReedStatsEchoLikeHolderTriggers(t *testing.T) {
	db := openReedStatsTestDB(t)
	ds := &DataService{db: db, serverID: "testserver"}
	ctx := context.Background()

	userSigID, serverSigID, pubKeyID := seedReedStatsServer(t, db, "testserver")
	seedReedStatsIdentity(t, db, "alice", "testserver")
	seedReedStatsIdentity(t, db, "bob", "testserver")

	root := "alice@testserver/root"
	echoReed := "bob@testserver/echo1"
	seedReedStatsReed(t, db, root, "alice@testserver", pubKeyID, userSigID, serverSigID)
	seedReedStatsReed(t, db, echoReed, "bob@testserver", pubKeyID, userSigID, serverSigID)

	// Echo: insert increments, self-echo is excluded, delete decrements.
	if _, err := db.Exec(`
		INSERT INTO reed_echoes (echoing_reed_id, echoed_reed_id, echoing_author_id, echoed_author_id, is_blank, signed_at)
		VALUES ($1, $2, $3, $4, FALSE, NOW())
	`, echoReed, root, "bob@testserver", "alice@testserver"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		INSERT INTO reed_echoes (echoing_reed_id, echoed_reed_id, echoing_author_id, echoed_author_id, is_blank, signed_at)
		VALUES ($1, $1, $2, $2, FALSE, NOW())
	`, root, "alice@testserver"); err != nil {
		t.Fatal(err)
	}
	echoCount, err := ds.CountEchoes(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	if echoCount != 1 {
		t.Fatalf("echo_count = %d, want 1 (self-echo must be excluded)", echoCount)
	}
	if _, err := db.Exec(`DELETE FROM reed_echoes WHERE echoing_reed_id = $1`, echoReed); err != nil {
		t.Fatal(err)
	}
	echoCount, err = ds.CountEchoes(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	if echoCount != 0 {
		t.Fatalf("echo_count after delete = %d, want 0", echoCount)
	}

	// Like: insert increments, delete decrements.
	if _, err := db.Exec(`
		INSERT INTO reeds_liked (liker_user_id, reed_id, liker_public_key_id, user_signature_id, server_signature_id)
		VALUES ($1, $2, $3, $4, $5)
	`, "bob@testserver", root, pubKeyID, userSigID, serverSigID); err != nil {
		t.Fatal(err)
	}
	likeCount, err := ds.CountLikes(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	if likeCount != 1 {
		t.Fatalf("like_count = %d, want 1", likeCount)
	}
	if _, err := db.Exec(`DELETE FROM reeds_liked WHERE liker_user_id = $1 AND reed_id = $2`, "bob@testserver", root); err != nil {
		t.Fatal(err)
	}
	likeCount, err = ds.CountLikes(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	if likeCount != 0 {
		t.Fatalf("like_count after delete = %d, want 0", likeCount)
	}

	// Holder allocation: insert increments, delete decrements.
	if _, err := db.Exec(`
		INSERT INTO users (id, username, user_signature_id, server_signature_id)
		VALUES ($1, 'alice', $2, $3)
	`, "alice@testserver", userSigID, serverSigID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO reed_allocations (reed_id, holder_user_id) VALUES ($1, $2)`, root, "alice@testserver"); err != nil {
		t.Fatal(err)
	}
	var holderCount int
	if err := db.QueryRow(`SELECT holder_count FROM reed_coverage WHERE reed_id = $1`, root).Scan(&holderCount); err != nil {
		t.Fatal(err)
	}
	if holderCount != 1 {
		t.Fatalf("holder_count = %d, want 1", holderCount)
	}
	if _, err := db.Exec(`DELETE FROM reed_allocations WHERE reed_id = $1 AND holder_user_id = $2`, root, "alice@testserver"); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT holder_count FROM reed_coverage WHERE reed_id = $1`, root).Scan(&holderCount); err != nil {
		t.Fatal(err)
	}
	if holderCount != 0 {
		t.Fatalf("holder_count after delete = %d, want 0", holderCount)
	}
}

// TestReedCoverageView verifies the view's coverage_percent matches
// coverage.Percent computed independently in Go for the same two inputs.
func TestReedCoverageView(t *testing.T) {
	db := openReedStatsTestDB(t)

	userSigID, serverSigID, pubKeyID := seedReedStatsServer(t, db, "testserver")
	seedReedStatsIdentity(t, db, "alice", "testserver")
	seedReedStatsIdentity(t, db, "bob", "testserver")
	seedReedStatsIdentity(t, db, "carol", "testserver")
	seedReedStatsIdentity(t, db, "dave", "testserver")

	root := "alice@testserver/root"
	seedReedStatsReed(t, db, root, "alice@testserver", pubKeyID, userSigID, serverSigID)

	if _, err := db.Exec(`UPDATE network_stats SET active_users = 4`); err != nil {
		t.Fatal(err)
	}

	for _, holder := range []string{"alice", "bob"} {
		if _, err := db.Exec(`
			INSERT INTO users (id, username, user_signature_id, server_signature_id)
			VALUES ($1, $2, $3, $4)
		`, holder+"@testserver", holder, userSigID, serverSigID); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`INSERT INTO reed_allocations (reed_id, holder_user_id) VALUES ($1, $2)`, root, holder+"@testserver"); err != nil {
			t.Fatal(err)
		}
	}

	var gotHolders, gotPercent int
	if err := db.QueryRow(`SELECT holder_count, coverage_percent FROM reed_coverage WHERE reed_id = $1`, root).Scan(&gotHolders, &gotPercent); err != nil {
		t.Fatal(err)
	}
	if gotHolders != 2 {
		t.Fatalf("holder_count = %d, want 2", gotHolders)
	}
	wantPercent := 50 // coverage.Percent(2, 4)
	if gotPercent != wantPercent {
		t.Fatalf("coverage_percent = %d, want %d", gotPercent, wantPercent)
	}
}
