//go:build !ops

package main

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"
)

// rebind has a1 open and a2 approve a rebind of target to a freshly minted
// key, and returns that key.
func (f *adminFixture) rebind(t *testing.T, target string) cryptoKeyPair {
	t.Helper()
	kp, err := f.h.services.crypto.createKeyPair("rebound", "", "")
	if err != nil {
		t.Fatal(err)
	}
	msg := f.request(t, "a1", adminActionRebind, "Lost their phone, checked in person", kp.PublicKey, target)
	f.openAs(t, "a1", msg)
	if rr := f.decide(t, "a2", msg.Id, adminDecisionApprove); rr.Code != http.StatusOK {
		t.Fatalf("approve rebind: %d %s", rr.Code, rr.Body.String())
	}
	return *kp
}

func TestRebind_RevokesLostKeyAndBindsRestrictedKey(t *testing.T) {
	f := newAdminFixture(t)
	f.useRealExecutors(adminActionRebind)
	f.grant(t, "a1", permUserRebind)
	f.grant(t, "a2", permUserRebind)
	ctx := context.Background()
	oldKeyID := f.keyID("u1")

	kp := f.rebind(t, "u1")
	newKeyID := string(appendEntity(identityID(f.ids["u1"]), kp.Fingerprint))

	rev, err := f.ds.GetKeyRevocation(ctx, oldKeyID)
	if err != nil || rev == nil {
		t.Fatalf("revocation = %v %v", rev, err)
	}
	if rev.Admin == nil || rev.Admin.Action != adminActionRebind || rev.Reason != keyRevocationReasonLost ||
		rev.Successor == nil || *rev.Successor != newKeyID || rev.UserSignature.Armor != "" {
		t.Fatalf("revocation = %+v, want an admin revocation as lost with the new key as successor", rev)
	}
	payload := buildAdminRevocationServerPayload(f.ids["u1"], oldKeyID, rev.Admin.ID, rev.Admin.RequesterSignature.Armor,
		rev.Admin.Decision.Signature.Armor, f.srv, f.h.signingKey.Fingerprint, rev.ServerSignature.SignedAt)
	if err := f.h.services.crypto.verifySignature(string(payload), rev.ServerSignature.Armor, f.h.signingKey.Armor); err != nil {
		t.Fatalf("admin revocation countersignature: %v", err)
	}
	if pbRev := pbKeyRevocation(rev); pbRev.GetAdmin().GetRequest().GetId() != rev.Admin.ID || pbRev.UserSignature != nil {
		t.Fatalf("wire revocation = %+v, want the admin request in place of a user signature", pbRev)
	}

	key, err := f.ds.GetPublicKey(ctx, newKeyID)
	if err != nil || key == nil || !key.Restricted || key.Revoked {
		t.Fatalf("new key = %+v err=%v, want restricted and active", key, err)
	}
	var active string
	if err := f.db.QueryRow(`SELECT active_key_id FROM users WHERE id = $1`, f.ids["u1"]).Scan(&active); err != nil || active != newKeyID {
		t.Fatalf("active key = %q err=%v, want %q", active, err, newKeyID)
	}

	entries := f.ledger(t)
	if len(entries) == 0 || !strings.Contains(entries[0].Message, "Key rebind to "+newKeyID+". Reason: Lost their phone") {
		t.Fatalf("opened entry = %+v, want the new key ID in it", entries)
	}

	f.keys["u1"] = kp
	if rr := f.serve(t, f.h.GetLedger, http.MethodGet, "/api/ledger", "u1", nil, nil); rr.Code != http.StatusForbidden ||
		!strings.Contains(rr.Body.String(), "restricted-key") {
		t.Fatalf("request signed by the restricted key: %d %q, want 403 restricted-key", rr.Code, rr.Body.String())
	}
}

func TestRebind_RestrictedKeyRotatesWithoutCooldown(t *testing.T) {
	f := newAdminFixture(t)
	f.useRealExecutors(adminActionRebind)
	f.grant(t, "a1", permUserRebind)
	f.grant(t, "a2", permUserRebind)
	ctx := context.Background()
	kp := f.rebind(t, "u1")
	restrictedID := string(appendEntity(identityID(f.ids["u1"]), kp.Fingerprint))

	freshID := f.ids["u1"] + "/fresh"
	now := time.Now().UTC()
	_, err := f.ds.AddPublicKey(ctx, AddPublicKeyInput{
		ID:                      freshID,
		UserID:                  f.ids["u1"],
		CreatedAt:               now,
		Armor:                   "fresh-armor",
		Server:                  ServerSignature{ID: string(canonicalID(f.srv, "sfp")), Armor: "s", SignedAt: now},
		PredecessorID:           restrictedID,
		PredecessorSignature:    "restricted-signs-fresh",
		RevocationReason:        "rotated",
		RevocationUserSignature: "restricted-revokes-itself",
		RevocationServer:        ServerSignature{ID: string(canonicalID(f.srv, "sfp")), Armor: "s", SignedAt: now},
	})
	if err != nil {
		t.Fatalf("rotating away from the restricted key: %v", err)
	}
	fresh, err := f.ds.GetPublicKey(ctx, freshID)
	if err != nil || fresh == nil || fresh.Restricted || fresh.Revoked {
		t.Fatalf("fresh key = %+v err=%v, want unrestricted and active", fresh, err)
	}
}

func TestRebind_Validation(t *testing.T) {
	f := newAdminFixture(t)
	f.useRealExecutors(adminActionRebind, adminActionSuspend)
	f.grant(t, "a1", permUserRebind)

	garbage := f.request(t, "a1", adminActionRebind, "Lost phone", "not a key", "u1")
	if rr := f.serve(t, f.h.OpenAdminRequest, http.MethodPost, "/api/admin/requests", "a1", garbage, nil); rr.Code != http.StatusBadRequest {
		t.Errorf("unparseable key: %d, want 400", rr.Code)
	}
	f.suspend(t, "u2")
	kp, err := f.h.services.crypto.createKeyPair("rebound", "", "")
	if err != nil {
		t.Fatal(err)
	}
	suspended := f.request(t, "a1", adminActionRebind, "Lost phone", kp.PublicKey, "u2")
	if rr := f.serve(t, f.h.OpenAdminRequest, http.MethodPost, "/api/admin/requests", "a1", suspended, nil); rr.Code != http.StatusConflict {
		t.Errorf("suspended target: %d, want 409", rr.Code)
	}
}
