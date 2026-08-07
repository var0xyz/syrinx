package crypto

import (
	"strings"
	"testing"
)

func TestEncryptDecryptSymmetric_RoundTrip(t *testing.T) {
	svc := NewService()
	plain := []byte(`{"version":1,"hello":"world"}`)
	armored, err := svc.EncryptSymmetric(plain, "bundle-password-xx")
	if err != nil {
		t.Fatalf("EncryptSymmetric: %v", err)
	}
	if strings.Contains(armored, `"hello"`) {
		t.Fatal("ciphertext contains plaintext JSON")
	}
	if !strings.Contains(armored, "BEGIN PGP MESSAGE") {
		t.Fatalf("expected armored message, got %q", armored[:min(60, len(armored))])
	}

	got, err := svc.DecryptSymmetric(armored, "bundle-password-xx")
	if err != nil {
		t.Fatalf("DecryptSymmetric: %v", err)
	}
	if string(got) != string(plain) {
		t.Fatalf("got %q, want %q", got, plain)
	}
}

func TestDecryptSymmetric_WrongPassword(t *testing.T) {
	svc := NewService()
	armored, err := svc.EncryptSymmetric([]byte("secret-payload"), "correct-password!!")
	if err != nil {
		t.Fatalf("EncryptSymmetric: %v", err)
	}
	got, err := svc.DecryptSymmetric(armored, "wrong-password!!!!!!")
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
