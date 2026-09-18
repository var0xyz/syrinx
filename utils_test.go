//go:build !ops && !ripplescleanup

package main

import "testing"

func TestBase64EncodeDecodeRoundTrip(t *testing.T) {
	const armor = "-----BEGIN PGP SIGNATURE-----\n\nabcd\n-----END PGP SIGNATURE-----\n"
	encoded := base64Encode(armor)
	decoded, err := base64Decode(encoded)
	if err != nil {
		t.Fatalf("base64Decode: %v", err)
	}
	if decoded != armor {
		t.Fatalf("round-trip mismatch: got %q, want %q", decoded, armor)
	}
}

func TestBase64DecodeInvalid(t *testing.T) {
	if _, err := base64Decode("not-valid-base64!!!"); err == nil {
		t.Fatal("expected error for invalid base64 input")
	}
}
