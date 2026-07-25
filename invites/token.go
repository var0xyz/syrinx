package invites

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"math/big"
	"strings"
	"time"
)

const inviteIDAlphabet = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"

// InviteCreateSkew is how far a client-supplied createdAt may drift from
// server now on create.
const InviteCreateSkew = 5 * time.Minute

// HashSecret returns SHA-256(secret). Create sends this digest (hex); the
// server stores it. Redeem sends the raw secret; the server hashes and
// compares. The secret itself never appears on create.
func HashSecret(secret string) []byte {
	sum := sha256.Sum256([]byte(secret))
	return sum[:]
}

// EncodeHashHex encodes a 32-byte digest as lowercase hex (wire / signed header).
func EncodeHashHex(digest []byte) string {
	return hex.EncodeToString(digest)
}

// DecodeHashHex parses a 32-byte digest from hex.
func DecodeHashHex(s string) ([]byte, error) {
	b, err := hex.DecodeString(strings.TrimSpace(s))
	if err != nil {
		return nil, err
	}
	if len(b) != sha256.Size {
		return nil, fmt.Errorf("hash must be %d bytes", sha256.Size)
	}
	return b, nil
}

// NewSecret returns a URL-fragment-safe raw secret (≥256 bits).
func NewSecret() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
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

// ValidInviteID reports whether id matches the client-mint alphabet/length.
func ValidInviteID(id string) bool {
	if len(id) != 12 {
		return false
	}
	for _, r := range id {
		if !strings.ContainsRune(inviteIDAlphabet, r) {
			return false
		}
	}
	return true
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

// Revoke / insert outcome errors for handlers.
var (
	ErrInviteNotFound       = fmt.Errorf("invite not found")
	ErrInviteNotOwner       = fmt.Errorf("invite not owned by caller")
	ErrInviteAlreadyClaimed = fmt.Errorf("invite already claimed")
	ErrInviteAlreadyRevoked = fmt.Errorf("invite already revoked")
	ErrInviteExists         = fmt.Errorf("invite already exists")
)
