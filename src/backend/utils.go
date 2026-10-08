package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"unicode"

	"github.com/gowebpki/jcs"
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

// signedFields is the field set of a signed payload, keyed by JSON name.
type signedFields map[string]any

// canonicalJSON returns the RFC 8785 (JCS) bytes signed over fields. Empty
// strings, nils and empty lists are dropped, so absent and empty sign the
// same; false and 0 are kept. Mirrored by canonicalJSON in signing.ts.
func canonicalJSON(fields signedFields) []byte {
	kept := make(map[string]any, len(fields))
	for k, v := range fields {
		if !isEmptySignedValue(v) {
			kept[k] = v
		}
	}
	raw, err := json.Marshal(kept)
	if err != nil {
		panic(fmt.Sprintf("canonicalJSON: marshal: %v", err))
	}
	out, err := jcs.Transform(raw)
	if err != nil {
		panic(fmt.Sprintf("canonicalJSON: transform: %v", err))
	}
	return out
}

func isEmptySignedValue(v any) bool {
	if v == nil {
		return true
	}
	if s, ok := v.(string); ok {
		return s == ""
	}
	rv := reflect.ValueOf(v)
	return rv.Kind() == reflect.Slice && rv.Len() == 0
}

// frontendURLFromOrigin is this server's frontend URL, from ALLOWED_ORIGIN.
func frontendURLFromOrigin(origin string) string {
	return strings.TrimRight(strings.TrimSpace(origin), "/")
}
