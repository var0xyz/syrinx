//go:build !ops && !ripplescleanup && !challengescleanup

package main

import (
	"encoding/json"
	"os"
	"testing"
)

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

// Shared with the SPA's test:signing harness, which asserts the same bytes.
func TestCanonicalJSONVectors(t *testing.T) {
	raw, err := os.ReadFile("testdata/canonical_json_vectors.json")
	if err != nil {
		t.Fatal(err)
	}
	var vectors []struct {
		Name     string       `json:"name"`
		Fields   signedFields `json:"fields"`
		Expected string       `json:"expected"`
	}
	if err := json.Unmarshal(raw, &vectors); err != nil {
		t.Fatal(err)
	}
	for _, v := range vectors {
		if got := string(canonicalJSON(v.Fields)); got != v.Expected {
			t.Errorf("%s:\n got=%s\nwant=%s", v.Name, got, v.Expected)
		}
	}
}
