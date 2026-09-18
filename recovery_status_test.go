//go:build !ops

package main

import (
	"context"
	"testing"
	"time"
)

func testStatusRecoveryProfile(serverID string, ts time.Time) recoveryProfile {
	return recoveryProfile{
		ID:              "user1",
		Username:        "alice",
		Role:            "user",
		MemberSince:     ts,
		UserSignature:   testRecoveryUserSig("AAA"),
		ServerSignature: testRecoveryServerSig(serverID, ts),
	}
}

func TestVerifyProfileServerCountersig_OK(t *testing.T) {
	ts := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	profile := testStatusRecoveryProfile("srv1", ts)
	err := verifyProfileServerCountersig(context.Background(), profile, "srv1",
		func(ctx context.Context, _ string) (string, error) { return "pub", nil },
		&fakeRecoveryVerifier{})
	if err != nil {
		t.Fatal(err)
	}
}

func TestVerifyProfileServerCountersig_WrongServerID(t *testing.T) {
	ts := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	profile := testStatusRecoveryProfile("other", ts)
	err := verifyProfileServerCountersig(context.Background(), profile, "srv1",
		func(ctx context.Context, _ string) (string, error) { return "pub", nil },
		&fakeRecoveryVerifier{})
	if err == nil {
		t.Fatal("expected mismatch")
	}
}

func TestVerifyProfileServerCountersig_BadSignature(t *testing.T) {
	ts := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	profile := testStatusRecoveryProfile("srv1", ts)
	payload := string(buildProfilePayload(
		profile.ID, profile.Username, profile.UserSignature.KeyID,
		"srv1", profile.ServerSignature.Fingerprint, profile.UserSignature.Armor, "",
		profile.Role, profile.Bio,
		profile.MemberSince, profile.ServerSignature.Timestamp,
	))
	err := verifyProfileServerCountersig(context.Background(), profile, "srv1",
		func(ctx context.Context, _ string) (string, error) { return "pub", nil },
		&fakeRecoveryVerifier{failSig: map[string]bool{payload: true}})
	if err == nil {
		t.Fatal("expected bad countersignature")
	}
}
