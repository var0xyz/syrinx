//go:build !ops

package main

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

// TestCreateReed_GenesisRequiresEmptyPreviousID covers the "author has zero
// reeds" branch of the tip check: an empty previousID is required and
// accepted; a non-empty previousID with no reeds yet is a fork.
func TestCreateReed_GenesisRequiresEmptyPreviousID(t *testing.T) {
	db := openMentionsTestDB(t)
	ctx := context.Background()
	svc := &DataService{db: db, serverID: "testserver"}

	seedMentionUser(t, db, "alice")

	reedID := newTestReedID(t)
	ts := time.Now().UTC().Truncate(time.Second)
	_, err := svc.CreateReed(ctx, createReedParams{
		ReedID: reedID, UserID: "alice@testserver", UserKeyID: "alicefp",
		UserSignatureB64: "sig", ServerFingerprint: "srvfp-alice", ServerSignatureB64: "sig",
		Timestamp: ts, PreviousID: "",
	})
	if err != nil {
		t.Fatalf("genesis create with empty previousID: %v", err)
	}
}

func TestCreateReed_GenesisWithNonemptyPreviousIDForks(t *testing.T) {
	db := openMentionsTestDB(t)
	ctx := context.Background()
	svc := &DataService{db: db, serverID: "testserver"}

	seedMentionUser(t, db, "alice")

	reedID := newTestReedID(t)
	ts := time.Now().UTC().Truncate(time.Second)
	_, err := svc.CreateReed(ctx, createReedParams{
		ReedID: reedID, UserID: "alice@testserver", UserKeyID: "alicefp",
		UserSignatureB64: "sig", ServerFingerprint: "srvfp-alice", ServerSignatureB64: "sig",
		Timestamp: ts, PreviousID: "some-nonexistent-reed",
	})
	if !errors.Is(err, ErrReedFork) {
		t.Fatalf("expected ErrReedFork, got %v", err)
	}
}

// TestCreateReed_MatchingTipSucceeds covers the normal case: second create
// naming the first reed as previousID succeeds.
func TestCreateReed_MatchingTipSucceeds(t *testing.T) {
	db := openMentionsTestDB(t)
	ctx := context.Background()
	svc := &DataService{db: db, serverID: "testserver"}

	seedMentionUser(t, db, "alice")

	first := newTestReedID(t)
	ts := time.Now().UTC().Truncate(time.Second)
	if _, err := svc.CreateReed(ctx, createReedParams{
		ReedID: first, UserID: "alice@testserver", UserKeyID: "alicefp",
		UserSignatureB64: "sig", ServerFingerprint: "srvfp-alice", ServerSignatureB64: "sig",
		Timestamp: ts, PreviousID: "",
	}); err != nil {
		t.Fatalf("first create: %v", err)
	}

	second := newTestReedID(t)
	if _, err := svc.CreateReed(ctx, createReedParams{
		ReedID: second, UserID: "alice@testserver", UserKeyID: "alicefp",
		UserSignatureB64: "sig", ServerFingerprint: "srvfp-alice", ServerSignatureB64: "sig",
		Timestamp: ts.Add(time.Second), PreviousID: first,
	}); err != nil {
		t.Fatalf("second create naming first as tip: %v", err)
	}
}

// TestCreateReed_StaleTipForks: naming the old tip after a newer reed has
// already been published must be rejected.
func TestCreateReed_StaleTipForks(t *testing.T) {
	db := openMentionsTestDB(t)
	ctx := context.Background()
	svc := &DataService{db: db, serverID: "testserver"}

	seedMentionUser(t, db, "alice")

	first := newTestReedID(t)
	ts := time.Now().UTC().Truncate(time.Second)
	if _, err := svc.CreateReed(ctx, createReedParams{
		ReedID: first, UserID: "alice@testserver", UserKeyID: "alicefp",
		UserSignatureB64: "sig", ServerFingerprint: "srvfp-alice", ServerSignatureB64: "sig",
		Timestamp: ts, PreviousID: "",
	}); err != nil {
		t.Fatalf("first create: %v", err)
	}

	second := newTestReedID(t)
	if _, err := svc.CreateReed(ctx, createReedParams{
		ReedID: second, UserID: "alice@testserver", UserKeyID: "alicefp",
		UserSignatureB64: "sig", ServerFingerprint: "srvfp-alice", ServerSignatureB64: "sig",
		Timestamp: ts.Add(time.Second), PreviousID: first,
	}); err != nil {
		t.Fatalf("second create: %v", err)
	}

	third := newTestReedID(t)
	_, err := svc.CreateReed(ctx, createReedParams{
		ReedID: third, UserID: "alice@testserver", UserKeyID: "alicefp",
		UserSignatureB64: "sig", ServerFingerprint: "srvfp-alice", ServerSignatureB64: "sig",
		Timestamp: ts.Add(2 * time.Second), PreviousID: first, // stale — second is now the tip
	})
	if !errors.Is(err, ErrReedFork) {
		t.Fatalf("expected ErrReedFork naming the stale tip, got %v", err)
	}
}

