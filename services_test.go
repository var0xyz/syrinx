package main

import (
	"testing"
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
date: 2024-01-15
author: testuser
origin: example.com
replying: post123
echoing: post456
---
Content here`,
			expected: ReedHeader{
				Date:     "2024-01-15",
				Author:   "testuser",
				Origin:   "example.com",
				Replying: "post123",
				Echoing:  "post456",
			},
		},
		{
			name: "minimal header with only mandatory fields",
			reed: `---
date: 2024-01-15
author: testuser
origin: example.com
---
Content here`,
			expected: ReedHeader{
				Date:     "2024-01-15",
				Author:   "testuser",
				Origin:   "example.com",
				Replying: "",
				Echoing:  "",
			},
		},
		{
			name: "header with only replying field",
			reed: `---
date: 2024-01-15
author: testuser
origin: example.com
replying: post123
---
Content here`,
			expected: ReedHeader{
				Date:     "2024-01-15",
				Author:   "testuser",
				Origin:   "example.com",
				Replying: "post123",
				Echoing:  "",
			},
		},
		{
			name: "header with only echoing field",
			reed: `---
date: 2024-01-15
author: testuser
origin: example.com
echoing: post456
---
Content here`,
			expected: ReedHeader{
				Date:     "2024-01-15",
				Author:   "testuser",
				Origin:   "example.com",
				Replying: "",
				Echoing:  "post456",
			},
		},
		{
			name: "empty reed",
			reed: "",
			expected: ReedHeader{
				Date:     "",
				Author:   "",
				Origin:   "",
				Replying: "",
				Echoing:  "",
			},
		},
		{
			name: "reed without header markers",
			reed: `date: 2024-01-15
author: testuser
origin: example.com
Content here`,
			expected: ReedHeader{
				Date:     "",
				Author:   "",
				Origin:   "",
				Replying: "",
				Echoing:  "",
			},
		},
		{
			name: "reed with only opening header marker",
			reed: `---
date: 2024-01-15
author: testuser
origin: example.com
Content here`,
			expected: ReedHeader{
				Date:     "2024-01-15",
				Author:   "testuser",
				Origin:   "example.com",
				Replying: "",
				Echoing:  "",
			},
		},
		{
			name: "header with extra whitespace",
			reed: `---
date:  2024-01-15
author:  testuser
origin:  example.com
---
Content here`,
			expected: ReedHeader{
				Date:     "2024-01-15",
				Author:   "testuser",
				Origin:   "example.com",
				Replying: "",
				Echoing:  "",
			},
		},
		{
			name: "header with duplicate fields (last one wins)",
			reed: `---
date: 2024-01-15
author: testuser
origin: example.com
date: 2024-01-16
author: anotheruser
---
Content here`,
			expected: ReedHeader{
				Date:     "2024-01-16",
				Author:   "anotheruser",
				Origin:   "example.com",
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
date: 2024-01-15
author: testuser
origin: example.com
---
Content here`,
			wantErr: false,
		},
		{
			name: "valid header with optional fields",
			reed: `---
date: 2024-01-15
author: testuser
origin: example.com
replying: post123
echoing: post456
---
Content here`,
			wantErr: false,
		},
		{
			name: "missing date field",
			reed: `---
author: testuser
origin: example.com
---
Content here`,
			wantErr: true,
			errMsg:  "mandatory headers missing",
		},
		{
			name: "missing author field",
			reed: `---
date: 2024-01-15
origin: example.com
---
Content here`,
			wantErr: true,
			errMsg:  "mandatory headers missing",
		},
		{
			name: "missing origin field",
			reed: `---
date: 2024-01-15
author: testuser
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
date: 2024-01-15
author: testuser
origin: example.com
invalid: field
---
Content here`,
			wantErr: true,
			errMsg:  "unrecognized header: invalid",
		},
		{
			name: "invalid header format (no colon)",
			reed: `---
date: 2024-01-15
author: testuser
origin: example.com
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
			reed: `date: 2024-01-15
author: testuser
origin: example.com
Content here`,
			wantErr: true,
			errMsg:  "mandatory headers missing",
		},
		{
			name: "reed with only opening header marker",
			reed: `---
date: 2024-01-15
author: testuser
origin: example.com
Content here`,
			wantErr: true, // This should fail because content after header without closing --- is invalid
			errMsg:  "invalid header format",
		},
		{
			name: "header with extra whitespace",
			reed: `---
date:  2024-01-15
author:  testuser
origin:  example.com
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
date: 2024-01-15
author: testuser
origin: example.com
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
date: 2024-01-15
author: testuser
origin: example.com
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
date: 2024-01-15
author: testuser
origin: example.com
---`,
			expected: "",
		},
		{
			name: "content without header markers",
			reed: `date: 2024-01-15
author: testuser
origin: example.com
This is the content`,
			expected: "",
		},
		{
			name: "content with only opening header marker",
			reed: `---
date: 2024-01-15
author: testuser
origin: example.com
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
date: 2024-01-15
author: testuser
origin: example.com
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
date: 2024-01-15
author: testuser
origin: example.com
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
date: 2024-01-15
author: testuser
origin: example.com
malformed line without colon
---
Content here`

		result := service.ExtractReedHeader(reed)
		// Should still extract valid fields and ignore malformed ones
		expected := ReedHeader{
			Date:     "2024-01-15",
			Author:   "testuser",
			Origin:   "example.com",
			Replying: "",
			Echoing:  "",
		}
		if result != expected {
			t.Errorf("ExtractReedHeader() with malformed header = %+v, want %+v", result, expected)
		}
	})

	t.Run("ValidateReedHeader with malformed header", func(t *testing.T) {
		reed := `---
date: 2024-01-15
author: testuser
origin: example.com
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
