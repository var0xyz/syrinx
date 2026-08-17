package recovery

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"testing"
	"time"

	"syrinx/crypto"
	"syrinx/identity"
)

// Real client wire shape (spa/src/lib/types/api.ts) — canonical `id`,
// never a bare fingerprint. A Go-literal fixture wouldn't catch this.
const clientShapedProfileJSON = `{
	"id": "user1@srv1",
	"username": "alice",
	"role": "user",
	"memberSince": "2026-07-19T12:00:00Z",
	"bio": "",
	"activeKeyFingerprint": "",
	"userSignature": {"id": "user1@srv1/AAA", "armor": "dXNlcg=="},
	"serverSignature": {"id": "SKEY@srv1", "armor": "c2VydmVy", "timestamp": "2026-07-19T12:00:00Z"},
	"hasReeds": false,
	"invitedBy": null
}`

func TestProfileUnmarshalJSON_ClientShape(t *testing.T) {
	var p Profile
	if err := json.Unmarshal([]byte(clientShapedProfileJSON), &p); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	// UserSignature.KeyID holds the FULL canonical id — the signed
	// profile payload's keyID header binds the whole thing, not a bare fp.
	if p.UserSignature.KeyID != "user1@srv1/AAA" {
		t.Errorf("UserSignature.KeyID = %q, want user1@srv1/AAA", p.UserSignature.KeyID)
	}
	// ServerSignature.Fingerprint IS bare — server countersig binds a bare
	// serverKeyFingerprint header, separate from the serverID header.
	if p.ServerSignature.Fingerprint != "SKEY" {
		t.Errorf("ServerSignature.Fingerprint = %q, want SKEY", p.ServerSignature.Fingerprint)
	}
	if p.ServerSignature.ServerID != "srv1" {
		t.Errorf("ServerSignature.ServerID = %q, want srv1", p.ServerSignature.ServerID)
	}
}

func TestProfileMarshalUnmarshalRoundTrip(t *testing.T) {
	ts := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	original := Profile{
		ID:              "user1@srv1",
		Username:        "alice",
		UserSignature:   UserSignature{KeyID: "user1@srv1/AAA", Armor: "dXNlcg=="},
		ServerSignature: ServerSignature{ServerID: "srv1", Fingerprint: "SKEY", Armor: "c2VydmVy", Timestamp: ts},
	}
	b, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var round Profile
	if err := json.Unmarshal(b, &round); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if round.UserSignature.KeyID != original.UserSignature.KeyID {
		t.Errorf("UserSignature.KeyID = %q, want %q", round.UserSignature.KeyID, original.UserSignature.KeyID)
	}
	if round.ServerSignature.Fingerprint != original.ServerSignature.Fingerprint {
		t.Errorf("ServerSignature.Fingerprint = %q, want %q", round.ServerSignature.Fingerprint, original.ServerSignature.Fingerprint)
	}
	if round.ServerSignature.ServerID != original.ServerSignature.ServerID {
		t.Errorf("ServerSignature.ServerID = %q, want %q", round.ServerSignature.ServerID, original.ServerSignature.ServerID)
	}
}

func TestServerSignatureUnmarshalJSON_MalformedID(t *testing.T) {
	var s ServerSignature
	err := json.Unmarshal([]byte(`{"id": "not-canonical", "armor": "x", "timestamp": "2026-07-19T12:00:00Z"}`), &s)
	if err == nil {
		t.Fatal("expected error for non-canonical serverSignature.id")
	}
}

// BuildProfilePayload's keyID header binds the full canonical user key
// id, not a bare fingerprint — fakeVerifier tests can't catch a wrong
// shape since they skip real crypto; this signs/verifies for real.
func TestVerifyProfileServerCountersig_RealSignatureAfterJSONRoundTrip(t *testing.T) {
	cryptoSvc := crypto.NewService()
	serverID := "srv1"

	serverKP, err := cryptoSvc.CreateKeyPair("server", "", "")
	if err != nil {
		t.Fatalf("CreateKeyPair(server): %v", err)
	}
	userKP, err := cryptoSvc.CreateKeyPair("user1", "", "")
	if err != nil {
		t.Fatalf("CreateKeyPair(user): %v", err)
	}

	userID := "user1@" + serverID
	keyID := identity.AppendEntity(identity.IdentityID(userID), userKP.Fingerprint)
	ts := time.Now().UTC().Truncate(time.Second)

	userPayload := identity.BuildUserIdentityPayload("alice", string(keyID), "")
	userSigArmor, err := cryptoSvc.Sign(string(userPayload), userKP.PrivateKey)
	if err != nil {
		t.Fatalf("Sign(user): %v", err)
	}
	userSigB64 := base64.StdEncoding.EncodeToString([]byte(userSigArmor))

	profilePayload := identity.BuildNewProfilePayload(
		userID, "alice", string(keyID), serverID, serverKP.Fingerprint,
		userSigB64, "", "user", ts,
	)
	serverSigArmor, err := cryptoSvc.Sign(string(profilePayload), serverKP.PrivateKey)
	if err != nil {
		t.Fatalf("Sign(server): %v", err)
	}

	// Client wire shape: userSignature.id / serverSignature.id are
	// canonical, armor is base64 — matches spa/src/lib/types/api.ts.
	wireJSON, err := json.Marshal(struct {
		ID              string `json:"id"`
		Username        string `json:"username"`
		Role            string `json:"role"`
		MemberSince     string `json:"memberSince"`
		Bio             string `json:"bio"`
		UserSignature   struct {
			ID    string `json:"id"`
			Armor string `json:"armor"`
		} `json:"userSignature"`
		ServerSignature struct {
			ID        string `json:"id"`
			Armor     string `json:"armor"`
			Timestamp string `json:"timestamp"`
		} `json:"serverSignature"`
	}{
		ID: userID, Username: "alice", Role: "user",
		MemberSince: ts.Format(time.RFC3339), Bio: "",
		UserSignature: struct {
			ID    string `json:"id"`
			Armor string `json:"armor"`
		}{ID: string(keyID), Armor: userSigB64},
		ServerSignature: struct {
			ID        string `json:"id"`
			Armor     string `json:"armor"`
			Timestamp string `json:"timestamp"`
		}{
			ID:        string(identity.CanonicalID(serverID, serverKP.Fingerprint)),
			Armor:     base64.StdEncoding.EncodeToString([]byte(serverSigArmor)),
			Timestamp: ts.Format(time.RFC3339),
		},
	})
	if err != nil {
		t.Fatalf("Marshal wire JSON: %v", err)
	}

	var profile Profile
	if err := json.Unmarshal(wireJSON, &profile); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	err = VerifyProfileServerCountersig(context.Background(), profile, serverID,
		func(ctx context.Context, fp string) (string, error) {
			if fp == serverKP.Fingerprint {
				return serverKP.PublicKey, nil
			}
			return "", nil
		},
		cryptoSvc,
	)
	if err != nil {
		t.Fatalf("VerifyProfileServerCountersig: %v", err)
	}
}
