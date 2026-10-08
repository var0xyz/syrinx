//go:build !ops

package main

import (
	"fmt"
	"testing"
	"time"
)

// Golden bytes. The SPA harness asserts the same strings; they must stay
// byte-identical or no thread signature verifies.
func TestThreadUserPayloadCanonicalShape(t *testing.T) {
	ids := make([]string, 11)
	for i := range ids {
		ids[i] = fmt.Sprintf("a@home/r%d", i)
	}
	got := buildThreadUserPayload("home", ids[0], ids)
	want := `{"reedIDs":["a@home/r0","a@home/r1","a@home/r2","a@home/r3","a@home/r4","a@home/r5",` +
		`"a@home/r6","a@home/r7","a@home/r8","a@home/r9","a@home/r10"],` +
		`"serverID":"home","threadID":"a@home/r0","type":"thread"}`
	if string(got) != want {
		t.Errorf("thread user payload mismatch:\n got=%s\nwant=%s", got, want)
	}
}

func TestThreadServerPayloadCanonicalShape(t *testing.T) {
	got := buildThreadServerPayload(
		"home",
		"a@home/r0",
		"a@home/k1",
		"SERVERKEY01",
		"SIG",
		time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC),
	)
	want := `{"authorKeyID":"a@home/k1","serverID":"home","serverKeyFingerprint":"SERVERKEY01",` +
		`"signedAt":"2026-10-07T12:00:00Z","threadID":"a@home/r0","type":"thread","userSignature":"SIG"}`
	if string(got) != want {
		t.Errorf("thread server payload mismatch:\n got=%s\nwant=%s", got, want)
	}
}

// Reordering the parts changes the signed bytes, so a signature can't be
// reused for a different order.
func TestThreadUserPayloadBindsOrder(t *testing.T) {
	a := buildThreadUserPayload("home", "a@home/r0", []string{"a@home/r0", "a@home/r1", "a@home/r2"})
	b := buildThreadUserPayload("home", "a@home/r0", []string{"a@home/r0", "a@home/r2", "a@home/r1"})
	if string(a) == string(b) {
		t.Error("reordered parts produced the same payload")
	}
}

func TestThreadRemovalPayloadsCanonicalShape(t *testing.T) {
	user := buildThreadRemovalUserPayload("home", "a@home/r0", "TSIG")
	wantUser := `{"serverID":"home","threadID":"a@home/r0","threadSignature":"TSIG","type":"thread_removal"}`
	if string(user) != wantUser {
		t.Errorf("thread removal user payload mismatch:\n got=%s\nwant=%s", user, wantUser)
	}
	server := buildThreadRemovalServerPayload(
		"home", "a@home/r0", "a@home/k1", "SERVERKEY01", "SIG",
		time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC),
	)
	wantServer := `{"authorKeyID":"a@home/k1","serverID":"home","serverKeyFingerprint":"SERVERKEY01",` +
		`"signedAt":"2026-10-07T12:00:00Z","threadID":"a@home/r0","type":"thread_removal","userSignature":"SIG"}`
	if string(server) != wantServer {
		t.Errorf("thread removal server payload mismatch:\n got=%s\nwant=%s", server, wantServer)
	}
}
