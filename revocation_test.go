//go:build !ops

package main

import (
	"testing"
	"time"
)

func TestUserRevocationPayloadCanonicalShape(t *testing.T) {
	got := buildUserRevocationPayload("user_abc", "FINGER01", "compromised")
	want := "---\n" +
		"fingerprint: FINGER01\n" +
		"type: revocation\n" +
		"userID: user_abc\n" +
		"---\n" +
		"compromised"
	if string(got) != want {
		t.Errorf("user revocation payload mismatch:\n got=%q\nwant=%q", got, want)
	}
}

func TestUserRevocationPayloadEmptyReason(t *testing.T) {
	got := string(buildUserRevocationPayload("user_abc", "FINGER01", ""))
	if got != "---\nfingerprint: FINGER01\ntype: revocation\nuserID: user_abc\n---\n" {
		t.Errorf("empty reason payload mismatch: got=%q", got)
	}
}

func TestServerRevocationPayloadCanonicalShape(t *testing.T) {
	signedAt := time.Date(2026, 7, 17, 10, 0, 0, 0, time.UTC)
	got := buildServerRevocationPayload(
		"user_abc", "FINGER01", "compromised",
		"srv_xyz", "SERVER01", "dXNlcnNpZw==",
		signedAt,
	)
	want := "---\n" +
		"fingerprint: FINGER01\n" +
		"serverID: srv_xyz\n" +
		"serverKeyFingerprint: SERVER01\n" +
		"signedAt: 2026-07-17T10:00:00Z\n" +
		"type: revocation\n" +
		"userID: user_abc\n" +
		"userSignature: dXNlcnNpZw==\n" +
		"---\n" +
		"compromised"
	if string(got) != want {
		t.Errorf("server revocation payload mismatch:\n got=%q\nwant=%q", got, want)
	}
}

func TestRevocationRoundTrip(t *testing.T) {
	userKP, cryptoSvc := newTestKeyPair(t, "alice")
	serverKP, _ := newTestKeyPair(t, "server")

	reason := "rotated"
	userPayload := buildUserRevocationPayload("user_abc", userKP.Fingerprint, reason)
	userSigB64 := signB64(t, cryptoSvc, userKP.PrivateKey, userPayload)

	if err := verifyB64(t, cryptoSvc, userKP.PublicKey, userSigB64, userPayload); err != nil {
		t.Fatalf("userSignature verify: %v", err)
	}

	signedAt := time.Now().UTC().Truncate(time.Second)
	serverPayload := buildServerRevocationPayload(
		"user_abc", userKP.Fingerprint, reason,
		"srv_xyz", serverKP.Fingerprint,
		userSigB64, signedAt,
	)
	serverSigB64 := signB64(t, cryptoSvc, serverKP.PrivateKey, serverPayload)

	rebuiltUser := buildUserRevocationPayload("user_abc", userKP.Fingerprint, reason)
	if err := verifyB64(t, cryptoSvc, userKP.PublicKey, userSigB64, rebuiltUser); err != nil {
		t.Errorf("rebuilt userSignature verify: %v", err)
	}
	rebuiltServer := buildServerRevocationPayload(
		"user_abc", userKP.Fingerprint, reason,
		"srv_xyz", serverKP.Fingerprint,
		userSigB64, signedAt,
	)
	if err := verifyB64(t, cryptoSvc, serverKP.PublicKey, serverSigB64, rebuiltServer); err != nil {
		t.Errorf("rebuilt serverSignature verify: %v", err)
	}
}

func TestServerRevocationBindsUserSignature(t *testing.T) {
	userKP, cryptoSvc := newTestKeyPair(t, "alice")
	serverKP, _ := newTestKeyPair(t, "server")

	sigA := signB64(t, cryptoSvc, userKP.PrivateKey,
		buildUserRevocationPayload("user_abc", userKP.Fingerprint, "reason A"))
	sigB := signB64(t, cryptoSvc, userKP.PrivateKey,
		buildUserRevocationPayload("user_abc", userKP.Fingerprint, "reason B"))

	ts := time.Now().UTC().Truncate(time.Second)
	serverPayload := buildServerRevocationPayload(
		"user_abc", userKP.Fingerprint, "reason A",
		"srv_xyz", serverKP.Fingerprint, sigA, ts,
	)
	serverSig := signB64(t, cryptoSvc, serverKP.PrivateKey, serverPayload)

	tampered := buildServerRevocationPayload(
		"user_abc", userKP.Fingerprint, "reason A",
		"srv_xyz", serverKP.Fingerprint, sigB, ts,
	)
	if err := verifyB64(t, cryptoSvc, serverKP.PublicKey, serverSig, tampered); err == nil {
		t.Fatal("swapping userSignature must break serverSignature verification")
	}
}

func TestTamperedReasonBreaksUserRevocationSignature(t *testing.T) {
	userKP, cryptoSvc := newTestKeyPair(t, "alice")

	payload := buildUserRevocationPayload("user_abc", userKP.Fingerprint, "original")
	sig := signB64(t, cryptoSvc, userKP.PrivateKey, payload)

	tampered := buildUserRevocationPayload("user_abc", userKP.Fingerprint, "tampered")
	if err := verifyB64(t, cryptoSvc, userKP.PublicKey, sig, tampered); err == nil {
		t.Fatal("rewriting reason must break userSignature")
	}
}
