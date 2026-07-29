package invites

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"syrinx/ids"
)

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

// NewInviteID returns a random invite id (same alphabet/length as user IDs).
func NewInviteID() (string, error) {
	return ids.New()
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
