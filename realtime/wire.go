package realtime

import (
	"encoding/json"
	"time"

	"syrinx/deletion"
	"syrinx/identity"
)

// UserSignatureWire is the nested user attestation block on removal certs.
type UserSignatureWire struct {
	Fingerprint string `json:"fingerprint"`
	Armor       string `json:"armor"`
}

// ServerSignatureWire is the nested server countersignature block on removal certs.
type ServerSignatureWire struct {
	ServerID    string    `json:"serverID"`
	Fingerprint string    `json:"fingerprint"`
	Armor       string    `json:"armor"`
	Timestamp   time.Time `json:"timestamp"`
}

// ReedRemovalWire is the wire shape of a signed reed-removal certificate.
type ReedRemovalWire struct {
	Type            string              `json:"type"`
	ServerID        string              `json:"serverID"`
	UserID          string              `json:"userID"`
	ReedID          string              `json:"reedID"`
	UserSignature   UserSignatureWire   `json:"userSignature"`
	ServerSignature ServerSignatureWire `json:"serverSignature"`
}

// AccountRemovalWire is the wire shape of a signed account-removal certificate.
type AccountRemovalWire struct {
	Type            string              `json:"type"`
	ServerID        string              `json:"serverID"`
	UserID          string              `json:"userID"`
	Note            string              `json:"note"`
	UserSignature   UserSignatureWire   `json:"userSignature"`
	ServerSignature ServerSignatureWire `json:"serverSignature"`
}

// NewReedRemovalWire builds the WS/HTTP wire cert from a stored deletion cert.
func NewReedRemovalWire(serverID string, cert *deletion.Cert) ReedRemovalWire {
	return ReedRemovalWire{
		Type:     identity.TypeReed,
		ServerID: serverID,
		UserID:   cert.UserID,
		ReedID:   cert.ReedID,
		UserSignature: UserSignatureWire{
			Fingerprint: cert.UserFingerprint,
			Armor:       cert.UserSignature,
		},
		ServerSignature: ServerSignatureWire{
			ServerID:    serverID,
			Fingerprint: cert.ServerFingerprint,
			Armor:       cert.ServerSignature,
			Timestamp:   cert.ServerSignedAt.UTC(),
		},
	}
}

// NewAccountRemovalWire builds the WS/HTTP wire cert from a stored deletion cert.
func NewAccountRemovalWire(serverID string, cert *deletion.AccountCert) AccountRemovalWire {
	return AccountRemovalWire{
		Type:     identity.TypeAccount,
		ServerID: serverID,
		UserID:   cert.UserID,
		Note:     cert.Note,
		UserSignature: UserSignatureWire{
			Fingerprint: cert.UserFingerprint,
			Armor:       cert.UserSignature,
		},
		ServerSignature: ServerSignatureWire{
			ServerID:    serverID,
			Fingerprint: cert.ServerFingerprint,
			Armor:       cert.ServerSignature,
			Timestamp:   cert.ServerSignedAt.UTC(),
		},
	}
}

// UserUpdateBroadcast is profile metadata pushed on user updates (reserved).
type UserUpdateBroadcast struct {
	Username string `json:"username"`
	Bio      string `json:"bio"`
}

// InboundJSONMsg is the common envelope for client JSON WebSocket frames.
type InboundJSONMsg struct {
	Type   string          `json:"type"`
	Data   json.RawMessage `json:"data"`
	UserID string          `json:"userID"`
	ReedID string          `json:"reedID"`
}

// PongMsg is the JSON pong response.
type PongMsg struct {
	Type string          `json:"type"`
	Data json.RawMessage `json:"data"`
}

// SubscribedMsg is the JSON subscription acknowledgement.
type SubscribedMsg struct {
	Type string `json:"type"`
	Data string `json:"data"`
}

// RequestReedData is the payload of an incoming REQUEST_REED message.
type RequestReedData struct {
	RequestID string `json:"request_id"`
	ReedID    string `json:"reed_id"`
	AuthorID  string `json:"author_id"`
}

// RelayResponseData is the payload of an incoming RELAY_RESPONSE message.
type RelayResponseData struct {
	EventID string          `json:"event_id"`
	Data    json.RawMessage `json:"data"`
}

