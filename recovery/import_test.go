package recovery

import "testing"

func TestIdentityMatches(t *testing.T) {
	b := &Bundle{
		Version:               BundleVersion,
		ServerID:              "Ab3xY9pQ",
		ServerName:            "syrinx.example",
		SigningKeyFingerprint: "AAAA",
		Keys: []BundleKey{
			{Fingerprint: "AAAA", PrivateKeyArmor: "priv-a", PublicKeyArmor: "pub-a"},
			{Fingerprint: "BBBB", PrivateKeyArmor: "priv-b", PublicKeyArmor: "pub-b"},
		},
	}
	self := ExistingSelf{ID: "Ab3xY9pQ", Name: "syrinx.example", SigningKey: "AAAA@Ab3xY9pQ"}
	keys := []ExistingKey{
		{Fingerprint: "AAAA@Ab3xY9pQ", Armor: "priv-a"},
		{Fingerprint: "BBBB@Ab3xY9pQ", Armor: "priv-b"},
	}
	if !IdentityMatches(b, self, keys) {
		t.Fatal("expected match")
	}

	if IdentityMatches(b, ExistingSelf{ID: "other", Name: "syrinx.example", SigningKey: "AAAA@Ab3xY9pQ"}, keys) {
		t.Fatal("different id should not match")
	}
	if IdentityMatches(b, ExistingSelf{ID: "Ab3xY9pQ", Name: "other", SigningKey: "AAAA@Ab3xY9pQ"}, keys) {
		t.Fatal("different name should not match")
	}
	if IdentityMatches(b, ExistingSelf{ID: "Ab3xY9pQ", Name: "syrinx.example", SigningKey: "CCCC@Ab3xY9pQ"}, keys) {
		t.Fatal("different signing key should not match")
	}
	if IdentityMatches(b, self, []ExistingKey{{Fingerprint: "AAAA@Ab3xY9pQ", Armor: "priv-a"}}) {
		t.Fatal("missing key should not match")
	}
	if IdentityMatches(b, self, []ExistingKey{
		{Fingerprint: "AAAA@Ab3xY9pQ", Armor: "priv-a"},
		{Fingerprint: "BBBB@Ab3xY9pQ", Armor: "CHANGED"},
	}) {
		t.Fatal("different armor should not match")
	}
}
