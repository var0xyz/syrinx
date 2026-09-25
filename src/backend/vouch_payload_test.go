//go:build !ops

package main

import (
	"strings"
	"testing"
	"time"
)

// Golden bytes. The SPA harness asserts the same strings; they must stay
// byte-identical or no vouch signature verifies.
func TestVouchUserPayloadCanonicalShape(t *testing.T) {
	got := buildVouchUserPayload(
		"alice@home1234/4a1e",
		"bob@peer5678",
		"bob@peer5678/9f3c",
		"met at the cafe",
	)
	want := "---\n" +
		"subjectKeyID: bob@peer5678/9f3c\n" +
		"subjectUserID: bob@peer5678\n" +
		"type: user_vouch\n" +
		"voucherKeyID: alice@home1234/4a1e\n" +
		"---\n" +
		"met at the cafe"
	if string(got) != want {
		t.Errorf("vouch user payload mismatch:\n got=%q\nwant=%q", got, want)
	}
}

// An empty note drops the content but leaves the envelope.
func TestVouchUserPayloadEmptyNote(t *testing.T) {
	got := buildVouchUserPayload("alice@home/k1", "bob@peer", "bob@peer/k2", "")
	want := "---\n" +
		"subjectKeyID: bob@peer/k2\n" +
		"subjectUserID: bob@peer\n" +
		"type: user_vouch\n" +
		"voucherKeyID: alice@home/k1\n" +
		"---\n"
	if string(got) != want {
		t.Errorf("noteless vouch payload mismatch:\n got=%q\nwant=%q", got, want)
	}
}

func TestVouchServerPayloadCanonicalShape(t *testing.T) {
	ts := time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC)
	got := buildVouchServerPayload(
		"alice@home1234",
		"bob@peer5678",
		"bob@peer5678/9f3c",
		"SERVERKEY01",
		"BASE64USERSIG",
		ts,
	)
	want := "---\n" +
		"serverKeyFingerprint: SERVERKEY01\n" +
		"signedAt: 2026-09-25T12:00:00Z\n" +
		"subjectKeyID: bob@peer5678/9f3c\n" +
		"subjectUserID: bob@peer5678\n" +
		"type: user_vouch\n" +
		"voucherUserID: alice@home1234\n" +
		"---\n" +
		"BASE64USERSIG"
	if string(got) != want {
		t.Errorf("vouch server payload mismatch:\n got=%q\nwant=%q", got, want)
	}
}

func TestVouchWithdrawalPayloadCanonicalShape(t *testing.T) {
	got := buildVouchWithdrawalUserPayload(
		"alice@home1234/beef",
		"bob@peer5678",
		"bob@peer5678/9f3c",
	)
	want := "---\n" +
		"subjectKeyID: bob@peer5678/9f3c\n" +
		"subjectUserID: bob@peer5678\n" +
		"type: user_vouch_withdrawal\n" +
		"voucherKeyID: alice@home1234/beef\n" +
		"---\n"
	if string(got) != want {
		t.Errorf("withdrawal payload mismatch:\n got=%q\nwant=%q", got, want)
	}
}

// A withdrawal must never verify as a vouch. The distinct type is what
// separates them, so assert the bytes actually differ.
func TestVouchWithdrawalServerPayloadCanonicalShape(t *testing.T) {
	signedAt := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	got := string(buildVouchWithdrawalServerPayload(
		"alice@home1234/0192f0c1-2b3d-7456-89ab-cdef01234567",
		"alice@home1234",
		"bob@peer5678/9f3c",
		"srv-fp",
		"WSIG",
		signedAt,
	))
	want := "---\n" +
		"serverKeyFingerprint: srv-fp\n" +
		"signedAt: " + signedAt.Format(identityRecordTimeFormat) + "\n" +
		"subjectKeyID: bob@peer5678/9f3c\n" +
		"type: user_vouch_withdrawal\n" +
		"vouchID: alice@home1234/0192f0c1-2b3d-7456-89ab-cdef01234567\n" +
		"voucherUserID: alice@home1234\n" +
		"---\nWSIG"
	if got != want {
		t.Errorf("withdrawal server payload mismatch:\ngot  %q\nwant %q", got, want)
	}
}

func TestVouchAndWithdrawalPayloadsDiffer(t *testing.T) {
	vouch := buildVouchUserPayload("alice@home/k1", "bob@peer", "bob@peer/k2", "")
	withdrawal := buildVouchWithdrawalUserPayload("alice@home/k1", "bob@peer", "bob@peer/k2")
	if string(vouch) == string(withdrawal) {
		t.Fatal("vouch and withdrawal payloads are identical; domain separation lost")
	}
	if !strings.Contains(string(withdrawal), identityTypeVouchWithdrawal) {
		t.Errorf("withdrawal payload missing its own type: %q", withdrawal)
	}
}

// The countersignature attests the voucher's signature, which is the
// body. The note is never in these bytes: it is already covered by the
// signature being attested.
func TestVouchServerPayloadExcludesNote(t *testing.T) {
	ts := time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC)
	args := func(sig string) []byte {
		return buildVouchServerPayload(
			"alice@home", "bob@peer", "bob@peer/k2", "SERVERKEY01", sig, ts,
		)
	}
	if string(args("SIG_A")) == string(args("SIG_B")) {
		t.Fatal("server payload ignores the user signature it is meant to bind")
	}
	if strings.Contains(string(args("SIG_A")), "met at the cafe") {
		t.Error("server payload leaked note content")
	}
}
