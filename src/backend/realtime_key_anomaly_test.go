//go:build !ops && !ripplescleanup

package main

import (
	"context"
	"testing"

	"syrinx/observability/metrics"
)

type fakeRealtimeRecorder struct {
	metrics.Noop
	keyFetchErrors  []realtimeCall3
	revokedKeysUsed []realtimeCall3
}

type realtimeCall3 struct{ reporter, target, keyID string }

func (f *fakeRealtimeRecorder) KeyFetchError(_ context.Context, reporter, target, keyID string) {
	f.keyFetchErrors = append(f.keyFetchErrors, realtimeCall3{reporter, target, keyID})
}

func (f *fakeRealtimeRecorder) RevokedKeyUsed(_ context.Context, reporter, target, keyID string) {
	f.revokedKeysUsed = append(f.revokedKeysUsed, realtimeCall3{reporter, target, keyID})
}

func TestHandleKeyFetchError(t *testing.T) {
	t.Parallel()

	rec := &fakeRealtimeRecorder{}
	rs := &realtimeService{metrics: rec}
	client := &realtimeClient{userID: "viewer-1"}

	rs.handleKeyFetchError(client, "author-1", "FP1")

	if len(rec.keyFetchErrors) != 1 {
		t.Fatalf("keyFetchErrors = %d calls, want 1", len(rec.keyFetchErrors))
	}
	got := rec.keyFetchErrors[0]
	if got.reporter != "viewer-1" || got.target != "author-1" || got.keyID != "FP1" {
		t.Fatalf("unexpected call: %+v", got)
	}
	if len(rec.revokedKeysUsed) != 0 {
		t.Fatalf("RevokedKeyUsed should not fire from handleKeyFetchError")
	}
}

func TestHandleKeyFetchErrorIgnoresEmptyPayload(t *testing.T) {
	t.Parallel()

	rec := &fakeRealtimeRecorder{}
	rs := &realtimeService{metrics: rec}
	client := &realtimeClient{userID: "viewer-1"}

	rs.handleKeyFetchError(client, "", "")
	rs.handleKeyFetchError(client, "author-1", "")
	rs.handleKeyFetchError(client, "", "FP1")

	if len(rec.keyFetchErrors) != 0 {
		t.Fatalf("keyFetchErrors = %d calls, want 0 for malformed payloads", len(rec.keyFetchErrors))
	}
}

func TestHandleRevokedKeyUsed(t *testing.T) {
	t.Parallel()

	rec := &fakeRealtimeRecorder{}
	rs := &realtimeService{metrics: rec}
	client := &realtimeClient{userID: "viewer-1"}

	rs.handleRevokedKeyUsed(client, "author-1", "FP1")

	if len(rec.revokedKeysUsed) != 1 {
		t.Fatalf("revokedKeysUsed = %d calls, want 1", len(rec.revokedKeysUsed))
	}
	got := rec.revokedKeysUsed[0]
	if got.reporter != "viewer-1" || got.target != "author-1" || got.keyID != "FP1" {
		t.Fatalf("unexpected call: %+v", got)
	}
	if len(rec.keyFetchErrors) != 0 {
		t.Fatalf("KeyFetchError should not fire from handleRevokedKeyUsed")
	}
}

func TestHandleRevokedKeyUsedIgnoresEmptyPayload(t *testing.T) {
	t.Parallel()

	rec := &fakeRealtimeRecorder{}
	rs := &realtimeService{metrics: rec}
	client := &realtimeClient{userID: "viewer-1"}

	rs.handleRevokedKeyUsed(client, "", "")
	rs.handleRevokedKeyUsed(client, "author-1", "")
	rs.handleRevokedKeyUsed(client, "", "FP1")

	if len(rec.revokedKeysUsed) != 0 {
		t.Fatalf("revokedKeysUsed = %d calls, want 0 for malformed payloads", len(rec.revokedKeysUsed))
	}
}
