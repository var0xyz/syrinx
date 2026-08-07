package recovery

import (
	"context"
	"testing"
	"time"

	"syrinx/identity"
)

func testUserSig(fingerprint string) UserSignature {
	return UserSignature{
		Fingerprint: fingerprint,
		Armor:       "dXNlcg==",
	}
}

func testServerSig(serverID string, ts time.Time) ServerSignature {
	return ServerSignature{
		ServerID:    serverID,
		Fingerprint: "SKEY",
		Timestamp:   ts,
		Armor:       "c2VydmVy",
	}
}

func testStatusProfile(serverID string, ts time.Time) Profile {
	return Profile{
		ID:              "user1",
		Username:        "alice",
		MemberSince:     ts,
		UserSignature:   testUserSig("AAA"),
		ServerSignature: testServerSig(serverID, ts),
	}
}

func TestVerifyProfileServerCountersig_OK(t *testing.T) {
	ts := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	profile := testStatusProfile("srv1", ts)
	err := VerifyProfileServerCountersig(context.Background(), profile, "srv1",
		func(ctx context.Context, _ string) (string, error) { return "pub", nil },
		&fakeVerifier{})
	if err != nil {
		t.Fatal(err)
	}
}

func TestVerifyProfileServerCountersig_WrongServerID(t *testing.T) {
	ts := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	profile := testStatusProfile("other", ts)
	err := VerifyProfileServerCountersig(context.Background(), profile, "srv1",
		func(ctx context.Context, _ string) (string, error) { return "pub", nil },
		&fakeVerifier{})
	if err == nil {
		t.Fatal("expected mismatch")
	}
}

func TestVerifyProfileServerCountersig_BadSignature(t *testing.T) {
	ts := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	profile := testStatusProfile("srv1", ts)
	payload := string(identity.BuildProfilePayload(
		profile.ID, profile.Username, profile.UserSignature.Fingerprint,
		"srv1", profile.ServerSignature.Fingerprint, profile.UserSignature.Armor, "",
		profile.Bio,
		profile.MemberSince, profile.ServerSignature.Timestamp,
	))
	err := VerifyProfileServerCountersig(context.Background(), profile, "srv1",
		func(ctx context.Context, _ string) (string, error) { return "pub", nil },
		&fakeVerifier{failSig: map[string]bool{payload: true}})
	if err == nil {
		t.Fatal("expected bad countersignature")
	}
}