// TestCreateReed_UnknownPreviousIDForks covers previousID naming a reed
// that doesn't exist at all (not just stale).
func TestCreateReed_UnknownPreviousIDForks(t *testing.T) {
	db := openMentionsTestDB(t)
	ctx := context.Background()
	svc := &DataService{db: db, serverID: "testserver"}

	seedMentionUser(t, db, "alice")

	first := newTestReedID(t)
	ts := time.Now().UTC().Truncate(time.Second)
	if _, err := svc.CreateReed(ctx, createReedParams{
		ReedID: first, UserID: "alice@testserver", UserKeyID: "alicefp",
		UserSignatureB64: "sig", ServerFingerprint: "srvfp-alice", ServerSignatureB64: "sig",
		Timestamp: ts, PreviousID: "",
	}); err != nil {
		t.Fatalf("first create: %v", err)
	}

	second := newTestReedID(t)
	_, err := svc.CreateReed(ctx, createReedParams{
		ReedID: second, UserID: "alice@testserver", UserKeyID: "alicefp",
		UserSignatureB64: "sig", ServerFingerprint: "srvfp-alice", ServerSignatureB64: "sig",
		Timestamp: ts.Add(time.Second), PreviousID: "totally-unknown-reed-id",
	})
	if !errors.Is(err, ErrReedFork) {
		t.Fatalf("expected ErrReedFork for unknown previousID, got %v", err)
	}
}

// TestCreateReed_AfterDeleteNamingNewTipSucceeds: once the tip is removed
// (reed_removals), the former second-newest becomes the tip; naming it
// succeeds, naming the removed (former tip) id still fails.
func TestCreateReed_AfterDeleteNamingNewTipSucceeds(t *testing.T) {
	db := openMentionsTestDB(t)
	ctx := context.Background()
	svc := &DataService{db: db, serverID: "testserver"}

	seedMentionUser(t, db, "alice")

	first := newTestReedID(t)
	ts := time.Now().UTC().Truncate(time.Second)
	if _, err := svc.CreateReed(ctx, createReedParams{
		ReedID: first, UserID: "alice@testserver", UserKeyID: "alicefp",
		UserSignatureB64: "sig", ServerFingerprint: "srvfp-alice", ServerSignatureB64: "sig",
		Timestamp: ts, PreviousID: "",
	}); err != nil {
		t.Fatalf("first create: %v", err)
	}

	second := newTestReedID(t)
	if _, err := svc.CreateReed(ctx, createReedParams{
		ReedID: second, UserID: "alice@testserver", UserKeyID: "alicefp",
		UserSignatureB64: "sig", ServerFingerprint: "srvfp-alice", ServerSignatureB64: "sig",
		Timestamp: ts.Add(time.Second), PreviousID: first,
	}); err != nil {
		t.Fatalf("second create: %v", err)
	}

	// Remove the tip (second). reed_id is canonical (embeds the author) —
	// no separate user_id column.
	if _, err := db.Exec(`INSERT INTO reed_removals (reed_id) VALUES ($1)`, second); err != nil {
		t.Fatal(err)
	}

	// Naming the new tip (first, since second is removed) must succeed.
	third := newTestReedID(t)
	if _, err := svc.CreateReed(ctx, createReedParams{
		ReedID: third, UserID: "alice@testserver", UserKeyID: "alicefp",
		UserSignatureB64: "sig", ServerFingerprint: "srvfp-alice", ServerSignatureB64: "sig",
		Timestamp: ts.Add(2 * time.Second), PreviousID: first,
	}); err != nil {
		t.Fatalf("create naming post-removal tip: %v", err)
	}

	// Naming the removed reed as tip must still fail (it's no longer a
	// valid tip even though the row exists).
	fourth := newTestReedID(t)
	_, err := svc.CreateReed(ctx, createReedParams{
		ReedID: fourth, UserID: "alice@testserver", UserKeyID: "alicefp",
		UserSignatureB64: "sig", ServerFingerprint: "srvfp-alice", ServerSignatureB64: "sig",
		Timestamp: ts.Add(3 * time.Second), PreviousID: second,
	})
	if !errors.Is(err, ErrReedFork) {
		t.Fatalf("expected ErrReedFork naming the removed reed, got %v", err)
	}
}

