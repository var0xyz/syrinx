//go:build !ops

package main

import (
	"context"
	"database/sql"
	"encoding/base64"
	"testing"
	"time"

	"syrinx/crypto"
	"syrinx/identity"

	"github.com/google/uuid"
	_ "github.com/lib/pq"
)

func openRipplesTestDB(t *testing.T) *sql.DB {
	t.Helper()
	return newTestDatabase(t, ensureRipplesSchema)
}

func ensureRipplesSchema(db *sql.DB) error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS user_signatures (id SERIAL PRIMARY KEY, fingerprint VARCHAR(255) NOT NULL, signature TEXT NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS server_signatures (id SERIAL PRIMARY KEY, fingerprint VARCHAR(255) NOT NULL, signature TEXT NOT NULL, signed_at TIMESTAMP NOT NULL)`,
		`DROP TABLE IF EXISTS ripple_responses CASCADE`,
		`DROP TABLE IF EXISTS ripples CASCADE`,
		`DROP TABLE IF EXISTS reed_removals CASCADE`,
		`DROP TABLE IF EXISTS reeds CASCADE`,
		`DROP TABLE IF EXISTS account_removals CASCADE`,
		`DROP TABLE IF EXISTS user_key_revocations CASCADE`,
		`DROP TABLE IF EXISTS user_keys CASCADE`,
		`DROP TABLE IF EXISTS users CASCADE`,
		`CREATE TABLE users (
			id VARCHAR(255) PRIMARY KEY,
			username VARCHAR(255) UNIQUE NOT NULL,
			user_fingerprint VARCHAR(255),
			user_signature_id INT NOT NULL REFERENCES user_signatures(id),
			server_signature_id INT NOT NULL REFERENCES server_signatures(id)
		)`,
		`CREATE TABLE user_keys (
			fingerprint VARCHAR(255) PRIMARY KEY,
			owner VARCHAR(255) NOT NULL REFERENCES users(id),
			armor TEXT NOT NULL,
			created_at TIMESTAMP NOT NULL,
			expires_at TIMESTAMP,
			server_signature_id INT NOT NULL REFERENCES server_signatures(id),
			predecessor_signature TEXT,
			predecessor_fingerprint VARCHAR(255)
		)`,
		`CREATE TABLE user_key_revocations (
			user_fingerprint VARCHAR(255) NOT NULL REFERENCES user_keys(fingerprint),
			owner VARCHAR(255) NOT NULL REFERENCES users(id),
			PRIMARY KEY (owner, user_fingerprint)
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
		`CREATE TABLE reed_removals (
			reed_id VARCHAR(255) NOT NULL,
			user_id VARCHAR(255) NOT NULL REFERENCES users(id),
			user_fingerprint VARCHAR(255) NOT NULL DEFAULT '',
			user_signature_id INT NOT NULL REFERENCES user_signatures(id),
			server_signature_id INT NOT NULL REFERENCES server_signatures(id),
			PRIMARY KEY (user_id, reed_id)
		)`,
		`CREATE TABLE account_removals (
			user_id VARCHAR(255) PRIMARY KEY REFERENCES users(id),
			note VARCHAR(140) NOT NULL DEFAULT '',
			user_fingerprint VARCHAR(255) NOT NULL DEFAULT '',
			user_signature_id INT NOT NULL REFERENCES user_signatures(id),
			server_signature_id INT NOT NULL REFERENCES server_signatures(id)
		)`,
		`CREATE TABLE ripples (
			reed_author_id VARCHAR(255) NOT NULL,
			reed_id VARCHAR(255) NOT NULL,
			expires_at TIMESTAMP NOT NULL,

			PRIMARY KEY (reed_author_id, reed_id),
			FOREIGN KEY (reed_author_id, reed_id) REFERENCES reeds(user_id, id)
				ON DELETE CASCADE
		)`,
		`CREATE TABLE ripple_responses (
			id VARCHAR(64) PRIMARY KEY,
			reed_author_id VARCHAR(255) NOT NULL,
			reed_id VARCHAR(255) NOT NULL,
			thread_id UUID NOT NULL,
			user_id VARCHAR(255) NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			content VARCHAR(140) NOT NULL,
			replying_to VARCHAR(64) REFERENCES ripple_responses(id) ON DELETE SET NULL,
			deleted BOOLEAN NOT NULL DEFAULT FALSE,
			posted_at TIMESTAMP NOT NULL,

			user_fingerprint VARCHAR(255) NOT NULL,
			user_signature_id INT NOT NULL REFERENCES user_signatures(id),
			server_signature_id INT NOT NULL REFERENCES server_signatures(id),

			FOREIGN KEY (reed_author_id, reed_id) REFERENCES ripples(reed_author_id, reed_id)
				ON DELETE CASCADE
		)`,
	}
	for _, stmt := range stmts {
		if _, err := db.Exec(stmt); err != nil {
			return err
		}
	}
	return nil
}

