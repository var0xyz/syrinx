//go:build !ops

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"
)

func TestValidateChallengeAge(t *testing.T) {
	now := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	if err := validateChallengeAge(now.Unix(), now, challengeMaxAge); err != nil {
		t.Fatal(err)
	}
	if err := validateChallengeAge(now.Unix()-59, now, challengeMaxAge); err != nil {
		t.Fatal(err)
	}
	if err := validateChallengeAge(now.Unix()-61, now, challengeMaxAge); err == nil {
		t.Fatal("expected stale")
	}
	if err := validateChallengeAge(now.Unix()+1, now, challengeMaxAge); err == nil {
		t.Fatal("expected future")
	}
}

type fakeRecoveryVerifier struct {
	failSig       map[string]bool
	failChallenge map[string]bool
}

func (f *fakeRecoveryVerifier) verifySignature(message, signature, publicKey string) error {
	if f.failSig[message] || f.failSig[signature] {
		return fmt.Errorf("bad signature")
	}
	return nil
}

func (f *fakeRecoveryVerifier) verifySignedChallenge(signature, publicKey, challenge string) error {
	if f.failChallenge[signature] || f.failChallenge[challenge] {
		return fmt.Errorf("bad predecessor")
	}
	return nil
}

func testRecoveryUserSig(keyID string) recoveryUserSignature {
	return recoveryUserSignature{
		KeyID: keyID,
		Armor: "dXNlcg==",
	}
}

func testRecoveryServerSig(serverID string, ts time.Time) recoveryServerSignature {
	return recoveryServerSignature{
		ServerID:    serverID,
		Fingerprint: "SKEY",
		Timestamp:   ts,
		Armor:       "c2VydmVy",
	}
}

func TestFlattenKeysNest_BrokenPredecessor(t *testing.T) {
	serverID := "srv1"
	ts := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	profile := recoveryProfile{
		ID:              "user1",
		Username:        "alice",
		Role:            "user",
		MemberSince:     ts,
		UserSignature:   testRecoveryUserSig("BBB"),
		ServerSignature: testRecoveryServerSig(serverID, ts),
	}
	root := recoveryKeyNode{
		recoveryKeyWire: recoveryKeyWire{
			Fingerprint:     "BBB",
			Armor:           "armor-b",
			UserID:          "user1",
			CreatedAt:       ts,
			ServerSignature: testRecoveryServerSig(serverID, ts),
		},
		Predecessor: &recoveryKeyNode{
			Signature: "bad-pred-sig",
			recoveryKeyWire: recoveryKeyWire{
				Fingerprint:     "AAA",
				Armor:           "armor-a",
				UserID:          "user1",
				CreatedAt:       ts,
				ServerSignature: testRecoveryServerSig(serverID, ts),
			},
		},
	}
	v := &fakeRecoveryVerifier{failChallenge: map[string]bool{"bad-pred-sig": true}}
	lookup := func(ctx context.Context, fp string) (string, error) {
		if fp == "SKEY" {
			return "server-pub", nil
		}
		return "", nil
	}
	_, _, err := flattenKeysNest(context.Background(), profile, root, serverID, lookup, v)
	if err == nil {
		t.Fatal("expected broken predecessor error")
	}
}

func TestFlattenKeysNest_ServerIDMismatch(t *testing.T) {
	ts := time.Now().UTC().Truncate(time.Second)
	profile := recoveryProfile{
		ID:              "user1",
		Username:        "alice",
		Role:            "user",
		UserSignature:   testRecoveryUserSig("AAA"),
		ServerSignature: testRecoveryServerSig("other", ts),
	}
	root := recoveryKeyNode{recoveryKeyWire: recoveryKeyWire{Fingerprint: "AAA", Armor: "a", ServerSignature: testRecoveryServerSig("", ts)}}
	_, _, err := flattenKeysNest(context.Background(), profile, root, "srv1", func(ctx context.Context, _ string) (string, error) { return "pub", nil }, &fakeRecoveryVerifier{})
	if err == nil {
		t.Fatal("expected server id mismatch")
	}
}

func TestRecoveryKeyNodeJSON_ArmorAtNodeLevel(t *testing.T) {
	const raw = `{"fingerprint":"AAA","armor":"armor-a","predecessor":{"signature":"s","fingerprint":"BBB","armor":"armor-b"}}`
	var n recoveryKeyNode
	if err := json.Unmarshal([]byte(raw), &n); err != nil {
		t.Fatal(err)
	}
	if n.Armor != "armor-a" || n.Predecessor == nil || n.Predecessor.Armor != "armor-b" {
		t.Fatalf("got armor=%q pred=%v", n.Armor, n.Predecessor)
	}
}
