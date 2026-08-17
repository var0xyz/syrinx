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

// EventIDHash returns a stable SHA-256 hex digest of a pending relay event
// id for metric attributes — the id embeds the requesting user's identity
// (requesterUserID@serverID/uuid), so it's hashed like a user id rather
// than emitted raw. Lets separate created/fulfilled/deleted lifecycle
// counters for the same event be correlated by this hash without exposing
// who requested it.
func EventIDHash(eventID string) string {
	return hex.EncodeToString(crypto.Hash(eventID))
}
