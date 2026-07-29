// Package ids generates and validates Syrinx entity identifiers.
// Server, user, reed, and invite IDs share the same alphabet and length.
package ids

import (
	"crypto/rand"
	"math/big"
)

// Alphabet is the character set for entity IDs.
const Alphabet = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"

// Length is the length of server, user, reed, and invite IDs.
const Length = 8

// New returns a cryptographically random ID of Length characters from Alphabet.
func New() (string, error) {
	result := make([]byte, Length)
	alphabetLen := big.NewInt(int64(len(Alphabet)))
	for i := range result {
		idx, err := rand.Int(rand.Reader, alphabetLen)
		if err != nil {
			return "", err
		}
		result[i] = Alphabet[idx.Int64()]
	}
	return string(result), nil
}

// Valid reports whether id has the correct length and alphabet.
func Valid(id string) bool {
	if len(id) != Length {
		return false
	}
	for i := 0; i < len(id); i++ {
		c := id[i]
		if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')) {
			return false
		}
	}
	return true
}
