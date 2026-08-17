package invites

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"syrinx/crypto"

	"github.com/google/uuid"
)

// InviteCreateSkew is how far a client-supplied createdAt may drift from
// server now on create.
const InviteCreateSkew = 5 * time.Minute

// HashSecret returns SHA-256(secret). Create sends this digest (hex); the
// server stores it. Redeem sends the raw secret; the server hashes and
// compares. The secret itself never appears on create.
func HashSecret(secret string) []byte {
	return crypto.Hash(secret)
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
	if len(b) != crypto.HashSize {
		return nil, fmt.Errorf("hash must be %d bytes", crypto.HashSize)
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

// NewInviteID returns a random UUIDv7 — the bare entity component of a
// canonical invite id (creatorID@serverID/uuid), same convention as reed ids.
func NewInviteID() (string, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return "", err
	}
	return id.String(), nil
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
