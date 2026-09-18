//go:build !ops

package main

import "testing"

func TestIdentityMatchesBundle(t *testing.T) {
	b := &recoveryBundle{
		Version:      bundleVersion,
		ServerID:     "Ab3xY9pQ",
		ServerName:   "syrinx.example",
		SigningKeyID: "AAAA@Ab3xY9pQ",
		Keys: []recoveryBundleKey{
			{ID: "AAAA@Ab3xY9pQ", PrivateKeyArmor: "priv-a", PublicKeyArmor: "pub-a"},
			{ID: "BBBB@Ab3xY9pQ", PrivateKeyArmor: "priv-b", PublicKeyArmor: "pub-b"},
		},
	}
	self := recoveryExistingSelf{ID: "Ab3xY9pQ", Name: "syrinx.example", SigningKey: "AAAA@Ab3xY9pQ"}
	keys := []recoveryExistingKey{
		{ID: "AAAA@Ab3xY9pQ", Armor: "priv-a"},
		{ID: "BBBB@Ab3xY9pQ", Armor: "priv-b"},
	}
	if !identityMatchesBundle(b, self, keys) {
		t.Fatal("expected match")
	}

	if identityMatchesBundle(b, recoveryExistingSelf{ID: "other", Name: "syrinx.example", SigningKey: "AAAA@Ab3xY9pQ"}, keys) {
		t.Fatal("different id should not match")
	}
	if identityMatchesBundle(b, recoveryExistingSelf{ID: "Ab3xY9pQ", Name: "other", SigningKey: "AAAA@Ab3xY9pQ"}, keys) {
		t.Fatal("different name should not match")
	}
	if identityMatchesBundle(b, recoveryExistingSelf{ID: "Ab3xY9pQ", Name: "syrinx.example", SigningKey: "CCCC@Ab3xY9pQ"}, keys) {
		t.Fatal("different signing key should not match")
	}
	if identityMatchesBundle(b, self, []recoveryExistingKey{{ID: "AAAA@Ab3xY9pQ", Armor: "priv-a"}}) {
		t.Fatal("missing key should not match")
	}
	if identityMatchesBundle(b, self, []recoveryExistingKey{
		{ID: "AAAA@Ab3xY9pQ", Armor: "priv-a"},
		{ID: "BBBB@Ab3xY9pQ", Armor: "CHANGED"},
	}) {
		t.Fatal("different armor should not match")
	}
}
