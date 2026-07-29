//go:build !ops

package main

import (
	"testing"

	"syrinx/ids"
)

func TestMarkdownService_ExtractReedHeader(t *testing.T) {
	service := NewMarkdownService()

	tests := []struct {
		name     string
		reed     string
		expected ReedHeader
	}{
		{
			name: "complete header with all fields",
			reed: `---
id: reed123
userID: testuser
replying: post123
echoing: post456
---
Content here`,
			expected: ReedHeader{
				ID:       "reed123",
				UserID:   "testuser",
				Replying: "post123",
				Echoing:  "post456",
			},
		},
		{
			name: "minimal header with only mandatory fields",
			reed: `---
id: reed123
userID: testuser
---
Content here`,
			expected: ReedHeader{
				ID:       "reed123",
				UserID:   "testuser",
				Replying: "",
				Echoing:  "",
			},
		},
		{
			name: "header with only replying field",
			reed: `---
id: reed123
userID: testuser
replying: post123
---
Content here`,
			expected: ReedHeader{
				ID:       "reed123",
				UserID:   "testuser",
				Replying: "post123",
				Echoing:  "",
			},
		},
		{
			name: "header with only echoing field",
			reed: `---
id: reed123
userID: testuser
echoing: post456
---
Content here`,
			expected: ReedHeader{
				ID:       "reed123",
				UserID:   "testuser",
				Replying: "",
				Echoing:  "post456",
			},
		},
		{
			name: "empty reed",
			reed: "",
			expected: ReedHeader{
				ID:       "",
				UserID:   "",
				Replying: "",
				Echoing:  "",
			},
		},
		{
			name: "reed without header markers",
			reed: `id: reed123
userID: testuser
Content here`,
			expected: ReedHeader{
				ID:       "",
				UserID:   "",
				Replying: "",
				Echoing:  "",
			},
		},
		{
			name: "reed with only opening header marker",
			reed: `---
id: reed123
userID: testuser
Content here`,
			expected: ReedHeader{
				ID:       "reed123",
				UserID:   "testuser",
				Replying: "",
				Echoing:  "",
			},
		},
		{
			name: "header with extra whitespace",
			reed: `---
id:  reed123
userID:  testuser
---
Content here`,
			expected: ReedHeader{
				ID:       "reed123",
				UserID:   "testuser",
				Replying: "",
				Echoing:  "",
			},
		},
		{
			name: "header with duplicate fields (last one wins)",
			reed: `---
id: reed123
userID: testuser
id: reed456
userID: anotheruser
---
Content here`,
			expected: ReedHeader{
				ID:       "reed456",
				UserID:   "anotheruser",
				Replying: "",
				Echoing:  "",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := service.ExtractReedHeader(tt.reed)
			if result != tt.expected {
				t.Errorf("ExtractReedHeader() = %+v, want %+v", result, tt.expected)
			}
		})
	}
}

func TestMarkdownService_ValidateReedHeader(t *testing.T) {
	service := NewMarkdownService()

	tests := []struct {
		name    string
		reed    string
		wantErr bool
		errMsg  string
	}{
		{
			name: "valid header with all mandatory fields",
			reed: `---
id: reed123
userID: testuser
---
Content here`,
			wantErr: false,
		},
		{
			name: "valid header with optional fields",
			reed: `---
id: reed123
userID: testuser
replying: post123
echoing: post456
---
Content here`,
			wantErr: false,
		},
		{
			name: "missing id field",
			reed: `---
userID: testuser
---
Content here`,
			wantErr: true,
			errMsg:  "mandatory headers missing",
		},
		{
			name: "missing userID field",
			reed: `---
id: reed123
---
Content here`,
			wantErr: true,
			errMsg:  "mandatory headers missing",
		},
		{
			name: "missing all mandatory fields",
			reed: `---
replying: post123
echoing: post456
---
Content here`,
			wantErr: true,
			errMsg:  "mandatory headers missing",
		},
		{
			name: "unrecognized header field",
			reed: `---
id: reed123
userID: testuser
invalid: field
---
Content here`,
			wantErr: true,
			errMsg:  "unrecognized header: invalid",
		},
		{
			name: "invalid header format (no colon)",
			reed: `---
id: reed123
userID: testuser
invalid field
---
Content here`,
			wantErr: true,
			errMsg:  "invalid header format: invalid field",
		},
		{
			name:    "empty reed",
			reed:    "",
			wantErr: true,
			errMsg:  "mandatory headers missing",
		},
		{
			name: "reed without header markers",
			reed: `id: reed123
userID: testuser
Content here`,
			wantErr: true,
			errMsg:  "mandatory headers missing",
		},
		{
			name: "reed with only opening header marker",
			reed: `---
id: reed123
userID: testuser
Content here`,
			wantErr: true, // This should fail because content after header without closing --- is invalid
			errMsg:  "invalid header format",
		},
		{
			name: "header with extra whitespace",
			reed: `---
id:  reed123
userID:  testuser
---
Content here`,
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := service.ValidateReedHeader(tt.reed)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateReedHeader() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if tt.wantErr && err != nil && tt.errMsg != "" {
				if err.Error()[:len(tt.errMsg)] != tt.errMsg {
					t.Errorf("ValidateReedHeader() error = %v, want error containing %v", err, tt.errMsg)
				}
			}
		})
	}
}

