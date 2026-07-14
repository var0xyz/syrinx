// Package signing produces deterministic byte sequences to feed into PGP
// signing and verification.
//
// Design: opaque signing input, not a document
//
// BytesToSign returns bytes that look like a markdown envelope with a
// front-matter header block:
//
//	---
//	<sortedKey>: <value>
//	<sortedKey>: <value>
//	---
//	<content>
//
// This shape is convenient for humans reading a hex dump in a debugger, but
// the bytes are NOT a document. Nothing in Syrinx ever parses them back into
// headers/content. They exist for one purpose: to be signed on one side and
// re-produced identically on the other side for verification.
//
// Consequences of that design choice:
//
//  1. No escaping. Values are inserted verbatim. If a value contains a
//     literal '\n', ':', or '---' sequence, those bytes appear in the output
//     as-is. This is safe because no code splits the output on '\n' or on
//     ": " to recover fields — the receiver rebuilds the same input map and
//     calls BytesToSign again.
//
//  2. Empty-string values are omitted (whole line dropped). Absent and
//     empty are equivalent by convention.
//
//  3. Keys are sorted ASCII byte-lexicographically to match sort.Strings in
//     Go and Array.prototype.sort with the default comparator in JavaScript
//     — the SPA has a mirror function `bytesToSign` and the two MUST be
//     byte-identical for signature verification to work.
//
//  4. The return type is []byte, not string, to nudge callers away from
//     treating the output as text or trying to parse it.
//
// A future contributor "hardening" this helper by adding an escape table
// would silently break signature compatibility with every record already
// signed against the current bytes. Do not do that.
package signing

import "sort"

// BytesToSign builds the canonical byte sequence for a signed record.
//
// Rules (see package doc for rationale):
//   - Headers are sorted ASCII byte-lexicographically.
//   - Header entries with an empty-string value are omitted.
//   - Line separator is a single "\n" (LF).
//   - Each header line is exactly "<key>: <value>".
//   - The output is:  "---\n" + joined headers + "\n---\n" + content
//     with no trailing newline added; if content itself ends with "\n"
//     that is preserved verbatim.
//   - No escaping. Values are inserted as-is.
func BytesToSign(headers map[string]string, content string) []byte {
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