func insertRipplesTestUser(t *testing.T, db *sql.DB, userID, username string) {
	t.Helper()
	var userSigID, serverSigID int
	if err := db.QueryRow(
		`INSERT INTO user_signatures (fingerprint, signature) VALUES ($1, 'sig') RETURNING id`,
		"fp-"+userID,
	).Scan(&userSigID); err != nil {
		t.Fatalf("insert user_signatures for %s: %v", userID, err)
	}
	if err := db.QueryRow(
		`INSERT INTO server_signatures (fingerprint, signature, signed_at) VALUES ($1, 'sig', now()) RETURNING id`,
		"server-fp-"+userID,
	).Scan(&serverSigID); err != nil {
		t.Fatalf("insert server_signatures for %s: %v", userID, err)
	}
	if _, err := db.Exec(
		`INSERT INTO users (id, username, user_fingerprint, user_signature_id, server_signature_id)
		 VALUES ($1, $2, $3, $4, $5)`,
		userID, username, "fp-"+userID, userSigID, serverSigID,
	); err != nil {
		t.Fatalf("insert user %s: %v", userID, err)
	}
}

func insertRipplesTestReed(t *testing.T, db *sql.DB, authorID, reedID string) {
	t.Helper()
	var userSigID, serverSigID int
	if err := db.QueryRow(
		`INSERT INTO user_signatures (fingerprint, signature) VALUES ($1, 'sig') RETURNING id`,
		"reed-fp-"+reedID,
	).Scan(&userSigID); err != nil {
		t.Fatalf("insert user_signatures for reed %s: %v", reedID, err)
	}
	if err := db.QueryRow(
		`INSERT INTO server_signatures (fingerprint, signature, signed_at) VALUES ($1, 'sig', now()) RETURNING id`,
		"reed-server-fp-"+reedID,
	).Scan(&serverSigID); err != nil {
		t.Fatalf("insert server_signatures for reed %s: %v", reedID, err)
	}
	if _, err := db.Exec(
		`INSERT INTO reeds (id, user_id, private_key_fingerprint, signed_at, user_signature_id, server_signature_id)
		 VALUES ($1, $2, $3, now(), $4, $5)`,
		reedID, authorID, "fp-"+authorID, userSigID, serverSigID,
	); err != nil {
		t.Fatalf("insert reed %s: %v", reedID, err)
	}
}

// insertReedRemoval marks (authorID, reedID) as reed-removed, satisfying
// GetReedOrRemovalCert's full deletion.GetCert read.
func insertReedRemoval(t *testing.T, db *sql.DB, authorID, reedID string) {
	t.Helper()
	var userSigID, serverSigID int
	if err := db.QueryRow(
		`INSERT INTO user_signatures (fingerprint, signature) VALUES ($1, 'sig') RETURNING id`,
		"reed-removal-fp-"+reedID,
	).Scan(&userSigID); err != nil {
		t.Fatalf("insert user_signatures for reed removal of %s: %v", reedID, err)
	}
	if err := db.QueryRow(
		`INSERT INTO server_signatures (fingerprint, signature, signed_at) VALUES ($1, 'sig', now()) RETURNING id`,
		"reed-removal-server-fp-"+reedID,
	).Scan(&serverSigID); err != nil {
		t.Fatalf("insert server_signatures for reed removal of %s: %v", reedID, err)
	}
	if _, err := db.Exec(
		`INSERT INTO reed_removals (reed_id, user_id, user_fingerprint, user_signature_id, server_signature_id)
		 VALUES ($1, $2, $3, $4, $5)`,
		reedID, authorID, "fp-"+authorID, userSigID, serverSigID,
	); err != nil {
		t.Fatalf("insert reed_removals for %s/%s: %v", authorID, reedID, err)
	}
}

