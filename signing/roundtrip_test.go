package signing_test

import (
	"testing"
	"time"

	"syrinx/crypto"
	"syrinx/signing"
)

// reedCountersignHeaders mirrors the header set that the handler-layer
// helper of the same name produces for a reed countersignature. It is
// redeclared here rather than imported to keep the signing package free
// of any handler-layer dependency; the test's job is to lock down the
// bytes, and repeating the shape here catches an accidental divergence.
func reedCountersignHeaders(serverID, reedID, authorID, fingerprint string, ts time.Time) map[string]string {
	return map[string]string{
		"algorithm":   "PGP+base64",
		"authorID":    authorID,
		"fingerprint": fingerprint,
		"id":          serverID,
		"reedID":      reedID,
		"timestamp":   ts.UTC().Format(time.RFC3339),
	}
}

// TestReedCountersignRoundtrip exercises the reed countersignature header
// set — `algorithm`, `authorID`, `fingerprint`, `id`, `reedID`,
// `timestamp` — with a base64 userSignature as content, signs the
// resulting bytes with a freshly minted key, and verifies them back. This
// is the end-to-end contract: the same bytes are produced on the signing
// and verification paths, so a PGP verify against those bytes succeeds.
func TestReedCountersignRoundtrip(t *testing.T) {
	svc := crypto.NewService()

	kp, err := svc.CreateKeyPair("test", "test@example.com", "test")
	if err != nil {
		t.Fatalf("CreateKeyPair: %v", err)
	}

	ts := time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC)
	headers := reedCountersignHeaders(
		"TestServer01",
		"0k2n1p0000000000000ReedA",
		"AuthorAlice0",
		kp.Fingerprint,
		ts,
	)
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

	// Negative: mutating a header value (swapped serverID) must fail.
	altServer := reedCountersignHeaders(
		"TestServer02",
		headers["reedID"], headers["authorID"], headers["fingerprint"], ts,
	)
	if err := svc.VerifySignature(string(signing.BytesToSign(altServer, content)), sig, kp.PublicKey); err == nil {
		t.Fatal("VerifySignature accepted a payload with a swapped serverID")
	}

	// Negative: cross-reed replay must fail — take the sig produced for
	// reed A and try to verify it as if it were reed B.
	altReed := reedCountersignHeaders(
		headers["id"],
		"0k2n1p0000000000000ReedB",
		headers["authorID"], headers["fingerprint"], ts,
	)
	if err := svc.VerifySignature(string(signing.BytesToSign(altReed, content)), sig, kp.PublicKey); err == nil {
		t.Fatal("VerifySignature accepted a cross-reed replay (reedID swap)")
	}

	// Negative: cross-author replay must fail.
	altAuthor := reedCountersignHeaders(
		headers["id"], headers["reedID"],
		"AuthorMallory",
		headers["fingerprint"], ts,
	)
	if err := svc.VerifySignature(string(signing.BytesToSign(altAuthor, content)), sig, kp.PublicKey); err == nil {
		t.Fatal("VerifySignature accepted a cross-author replay (authorID swap)")
	}

	// Negative: wrong fingerprint in the header must fail — the bytes are
	// what change, so even the same key won't verify against the swapped
	// envelope.
	altFP := reedCountersignHeaders(
		headers["id"], headers["reedID"], headers["authorID"],
		"0000000000000000000000000000000000000000",
		ts,
	)
	if err := svc.VerifySignature(string(signing.BytesToSign(altFP, content)), sig, kp.PublicKey); err == nil {
		t.Fatal("VerifySignature accepted a payload with a swapped fingerprint")
	}
}