func TestMarkdownService_ExtractReedContent(t *testing.T) {
	service := NewMarkdownService()

	tests := []struct {
		name     string
		reed     string
		expected string
	}{
		{
			name: "content after header",
			reed: `---
id: reed123
userID: testuser
---
This is the content
With multiple lines
And more text`,
			expected: `This is the content
With multiple lines
And more text`,
		},
		{
			name: "content with empty lines",
			reed: `---
id: reed123
userID: testuser
---

This is the content

With empty lines

And more text`,
			expected: `This is the content

With empty lines

And more text`,
		},
		{
			name: "content with only header",
			reed: `---
id: reed123
userID: testuser
---`,
			expected: "",
		},
		{
			name: "content without header markers",
			reed: `id: reed123
userID: testuser
This is the content`,
			expected: "",
		},
		{
			name: "content with only opening header marker",
			reed: `---
id: reed123
userID: testuser
This is the content`,
			expected: ``, // Content extraction requires proper header closing
		},
		{
			name:     "empty reed",
			reed:     "",
			expected: "",
		},
		{
			name: "content with multiple header sections",
			reed: `---
id: reed123
userID: testuser
---
First content section
---
Second header
---
Second content section`,
			expected: `First content section
---
Second header
---
Second content section`,
		},
		{
			name: "content with whitespace",
			reed: `---
id: reed123
userID: testuser
---

    Indented content
    With spaces
    And tabs`,
			expected: `Indented content
    With spaces
    And tabs`, // TrimSpace removes leading whitespace
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := service.ExtractReedContent(tt.reed)
			if result != tt.expected {
				t.Errorf("ExtractReedContent() = %q, want %q", result, tt.expected)
			}
		})
	}
}

func TestMarkdownService_ParseMarkdown(t *testing.T) {
	service := NewMarkdownService()

	tests := []struct {
		name     string
		reed     string
		expected string
	}{
		{
			name:     "bold text",
			reed:     "This is *bold* text",
			expected: "This is bold text",
		},
		{
			name:     "italic text",
			reed:     "This is _italic_ text",
			expected: "This is italic text",
		},
		{
			name:     "strikethrough text",
			reed:     "This is ~strikethrough~ text",
			expected: "This is strikethrough text",
		},
		{
			name:     "mixed formatting",
			reed:     "This is *bold* and _italic_ and ~strikethrough~ text",
			expected: "This is bold and italic and strikethrough text",
		},
		{
			name:     "nested formatting",
			reed:     "This is *bold _italic_ bold* text",
			expected: "This is bold italic bold text", // The regex processes all formatting
		},
		{
			name:     "link with text",
			reed:     "This is a [link text](https://example.com) in the content",
			expected: "This is a link text in the content",
		},
		{
			name:     "multiple links",
			reed:     "Here are [link1](url1) and [link2](url2) links",
			expected: "Here are link1 and link2 links",
		},
		{
			name:     "mixed markdown and links",
			reed:     "This is *bold* text with [a link](url) and _italic_ text",
			expected: "This is bold text with a link and italic text",
		},
		{
			name:     "empty string",
			reed:     "",
			expected: "",
		},
		{
			name:     "text without markdown",
			reed:     "This is plain text without any formatting",
			expected: "This is plain text without any formatting",
		},
		{
			name:     "unclosed formatting",
			reed:     "This is *bold text without closing",
			expected: "This is *bold text without closing",
		},
		{
			name:     "unclosed link",
			reed:     "This is [unclosed link text",
			expected: "This is [unclosed link text",
		},
		{
			name:     "complex formatting",
			reed:     "Check out [this *bold* link](url) and _italic_ text with ~strikethrough~",
			expected: "Check out this bold link and italic text with strikethrough", // Links are processed first, then formatting
		},
		{
			name:     "multiple asterisks",
			reed:     "This has ***triple asterisks*** and **double**",
			expected: "This has triple asterisks and double", // All formatting is processed
		},
		{
			name:     "multiple underscores",
			reed:     "This has ___triple underscores___ and __double__",
			expected: "This has triple underscores and double", // All formatting is processed
		},
		{
			name:     "multiple tildes",
			reed:     "This has ~~~triple tildes~~~ and ~~double~~",
			expected: "This has triple tildes and double", // All formatting is processed
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := service.ParseMarkdown(tt.reed)
			if result != tt.expected {
				t.Errorf("ParseMarkdown() = %q, want %q", result, tt.expected)
			}
		})
	}
}

