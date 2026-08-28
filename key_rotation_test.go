package main

import (
	"context"
	"fmt"
	"testing"
	"time"

	"syrinx/roles"
)

// seedPublicKey inserts a minimal public_keys row directly (not via
// AddPublicKey, which is the function under test) so a test can seed a
// predecessor key to rotate away from.
func seedPublicKey(t *testing.T, ds *DataService, id, owner string) {
	t.Helper()
	var serverSigID int64
	if err := ds.db.QueryRow(
		`INSERT INTO server_signatures (fingerprint, signature, signed_at) VALUES ($1, 'sig', now()) RETURNING id`,
		"seed-"+id,
	).Scan(&serverSigID); err != nil {
		t.Fatal(err)
	}
	if _, err := ds.db.Exec(
		`INSERT INTO public_keys (id, owner, armor, created_at, server_signature_id) VALUES ($1, $2, 'armor', now(), $3)`,
		id, owner, serverSigID,
	); err != nil {
		t.Fatal(err)
	}
}

// TestAddPublicKey_RotatesAtomically verifies a single AddPublicKey call
// both revokes the predecessor and registers the successor in one
// transaction — there is no intermediate state where the account has no
// valid key at all (the bug this replaced a separate RevokeKey call for).
func TestAddPublicKey_RotatesAtomically(t *testing.T) {
	_, ds, _, _ := testFederationHandlers(t)
	user := seedFederationUser(t, ds, "rotuser1", "rotuser1", roles.RoleUser)
	oldKeyID := user + "/oldkey1"
	seedPublicKey(t, ds, oldKeyID, user)

	newKeyID := user + "/newkey1"
	_, err := ds.AddPublicKey(context.Background(), AddPublicKeyInput{
		ID:        newKeyID,
		UserID:    user,
		CreatedAt: time.Now().UTC(),
		Armor:     "new-armor",
		Server:    ServerSignature{Fingerprint: "new-key-sfp", Armor: "s", SignedAt: time.Now().UTC()},

		PredecessorID:        oldKeyID,
		PredecessorSignature: "predecessor-signs-new-armor",

		RevocationReason:        "rotating",
		RevocationUserSignature: "old-key-signs-revocation",
		RevocationServer:        ServerSignature{Fingerprint: "rev-sfp", Armor: "s", SignedAt: time.Now().UTC()},
	})
	if err != nil {
		t.Fatalf("AddPublicKey: %v", err)
	}

	newKey, err := ds.GetPublicKey(context.Background(), newKeyID)
	if err != nil || newKey == nil {
		t.Fatalf("new key not found: key=%v err=%v", newKey, err)
	}
	if newKey.Revoked {
		t.Fatal("newly-added key should not be revoked")
	}

	oldKey, err := ds.GetPublicKey(context.Background(), oldKeyID)
	if err != nil || oldKey == nil {
		t.Fatalf("old key not found: key=%v err=%v", oldKey, err)
	}
	if !oldKey.Revoked {
		t.Fatal("predecessor key should be revoked by the same AddPublicKey call that registered its successor")
	}
}

// TestAddPublicKey_DoubleRotationRejected verifies a predecessor can only
// be replaced once — rotating against the same already-revoked-with-a-
// successor key is rejected, same as before the revoke+add merge.
func TestAddPublicKey_DoubleRotationRejected(t *testing.T) {
	_, ds, _, _ := testFederationHandlers(t)
	user := seedFederationUser(t, ds, "rotuser2", "rotuser2", roles.RoleUser)
	oldKeyID := user + "/oldkey2"
	seedPublicKey(t, ds, oldKeyID, user)

	firstNewKeyID := user + "/newkey2a"
	if _, err := ds.AddPublicKey(context.Background(), AddPublicKeyInput{
		ID:                      firstNewKeyID,
		UserID:                  user,
		CreatedAt:               time.Now().UTC(),
		Armor:                   "new-armor-a",
		Server:                  ServerSignature{Fingerprint: "new-key-sfp-a", Armor: "s", SignedAt: time.Now().UTC()},
		PredecessorID:           oldKeyID,
		PredecessorSignature:    "predecessor-signs-new-armor-a",
		RevocationReason:        "rotating",
		RevocationUserSignature: "old-key-signs-revocation",
		RevocationServer:        ServerSignature{Fingerprint: "rev-sfp", Armor: "s", SignedAt: time.Now().UTC()},
	}); err != nil {
		t.Fatalf("first rotation: %v", err)
	}

	// firstNewKeyID has no active key relationship required for this
	// second call's predecessor check — a fresh predecessor pointing at
	// the ALREADY-ROTATED oldKeyID must be rejected.
	_, err := ds.AddPublicKey(context.Background(), AddPublicKeyInput{
		ID:                      fmt.Sprintf("%s/newkey2b", user),
		UserID:                  user,
		CreatedAt:               time.Now().UTC(),
		Armor:                   "new-armor-b",
		Server:                  ServerSignature{Fingerprint: "new-key-sfp-b", Armor: "s", SignedAt: time.Now().UTC()},
		PredecessorID:           oldKeyID,
		PredecessorSignature:    "predecessor-signs-new-armor-b",
		RevocationReason:        "rotating again",
		RevocationUserSignature: "old-key-signs-revocation-again",
		RevocationServer:        ServerSignature{Fingerprint: "rev-sfp-2", Armor: "s", SignedAt: time.Now().UTC()},
	})
	if err != ErrPredecessorAlreadyReplaced {
		t.Fatalf("want ErrPredecessorAlreadyReplaced, got %v", err)
	}
}