// PublishReadyData is the payload of an incoming PUBLISH_READY message.
type PublishReadyData struct {
	ReedID    string          `json:"reed_id"`
	Broadcast json.RawMessage `json:"broadcast"`
}

// PublishReadyAckMsg confirms fanout was processed (or reed already exists).
type PublishReadyAckMsg struct {
	Type string              `json:"type"`
	Data PublishReadyAckData `json:"data"`
}

type PublishReadyAckData struct {
	ReedID string `json:"reed_id"`
}

// SubscribePipeData is the payload of SUBSCRIBE_PIPE / UNSUBSCRIBE_PIPE.
type SubscribePipeData struct {
	Tag string `json:"tag"`
}

// ReedStatsMsg is pushed when a client subscribes to reed stats.
type ReedStatsMsg struct {
	Type            string `json:"type"`
	UserID          string `json:"userID"`
	ReedID          string `json:"reedID"`
	Echoes          int    `json:"echoes"`
	CoveragePercent int    `json:"coveragePercent"`
	Replies         int    `json:"replies"`
	Likes           int    `json:"likes"`
}

// ReedCoverageMsg notifies reed subscribers of holder coverage changes.
type ReedCoverageMsg struct {
	Type            string `json:"type"`
	UserID          string `json:"userID"`
	ReedID          string `json:"reedID"`
	CoveragePercent int    `json:"coveragePercent"`
}

// ReedEchoesMsg notifies reed subscribers of echo count changes.
type ReedEchoesMsg struct {
	Type   string `json:"type"`
	UserID string `json:"userID"`
	ReedID string `json:"reedID"`
	Echoes int    `json:"echoes"`
}

// ReedRepliesMsg notifies reed subscribers of reply subtree count changes.
type ReedRepliesMsg struct {
	Type    string `json:"type"`
	UserID  string `json:"userID"`
	ReedID  string `json:"reedID"`
	Replies int    `json:"replies"`
}

// ReedLikesMsg notifies reed subscribers of like count changes.
type ReedLikesMsg struct {
	Type   string `json:"type"`
	UserID string `json:"userID"`
	ReedID string `json:"reedID"`
	Likes  int    `json:"likes"`
}

// RippleWire is the wire shape of one ripple response, mirroring the
// RippleWire struct handlers.go returns from POST/GET — duplicated here
// (not imported) because realtime cannot depend on the main package,
// same reasoning as ReedRemovalWire/AccountRemovalWire above. The id is
// `hash`, the hex-SHA256 digest of the signed server payload — see
// specs/ripples/00_design.md's Signing section.
type RippleWire struct {
	Hash            string              `json:"hash"`
	ThreadID        string              `json:"threadID"`
	UserID          string              `json:"userID"`
	Content         string              `json:"content"`
	ReplyingTo      *string             `json:"replyingTo"`
	Deleted         bool                `json:"deleted"`
	PostedAt        time.Time           `json:"postedAt"`
	UserSignature   UserSignatureWire   `json:"userSignature"`
	ServerSignature ServerSignatureWire `json:"serverSignature"`
}

// RipplePostedMsg notifies reed subscribers a new ripple response landed.
type RipplePostedMsg struct {
	Type   string     `json:"type"`
	UserID string     `json:"userID"` // reed author
	ReedID string     `json:"reedID"`
	Ripple RippleWire `json:"ripple"`
}

// RippleUpdatedMsg notifies reed subscribers an existing ripple response
// was soft-deleted (content patched to "[DELETED]"). Named Updated, not
// Deleted, because the client patches the row in place rather than
// removing it — there is no RIPPLE_DELETED event.
type RippleUpdatedMsg struct {
	Type   string     `json:"type"`
	UserID string     `json:"userID"`
	ReedID string     `json:"reedID"`
	Ripple RippleWire `json:"ripple"`
}

// ShutdownMsg is broadcast to every connected client right before the
// server closes their socket for a graceful shutdown (SIGTERM/SIGINT), so
// the client can reconnect immediately instead of waiting on a connection
// that silently went dead.
type ShutdownMsg struct {
	Type string `json:"type"`
}
