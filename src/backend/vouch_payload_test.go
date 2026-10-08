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
		"bob@peer5678/9f3c",
		"met at the cafe",
	)
	want := `{"note":"met at the cafe","subjectKeyID":"bob@peer5678/9f3c","voucherKeyID":"alice@home1234/4a1e"}`
	if string(got) != want {
		t.Errorf("vouch user payload mismatch:\n got=%q\nwant=%q", got, want)
	}
}

// An empty note is left out of the payload entirely.
func TestVouchUserPayloadEmptyNote(t *testing.T) {
	got := buildVouchUserPayload("alice@home/k1", "bob@peer/k2", "")
	want := `{"subjectKeyID":"bob@peer/k2","voucherKeyID":"alice@home/k1"}`
	if string(got) != want {
		t.Errorf("noteless vouch payload mismatch:\n got=%q\nwant=%q", got, want)
	}
}

func TestVouchServerPayloadCanonicalShape(t *testing.T) {
	ts := time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC)
	got := buildVouchServerPayload(
		"bob@peer5678/9f3c",
		"SERVERKEY01",
		"USERSIG",
		ts,
	)
	want := `{"serverKeyFingerprint":"SERVERKEY01","signedAt":"2026-09-25T12:00:00Z",` +
		`"subjectKeyID":"bob@peer5678/9f3c","userSignature":"USERSIG"}`
	if string(got) != want {
		t.Errorf("vouch server payload mismatch:\n got=%q\nwant=%q", got, want)
	}
}

func TestVouchWithdrawalPayloadCanonicalShape(t *testing.T) {
	got := buildVouchWithdrawalUserPayload(
		"alice@home1234/0192f0c1-2b3d-7456-89ab-cdef01234567",
	)
	want := `{"vouchID":"alice@home1234/0192f0c1-2b3d-7456-89ab-cdef01234567"}`
	if string(got) != want {
		t.Errorf("withdrawal payload mismatch:\n got=%q\nwant=%q", got, want)
	}
}

func TestVouchWithdrawalServerPayloadCanonicalShape(t *testing.T) {
	signedAt := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	got := string(buildVouchWithdrawalServerPayload(
		"alice@home1234/0192f0c1-2b3d-7456-89ab-cdef01234567",
		"srv-fp",
		"WSIG",
		signedAt,
	))
	want := `{"serverKeyFingerprint":"srv-fp","signedAt":"2026-03-01T12:00:00Z",` +
		`"userSignature":"WSIG","vouchID":"alice@home1234/0192f0c1-2b3d-7456-89ab-cdef01234567"}`
	if got != want {
		t.Errorf("withdrawal server payload mismatch:\ngot  %q\nwant %q", got, want)
	}
}

// A withdrawal must never verify as a vouch. With no type discriminator,
// the field sets are what separate them: a vouch signs key ids, a
// withdrawal signs a vouch id.
func TestVouchAndWithdrawalPayloadsDiffer(t *testing.T) {
	vouch := buildVouchUserPayload("alice@home/k1", "bob@peer/k2", "")
	withdrawal := buildVouchWithdrawalUserPayload("alice@home/0192f0c1-2b3d-7456-89ab-cdef01234567")
	if string(vouch) == string(withdrawal) {
		t.Fatal("vouch and withdrawal payloads are identical; domain separation lost")
	}
	if strings.Contains(string(withdrawal), "voucherKeyID") ||
		strings.Contains(string(withdrawal), "subjectKeyID") {
		t.Errorf("withdrawal payload should name only the vouch: %q", withdrawal)
	}
	if strings.Contains(string(vouch), "vouchID") {
		t.Errorf("vouch payload should not carry a vouch id: %q", vouch)
	}
}

// Re-vouching is allowed, so two vouches for the same subject key must
// get distinct withdrawal payloads — otherwise one signature would
// retract either.
func TestWithdrawalPayloadsDistinguishRevouches(t *testing.T) {
	first := buildVouchWithdrawalUserPayload("alice@home/0192f0c1-2b3d-7456-89ab-cdef01234567")
	second := buildVouchWithdrawalUserPayload("alice@home/0192f0c1-2b3d-7456-89ab-cdef01234568")
	if string(first) == string(second) {
		t.Fatal("withdrawals for different vouches produced identical bytes")
	}
}

// The countersignature attests the voucher's signature; the note is never
// in these bytes, since that signature already covers it.
func TestVouchServerPayloadExcludesNote(t *testing.T) {
	ts := time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC)
	args := func(sig string) []byte {
		return buildVouchServerPayload("bob@peer/k2", "SERVERKEY01", sig, ts)
	}
	if string(args("SIG_A")) == string(args("SIG_B")) {
		t.Fatal("server payload ignores the user signature it is meant to bind")
	}
	if strings.Contains(string(args("SIG_A")), "met at the cafe") {
		t.Error("server payload leaked note content")
	}
}
