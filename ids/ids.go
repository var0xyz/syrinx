// Package ids generates and validates Syrinx entity identifiers.
// Server, user, and invite IDs are random 8-char strings; reed IDs are UUID v7.
package ids

import (
	"crypto/rand"
	"math/big"

	"github.com/google/uuid"
)

// Alphabet is the character set for entity IDs.
const Alphabet = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"

// Length is the length of server, user, and invite IDs.
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

// ValidReed reports whether id is a UUID version 7 (time-ordered reed id).
func ValidReed(id string) bool {
	u, err := uuid.Parse(id)
	if err != nil {
		return false
	}
	return u.Version() == 7
}
