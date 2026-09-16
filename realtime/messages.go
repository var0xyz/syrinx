package realtime

import (
	"encoding/json"

	"syrinx/identity"
)

// EventName identifies the reason a pending relay event was created.
type EventName string

const (
	RequestReedEvent         EventName = "request_reed"
	ProfileSubscriptionEvent EventName = "profile_subscription"
	FollowReedEvent          EventName = "follow_reed"
	BroadcastReedEvent       EventName = "broadcast_reed"
	PipeReedEvent            EventName = "pipe_reed"
	ReedRemovedEvent         EventName = "reed_removed"
	AccountRemovedEvent      EventName = "account_removed"
	ReedReplyEvent           EventName = "reed_reply"
	ArchiveReedEvent         EventName = "archive_reed"
)

// RelayRequestMsg is sent from the server to a holder to request reed
// content. ReedID is the canonical id (userID@serverID/uuid) — already
// globally unique and already embeds the author, so no separate author
// field is needed to disambiguate it. RequesterID is who the holder must
// encrypt the content to before responding — the server relays ciphertext
// blindly and never sees the body.
type RelayRequestMsg struct {
	Type string           `json:"type"`
	ID   string           `json:"id"`
	Data RelayRequestData `json:"data"`
}

type RelayRequestData struct {
	ReedID      string `json:"reed_id"`
	RequesterID string `json:"requester_id"`
}

func NewRelayRequestMsg(eventID, reedID, requesterID string) RelayRequestMsg {
	return RelayRequestMsg{Type: "RELAY_REQUEST", ID: eventID, Data: RelayRequestData{ReedID: reedID, RequesterID: requesterID}}
}

// RequestAckMsg is sent from the server to a requester confirming the relay request was registered.
type RequestAckMsg struct {
	Type string         `json:"type"`
	ID   string         `json:"id"`
	Data RequestAckData `json:"data"`
}

type RequestAckData struct {
	RequestID string `json:"request_id"`
	ReedID    string `json:"reed_id"`
}

func NewRequestAckMsg(requestID, eventID, reedID string) RequestAckMsg {
	return RequestAckMsg{Type: "REQUEST_ACK", ID: eventID, Data: RequestAckData{RequestID: requestID, ReedID: reedID}}
}

// DataResponseMsg is sent from the server to the requester with the relayed reed content.
type DataResponseMsg struct {
	Type string           `json:"type"`
	ID   string           `json:"id,omitempty"`
	Data DataResponseData `json:"data"`
}

type DataResponseData struct {
	RequestID  string          `json:"request_id,omitempty"`
	ReedID     string          `json:"reed_id,omitempty"`
	UserID     string          `json:"user_id,omitempty"`
	Data       json.RawMessage `json:"data,omitempty"`
	Ciphertext string          `json:"ciphertext,omitempty"`
	Username   string          `json:"username,omitempty"`
}

// jsonString JSON-encodes a Go string (adds quoting/escaping) — used to
// wrap ciphertext for the federation transport layer (deliverOrForward's
// data json.RawMessage param), not for DataResponseData's own JSON shape.
func jsonString(s string) json.RawMessage {
	raw, _ := json.Marshal(s)
	return raw
}

func NewDataResponseMsg(eventID, requestID, reedID, ciphertext string) DataResponseMsg {
	return DataResponseMsg{Type: "DATA_RESPONSE", ID: eventID, Data: DataResponseData{RequestID: requestID, ReedID: reedID, Ciphertext: ciphertext}}
}

// NewBroadcastReedMsg builds a BROADCAST_REED delivery message (no request_id or event id needed).
func NewBroadcastReedMsg(reedID, ciphertext, username string) DataResponseMsg {
	return DataResponseMsg{
		Type: "BROADCAST_REED",
		Data: DataResponseData{
			ReedID:     reedID,
			Ciphertext: ciphertext,
			Username:   username,
		},
	}
}