func insertAccountRemoval(t *testing.T, db *sql.DB, userID string) {
	t.Helper()
	var userSigID, serverSigID int
	if err := db.QueryRow(
		`INSERT INTO user_signatures (fingerprint, signature) VALUES ($1, 'sig') RETURNING id`,
		"removal-fp-"+userID,
	).Scan(&userSigID); err != nil {
		t.Fatalf("insert user_signatures for removal of %s: %v", userID, err)
	}
	if err := db.QueryRow(
		`INSERT INTO server_signatures (fingerprint, signature, signed_at) VALUES ($1, 'sig', now()) RETURNING id`,
		"removal-server-fp-"+userID,
	).Scan(&serverSigID); err != nil {
		t.Fatalf("insert server_signatures for removal of %s: %v", userID, err)
	}
	if _, err := db.Exec(
		`INSERT INTO account_removals (user_id, user_fingerprint, user_signature_id, server_signature_id)
		 VALUES ($1, $2, $3, $4)`,
		userID, "fp-"+userID, userSigID, serverSigID,
	); err != nil {
		t.Fatalf("insert account_removals for %s: %v", userID, err)
	}
}

// rippleTestKey is a real PGP keypair registered as userID's active key in
// user_keys, so DataService.GetPublicKey (used by the handler-level
// signature check, and by store tests that verify round-trip signing)
// resolves it.
type rippleTestKey struct {
	*crypto.KeyPair
	cryptoSvc *crypto.Service
}

func newRippleTestKey(t *testing.T, db *sql.DB, userID string) rippleTestKey {
	t.Helper()
	svc := crypto.NewService()
	kp, err := svc.CreateKeyPair(userID, "", "")
	if err != nil {
		t.Fatalf("CreateKeyPair for %s: %v", userID, err)
	}
	var serverSigID int
	if err := db.QueryRow(
		`INSERT INTO server_signatures (fingerprint, signature, signed_at) VALUES ($1, 'sig', now()) RETURNING id`,
		"key-server-fp-"+kp.Fingerprint,
	).Scan(&serverSigID); err != nil {
		t.Fatalf("insert server_signatures for key %s: %v", kp.Fingerprint, err)
	}
	if _, err := db.Exec(
		`INSERT INTO user_keys (fingerprint, owner, armor, created_at, server_signature_id)
		 VALUES ($1, $2, $3, now(), $4)`,
		kp.Fingerprint, userID, kp.PublicKey, serverSigID,
	); err != nil {
		t.Fatalf("insert user_keys for %s: %v", userID, err)
	}
	return rippleTestKey{KeyPair: kp, cryptoSvc: svc}
}

// signRippleUserPayload builds and signs a ripple's user payload exactly
// as the SPA would, returning the base64-armored signature ready for
// DataService.PostRipple / the HTTP handler's `userSignature` field.
func signRippleUserPayload(t *testing.T, key rippleTestKey, reedAuthorID, reedID, rippleAuthorID, threadID, replyingTo, content string) string {
	t.Helper()
	payload := identity.BuildRippleUserPayload(reedAuthorID, reedID, rippleAuthorID, key.Fingerprint, threadID, replyingTo, content)
	armor, err := key.cryptoSvc.Sign(string(payload), key.PrivateKey)
	if err != nil {
		t.Fatalf("sign ripple user payload: %v", err)
	}
	return base64.StdEncoding.EncodeToString([]byte(armor))
}

