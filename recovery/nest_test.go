package recovery

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"
)

func TestValidateChallengeAge(t *testing.T) {
	now := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	if err := ValidateChallengeAge(now.Unix(), now, challengeMaxAge); err != nil {
		t.Fatal(err)
	}
	if err := ValidateChallengeAge(now.Unix()-59, now, challengeMaxAge); err != nil {
		t.Fatal(err)
	}
	if err := ValidateChallengeAge(now.Unix()-61, now, challengeMaxAge); err == nil {
		t.Fatal("expected stale")
	}
	if err := ValidateChallengeAge(now.Unix()+1, now, challengeMaxAge); err == nil {
		t.Fatal("expected future")
	}
}

type fakeVerifier struct {
	failSig       map[string]bool
	failChallenge map[string]bool
}

func (f *fakeVerifier) VerifySignature(message, signature, publicKey string) error {
	if f.failSig[message] || f.failSig[signature] {
		return fmt.Errorf("bad signature")
	}
	return nil
}

func (f *fakeVerifier) VerifySignedChallenge(signature, publicKey, challenge string) error {
	if f.failChallenge[signature] || f.failChallenge[challenge] {
		return fmt.Errorf("bad predecessor")
	}
	return nil
}

func TestFlattenKeysNest_BrokenPredecessor(t *testing.T) {
	serverID := "srv1"
	ts := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	profile := Profile{
		ID:              "user1",
		Username:        "alice",
		Role:            "user",
		MemberSince:     ts,
		UserSignature:   testUserSig("BBB"),
		ServerSignature: testServerSig(serverID, ts),
	}
	root := KeyNode{
		KeyWire: KeyWire{
			Fingerprint:     "BBB",
			Armor:           "armor-b",
			UserID:          "user1",
			CreatedAt:       ts,
			ServerSignature: testServerSig(serverID, ts),
		},
		Predecessor: &KeyNode{
			Signature: "bad-pred-sig",
			KeyWire: KeyWire{
				Fingerprint:     "AAA",
				Armor:           "armor-a",
				UserID:          "user1",
				CreatedAt:       ts,
				ServerSignature: testServerSig(serverID, ts),
			},
		},
	}
	v := &fakeVerifier{failChallenge: map[string]bool{"bad-pred-sig": true}}
	lookup := func(ctx context.Context, fp string) (string, error) {
		if fp == "SKEY" {
			return "server-pub", nil
		}
		return "", nil
	}
	_, _, err := FlattenKeysNest(context.Background(), profile, root, serverID, lookup, v)
	if err == nil {
		t.Fatal("expected broken predecessor error")
	}
}

func TestFlattenKeysNest_ServerIDMismatch(t *testing.T) {
	ts := time.Now().UTC().Truncate(time.Second)
	profile := Profile{
		ID:              "user1",
		Username:        "alice",
		Role:            "user",
		UserSignature:   testUserSig("AAA"),
		ServerSignature: testServerSig("other", ts),
	}
	root := KeyNode{KeyWire: KeyWire{Fingerprint: "AAA", Armor: "a", ServerSignature: testServerSig("", ts)}}
	_, _, err := FlattenKeysNest(context.Background(), profile, root, "srv1", func(ctx context.Context, _ string) (string, error) { return "pub", nil }, &fakeVerifier{})
	if err == nil {
		t.Fatal("expected server id mismatch")
	}
}

func TestKeyNodeJSON_ArmorAtNodeLevel(t *testing.T) {
	const raw = `{"fingerprint":"AAA","armor":"armor-a","predecessor":{"signature":"s","fingerprint":"BBB","armor":"armor-b"}}`
	var n KeyNode
	if err := json.Unmarshal([]byte(raw), &n); err != nil {
		t.Fatal(err)
	}
	if n.Armor != "armor-a" || n.Predecessor == nil || n.Predecessor.Armor != "armor-b" {
		t.Fatalf("got armor=%q pred=%v", n.Armor, n.Predecessor)
	}
}
