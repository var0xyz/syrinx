//go:build !ops && !ripplescleanup

package main

import (
	"encoding/base64"
	"strings"
	"unicode"
)

// trimInvisibleChars removes invisible characters from a string
func trimInvisibleChars(s string) string {
	// First trim standard whitespace
	s = strings.TrimSpace(s)

	// Remove invisible characters (non-printable characters except spaces)
	var result strings.Builder
	for _, r := range s {
		if unicode.IsPrint(r) || r == ' ' {
			result.WriteRune(r)
		}
	}

	// Trim spaces again after removing invisible chars
	return strings.TrimSpace(result.String())
}

// ============ //
//   encoding   //
// ============ //

// base64Encode encodes s as base64 (standard alphabet) for the wire.
func base64Encode(s string) string {
	return base64.StdEncoding.EncodeToString([]byte(s))
}

// base64Decode decodes a base64 (standard alphabet) string back to plain text.
func base64Decode(s string) (string, error) {
	raw, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}