// testCountersign is a DataService.PostRipple-compatible countersign
// callback backed by a real, freshly generated server key — mirrors
// Handlers.countersign without needing a full *Handlers.
func testCountersign(t *testing.T) (func(payload []byte, ts time.Time) (ServerSignature, error), string) {
	t.Helper()
	svc := crypto.NewService()
	kp, err := svc.CreateKeyPair("test-server", "", "")
	if err != nil {
		t.Fatalf("CreateKeyPair for server: %v", err)
	}
	return func(payload []byte, ts time.Time) (ServerSignature, error) {
		armor, err := svc.Sign(string(payload), kp.PrivateKey)
		if err != nil {
			return ServerSignature{}, err
		}
		return ServerSignature{
			ServerID:    "testserver",
			Fingerprint: kp.Fingerprint,
			Armor:       base64.StdEncoding.EncodeToString([]byte(armor)),
			SignedAt:    ts,
		}, nil
	}, kp.Fingerprint
}

// postTestRipple signs and posts a ripple response as key's owner, in one
// call — the common case for store-level tests that don't care about the
// signing mechanics themselves.
func postTestRipple(t *testing.T, svc *DataService, key rippleTestKey, reedAuthorID, reedID, rippleAuthorID, content string, replyingTo *string, now time.Time) *Ripple {
	t.Helper()
	threadID := uuid.NewString()
	replyingToVal := ""
	if replyingTo != nil {
		threadID = mustRippleThreadID(t, svc, *replyingTo)
		replyingToVal = *replyingTo
	}
	userSig := signRippleUserPayload(t, key, reedAuthorID, reedID, rippleAuthorID, threadID, replyingToVal, content)
	countersign, _ := testCountersign(t)
	resp, err := svc.PostRipple(context.Background(), reedAuthorID, reedID, rippleAuthorID, content, threadID, replyingTo, key.Fingerprint, userSig, countersign, now)
	if err != nil {
		t.Fatalf("PostRipple: %v", err)
	}
	return resp
}

func mustRippleThreadID(t *testing.T, svc *DataService, rippleID string) string {
	t.Helper()
	r, err := svc.GetRipple(context.Background(), rippleID)
	if err != nil {
		t.Fatalf("GetRipple for thread lookup: %v", err)
	}
	return r.ThreadID
}

func TestPostRipple_TopLevel_MintsNewThreadID(t *testing.T) {
	db := openRipplesTestDB(t)
	insertRipplesTestUser(t, db, "author1", "author")
	insertRipplesTestUser(t, db, "commenter1", "commenter")
	insertRipplesTestReed(t, db, "author1", "reed1")
	key := newRippleTestKey(t, db, "commenter1")

	svc := &DataService{db: db}
	resp := postTestRipple(t, svc, key, "author1", "reed1", "commenter1", "hello", nil, time.Now())
	if resp.ThreadID == "" {
		t.Error("expected a non-empty thread_id")
	}
	if resp.ReplyingTo != nil {
		t.Errorf("expected nil ReplyingTo, got %v", *resp.ReplyingTo)
	}
	if resp.ID == "" {
		t.Error("expected a non-empty id/hash")
	}
	if resp.UserSignature.Fingerprint != key.Fingerprint {
		t.Errorf("UserSignature.Fingerprint = %q, want %q", resp.UserSignature.Fingerprint, key.Fingerprint)
	}
}

func TestPostRipple_Reply_InheritsThreadID(t *testing.T) {
	db := openRipplesTestDB(t)
	insertRipplesTestUser(t, db, "author1", "author")
	insertRipplesTestUser(t, db, "commenter1", "commenter")
	insertRipplesTestUser(t, db, "commenter2", "commenter2")
	insertRipplesTestReed(t, db, "author1", "reed1")
	key1 := newRippleTestKey(t, db, "commenter1")
	key2 := newRippleTestKey(t, db, "commenter2")

	svc := &DataService{db: db}
	root := postTestRipple(t, svc, key1, "author1", "reed1", "commenter1", "root", nil, time.Now())
	reply := postTestRipple(t, svc, key2, "author1", "reed1", "commenter2", "reply", &root.ID, time.Now())
	if reply.ThreadID != root.ThreadID {
		t.Errorf("reply.ThreadID = %q, want %q (inherited from root)", reply.ThreadID, root.ThreadID)
	}

	reply2 := postTestRipple(t, svc, key1, "author1", "reed1", "commenter1", "reply2", &reply.ID, time.Now())
	if reply2.ThreadID != root.ThreadID {
		t.Errorf("reply2.ThreadID = %q, want %q (3-deep chain)", reply2.ThreadID, root.ThreadID)
	}
}

