package main

import (
	"context"
	"testing"
	"time"
)

// TestUpdateUser_SwapsSignatures checks the profile edit persists the new
// fields and signatures and deletes the superseded signature rows.
func TestUpdateUser_SwapsSignatures(t *testing.T) {
	_, ds, _, _ := testFederationHandlers(t)
	ctx := context.Background()
	user := seedFederationUser(t, ds, "edituser1", "edituser1", roleUser)
	keyID := user + "/key1"
	seedPublicKey(t, ds, keyID, user, time.Now())
	if _, err := ds.db.Exec(`UPDATE users SET active_key_id = $1 WHERE id = $2`, keyID, user); err != nil {
		t.Fatal(err)
	}

	key, err := ds.GetActiveKey(ctx, user)
	if err != nil {
		t.Fatal(err)
	}
	if key == nil || key.ID != keyID || key.Armor != "armor" || key.Revoked {
		t.Fatalf("GetActiveKey = %+v, want %s unrevoked", key, keyID)
	}

	var oldUserSigID, oldServerSigID int64
	if err := ds.db.QueryRow(`SELECT user_signature_id, server_signature_id FROM users WHERE id = $1`, user).
		Scan(&oldUserSigID, &oldServerSigID); err != nil {
		t.Fatal(err)
	}

	signedAt := time.Now().UTC().Truncate(time.Second)
	if err := ds.UpdateUser(ctx, UpdateUserInput{
		UserID:           user,
		Username:         "renamed1",
		Bio:              "new bio",
		Fingerprint:      keyID,
		UserSignature:    "user-sig",
		ProfileSignature: ServerSignature{ID: "server-key", Armor: "server-sig", SignedAt: signedAt},
	}); err != nil {
		t.Fatal(err)
	}

	got, err := ds.GetUserProfile(ctx, user)
	if err != nil {
		t.Fatal(err)
	}
	if got.Username != "renamed1" || got.Bio != "new bio" ||
		got.UserSignature != (UserSignature{ID: keyID, Armor: "user-sig"}) ||
		got.ServerSignature.Armor != "server-sig" || !got.ServerSignature.SignedAt.Equal(signedAt) {
		t.Fatalf("profile after update = %+v", got)
	}

	var leftover int
	if err := ds.db.QueryRow(`
		SELECT (SELECT COUNT(*) FROM user_signatures WHERE id = $1)
		     + (SELECT COUNT(*) FROM server_signatures WHERE id = $2)
	`, oldUserSigID, oldServerSigID).Scan(&leftover); err != nil {
		t.Fatal(err)
	}
	if leftover != 0 {
		t.Fatalf("superseded signature rows left: %d", leftover)
	}
}
