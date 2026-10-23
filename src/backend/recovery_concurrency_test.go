//go:build !ops

package main

import (
	"context"
	"testing"
)

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