func TestPostRipple_ThreadMismatchRejected(t *testing.T) {
	db := openRipplesTestDB(t)
	insertRipplesTestUser(t, db, "author1", "author")
	insertRipplesTestUser(t, db, "commenter1", "commenter")
	insertRipplesTestReed(t, db, "author1", "reed1")
	key := newRippleTestKey(t, db, "commenter1")

	svc := &DataService{db: db}
	root := postTestRipple(t, svc, key, "author1", "reed1", "commenter1", "root", nil, time.Now())

	wrongThreadID := uuid.NewString()
	userSig := signRippleUserPayload(t, key, "author1", "reed1", "commenter1", wrongThreadID, root.ID, "reply")
	countersign, _ := testCountersign(t)
	_, err := svc.PostRipple(context.Background(), "author1", "reed1", "commenter1", "reply", wrongThreadID, &root.ID, key.Fingerprint, userSig, countersign, time.Now())
	if err != ErrRippleThreadMismatch {
		t.Fatalf("err = %v, want ErrRippleThreadMismatch", err)
	}
}

func TestPostRipple_CreatesBookkeepingRowLazily(t *testing.T) {
	db := openRipplesTestDB(t)
	insertRipplesTestUser(t, db, "author1", "author")
	insertRipplesTestUser(t, db, "commenter1", "commenter")
	insertRipplesTestReed(t, db, "author1", "reed1")
	key := newRippleTestKey(t, db, "commenter1")

	svc := &DataService{db: db}
	postTestRipple(t, svc, key, "author1", "reed1", "commenter1", "first", nil, time.Now())
	postTestRipple(t, svc, key, "author1", "reed1", "commenter1", "second", nil, time.Now())

	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM ripples WHERE reed_author_id = $1 AND reed_id = $2`, "author1", "reed1").Scan(&count); err != nil {
		t.Fatalf("count ripples rows: %v", err)
	}
	if count != 1 {
		t.Errorf("ripples bookkeeping row count = %d, want 1 (one row per reed, not per thread)", count)
	}
}

func TestPostRipple_BumpsSharedExpiryAcrossThreads(t *testing.T) {
	db := openRipplesTestDB(t)
	insertRipplesTestUser(t, db, "author1", "author")
	insertRipplesTestUser(t, db, "commenter1", "commenter")
	insertRipplesTestReed(t, db, "author1", "reed1")
	key := newRippleTestKey(t, db, "commenter1")

	svc := &DataService{db: db}
	t1 := time.Now().Add(-2 * time.Hour)
	postTestRipple(t, svc, key, "author1", "reed1", "commenter1", "threadA", nil, t1)

	var firstExpiry time.Time
	db.QueryRow(`SELECT expires_at FROM ripples WHERE reed_author_id = $1 AND reed_id = $2`, "author1", "reed1").Scan(&firstExpiry)

	t2 := time.Now()
	postTestRipple(t, svc, key, "author1", "reed1", "commenter1", "threadB", nil, t2)

	var secondExpiry time.Time
	db.QueryRow(`SELECT expires_at FROM ripples WHERE reed_author_id = $1 AND reed_id = $2`, "author1", "reed1").Scan(&secondExpiry)
	if !secondExpiry.After(firstExpiry) {
		t.Errorf("expires_at did not bump: first=%v second=%v", firstExpiry, secondExpiry)
	}
}

func TestListRipples_OrdersByThreadCreationThenPostOrder(t *testing.T) {
	db := openRipplesTestDB(t)
	insertRipplesTestUser(t, db, "author1", "author")
	insertRipplesTestUser(t, db, "commenter1", "commenter")
	insertRipplesTestReed(t, db, "author1", "reed1")
	key := newRippleTestKey(t, db, "commenter1")

	svc := &DataService{db: db}
	base := time.Now().Add(-1 * time.Hour)

	threadARoot := postTestRipple(t, svc, key, "author1", "reed1", "commenter1", "A-root", nil, base)
	threadBRoot := postTestRipple(t, svc, key, "author1", "reed1", "commenter1", "B-root", nil, base.Add(10*time.Second))
	threadAReply := postTestRipple(t, svc, key, "author1", "reed1", "commenter1", "A-reply", &threadARoot.ID, base.Add(20*time.Second))

	list, err := svc.ListRipples(context.Background(), "author1", "reed1", 50, "")
	if err != nil {
		t.Fatalf("ListRipples: %v", err)
	}
	if len(list.Ripples) != 3 {
		t.Fatalf("got %d ripples, want 3", len(list.Ripples))
	}
	want := []string{threadARoot.ID, threadAReply.ID, threadBRoot.ID}
	for i, id := range want {
		if list.Ripples[i].ID != id {
			t.Errorf("position %d: got id %q, want %q (order: all of thread A together, then thread B)", i, list.Ripples[i].ID, id)
		}
	}
}

func TestListRipples_Pagination(t *testing.T) {
	db := openRipplesTestDB(t)
	insertRipplesTestUser(t, db, "author1", "author")
	insertRipplesTestUser(t, db, "commenter1", "commenter")
	insertRipplesTestReed(t, db, "author1", "reed1")
	key := newRippleTestKey(t, db, "commenter1")

	svc := &DataService{db: db}
	base := time.Now().Add(-1 * time.Hour)

	var posted []string
	for i := 0; i < 5; i++ {
		resp := postTestRipple(t, svc, key, "author1", "reed1", "commenter1", "msg", nil, base.Add(time.Duration(i)*time.Second))
		posted = append(posted, resp.ID)
	}

	page1, err := svc.ListRipples(context.Background(), "author1", "reed1", 2, "")
	if err != nil {
		t.Fatalf("page1: %v", err)
	}
	if len(page1.Ripples) != 2 || !page1.HasMore {
		t.Fatalf("page1: got %d ripples, hasMore=%v, want 2/true", len(page1.Ripples), page1.HasMore)
	}

	var seen []string
	for _, r := range page1.Ripples {
		seen = append(seen, r.ID)
	}
	cursor := page1.NextCursor
	for len(seen) < 5 {
		page, err := svc.ListRipples(context.Background(), "author1", "reed1", 2, cursor)
		if err != nil {
			t.Fatalf("page fetch: %v", err)
		}
		if len(page.Ripples) == 0 {
			t.Fatal("page fetch returned zero ripples before exhausting all 5")
		}
		for _, r := range page.Ripples {
			seen = append(seen, r.ID)
		}
		cursor = page.NextCursor
		if !page.HasMore {
			break
		}
	}

	if len(seen) != 5 {
		t.Fatalf("total items seen across pages = %d, want 5", len(seen))
	}
	for i, id := range posted {
		if seen[i] != id {
			t.Errorf("position %d: got %q, want %q — duplicate or missing item across pages", i, seen[i], id)
		}
	}
}

func TestListRipples_IncludesSoftDeletedRowsWithOriginalSignatures(t *testing.T) {
	db := openRipplesTestDB(t)
	insertRipplesTestUser(t, db, "author1", "author")
	insertRipplesTestUser(t, db, "commenter1", "commenter")
	insertRipplesTestReed(t, db, "author1", "reed1")
	key := newRippleTestKey(t, db, "commenter1")

	svc := &DataService{db: db}
	resp := postTestRipple(t, svc, key, "author1", "reed1", "commenter1", "hello", nil, time.Now())
	origArmor := resp.UserSignature.Armor
	if _, _, err := svc.SoftDeleteRipple(context.Background(), resp.ID, "commenter1"); err != nil {
		t.Fatalf("SoftDeleteRipple: %v", err)
	}

	list, err := svc.ListRipples(context.Background(), "author1", "reed1", 50, "")
	if err != nil {
		t.Fatalf("ListRipples: %v", err)
	}
	if len(list.Ripples) != 1 {
		t.Fatalf("got %d ripples, want 1 (soft-deleted row must still be listed)", len(list.Ripples))
	}
	got := list.Ripples[0]
	if !got.Deleted || got.Content != "[DELETED]" {
		t.Errorf("got Deleted=%v Content=%q, want Deleted=true Content=\"[DELETED]\"", got.Deleted, got.Content)
	}
	if got.ID != resp.ID {
		t.Errorf("id changed after soft-delete: got %q, want %q", got.ID, resp.ID)
	}
	if got.UserSignature.Armor != origArmor {
		t.Error("userSignature must remain the ORIGINAL signature after soft-delete, not be cleared or changed")
	}
}

func TestSoftDeleteRipple_OwnerSucceeds(t *testing.T) {
	db := openRipplesTestDB(t)
	insertRipplesTestUser(t, db, "author1", "author")
	insertRipplesTestUser(t, db, "commenter1", "commenter")
	insertRipplesTestReed(t, db, "author1", "reed1")
	key := newRippleTestKey(t, db, "commenter1")

	svc := &DataService{db: db}
	resp := postTestRipple(t, svc, key, "author1", "reed1", "commenter1", "hello", nil, time.Now())

	var expiryBefore time.Time
	db.QueryRow(`SELECT expires_at FROM ripples WHERE reed_author_id = $1 AND reed_id = $2`, "author1", "reed1").Scan(&expiryBefore)

	found, owned, err := svc.SoftDeleteRipple(context.Background(), resp.ID, "commenter1")
	if err != nil {
		t.Fatalf("SoftDeleteRipple: %v", err)
	}
	if !found || !owned {
		t.Fatalf("found=%v owned=%v, want true/true", found, owned)
	}

	got, err := svc.GetRipple(context.Background(), resp.ID)
	if err != nil {
		t.Fatalf("GetRipple: %v", err)
	}
	if !got.Deleted || got.Content != "[DELETED]" {
		t.Errorf("got Deleted=%v Content=%q, want true/\"[DELETED]\"", got.Deleted, got.Content)
	}
	if got.ID != resp.ID {
		t.Errorf("id changed after delete: got %q, want %q", got.ID, resp.ID)
	}
	if got.ThreadID != resp.ThreadID {
		t.Errorf("ThreadID changed after delete: got %q, want %q", got.ThreadID, resp.ThreadID)
	}
	if got.UserSignature.Armor != resp.UserSignature.Armor {
		t.Error("userSignature must not change on soft-delete")
	}

	var expiryAfter time.Time
	db.QueryRow(`SELECT expires_at FROM ripples WHERE reed_author_id = $1 AND reed_id = $2`, "author1", "reed1").Scan(&expiryAfter)
	if !expiryAfter.Equal(expiryBefore) {
		t.Errorf("expires_at changed by a delete: before=%v after=%v", expiryBefore, expiryAfter)
	}
}

func TestSoftDeleteRipple_NonOwnerFails(t *testing.T) {
	db := openRipplesTestDB(t)
	insertRipplesTestUser(t, db, "author1", "author")
	insertRipplesTestUser(t, db, "commenter1", "commenter")
	insertRipplesTestUser(t, db, "commenter2", "commenter2")
	insertRipplesTestReed(t, db, "author1", "reed1")
	key := newRippleTestKey(t, db, "commenter1")

	svc := &DataService{db: db}
	resp := postTestRipple(t, svc, key, "author1", "reed1", "commenter1", "hello", nil, time.Now())

	found, owned, err := svc.SoftDeleteRipple(context.Background(), resp.ID, "commenter2")
	if err != nil {
		t.Fatalf("SoftDeleteRipple: %v", err)
	}
	if !found || owned {
		t.Fatalf("found=%v owned=%v, want true/false", found, owned)
	}

	got, err := svc.GetRipple(context.Background(), resp.ID)
	if err != nil {
		t.Fatalf("GetRipple: %v", err)
	}
	if got.Deleted {
		t.Error("response was deleted by a non-owner")
	}
}

func TestSoftDeleteRipple_MissingIDNotFound(t *testing.T) {
	db := openRipplesTestDB(t)
	svc := &DataService{db: db}
	found, owned, err := svc.SoftDeleteRipple(context.Background(), "nonexistent", "someone")
	if err != nil {
		t.Fatalf("SoftDeleteRipple: %v", err)
	}
	if found || owned {
		t.Fatalf("found=%v owned=%v, want false/false", found, owned)
	}
}

func TestSoftDeleteRipple_IdempotentOnAlreadyDeleted(t *testing.T) {
	db := openRipplesTestDB(t)
	insertRipplesTestUser(t, db, "author1", "author")
	insertRipplesTestUser(t, db, "commenter1", "commenter")
	insertRipplesTestReed(t, db, "author1", "reed1")
	key := newRippleTestKey(t, db, "commenter1")

	svc := &DataService{db: db}
	resp := postTestRipple(t, svc, key, "author1", "reed1", "commenter1", "hello", nil, time.Now())

	if _, _, err := svc.SoftDeleteRipple(context.Background(), resp.ID, "commenter1"); err != nil {
		t.Fatalf("first delete: %v", err)
	}
	found, owned, err := svc.SoftDeleteRipple(context.Background(), resp.ID, "commenter1")
	if err != nil {
		t.Fatalf("second delete: %v", err)
	}
	if !found || !owned {
		t.Fatalf("second delete: found=%v owned=%v, want true/true (idempotent)", found, owned)
	}
}

func TestReplyingToSoftDeletedRipple_StillResolves(t *testing.T) {
	db := openRipplesTestDB(t)
	insertRipplesTestUser(t, db, "author1", "author")
	insertRipplesTestUser(t, db, "commenter1", "commenter")
	insertRipplesTestReed(t, db, "author1", "reed1")
	key := newRippleTestKey(t, db, "commenter1")

	svc := &DataService{db: db}
	root := postTestRipple(t, svc, key, "author1", "reed1", "commenter1", "root", nil, time.Now())
	if _, _, err := svc.SoftDeleteRipple(context.Background(), root.ID, "commenter1"); err != nil {
		t.Fatalf("SoftDeleteRipple: %v", err)
	}

	reply := postTestRipple(t, svc, key, "author1", "reed1", "commenter1", "reply", &root.ID, time.Now())
	if reply.ThreadID != root.ThreadID {
		t.Errorf("reply.ThreadID = %q, want %q (inherited from soft-deleted target)", reply.ThreadID, root.ThreadID)
	}
}

func TestListRipples_IncludesRemovedAccountAuthorsUnfiltered(t *testing.T) {
	db := openRipplesTestDB(t)
	insertRipplesTestUser(t, db, "author1", "author")
	insertRipplesTestUser(t, db, "commenter1", "commenter")
	insertRipplesTestReed(t, db, "author1", "reed1")
	key := newRippleTestKey(t, db, "commenter1")

	svc := &DataService{db: db}
	resp := postTestRipple(t, svc, key, "author1", "reed1", "commenter1", "hello", nil, time.Now())

	insertAccountRemoval(t, db, "commenter1")

	list, err := svc.ListRipples(context.Background(), "author1", "reed1", 50, "")
	if err != nil {
		t.Fatalf("ListRipples: %v", err)
	}
	if len(list.Ripples) != 1 || list.Ripples[0].ID != resp.ID {
		t.Fatalf("removed account's response missing from list results")
	}
	if list.Ripples[0].Deleted {
		t.Error("account removal must not implicitly soft-delete the response")
	}
}

func TestPostRipple_AccountRemovalDoesNotCascade(t *testing.T) {
	db := openRipplesTestDB(t)
	insertRipplesTestUser(t, db, "author1", "author")
	insertRipplesTestUser(t, db, "commenter1", "commenter")
	insertRipplesTestReed(t, db, "author1", "reed1")
	key := newRippleTestKey(t, db, "commenter1")

	svc := &DataService{db: db}
	resp := postTestRipple(t, svc, key, "author1", "reed1", "commenter1", "hello", nil, time.Now())

	insertAccountRemoval(t, db, "commenter1")

	got, err := svc.GetRipple(context.Background(), resp.ID)
	if err != nil {
		t.Fatalf("GetRipple: %v", err)
	}
	if got.Deleted || got.Content != "hello" {
		t.Errorf("response mutated by account removal: Deleted=%v Content=%q", got.Deleted, got.Content)
	}
}
