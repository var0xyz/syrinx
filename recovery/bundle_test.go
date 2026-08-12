package recovery

import (
	"testing"
	"time"

	"syrinx/crypto"
)

func TestDefaultExportFilename(t *testing.T) {
	at := time.Date(2026, 7, 17, 15, 4, 5, 0, time.UTC)
	got := DefaultExportFilename("Ab3xY9pQ", at)
	want := "syrinx-Ab3xY9pQ-20260717T150405Z.sxi.gpg"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestValidateShape_AndDecrypt(t *testing.T) {
	svc := crypto.NewService()
	kp, err := svc.CreateKeyPair("test-server", "", "")
	if err != nil {
		t.Fatalf("CreateKeyPair: %v", err)
	}
	pass := "server-key-pass-16"
	encPriv, err := svc.EncryptPrivateKey(kp.PrivateKey, pass)
	if err != nil {
		t.Fatalf("EncryptPrivateKey: %v", err)
	}

	now := time.Now().UTC().Truncate(time.Second)
	b := &Bundle{
		Version:               BundleVersion,
		ExportedAt:            now,
		ServerID:              "Ab3xY9pQ",
		ServerName:            "syrinx.example",
		SigningKeyFingerprint: kp.Fingerprint,
		Keys: []BundleKey{{
			Fingerprint:     kp.Fingerprint,
			PrivateKeyArmor: encPriv,
			PublicKeyArmor:  kp.PublicKey,
			CreatedAt:       now,
		}},
	}
	if err := ValidateShape(b); err != nil {
		t.Fatalf("ValidateShape: %v", err)
	}
	if err := ValidateDecrypt(b, svc, pass); err != nil {
		t.Fatalf("ValidateDecrypt: %v", err)
	}
	if err := ValidateDecrypt(b, svc, "wrong-passphrase!!"); err == nil {
		t.Fatal("ValidateDecrypt should fail on wrong passphrase")
	}

	raw, err := MarshalBundleJSON(b)
	if err != nil {
		t.Fatalf("MarshalBundleJSON: %v", err)
	}
	armored, err := svc.EncryptSymmetric(raw, "bundle-pw-16chars!")
	if err != nil {
		t.Fatalf("EncryptSymmetric: %v", err)
	}
	plain, err := svc.DecryptSymmetric(armored, "bundle-pw-16chars!")
	if err != nil {
		t.Fatalf("DecryptSymmetric: %v", err)
	}
	parsed, err := ParseBundleJSON(plain)
	if err != nil {
		t.Fatalf("ParseBundleJSON: %v", err)
	}
	if parsed.ServerID != b.ServerID || parsed.ServerName != b.ServerName || parsed.SigningKeyFingerprint != b.SigningKeyFingerprint {
		t.Fatalf("parsed mismatch: %+v", parsed)
	}
	if len(parsed.Keys) != 1 || parsed.Keys[0].PrivateKeyArmor != encPriv || parsed.Keys[0].PublicKeyArmor != kp.PublicKey {
		t.Fatalf("key armor round-trip mismatch: %+v", parsed.Keys)
	}
}

func TestValidateShape_SigningKeyMissing(t *testing.T) {
	now := time.Now().UTC()
	b := &Bundle{
		Version:               BundleVersion,
		ExportedAt:            now,
		ServerID:              "Ab3xY9pQ",
		ServerName:            "syrinx.example",
		SigningKeyFingerprint: "DEADBEEF",
		Keys: []BundleKey{{
			Fingerprint:     "CAFEBABE",
			PrivateKeyArmor: "priv",
			PublicKeyArmor:  "pub",
			CreatedAt:       now,
		}},
	}
	if err := ValidateShape(b); err == nil {
		t.Fatal("expected error for missing signing key")
	}
}
