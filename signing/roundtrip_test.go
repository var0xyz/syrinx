package signing_test

import (
	"testing"
	"time"

	"syrinx/crypto"
	"syrinx/identity"
)

// TestReedCountersignRoundtrip exercises the reed countersignature header
// set with a base64 userSignature as content, signs the resulting bytes
// with a freshly minted key, and verifies them back. This is the
// end-to-end contract: the same bytes are produced on the signing and
// verification paths, so a PGP verify against those bytes succeeds.
func TestReedCountersignRoundtrip(t *testing.T) {
	svc := crypto.NewService()

	kp, err := svc.CreateKeyPair("test", "test@example.com", "test")
	if err != nil {
		t.Fatalf("CreateKeyPair: %v", err)
	}

	ts := time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC)
	serverID := "TestServer01"
	authorID := "AuthorAlice0"
	reedID := authorID + "@" + serverID + "/0k2n1p0000000000000ReedA"
	content := "dXNlci1zaWctYnl0ZXM="

	payload := identity.BuildReedPayload(
		serverID, reedID, kp.Fingerprint, content, ts,
	)

	sig, err := svc.Sign(string(payload), kp.PrivateKey)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}

	if err := svc.VerifySignature(string(payload), sig, kp.PublicKey); err != nil {
		t.Fatalf("VerifySignature (positive) failed: %v", err)
	}

	mutated := append([]byte(nil), payload...)
	mutated[len(mutated)-1] ^= 0x01
	if err := svc.VerifySignature(string(mutated), sig, kp.PublicKey); err == nil {
		t.Fatal("VerifySignature accepted a mutated payload")
	}

	altServer := identity.BuildReedPayload(
		"TestServer02", reedID, kp.Fingerprint, content, ts,
	)
	if err := svc.VerifySignature(string(altServer), sig, kp.PublicKey); err == nil {
		t.Fatal("VerifySignature accepted a payload with a swapped serverID")
	}

	altReed := identity.BuildReedPayload(
		serverID, authorID+"@"+serverID+"/0k2n1p0000000000000ReedB", kp.Fingerprint, content, ts,
	)
	if err := svc.VerifySignature(string(altReed), sig, kp.PublicKey); err == nil {
		t.Fatal("VerifySignature accepted a cross-reed replay (reedID swap)")
	}

	altAuthor := identity.BuildReedPayload(
		serverID, "AuthorMallory@"+serverID+"/0k2n1p0000000000000ReedA", kp.Fingerprint, content, ts,
	)
	if err := svc.VerifySignature(string(altAuthor), sig, kp.PublicKey); err == nil {
		t.Fatal("VerifySignature accepted a cross-author replay (authorID swap)")
	}

	altFP := identity.BuildReedPayload(
		serverID, reedID, "0000000000000000000000000000000000000000", content, ts,
	)
	if err := svc.VerifySignature(string(altFP), sig, kp.PublicKey); err == nil {
		t.Fatal("VerifySignature accepted a payload with a swapped fingerprint")
	}
}
