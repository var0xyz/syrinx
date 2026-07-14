package signing

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

type vector struct {
	Name     string            `json:"name"`
	Headers  map[string]string `json:"headers"`
	Content  string            `json:"content"`
	Expected string            `json:"expected"`
}

func loadVectors(t *testing.T) []vector {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(".", "testvectors.json"))
	if err != nil {
		t.Fatalf("read testvectors.json: %v", err)
	}
	var vs []vector
	if err := json.Unmarshal(data, &vs); err != nil {
		t.Fatalf("unmarshal testvectors.json: %v", err)
	}
	if len(vs) == 0 {
		t.Fatal("no test vectors loaded")
	}
	return vs
}

func TestBytesToSign_Vectors(t *testing.T) {
	for _, v := range loadVectors(t) {
		v := v
		t.Run(v.Name, func(t *testing.T) {
			got := BytesToSign(v.Headers, v.Content)
			if string(got) != v.Expected {
				t.Errorf("mismatch for %s:\n got  = %q\n want = %q", v.Name, string(got), v.Expected)
			}
		})
	}
}

func TestBytesToSign_KeysSorted(t *testing.T) {
	// Insertion order should not matter: two maps with the same content but
	// different insertion order must produce identical output.
	h1 := map[string]string{"z": "1", "a": "2", "m": "3"}
	h2 := map[string]string{"a": "2", "m": "3", "z": "1"}
	if string(BytesToSign(h1, "x")) != string(BytesToSign(h2, "x")) {
		t.Fatal("BytesToSign output depended on map iteration order")
	}
}

func TestBytesToSign_EmptyValueOmitted(t *testing.T) {
	full := BytesToSign(map[string]string{"a": "1", "b": ""}, "")
	trimmed := BytesToSign(map[string]string{"a": "1"}, "")
	if string(full) != string(trimmed) {
		t.Errorf("empty-value header not omitted:\n full    = %q\n trimmed = %q", string(full), string(trimmed))
	}
}

func TestBytesToSign_NoTrailingNewlineAdded(t *testing.T) {
	// Content without trailing newline must not gain one.
	got := BytesToSign(map[string]string{"k": "v"}, "abc")
	if got[len(got)-1] == '\n' {
		t.Errorf("unexpected trailing newline: %q", string(got))
	}
	// Content with trailing newline must preserve it exactly.
	got2 := BytesToSign(map[string]string{"k": "v"}, "abc\n")
	if got2[len(got2)-1] != '\n' {
		t.Errorf("trailing newline dropped: %q", string(got2))
	}
}
