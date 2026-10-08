//go:build !ops

package main

import (
	"testing"
	"time"
)

// Golden bytes. The SPA harness asserts the same strings; they must stay
// byte-identical or no block signature verifies.
func TestBlockUserPayloadCanonicalShape(t *testing.T) {
	got := buildBlockUserPayload("k3x9@home1234", "p7q2@peer5678", "k3x9@home1234/4a1e")
	want := `{"blockedUserID":"p7q2@peer5678","keyID":"k3x9@home1234/4a1e","type":"block","userID":"k3x9@home1234"}`
	if string(got) != want {
		t.Errorf("block user payload mismatch:\n got=%q\nwant=%q", got, want)
	}
}

func TestBlockServerPayloadCanonicalShape(t *testing.T) {
	ts := time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)
	got := buildBlockServerPayload("k3x9@home1234", "p7q2@peer5678", "SERVERKEY01", "USERSIG", ts)
	want := `{"blockedUserID":"p7q2@peer5678","serverKeyFingerprint":"SERVERKEY01",` +
		`"signedAt":"2026-10-08T12:00:00Z","type":"block","userID":"k3x9@home1234","userSignature":"USERSIG"}`
	if string(got) != want {
		t.Errorf("block server payload mismatch:\n got=%q\nwant=%q", got, want)
	}
}
