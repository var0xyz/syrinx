//go:build !ops && !ripplescleanup && !challengescleanup

package main

import (
	"encoding/json"
	"os"
	"strings"
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

// Shared with the SPA's test:character-count harness, which asserts the same counts.
func TestCountCharactersVectors(t *testing.T) {
	raw, err := os.ReadFile("testdata/character_count_vectors.json")
	if err != nil {
		t.Fatal(err)
	}
	var vectors []struct {
		Name  string `json:"name"`
		Text  string `json:"text"`
		Count int    `json:"count"`
	}
	if err := json.Unmarshal(raw, &vectors); err != nil {
		t.Fatal(err)
	}
	for _, v := range vectors {
		if got := countCharacters(v.Text); got != v.Count {
			t.Errorf("%s: got %d, want %d", v.Name, got, v.Count)
		}
	}
}

func TestLongTextFits(t *testing.T) {
	family := "👨‍👩‍👧‍👦" // one character, 11 UTF-16 units
	cases := []struct {
		name string
		text string
		want bool
	}{
		{"at the visible limit", strings.Repeat("a", maxLongTextChars), true},
		{"over the visible limit", strings.Repeat("a", maxLongTextChars+1), false},
		{"within both", strings.Repeat(family, 127), true},
		{"over the raw limit", strings.Repeat(family, 128), false},
	}
	for _, c := range cases {
		if got := longTextFits(c.text); got != c.want {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
}
