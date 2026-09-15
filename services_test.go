//go:build !ops

package main

import (
	"testing"

	"syrinx/crypto"
)

func TestGenerateUserID(t *testing.T) {
	const iterations = 1000

	allowed := make(map[byte]bool, len(crypto.Alphabet))
	for i := 0; i < len(crypto.Alphabet); i++ {
		allowed[crypto.Alphabet[i]] = true
	}

	seen := make(map[string]struct{}, iterations)
	for i := 0; i < iterations; i++ {
		id, err := generateUserID()
		if err != nil {
			t.Fatalf("generateUserID() error = %v", err)
		}
		if len(id) != crypto.Length {
			t.Fatalf("generateUserID() len = %d, want %d (id=%q)", len(id), crypto.Length, id)
		}
		for j := 0; j < len(id); j++ {
			if !allowed[id[j]] {
				t.Fatalf("generateUserID() produced disallowed byte %q in %q", id[j], id)
			}
		}
		if _, dup := seen[id]; dup {
			t.Fatalf("generateUserID() produced duplicate %q within %d iterations", id, iterations)
		}
		seen[id] = struct{}{}
	}
}

func TestGenerateServerIDLength(t *testing.T) {
	id, err := generateServerID()
	if err != nil {
		t.Fatalf("generateServerID() error = %v", err)
	}
	if len(id) != crypto.Length {
		t.Fatalf("generateServerID() len = %d, want %d", len(id), crypto.Length)
	}
}

func TestParseReedRef(t *testing.T) {
	tests := []struct {
		in     string
		ok     bool
		author string
		server string
		reedID string
	}{
		{"", false, "", "", ""},
		{"   ", false, "", "", ""},
		{"bareReedOnly", false, "", "", ""},
		{"author!reed", false, "", "", ""},
		{"@server/reed", false, "", "", ""},
		{"author@/reed", false, "", "", ""},
		{"author@server/", false, "", "", ""},
		{"author@server", false, "", "", ""},
		{"author@server/reed", true, "author", "server", "reed"},
		{"  author@server/reed  ", true, "author", "server", "reed"},
	}
	for _, tt := range tests {
		ref, ok := ParseReedRef(tt.in)
		if ok != tt.ok {
			t.Errorf("ParseReedRef(%q) ok=%v want %v", tt.in, ok, tt.ok)
			continue
		}
		if !ok {
			continue
		}
		if ref.AuthorID != tt.author || ref.ServerID != tt.server || ref.ReedID != tt.reedID {
			t.Errorf("ParseReedRef(%q) = %+v want author=%q server=%q reed=%q",
				tt.in, ref, tt.author, tt.server, tt.reedID)
		}
	}
}

func TestFormatReedRef(t *testing.T) {
	got := FormatReedRef(ReedRef{AuthorID: "a", ServerID: "s", ReedID: "r"})
	if got != "a@s/r" {
		t.Fatalf("got %q", got)
	}
}

func TestCountMarkdownCharacters(t *testing.T) {
	if got := CountMarkdownCharacters("*bold*"); got != 4 {
		t.Fatalf("got %d want 4", got)
	}
}