// NewPipeReedMsg builds a PIPE_REED delivery (pipe subscription push).
// Carries the event id so the viewer can DATA_ACK after verify+store (same as DATA_RESPONSE).
func NewPipeReedMsg(eventID, requestID, reedID, ciphertext string) DataResponseMsg {
	return DataResponseMsg{
		Type: "PIPE_REED",
		ID:   eventID,
		Data: DataResponseData{
			RequestID:  requestID,
			ReedID:     reedID,
			Ciphertext: ciphertext,
		},
	}
}

// NewFollowReedMsg builds a FOLLOW_REED delivery (followcast / follow catch-up push).
func NewFollowReedMsg(eventID, requestID, reedID, ciphertext string) DataResponseMsg {
	return DataResponseMsg{
		Type: "FOLLOW_REED",
		ID:   eventID,
		Data: DataResponseData{
			RequestID:  requestID,
			ReedID:     reedID,
			Ciphertext: ciphertext,
		},
	}
}

// NewArchiveReedMsg builds an ARCHIVE_REED delivery to an admin/root
// resilience holder. No feed/UI semantics — the client stores and holds
// the reed without touching any social-graph state.
func NewArchiveReedMsg(eventID, requestID, reedID, ciphertext string) DataResponseMsg {
	return DataResponseMsg{
		Type: "ARCHIVE_REED",
		ID:   eventID,
		Data: DataResponseData{
			RequestID:  requestID,
			ReedID:     reedID,
			Ciphertext: ciphertext,
		},
	}
}

// NewReedReplyMsg builds a REED_REPLY delivery: pushed to subscribers of a
// reed (and its ancestors) when a new reply lands, distinct from FOLLOW_REED
// so it doesn't also feed the follow-feed cache — the recipient isn't
// necessarily following the reply's author, they're just viewing the thread.
// Used both for same-server subscriber fanout and for a foreign viewer whose
// home server relayed it to us on their behalf (see
// notifyForeignReedSubscribersOfReply) — the client handles both identically,
// so there is no separate cross-server wire type.
func NewReedReplyMsg(eventID, requestID, reedID, ciphertext string) DataResponseMsg {
	return DataResponseMsg{
		Type: "REED_REPLY",
		ID:   eventID,
		Data: DataResponseData{
			RequestID:  requestID,
			ReedID:     reedID,
			Ciphertext: ciphertext,
		},
	}
}

// NewReedRemovedMsg builds a REED_REMOVED delivery with the full signed cert as data.
func NewReedRemovedMsg(eventID, requestID, reedID string, cert ReedRemovalWire) DataResponseMsg {
	raw, _ := json.Marshal(cert)
	return DataResponseMsg{
		Type: "REED_REMOVED",
		ID:   eventID,
		Data: DataResponseData{
			RequestID: requestID,
			ReedID:    reedID,
			Data:      raw,
		},
	}
}

// NewAccountRemovedMsg builds an ACCOUNT_REMOVED delivery with the full signed cert.
func NewAccountRemovedMsg(eventID, requestID, removedUserID string, cert AccountRemovalWire) DataResponseMsg {
	raw, _ := json.Marshal(cert)
	return DataResponseMsg{
		Type: "ACCOUNT_REMOVED",
		ID:   eventID,
		Data: DataResponseData{
			RequestID: requestID,
			UserID:    removedUserID,
			Data:      raw,
		},
	}
}

// RELAY_MISS, RELAY_ERROR, DATA_ACK, and DATA_INVALID carry no payload
// beyond the event id, which lives on InboundJSONMsg.ID — no dedicated
// data struct needed for any of them.

// MailboxMsg delivers one pending user_mailbox row. The server never
// decrypts or inspects Ciphertext — it's opaque bytes to everyone but the
// recipient's own client. Sent both on live delivery and on catch-up.
type MailboxMsg struct {
	Type string         `json:"type"`
	Data MailboxMsgData `json:"data"`
}

type MailboxMsgData struct {
	ID         string `json:"id"`
	Ciphertext string `json:"ciphertext"`
}

// NewMailboxMsg sends the canonical ref (userID@serverID/id), not the bare
// row id — the client has no business reconstructing this itself, and
// user_mailbox.id alone is only unique per-user, not globally.
func NewMailboxMsg(userID, id, ciphertext string) MailboxMsg {
	canonicalID := string(identity.AppendEntity(identity.IdentityID(userID), id))
	return MailboxMsg{Type: "MAILBOX", Data: MailboxMsgData{ID: canonicalID, Ciphertext: ciphertext}}
}

