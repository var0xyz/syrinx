package recovery

import (
	"strings"
	"testing"
	"time"

	"syrinx/crypto"
)

func TestEncryptDecryptSymmetric_RoundTrip(t *testing.T) {
	plain := []byte(`{"version":1,"hello":"world"}`)
	armored, err := EncryptSymmetric(plain, "bundle-password-xx")
	if err != nil {
		t.Fatalf("EncryptSymmetric: %v", err)
	}
	if strings.Contains(armored, `"hello"`) {
		t.Fatal("ciphertext contains plaintext JSON")
	}
	if !strings.Contains(armored, "BEGIN PGP MESSAGE") {
		t.Fatalf("expected armored message, got %q", armored[:min(60, len(armored))])
	}

	got, err := DecryptSymmetric(armored, "bundle-password-xx")
	if err != nil {
		t.Fatalf("DecryptSymmetric: %v", err)
	}
	if string(got) != string(plain) {
		t.Fatalf("got %q, want %q", got, plain)
	}
}

func TestDecryptSymmetric_WrongPassword(t *testing.T) {
	armored, err := EncryptSymmetric([]byte("secret-payload"), "correct-password!!")
	if err != nil {
		t.Fatalf("EncryptSymmetric: %v", err)
	}
	got, err := DecryptSymmetric(armored, "wrong-password!!!!!!")
	if err == nil {
		t.Fatalf("expected error, got plaintext %q", got)
	}
	if strings.Contains(err.Error(), "secret-payload") {
		t.Fatalf("error leaked plaintext: %v", err)
	}
	if got != nil {
		t.Fatalf("partial plaintext returned: %q", got)
	}
}

func TestDefaultExportFilename(t *testing.T) {
	at := time.Date(2026, 7, 17, 15, 4, 5, 0, time.UTC)
	got := DefaultExportFilename("Ab3xY9pQ", at)
	want := "syrinx-Ab3xY9pQ-20260717T150405Z.json.gpg"
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
	armored, err := EncryptSymmetric(raw, "bundle-pw-16chars!")
	if err != nil {
		t.Fatalf("EncryptSymmetric: %v", err)
	}
	plain, err := DecryptSymmetric(armored, "bundle-pw-16chars!")
	if err != nil {
		t.Fatalf("DecryptSymmetric: %v", err)
	}
	parsed, err := ParseBundleJSON(plain)
	if err != nil {
		t.Fatalf("ParseBundleJSON: %v", err)
	}
	if parsed.ServerID != b.ServerID || parsed.SigningKeyFingerprint != b.SigningKeyFingerprint {
		t.Fatalf("parsed mismatch: %+v", parsed)
	}
}

func TestValidateShape_SigningKeyMissing(t *testing.T) {
	now := time.Now().UTC()
	b := &Bundle{
		Version:               BundleVersion,
		ExportedAt:            now,
		ServerID:              "Ab3xY9pQ",
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
