package signing_test

import (
	"testing"
	"time"

	"syrinx/crypto"
	"syrinx/signing"
)

// TestReedCountersignRoundtrip exercises the exact shape of headers
// SignReed uses (algorithm/id/timestamp) plus a base64 userSignature as
// content, signs the resulting bytes with a freshly minted key, and
// verifies them back. This is the end-to-end contract Proposal 01 restores:
// the same bytes are produced on the signing and verification paths, so a
// PGP verify against those bytes succeeds.
func TestReedCountersignRoundtrip(t *testing.T) {
	svc := crypto.NewService()

	kp, err := svc.CreateKeyPair("test", "test@example.com", "test")
	if err != nil {
		t.Fatalf("CreateKeyPair: %v", err)
	}

	headers := map[string]string{
		"algorithm": "PGP+base64",
		"id":        "TestServer01",
		"timestamp": time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC).Format(time.RFC3339),
	}
	// A plausible userSignature payload: any base64-looking string will do
	// here; the countersignature is over the bytes, not their meaning.
	content := "dXNlci1zaWctYnl0ZXM="

	payload := signing.BytesToSign(headers, content)

	sig, err := svc.Sign(string(payload), kp.PrivateKey)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}

	// Positive: verifying the exact same bytes must succeed.
	if err := svc.VerifySignature(string(payload), sig, kp.PublicKey); err != nil {
		t.Fatalf("VerifySignature (positive) failed: %v", err)
	}

	// Negative: mutating one byte of the payload must fail verification.
	mutated := append([]byte(nil), payload...)
	mutated[len(mutated)-1] ^= 0x01
	if err := svc.VerifySignature(string(mutated), sig, kp.PublicKey); err == nil {
		t.Fatal("VerifySignature accepted a mutated payload")
	}

	// Negative: mutating a header value (via a fresh BytesToSign call with a
	// different serverID) must fail verification.
	altHeaders := map[string]string{
		"algorithm": "PGP+base64",
		"id":        "TestServer02",
		"timestamp": headers["timestamp"],
	}
	altPayload := signing.BytesToSign(altHeaders, content)
	if err := svc.VerifySignature(string(altPayload), sig, kp.PublicKey); err == nil {
		t.Fatal("VerifySignature accepted a payload with a swapped serverID")
	}
}
