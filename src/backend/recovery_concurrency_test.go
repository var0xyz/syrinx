//go:build !ops

package main

import (
	"context"
	"database/sql"
	"testing"
	"time"
)

// waitForBlockedAdvisoryLock polls until some session is waiting on an
// advisory lock, failing the test if none does within a few seconds.
func waitForBlockedAdvisoryLock(t *testing.T, db *sql.DB) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		var waiting bool
		if err := db.QueryRow(`
			SELECT EXISTS(SELECT 1 FROM pg_locks WHERE locktype = 'advisory' AND NOT granted)
		`).Scan(&waiting); err != nil {
			t.Fatalf("query pg_locks: %v", err)
		}
		if waiting {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("follower never waited for the target's recovery to commit")
}

// TestSaveRecoveryFollowing_WaitsForConcurrentTargetRecovery: a follow
// reported while its target is mid-recovery must not land as a pending
// row the target's drain already ran past.
func TestSaveRecoveryFollowing_WaitsForConcurrentTargetRecovery(t *testing.T) {
	db := openRecoveryCollisionTestDB(t)
	ctx := context.Background()
	follower, target := "follower@test", "target@test"
	insertFollowingTestIdentity(t, db, follower, "test")

	// The target's recovery, mirroring savePeerIdentity, left uncommitted.
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer tx.Rollback()
	signedAt := time.Now().UTC().Truncate(time.Second)
	profile := testRecoveryProfile("target", "target", signedAt)
	if _, err := upsertRecoveryIdentity(ctx, tx, "test", testSigner(new([][]byte)), profile, testRecoveryFlatKey("target", signedAt)); err != nil {
		t.Fatalf("upsertRecoveryIdentity: %v", err)
	}
	if err := drainRecoveryPendingFollows(ctx, tx, identityID(target)); err != nil {
		t.Fatalf("drain: %v", err)
	}

	done := make(chan error, 1)
	go func() {
		done <- saveRecoveryFollowing(ctx, db, "test", follower, []string{target})
	}()
	waitForBlockedAdvisoryLock(t, db)
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit target recovery: %v", err)
	}
	if err := <-done; err != nil {
		t.Fatalf("saveRecoveryFollowing: %v", err)
	}

	var following, pending int
	if err := db.QueryRow(`
		SELECT
			(SELECT COUNT(*) FROM user_following WHERE user_id = $1 AND following_user_id = $2),
			(SELECT COUNT(*) FROM pending_follows WHERE follower_user_id = $1)
	`, follower, target).Scan(&following, &pending); err != nil {
		t.Fatalf("query follows: %v", err)
	}
	if following != 1 || pending != 0 {
		t.Errorf("following=%d pending=%d, want the follow applied and nothing left pending", following, pending)
	}
}

// TestResetDispatchedAt_OnlyResetsItsOwnClaim: a stale failure from one
// holder must not cancel a newer dispatch to another.
func TestResetDispatchedAt_OnlyResetsItsOwnClaim(t *testing.T) {
	f := newKeyRevocationFixture(t)
	ctx := context.Background()
	if _, err := f.db.Exec(`INSERT INTO online_users (user_id) VALUES ($1) ON CONFLICT DO NOTHING`, f.bob); err != nil {
		t.Fatal(err)
	}
	if err := f.ds.CreatePendingKeyEvent(ctx, "ev-reset", "req-1", f.bob, f.aliceKey); err != nil {
		t.Fatalf("CreatePendingKeyEvent: %v", err)
	}
	if ok, err := f.ds.MarkEventDispatched(ctx, "ev-reset", f.carol); err != nil || !ok {
		t.Fatalf("MarkEventDispatched: ok=%v err=%v", ok, err)
	}

	if reset, err := f.ds.ResetDispatchedAt(ctx, "ev-reset", f.alice); err != nil || reset {
		t.Fatalf("reset by a stale holder: reset=%v err=%v", reset, err)
	}
	if reset, err := f.ds.ResetEventDelivery(ctx, "ev-reset", ""); err != nil || reset {
		t.Fatalf("reset of a claim read before dispatch: reset=%v err=%v", reset, err)
	}
	if pe, _ := f.ds.GetPendingSubject(ctx, "ev-reset"); pe == nil || pe.DispatchedTo != f.carol {
		t.Fatalf("claim = %+v, want it still with carol", pe)
	}

	if reset, err := f.ds.ResetDispatchedAt(ctx, "ev-reset", f.carol); err != nil || !reset {
		t.Fatalf("reset by the claiming holder: reset=%v err=%v", reset, err)
	}
	if pe, _ := f.ds.GetPendingSubject(ctx, "ev-reset"); pe == nil || pe.Dispatched || pe.DispatchedTo != "" {
		t.Fatalf("claim = %+v, want it released", pe)
	}
}