// TestCreateReed_PreviousIDFromAnotherUserForks: previousID naming a reed
// that exists but belongs to a different author is never that author's tip.
func TestCreateReed_PreviousIDFromAnotherUserForks(t *testing.T) {
	db := openMentionsTestDB(t)
	ctx := context.Background()
	svc := &DataService{db: db, serverID: "testserver"}

	seedMentionUser(t, db, "alice")
	seedMentionUser(t, db, "bob")

	bobsReed := newTestReedID(t)
	ts := time.Now().UTC().Truncate(time.Second)
	if _, err := svc.CreateReed(ctx, createReedParams{
		ReedID: bobsReed, UserID: "bob@testserver", UserKeyID: "bobfp",
		UserSignatureB64: "sig", ServerFingerprint: "srvfp-bob", ServerSignatureB64: "sig",
		Timestamp: ts, PreviousID: "",
	}); err != nil {
		t.Fatalf("bob's create: %v", err)
	}

	aliceReed := newTestReedID(t)
	_, err := svc.CreateReed(ctx, createReedParams{
		ReedID: aliceReed, UserID: "alice@testserver", UserKeyID: "alicefp",
		UserSignatureB64: "sig", ServerFingerprint: "srvfp-alice", ServerSignatureB64: "sig",
		Timestamp: ts.Add(time.Second), PreviousID: bobsReed,
	})
	if !errors.Is(err, ErrReedFork) {
		t.Fatalf("expected ErrReedFork naming another user's reed, got %v", err)
	}
}

// TestCreateReed_ConcurrentSameTipOneWinsOneForks proves the per-author
// lock actually serializes concurrent creates sharing one believed tip:
// exactly one must succeed, the other must see ErrReedFork — not two
// successes (which would be the fork this feature exists to prevent).
func TestCreateReed_ConcurrentSameTipOneWinsOneForks(t *testing.T) {
	db := openMentionsTestDB(t)
	ctx := context.Background()
	svc := &DataService{db: db, serverID: "testserver"}

	seedMentionUser(t, db, "alice")

	first := newTestReedID(t)
	ts := time.Now().UTC().Truncate(time.Second)
	if _, err := svc.CreateReed(ctx, createReedParams{
		ReedID: first, UserID: "alice@testserver", UserKeyID: "alicefp",
		UserSignatureB64: "sig", ServerFingerprint: "srvfp-alice", ServerSignatureB64: "sig",
		Timestamp: ts, PreviousID: "",
	}); err != nil {
		t.Fatalf("first create: %v", err)
	}

	second := newTestReedID(t)
	third := newTestReedID(t)

	var wg sync.WaitGroup
	errs := make([]error, 2)
	wg.Add(2)
	go func() {
		defer wg.Done()
		_, errs[0] = svc.CreateReed(ctx, createReedParams{
			ReedID: second, UserID: "alice@testserver", UserKeyID: "alicefp",
			UserSignatureB64: "sig", ServerFingerprint: "srvfp-alice", ServerSignatureB64: "sig",
			Timestamp: ts.Add(time.Second), PreviousID: first,
		})
	}()
	go func() {
		defer wg.Done()
		_, errs[1] = svc.CreateReed(ctx, createReedParams{
			ReedID: third, UserID: "alice@testserver", UserKeyID: "alicefp",
			UserSignatureB64: "sig", ServerFingerprint: "srvfp-alice", ServerSignatureB64: "sig",
			Timestamp: ts.Add(time.Second), PreviousID: first,
		})
	}()
	wg.Wait()

	successes := 0
	forks := 0
	for _, e := range errs {
		switch {
		case e == nil:
			successes++
		case errors.Is(e, ErrReedFork):
			forks++
		default:
			t.Fatalf("unexpected error: %v", e)
		}
	}
	if successes != 1 || forks != 1 {
		t.Fatalf("expected exactly 1 success and 1 fork, got %d successes, %d forks (errs=%v)", successes, forks, errs)
	}
}
