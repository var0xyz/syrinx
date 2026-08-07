package metrics

import (
	"encoding/hex"

	"syrinx/crypto"
)

// UserIDHash returns a stable SHA-256 hex digest of userID for metric
// attributes. Every user-scoped series uses `*.id_hash` (never raw user ids).
func UserIDHash(userID string) string {
	return hex.EncodeToString(crypto.Hash(userID))
}
