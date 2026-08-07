package crypto

import (
	"testing"

	"github.com/google/uuid"
)

func TestNewIDLengthAndAlphabet(t *testing.T) {
	allowed := make(map[byte]bool, len(Alphabet))
	for i := 0; i < len(Alphabet); i++ {
		allowed[Alphabet[i]] = true
	}
	seen := make(map[string]struct{}, 1000)
	for i := 0; i < 1000; i++ {
		id, err := NewID()
		if err != nil {
			t.Fatalf("NewID() error = %v", err)
		}
		if len(id) != Length {
			t.Fatalf("NewID() len = %d, want %d (id=%q)", len(id), Length, id)
		}
		if !IsValidID(id) {
			t.Fatalf("NewID() produced id that IsValidID rejects: %q", id)
		}
		for j := 0; j < len(id); j++ {
			if !allowed[id[j]] {
				t.Fatalf("NewID() produced disallowed byte %q in %q", id[j], id)
			}
		}
		if _, dup := seen[id]; dup {
			t.Fatalf("NewID() duplicate %q within 1000 iterations", id)
		}
		seen[id] = struct{}{}
	}
}

func TestIsValidIDRejects(t *testing.T) {
	cases := []string{"", "short", "toolong01", "bad-char", "!!!!!!!!"}
	for _, id := range cases {
		if IsValidID(id) {
			t.Fatalf("IsValidID(%q) = true, want false", id)
		}
	}
}

func TestIsValidUUIDv7(t *testing.T) {
	id, err := uuid.NewV7()
	if err != nil {
		t.Fatal(err)
	}
	if !IsValidUUIDv7(id.String()) {
		t.Fatalf("IsValidUUIDv7 rejected v7 %q", id)
	}

	v4, err := uuid.NewRandom()
	if err != nil {
		t.Fatal(err)
	}
	if IsValidUUIDv7(v4.String()) {
		t.Fatalf("IsValidUUIDv7 accepted v4 %q", v4)
	}

	random8, err := NewID()
	if err != nil {
		t.Fatal(err)
	}
	if IsValidUUIDv7(random8) {
		t.Fatalf("IsValidUUIDv7 accepted random id %q", random8)
	}
}
