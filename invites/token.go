package invites

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"math/big"
)

const inviteIDAlphabet = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"

// HashToken returns SHA-256(raw) for durable storage. Never log or persist raw.
func HashToken(raw string) []byte {
	sum := sha256.Sum256([]byte(raw))
	return sum[:]
}

// NewToken returns a URL-safe raw token (≥256 bits) and its SHA-256 hash.
func NewToken() (raw string, hash []byte, err error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", nil, err
	}
	raw = base64.RawURLEncoding.EncodeToString(buf)
	return raw, HashToken(raw), nil
}

// NewInviteID returns a 12-character ID using the same alphabet as user IDs.
func NewInviteID() (string, error) {
	result := make([]byte, 12)
	alphabetLen := big.NewInt(int64(len(inviteIDAlphabet)))
	for i := range result {
		idx, err := rand.Int(rand.Reader, alphabetLen)
		if err != nil {
			return "", err
		}
		result[i] = inviteIDAlphabet[idx.Int64()]
	}
	return string(result), nil
}

// Status derives the invite read-model status (revoked wins over claimed).
func (inv Invite) Status() string {
	if inv.RevokedAt != nil {
		return "revoked"
	}
	if inv.ClaimedAt != nil {
		return "claimed"
	}
	return "pending"
}

// Revoke outcome errors for handlers.
var (
	ErrInviteNotFound       = fmt.Errorf("invite not found")
	ErrInviteNotOwner       = fmt.Errorf("invite not owned by caller")
	ErrInviteAlreadyClaimed = fmt.Errorf("invite already claimed")
	ErrInviteAlreadyRevoked = fmt.Errorf("invite already revoked")
)