func TestMarkdownService_NewMarkdownService(t *testing.T) {
	service := NewMarkdownService()
	if service == nil {
		t.Error("NewMarkdownService() returned nil")
	}
}

// Test edge cases and error conditions
func TestMarkdownService_EdgeCases(t *testing.T) {
	service := NewMarkdownService()

	t.Run("ExtractReedHeader with malformed header", func(t *testing.T) {
		reed := `---
id: reed123
userID: testuser
malformed line without colon
---
Content here`

		result := service.ExtractReedHeader(reed)
		// Should still extract valid fields and ignore malformed ones
		expected := ReedHeader{
			ID:       "reed123",
			UserID:   "testuser",
			Replying: "",
			Echoing:  "",
		}
		if result != expected {
			t.Errorf("ExtractReedHeader() with malformed header = %+v, want %+v", result, expected)
		}
	})

	t.Run("ValidateReedHeader with malformed header", func(t *testing.T) {
		reed := `---
id: reed123
userID: testuser
malformed line without colon
---
Content here`

		err := service.ValidateReedHeader(reed)
		if err == nil {
			t.Error("ValidateReedHeader() with malformed header should return error")
		}
		if err.Error() != "invalid header format: malformed line without colon" {
			t.Errorf("ValidateReedHeader() error = %v, want 'invalid header format: malformed line without colon'", err)
		}
	})

	t.Run("ExtractReedContent with only header markers", func(t *testing.T) {
		reed := `---
---`

		result := service.ExtractReedContent(reed)
		if result != "" {
			t.Errorf("ExtractReedContent() with only header markers = %q, want empty string", result)
		}
	})

	t.Run("ParseMarkdown with special regex characters", func(t *testing.T) {
		reed := "This has [special chars: .*+?^${}()|[]\\](url) and *bold* text"
		result := service.ParseMarkdown(reed)
		expected := "This has special chars: .+?^${}()|[]\\ and bold* text" // Regex escapes affect the result
		if result != expected {
			t.Errorf("ParseMarkdown() with special chars = %q, want %q", result, expected)
		}
	})
}

func TestGenerateUserID(t *testing.T) {
	const iterations = 1000

	allowed := make(map[byte]bool, len(ids.Alphabet))
	for i := 0; i < len(ids.Alphabet); i++ {
		allowed[ids.Alphabet[i]] = true
	}

	seen := make(map[string]struct{}, iterations)
	for i := 0; i < iterations; i++ {
		id, err := generateUserID()
		if err != nil {
			t.Fatalf("generateUserID() error = %v", err)
		}
		if len(id) != ids.Length {
			t.Fatalf("generateUserID() len = %d, want %d (id=%q)", len(id), ids.Length, id)
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
	if len(id) != ids.Length {
		t.Fatalf("generateServerID() len = %d, want %d", len(id), ids.Length)
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

func TestReedContentWithinLimits(t *testing.T) {
	if !ReedContentWithinLimits("hello") {
		t.Fatal("short content should pass")
	}
	raw := make([]byte, MaxReedRawChars+1)
	for i := range raw {
		raw[i] = 'a'
	}
	if ReedContentWithinLimits(string(raw)) {
		t.Fatal("over raw limit should fail")
	}
	visible := make([]byte, MaxReedVisibleChars+1)
	for i := range visible {
		visible[i] = 'b'
	}
	if ReedContentWithinLimits(string(visible)) {
		t.Fatal("over visible limit should fail")
	}
}

func TestCountMarkdownCharacters(t *testing.T) {
	if got := CountMarkdownCharacters("*bold*"); got != 4 {
		t.Fatalf("got %d want 4", got)
	}
}

func TestReedAsMarkdown(t *testing.T) {
	got := ReedAsMarkdown("rid", "uid", "hello", "a@s/b", "")
	want := "---\nechoing: a@s/b\nid: rid\nuserID: uid\n---\nhello"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}
