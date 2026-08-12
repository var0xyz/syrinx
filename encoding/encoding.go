// Package encoding wraps the project's one wire/file encoding rule: base64
// (standard alphabet) for anything serialized — HTTP fields, headers, backup
// files — never nested base64-of-base64. See docs/cryptography.md.
package encoding

import "encoding/base64"

// Base64Encode encodes s as base64 (standard alphabet) for the wire.
func Base64Encode(s string) string {
	return base64.StdEncoding.EncodeToString([]byte(s))
}

// Base64Decode decodes a base64 (standard alphabet) string back to plain text.
func Base64Decode(s string) (string, error) {
	raw, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}
