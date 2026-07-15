package main

import (
	"encoding/base64"
	"strings"
	"testing"
	"time"

	"syrinx/crypto"
	"syrinx/signing"
)

// End-to-end tests for the identity record envelope + signing dance.
// These tests do not touch Postgres — they exercise the pure-functional
// layer (signing.BytesToSign + identity.go helpers) and verify
// signatures produced against real PGP keys via crypto.Service.

// newTestKeyPair generates a fresh, unencrypted PGP keypair for a test.
// The identity is arbitrary — we only use the key for detached signing.
func newTestKeyPair(t *testing.T, identity string) (*crypto.KeyPair, *crypto.Service) {
	t.Helper()
	svc := crypto.NewService()
	kp, err := svc.CreateKeyPair(identity, "", "")
	if err != nil {
		t.Fatalf("CreateKeyPair: %v", err)
	}
	return kp, svc
}

// signB64 produces a base64-encoded armored detached PGP signature over
// `payload` using the given private key armor. Matches the wire format
// clients submit.
func signB64(t *testing.T, svc *crypto.Service, privateKeyArmor string, payload []byte) string {
	t.Helper()
	armored, err := svc.Sign(string(payload), privateKeyArmor)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	return base64.StdEncoding.EncodeToString([]byte(armored))
}

// verifyB64 base64-decodes a wire-format signature and verifies it
// against `payload` using the given public key armor.
func verifyB64(t *testing.T, svc *crypto.Service, publicKeyArmor, sigB64 string, payload []byte) error {
	t.Helper()
	sig, err := base64.StdEncoding.DecodeString(sigB64)
	if err != nil {
		t.Fatalf("base64 decode: %v", err)
	}
	return svc.VerifySignature(string(payload), string(sig), publicKeyArmor)
}

// TestUserPayloadCanonicalShape guards the byte-level layout of the
// user-signed payload. If this changes without a coordinated bump of
// the SPA mirror in bytesToSign(), every existing user signature stops
// verifying — hence the strict byte comparison.
func TestUserPayloadCanonicalShape(t *testing.T) {
	got := buildUserIdentityPayload("alice", "ABCDEF", "https://ex.com/a.png", "hello\nworld")
	want := "---\n" +
		"avatarURL: https://ex.com/a.png\n" +
		"fingerprint: ABCDEF\n" +
		"type: identity-user\n" +
		"username: alice\n" +
		"---\n" +
		"hello\nworld"
	if string(got) != want {
		t.Errorf("user payload mismatch:\n got=%q\nwant=%q", got, want)
	}
}

// TestUserPayloadOmitsEmptyAvatar confirms the "absent == empty"
// convention: an empty avatarURL is dropped from the signed bytes
// entirely, so a user who sets an avatarURL later will not invalidate
// their original signup signature (which never covered a placeholder).
func TestUserPayloadOmitsEmptyAvatar(t *testing.T) {
	got := string(buildUserIdentityPayload("alice", "ABCDEF", "", ""))
	if strings.Contains(got, "avatarURL") {
		t.Errorf("empty avatarURL should not appear in signed bytes; got=%q", got)
	}
}

// TestServerPayloadCanonicalShape mirrors the user payload guard for the
// server-signed bytes. In particular this locks in that `userSignature`
// is a header on the server payload — the linchpin that binds the two
// attestations together.
func TestServerPayloadCanonicalShape(t *testing.T) {
	memberSince := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	serverTs := time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC)
	got := buildServerIdentityPayload(
		"abc123", "alice", "ABCDEF", "https://ex.com/a.png",
		"Server01", "0011FF",
		"dXNlcnNpZw==",
		"bio text",
		memberSince, serverTs,
	)
	want := "---\n" +
		"avatarURL: https://ex.com/a.png\n" +
		"fingerprint: ABCDEF\n" +
		"memberSince: 2026-01-01T00:00:00Z\n" +
		"serverID: Server01\n" +
		"serverKeyFingerprint: 0011FF\n" +
		"serverTs: 2026-07-14T12:00:00Z\n" +
		"type: identity-server\n" +
		"userID: abc123\n" +
		"userSignature: dXNlcnNpZw==\n" +
		"username: alice\n" +
		"---\n" +
		"bio text"
	if string(got) != want {
		t.Errorf("server payload mismatch:\n got=%q\nwant=%q", got, want)
	}
}

// TestIdentityRoundTrip is the golden end-to-end test: it exercises the
// full flow the way the Signup handler does, then verifies both
// signatures the way a profile-view client would. If this passes, the
// wire contract is coherent.
func TestIdentityRoundTrip(t *testing.T) {
	userKP, cryptoSvc := newTestKeyPair(t, "alice")
	serverKP, _ := newTestKeyPair(t, "server")

	// Client side: sign the user payload with the user's private key.
	userPayload := buildUserIdentityPayload("alice", userKP.Fingerprint, "", "")
	userSigB64 := signB64(t, cryptoSvc, userKP.PrivateKey, userPayload)

	// Server side: verify userSignature, then produce serverSignature
	// over a payload that binds userSignature as a header.
	if err := verifyB64(t, cryptoSvc, userKP.PublicKey, userSigB64, userPayload); err != nil {
		t.Fatalf("userSignature verify: %v", err)
	}

	memberSince := time.Now().UTC().Truncate(time.Second)
	serverTs := memberSince
	serverPayload := buildServerIdentityPayload(
		"user_abc", "alice", userKP.Fingerprint, "",
		"srv_xyz", serverKP.Fingerprint,
		userSigB64,
		"",
		memberSince, serverTs,
	)
	serverSigB64 := signB64(t, cryptoSvc, serverKP.PrivateKey, serverPayload)

	// Profile-view client: rebuild both payloads from the observable
	// fields and re-verify both signatures.
	rebuiltUser := buildUserIdentityPayload("alice", userKP.Fingerprint, "", "")
	if err := verifyB64(t, cryptoSvc, userKP.PublicKey, userSigB64, rebuiltUser); err != nil {
		t.Errorf("rebuilt userSignature verify: %v", err)
	}
	rebuiltServer := buildServerIdentityPayload(
		"user_abc", "alice", userKP.Fingerprint, "",
		"srv_xyz", serverKP.Fingerprint,
		userSigB64,
		"",
		memberSince, serverTs,
	)
	if err := verifyB64(t, cryptoSvc, serverKP.PublicKey, serverSigB64, rebuiltServer); err != nil {
		t.Errorf("rebuilt serverSignature verify: %v", err)
	}
}

