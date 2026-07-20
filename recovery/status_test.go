package recovery

import (
	"testing"
	"time"

	"syrinx/identity"
)

func testStatusProfile(serverID string, ts time.Time) Profile {
	return Profile{
		ID:                   "user1",
		Username:             "alice",
		MemberSince:          ts,
		SignatureFingerprint: "AAA",
		Signature:            "dXNlcg==",
		Server: ServerSignature{
			ID:          serverID,
			Fingerprint: "SKEY",
			Timestamp:   ts,
			Algorithm:   identity.Algorithm,
			Signature:   "c2VydmVy",
		},
	}
}

func TestVerifyProfileServerCountersig_OK(t *testing.T) {
	ts := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	profile := testStatusProfile("srv1", ts)
	err := VerifyProfileServerCountersig(profile, "srv1",
		func(string) (string, error) { return "pub", nil },
		&fakeVerifier{})
	if err != nil {
		t.Fatal(err)
	}
}

func TestVerifyProfileServerCountersig_WrongServerID(t *testing.T) {
	ts := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	profile := testStatusProfile("other", ts)
	err := VerifyProfileServerCountersig(profile, "srv1",
		func(string) (string, error) { return "pub", nil },
		&fakeVerifier{})
	if err == nil {
		t.Fatal("expected mismatch")
	}
}

func TestVerifyProfileServerCountersig_BadSignature(t *testing.T) {
	ts := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	profile := testStatusProfile("srv1", ts)
	payload := string(identity.BuildProfilePayload(
		profile.ID, profile.Username, profile.SignatureFingerprint, profile.AvatarURL,
		"srv1", profile.Server.Fingerprint, profile.Signature, profile.Bio,
		profile.MemberSince, profile.Server.Timestamp,
	))
	err := VerifyProfileServerCountersig(profile, "srv1",
		func(string) (string, error) { return "pub", nil },
		&fakeVerifier{failSig: map[string]bool{payload: true}})
	if err == nil {
		t.Fatal("expected bad countersignature")
	}
}
