package recovery

import "time"

// UserSignature is the nested user attestation wire block.
type UserSignature struct {
	Fingerprint string `json:"fingerprint"`
	Armor       string `json:"armor"`
}

// ServerSignature is the server's countersignature metadata on a signed
// resource (identity, public key, revocation, reed).
type ServerSignature struct {
	ServerID    string    `json:"serverID"`
	Fingerprint string    `json:"fingerprint"`
	Armor       string    `json:"armor"`
	Timestamp   time.Time `json:"timestamp"`
}

// Profile is the User wire shape of a countersigned identity record.
type Profile struct {
	ID                   string          `json:"id"`
	Username             string          `json:"username"`
	Role                 string          `json:"role"`
	MemberSince          time.Time       `json:"memberSince"`
	Bio                  string          `json:"bio"`
	ActiveKeyFingerprint string          `json:"activeKeyFingerprint"`
	UserSignature        UserSignature   `json:"userSignature"`
	ServerSignature      ServerSignature `json:"serverSignature"`
	HasReeds             bool            `json:"hasReeds"`
	InvitedBy            *InvitedBy      `json:"invitedBy"`
}

// InvitedBy is the inviter identity nested on User/Profile wire when set.
type InvitedBy struct {
	ID       string `json:"id"`
	Username string `json:"username"`
}

// KeyWire is the public-key fields shared by live Key responses and each
// level of a recovery nest (fingerprint, armor, server countersig, …).
type KeyWire struct {
	Fingerprint     string          `json:"fingerprint"`
	UserID          string          `json:"userID"`
	Armor           string          `json:"armor"`
	CreatedAt       time.Time       `json:"createdAt"`
	ExpiresAt       *time.Time      `json:"expiresAt,omitempty"`
	Revoked         bool            `json:"revoked"`
	ServerSignature ServerSignature `json:"serverSignature"`
}

// Revocation is a signed revocation attestation for a user key.
type Revocation struct {
	Fingerprint     string          `json:"fingerprint"`
	UserID          string          `json:"userID"`
	Reason          string          `json:"reason"`
	Successor       *string         `json:"successor"`
	UserSignature   UserSignature   `json:"userSignature"`
	ServerSignature ServerSignature `json:"serverSignature"`
}

// KeyNode is one level of the nested key chain. KeyWire is embedded so the
// wire is `key.armor` / `predecessor.armor` (not `key.key.armor`).
// Outermost is the active key; Predecessor walks back to the signup key
// (nil). Signature is set only on predecessor links: the older key's
// detached sig over the newer (parent) key's armor.
type KeyNode struct {
	KeyWire
	Signature   string      `json:"signature,omitempty"`
	Revocation  *Revocation `json:"revocation"`
	Predecessor *KeyNode    `json:"predecessor"`
}

// ClaimRequest is the POST /api/recovery/identity/claim body.
type ClaimRequest struct {
	Challenge int64   `json:"challenge"`
	Signature string  `json:"signature"`
	Profile   Profile `json:"profile"`
	Key       KeyNode `json:"key"`
}

// PeerIdentityRequest is the POST /api/recovery/identity body.
type PeerIdentityRequest struct {
	Profile Profile `json:"profile"`
	Key     KeyNode `json:"key"`
}

// ChallengeResponse is the GET /api/recovery/identity/claim body.
type ChallengeResponse struct {
	Challenge int64 `json:"challenge"`
}

// ReedRequest is the POST /api/recovery/reeds body.
type ReedRequest struct {
	ReedID          string          `json:"reedID"`
	AuthorID        string          `json:"authorID"`
	UserSignature   UserSignature   `json:"userSignature"`
	ServerSignature ServerSignature `json:"serverSignature"`
}

// FollowingRequest is the POST /api/recovery/following body.
type FollowingRequest struct {
	UserIDs []string `json:"userIDs"`
}

// FlatKey is one key in oldest→newest order after nest verification.
// Oldest-first matches user_keys.predecessor_fingerprint FK insert order.
type FlatKey struct {
	Key                    KeyWire
	Revocation             *Revocation
	PredecessorFingerprint string // empty for the oldest key
	PredecessorSignature   string // older key's sig over this key's armor
}
