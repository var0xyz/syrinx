//go:build !ops

package main

import (
	"testing"
	"time"
)

func TestPublicKeyCountersignCanonicalShape(t *testing.T) {
	ts := time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC)
	armor := "-----BEGIN PGP PUBLIC KEY BLOCK-----\nxyz\n-----END PGP PUBLIC KEY BLOCK-----"
	got := buildPublicKeyPayload("Server01", "userABC", "FINGERPRINT01", "SERVERKEY01", armor, ts)
	want := `{"armor":"-----BEGIN PGP PUBLIC KEY BLOCK-----\nxyz\n-----END PGP PUBLIC KEY BLOCK-----",` +
		`"keyID":"FINGERPRINT01","serverID":"Server01","serverKeyFingerprint":"SERVERKEY01",` +
		`"signedAt":"2026-07-16T12:00:00Z","userID":"userABC"}`
	if string(got) != want {
		t.Errorf("public key countersign payload mismatch:\n got=%s\nwant=%s", got, want)
	}
}

func TestRealtimeAuthPayloadShape(t *testing.T) {
	got := buildRealtimeAuthPayload("Server01", "alice@Server01", "1767225600")
	want := `{"serverID":"Server01","timestamp":"1767225600","type":"realtime-auth","userID":"alice@Server01"}`
	if string(got) != want {
		t.Errorf("realtime auth payload mismatch:\n got=%s\nwant=%s", got, want)
	}
}

func TestReedCountersignPayloadShape(t *testing.T) {
	ts := time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC)
	got := buildReedPayload("Server01", "alice@Server01/reed1", "SERVERKEY01", "alice@Server01/KEY01", "USERSIG", ts)
	want := `{"authorKeyID":"alice@Server01/KEY01","fingerprint":"SERVERKEY01","reedID":"alice@Server01/reed1",` +
		`"serverID":"Server01","timestamp":"2026-07-16T12:00:00Z","userSignature":"USERSIG"}`
	if string(got) != want {
		t.Errorf("reed countersign payload mismatch:\n got=%s\nwant=%s", got, want)
	}
}

// Golden bytes, asserted by the SPA's test:removal-payload too.
func TestReedRemovalServerPayloadCanonicalShape(t *testing.T) {
	got := buildReedRemovalServerPayload(
		"home", "a@home/r0", "a@home/k1", "SERVERKEY01", "SIG",
		time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC),
	)
	want := `{"authorKeyID":"a@home/k1","reedID":"a@home/r0","serverID":"home","serverKeyFingerprint":"SERVERKEY01",` +
		`"signedAt":"2026-10-07T12:00:00Z","type":"reed","userSignature":"SIG"}`
	if string(got) != want {
		t.Errorf("reed removal server payload mismatch:\n got=%s\nwant=%s", got, want)
	}
}

// Armor goes in as-is: JSON escapes its newlines, so no base64 wrapping.
func TestProfilePayloadEmbedsArmorVerbatim(t *testing.T) {
	ts := time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC)
	got := buildProfilePayload("alice@home", "alice", "alice@home/k1", "home", "SERVERKEY01",
		"-----BEGIN PGP SIGNATURE-----\n\nabc\n-----END PGP SIGNATURE-----\n", "", "user", "line 1\nline \"2\"", ts, ts)
	want := `{"bio":"line 1\nline \"2\"","keyID":"alice@home/k1","memberSince":"2026-07-16T12:00:00Z","role":"user",` +
		`"serverID":"home","serverKeyFingerprint":"SERVERKEY01","signedAt":"2026-07-16T12:00:00Z",` +
		`"type":"identity-server","userID":"alice@home",` +
		`"userSignature":"-----BEGIN PGP SIGNATURE-----\n\nabc\n-----END PGP SIGNATURE-----\n","username":"alice"}`
	if string(got) != want {
		t.Errorf("profile payload mismatch:\n got=%s\nwant=%s", got, want)
	}
}