// TestServerPayloadBindsUserSignature enforces the property that makes
// the whole design work: swap userSignature in the response and
// serverSignature must stop verifying. If this test ever passes with
// the swap succeeding, a compromised server can re-pair a genuine
// userSignature with fabricated server-authored fields.
func TestServerPayloadBindsUserSignature(t *testing.T) {
	userKP, cryptoSvc := newTestKeyPair(t, "alice")
	serverKP, _ := newTestKeyPair(t, "server")

	// Produce two DIFFERENT userSignatures over two DIFFERENT user
	// payloads (different usernames). Both are valid on their own.
	payloadAlice := buildUserIdentityPayload("alice", userKP.Fingerprint, "", "")
	sigAlice := signB64(t, cryptoSvc, userKP.PrivateKey, payloadAlice)
	payloadBob := buildUserIdentityPayload("bob", userKP.Fingerprint, "", "")
	sigBob := signB64(t, cryptoSvc, userKP.PrivateKey, payloadBob)

	ts := time.Now().UTC().Truncate(time.Second)
	// Server signs a record binding sigAlice.
	serverPayload := buildServerIdentityPayload(
		"user_abc", "alice", userKP.Fingerprint, "",
		"srv_xyz", serverKP.Fingerprint,
		sigAlice,
		"",
		ts, ts,
	)
	serverSig := signB64(t, cryptoSvc, serverKP.PrivateKey, serverPayload)

	// Attacker rebuilds the server payload with sigBob substituted.
	tamperedPayload := buildServerIdentityPayload(
		"user_abc", "alice", userKP.Fingerprint, "",
		"srv_xyz", serverKP.Fingerprint,
		sigBob,
		"",
		ts, ts,
	)
	if err := verifyB64(t, cryptoSvc, serverKP.PublicKey, serverSig, tamperedPayload); err == nil {
		t.Fatal("swapping userSignature must break serverSignature verification, but it verified")
	}
}

// TestTamperedUsernameBreaksUserSignature confirms the user's signature
// covers `username`. A server that rewrites a user's username in the
// response must produce a verification failure at the profile viewer.
func TestTamperedUsernameBreaksUserSignature(t *testing.T) {
	userKP, cryptoSvc := newTestKeyPair(t, "alice")

	payload := buildUserIdentityPayload("alice", userKP.Fingerprint, "", "")
	sig := signB64(t, cryptoSvc, userKP.PrivateKey, payload)

	tampered := buildUserIdentityPayload("eve", userKP.Fingerprint, "", "")
	if err := verifyB64(t, cryptoSvc, userKP.PublicKey, sig, tampered); err == nil {
		t.Fatal("rewriting username must break userSignature, but it verified")
	}
}

// TestTamperedFingerprintBreaksUserSignature confirms the user's
// signature covers `fingerprint`. A server that swaps the fingerprint
// in the profile response (to point at an attacker-controlled key)
// must produce a verification failure at the profile viewer.
func TestTamperedFingerprintBreaksUserSignature(t *testing.T) {
	userKP, cryptoSvc := newTestKeyPair(t, "alice")

	payload := buildUserIdentityPayload("alice", userKP.Fingerprint, "", "")
	sig := signB64(t, cryptoSvc, userKP.PrivateKey, payload)

	tampered := buildUserIdentityPayload("alice", "DEADBEEF", "", "")
	if err := verifyB64(t, cryptoSvc, userKP.PublicKey, sig, tampered); err == nil {
		t.Fatal("rewriting fingerprint must break userSignature, but it verified")
	}
}

// TestUserPayloadRejectsCrossTypeConfusion confirms that the `type`
// header discriminates the two envelopes. A user-signed payload can
// never be validly interpreted as a server payload (or vice versa),
// because the very first non-boilerplate header line differs.
func TestUserPayloadRejectsCrossTypeConfusion(t *testing.T) {
	userKP, cryptoSvc := newTestKeyPair(t, "alice")

	userPayload := buildUserIdentityPayload("alice", userKP.Fingerprint, "", "")
	userSig := signB64(t, cryptoSvc, userKP.PrivateKey, userPayload)

	// Attempt to reuse the user signature as if it were a server signature
	// over a truncated server payload (same headers minus the ones only
	// on the server side). The bytes cannot match because `type` differs
	// (identity-user vs identity-server).
	fakeServerPayload := signing.BytesToSign(map[string]string{
		"type":        "identity-server",
		"username":    "alice",
		"fingerprint": userKP.Fingerprint,
	}, "")
	if err := verifyB64(t, cryptoSvc, userKP.PublicKey, userSig, fakeServerPayload); err == nil {
		t.Fatal("user signature must not verify against a server-typed payload")
	}
}
