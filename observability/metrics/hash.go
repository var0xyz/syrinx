package metrics

import (
	"crypto/sha256"
	"encoding/hex"
)

// UserIDHash returns a stable SHA-256 hex digest of userID for metric
// attributes where raw IDs must not appear (e.g. backup telemetry).
func UserIDHash(userID string) string {
	sum := sha256.Sum256([]byte(userID))
	return hex.EncodeToString(sum[:])
}
