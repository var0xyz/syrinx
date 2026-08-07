package crypto

import (
	gocrypto "crypto/rand"
	"math/big"

	"github.com/google/uuid"
)

// Alphabet is the character set for entity IDs.
const Alphabet = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"

// Length is the length of server, user, and invite IDs.
const Length = 8

// NewID returns a cryptographically random ID of Length characters from Alphabet.
func NewID() (string, error) {
	result := make([]byte, Length)
	alphabetLen := big.NewInt(int64(len(Alphabet)))
	for i := range result {
		idx, err := gocrypto.Int(gocrypto.Reader, alphabetLen)
		if err != nil {
			return "", err
		}
		result[i] = Alphabet[idx.Int64()]
	}
	return string(result), nil
}

// IsValidID reports whether id has the correct length and alphabet.
func IsValidID(id string) bool {
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

// IsValidUUIDv7 reports whether id is a UUID version 7 (time-ordered reed id).
func IsValidUUIDv7(id string) bool {
	u, err := uuid.Parse(id)
	if err != nil {
		return false
	}
	return u.Version() == 7
}
