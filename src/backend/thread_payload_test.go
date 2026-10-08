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
	// Index keys sort as strings: 10 precedes 2.
	want := "---\n" +
		"0: a@home/r0\n" +
		"1: a@home/r1\n" +
		"10: a@home/r10\n" +
		"2: a@home/r2\n" +
		"3: a@home/r3\n" +
		"4: a@home/r4\n" +
		"5: a@home/r5\n" +
		"6: a@home/r6\n" +
		"7: a@home/r7\n" +
		"8: a@home/r8\n" +
		"9: a@home/r9\n" +
		"serverID: home\n" +
		"threadID: a@home/r0\n" +
		"type: thread\n" +
		"---\n"
	if string(got) != want {
		t.Errorf("thread user payload mismatch:\n got=%q\nwant=%q", got, want)
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
	want := "---\n" +
		"authorKeyID: a@home/k1\n" +
		"serverID: home\n" +
		"serverKeyFingerprint: SERVERKEY01\n" +
		"signedAt: 2026-10-07T12:00:00Z\n" +
		"threadID: a@home/r0\n" +
		"type: thread\n" +
		"userSignature: U0lH\n" +
		"---\n"
	if string(got) != want {
		t.Errorf("thread server payload mismatch:\n got=%q\nwant=%q", got, want)
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
	wantUser := "---\n" +
		"serverID: home\n" +
		"threadID: a@home/r0\n" +
		"threadSignature: VFNJRw==\n" +
		"type: thread_removal\n" +
		"---\n"
	if string(user) != wantUser {
		t.Errorf("thread removal user payload mismatch:\n got=%q\nwant=%q", user, wantUser)
	}
	server := buildThreadRemovalServerPayload(
		"home", "a@home/r0", "a@home/k1", "SERVERKEY01", "SIG",
		time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC),
	)
	wantServer := "---\n" +
		"authorKeyID: a@home/k1\n" +
		"serverID: home\n" +
		"serverKeyFingerprint: SERVERKEY01\n" +
		"signedAt: 2026-10-07T12:00:00Z\n" +
		"threadID: a@home/r0\n" +
		"type: thread_removal\n" +
		"userSignature: U0lH\n" +
		"---\n"
	if string(server) != wantServer {
		t.Errorf("thread removal server payload mismatch:\n got=%q\nwant=%q", server, wantServer)
	}
}
