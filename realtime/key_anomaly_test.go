package realtime

import (
	"context"
	"testing"

	"syrinx/observability/metrics"
)

type fakeRecorder struct {
	metrics.Noop
	keyFetchErrors  []call3
	revokedKeysUsed []call3
}

type call3 struct{ reporter, target, fingerprint string }

func (f *fakeRecorder) KeyFetchError(_ context.Context, reporter, target, fingerprint string) {
	f.keyFetchErrors = append(f.keyFetchErrors, call3{reporter, target, fingerprint})
}

func (f *fakeRecorder) RevokedKeyUsed(_ context.Context, reporter, target, fingerprint string) {
	f.revokedKeysUsed = append(f.revokedKeysUsed, call3{reporter, target, fingerprint})
}

func TestHandleKeyFetchError(t *testing.T) {
	t.Parallel()

	rec := &fakeRecorder{}
	rs := &RealtimeService{metrics: rec}
	client := &Client{userID: "viewer-1"}

	rs.handleKeyFetchError(client, KeyFetchErrorData{UserID: "author-1", Fingerprint: "FP1"})

	if len(rec.keyFetchErrors) != 1 {
		t.Fatalf("keyFetchErrors = %d calls, want 1", len(rec.keyFetchErrors))
	}
	got := rec.keyFetchErrors[0]
	if got.reporter != "viewer-1" || got.target != "author-1" || got.fingerprint != "FP1" {
		t.Fatalf("unexpected call: %+v", got)
	}
	if len(rec.revokedKeysUsed) != 0 {
		t.Fatalf("RevokedKeyUsed should not fire from handleKeyFetchError")
	}
}

func TestHandleKeyFetchErrorIgnoresEmptyPayload(t *testing.T) {
	t.Parallel()

	rec := &fakeRecorder{}
	rs := &RealtimeService{metrics: rec}
	client := &Client{userID: "viewer-1"}

	rs.handleKeyFetchError(client, KeyFetchErrorData{})
	rs.handleKeyFetchError(client, KeyFetchErrorData{UserID: "author-1"})
	rs.handleKeyFetchError(client, KeyFetchErrorData{Fingerprint: "FP1"})

	if len(rec.keyFetchErrors) != 0 {
		t.Fatalf("keyFetchErrors = %d calls, want 0 for malformed payloads", len(rec.keyFetchErrors))
	}
}

func TestHandleRevokedKeyUsed(t *testing.T) {
	t.Parallel()

	rec := &fakeRecorder{}
	rs := &RealtimeService{metrics: rec}
	client := &Client{userID: "viewer-1"}

	rs.handleRevokedKeyUsed(client, RevokedKeyUsedData{UserID: "author-1", Fingerprint: "FP1"})

	if len(rec.revokedKeysUsed) != 1 {
		t.Fatalf("revokedKeysUsed = %d calls, want 1", len(rec.revokedKeysUsed))
	}
	got := rec.revokedKeysUsed[0]
	if got.reporter != "viewer-1" || got.target != "author-1" || got.fingerprint != "FP1" {
		t.Fatalf("unexpected call: %+v", got)
	}
	if len(rec.keyFetchErrors) != 0 {
		t.Fatalf("KeyFetchError should not fire from handleRevokedKeyUsed")
	}
}

func TestHandleRevokedKeyUsedIgnoresEmptyPayload(t *testing.T) {
	t.Parallel()

	rec := &fakeRecorder{}
	rs := &RealtimeService{metrics: rec}
	client := &Client{userID: "viewer-1"}

	rs.handleRevokedKeyUsed(client, RevokedKeyUsedData{})
	rs.handleRevokedKeyUsed(client, RevokedKeyUsedData{UserID: "author-1"})
	rs.handleRevokedKeyUsed(client, RevokedKeyUsedData{Fingerprint: "FP1"})

	if len(rec.revokedKeysUsed) != 0 {
		t.Fatalf("revokedKeysUsed = %d calls, want 0 for malformed payloads", len(rec.revokedKeysUsed))
	}
}
