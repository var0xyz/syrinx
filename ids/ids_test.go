package ids

import (
	"testing"

	"github.com/google/uuid"
)

func TestNewLengthAndAlphabet(t *testing.T) {
	allowed := make(map[byte]bool, len(Alphabet))
	for i := 0; i < len(Alphabet); i++ {
		allowed[Alphabet[i]] = true
	}
	seen := make(map[string]struct{}, 1000)
	for i := 0; i < 1000; i++ {
		id, err := New()
		if err != nil {
			t.Fatalf("New() error = %v", err)
		}
		if len(id) != Length {
			t.Fatalf("New() len = %d, want %d (id=%q)", len(id), Length, id)
		}
		if !Valid(id) {
			t.Fatalf("New() produced id that Valid rejects: %q", id)
		}
		for j := 0; j < len(id); j++ {
			if !allowed[id[j]] {
				t.Fatalf("New() produced disallowed byte %q in %q", id[j], id)
			}
		}
		if _, dup := seen[id]; dup {
			t.Fatalf("New() duplicate %q within 1000 iterations", id)
		}
		seen[id] = struct{}{}
	}
}

func TestValidRejects(t *testing.T) {
	cases := []string{"", "short", "toolong01", "bad-char", "!!!!!!!!"}
	for _, id := range cases {
		if Valid(id) {
			t.Fatalf("Valid(%q) = true, want false", id)
		}
	}
}

func TestValidReed(t *testing.T) {
	id, err := uuid.NewV7()
	if err != nil {
		t.Fatal(err)
	}
	if !ValidReed(id.String()) {
		t.Fatalf("ValidReed rejected v7 %q", id)
	}

	v4, err := uuid.NewRandom()
	if err != nil {
		t.Fatal(err)
	}
	if ValidReed(v4.String()) {
		t.Fatalf("ValidReed accepted v4 %q", v4)
	}

	random8, err := New()
	if err != nil {
		t.Fatal(err)
	}
	if ValidReed(random8) {
		t.Fatalf("ValidReed accepted random id %q", random8)
	}
}
