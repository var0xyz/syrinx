package main

import (
	"encoding/base64"
	"sort"
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

// ========== //
//   signing   //
// ========== //

// bytesToSign builds the canonical byte sequence for a signed record.
//
// Design: opaque signing input, not a document. The bytes look like a
// markdown envelope with a front-matter header block:
//
//	---
//	<sortedKey>: <value>
//	<sortedKey>: <value>
//	---
//	<content>
//
// This shape is convenient for humans reading a hex dump in a debugger,
// but the bytes are NOT a document — nothing ever parses them back into
// headers/content. They exist for one purpose: to be signed on one side
// and re-produced identically on the other side for verification.
//
// Rules:
//   - Headers are sorted ASCII byte-lexicographically to match
//     sort.Strings in Go and Array.prototype.sort with the default
//     comparator in JavaScript — the SPA has a mirror function
//     `bytesToSign` and the two MUST be byte-identical for signature
//     verification to work.
//   - Header entries with an empty-string value are omitted (whole line
//     dropped). Absent and empty are equivalent by convention.
//   - No escaping. Values are inserted as-is. This is safe because no
//     code splits the output on '\n' or on ": " to recover fields — the
//     receiver rebuilds the same input map and calls bytesToSign again.
//   - Line separator is a single "\n" (LF).
//   - Each header line is exactly "<key>: <value>".
//   - The output is:  "---\n" + joined headers + "\n---\n" + content
//     with no trailing newline added; if content itself ends with "\n"
//     that is preserved verbatim.
//
// A future contributor "hardening" this helper by adding an escape table
// would silently break signature compatibility with every record already
// signed against the current bytes. Do not do that.
func bytesToSign(headers map[string]string, content string) []byte {
	keys := make([]string, 0, len(headers))
	for k, v := range headers {
		if v == "" {
			continue
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)

	// Preallocate: "---\n" + "---\n" + content + per-key overhead.
	size := 4 + 4 + len(content)
	for _, k := range keys {
		size += len(k) + 2 + len(headers[k]) + 1
	}
	out := make([]byte, 0, size)

	out = append(out, "---\n"...)
	for i, k := range keys {
		if i > 0 {
			out = append(out, '\n')
		}
		out = append(out, k...)
		out = append(out, ':', ' ')
		out = append(out, headers[k]...)
	}
	if len(keys) > 0 {
		out = append(out, '\n')
	}
	out = append(out, "---\n"...)
	out = append(out, content...)
	return out
}
