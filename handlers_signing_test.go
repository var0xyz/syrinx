package main

import (
	"testing"
	"time"

	"syrinx/identity"
	"syrinx/signing"
)

func TestPublicKeyCountersignCanonicalShape(t *testing.T) {
	ts := time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC)
	armor := "-----BEGIN PGP PUBLIC KEY BLOCK-----\nxyz\n-----END PGP PUBLIC KEY BLOCK-----"
	got := signing.BytesToSign(
		identity.PublicKeyCountersignHeaders("userABC", "FINGERPRINT01", "Server01", "SERVERKEY01", ts),
		armor,
	)
	want := "---\n" +
		"fingerprint: FINGERPRINT01\n" +
		"serverID: Server01\n" +
		"serverKeyFingerprint: SERVERKEY01\n" +
		"signedAt: 2026-07-16T12:00:00Z\n" +
		"userID: userABC\n" +
		"---\n" +
		armor
	if string(got) != want {
		t.Errorf("public key countersign payload mismatch:\n got=%q\nwant=%q", got, want)
	}
}