// MailboxAckData is the parsed payload of an incoming MAILBOX_ACK message.
type MailboxAckData struct {
	ID string `json:"id"`
}

// KeyFetchErrorData is the parsed payload of an incoming KEY_FETCH_ERROR
// message: the client tried to fetch userID's key (keyID) to verify
// signed content and the request failed (network error, non-2xx other than
// a legitimate "key not found"). The server was reachable enough to have
// delivered the content in the first place, so this is an anomaly worth
// logging, not a routine cache miss.
type KeyFetchErrorData struct {
	UserID string `json:"user_id"`
	KeyID  string `json:"key_id"`
}

// RevokedKeyUsedData is the parsed payload of an incoming REVOKED_KEY_USED
// message: the client fetched userID's key (keyID), found it revoked,
// and the signed content's timestamp was at or after the revocation time —
// i.e. content purportedly signed with an already-revoked key.
type RevokedKeyUsedData struct {
	UserID string `json:"user_id"`
	KeyID  string `json:"key_id"`
}

// ContentRejectedData is the parsed payload of an incoming CONTENT_REJECTED
// message: the client failed to verify a signed resource and refused to
// store it. Reason is optional, one of a small standardized set.
type ContentRejectedData struct {
	StoreName string `json:"store_name"`
	Reason    string `json:"reason,omitempty"`
}

// SyncRequestData is the parsed payload of an incoming SYNC_REQUEST message.
type SyncRequestData struct {
	RequestID string `json:"request_id"`
}

// SubscribeProfileData is the parsed payload of an incoming SUBSCRIBE_PROFILE message.
type SubscribeProfileData struct {
	UserID string `json:"user_id"`
}

// UnsubscribeProfileData is the parsed payload of an incoming UNSUBSCRIBE_PROFILE message.
type UnsubscribeProfileData struct {
	UserID string `json:"user_id"`
}

// ReedNotFoundMsg is sent from the server to a requester when the requested reed does not exist.
type ReedNotFoundMsg struct {
	Type string           `json:"type"`
	Data ReedNotFoundData `json:"data"`
}

type ReedNotFoundData struct {
	RequestID string `json:"request_id"`
	ReedID    string `json:"reed_id"`
}

func NewReedNotFoundMsg(requestID, reedID string) ReedNotFoundMsg {
	return ReedNotFoundMsg{
		Type: "REED_NOT_FOUND",
		Data: ReedNotFoundData{
			RequestID: requestID,
			ReedID:    reedID,
		},
	}
}

// ReedNotHeldMsg is sent when reed metadata exists but no peer holds the body.
type ReedNotHeldMsg struct {
	Type string          `json:"type"`
	Data ReedNotHeldData `json:"data"`
}

type ReedNotHeldData struct {
	RequestID string `json:"request_id"`
	ReedID    string `json:"reed_id"`
}

func NewReedNotHeldMsg(requestID, reedID string) ReedNotHeldMsg {
	return ReedNotHeldMsg{
		Type: "REED_NOT_HELD",
		Data: ReedNotHeldData{
			RequestID: requestID,
			ReedID:    reedID,
		},
	}
}

// InvalidRequestIDErrorMsg is sent when an inbound message's request_id
// doesn't embed the identity of the connection that sent it (malformed,
// or claiming a different user/server than this WebSocket authenticated
// as) — the client should discard the offending local record rather than
// retry it, since the server never created any pending state for it.
type InvalidRequestIDErrorMsg struct {
	Type string                `json:"type"`
	Data InvalidRequestIDError `json:"data"`
}

type InvalidRequestIDError struct {
	RequestID string `json:"request_id"`
}

func NewInvalidRequestIDErrorMsg(requestID string) InvalidRequestIDErrorMsg {
	return InvalidRequestIDErrorMsg{
		Type: "INVALID_REQUEST_ID_ERROR",
		Data: InvalidRequestIDError{RequestID: requestID},
	}
}
