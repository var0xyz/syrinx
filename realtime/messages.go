package realtime

import "encoding/json"

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
)

// RelayRequestMsg is sent from the server to a holder to request reed content.
// AuthorID disambiguates ReedID, which is only unique per author — without
// it a holder caching more than one author's content under a colliding
// local ID has no way to tell which reed is actually being asked for.
type RelayRequestMsg struct {
	Type string           `json:"type"`
	Data RelayRequestData `json:"data"`
}

type RelayRequestData struct {
	EventID  string `json:"event_id"`
	AuthorID string `json:"author_id"`
	ReedID   string `json:"reed_id"`
}

func NewRelayRequestMsg(eventID, authorID, reedID string) RelayRequestMsg {
	return RelayRequestMsg{Type: "RELAY_REQUEST", Data: RelayRequestData{EventID: eventID, AuthorID: authorID, ReedID: reedID}}
}

// RequestAckMsg is sent from the server to a requester confirming the relay request was registered.
type RequestAckMsg struct {
	Type string         `json:"type"`
	Data RequestAckData `json:"data"`
}

type RequestAckData struct {
	RequestID string `json:"request_id"`
	EventID   string `json:"event_id"`
	ReedID    string `json:"reed_id"`
}

func NewRequestAckMsg(requestID, eventID, reedID string) RequestAckMsg {
	return RequestAckMsg{Type: "REQUEST_ACK", Data: RequestAckData{RequestID: requestID, EventID: eventID, ReedID: reedID}}
}

// DataResponseMsg is sent from the server to the requester with the relayed reed content.
type DataResponseMsg struct {
	Type string           `json:"type"`
	Data DataResponseData `json:"data"`
}

type DataResponseData struct {
	EventID   string          `json:"event_id,omitempty"`
	RequestID string          `json:"request_id,omitempty"`
	ReedID    string          `json:"reed_id,omitempty"`
	UserID    string          `json:"user_id,omitempty"`
	Data      json.RawMessage `json:"data"`
	Username  string          `json:"username,omitempty"`
}

func NewDataResponseMsg(eventID, requestID, reedID string, data json.RawMessage) DataResponseMsg {
	return DataResponseMsg{Type: "DATA_RESPONSE", Data: DataResponseData{EventID: eventID, RequestID: requestID, ReedID: reedID, Data: data}}
}

// NewBroadcastReedMsg builds a BROADCAST_REED delivery message (no request_id needed).
func NewBroadcastReedMsg(reedID string, reedData json.RawMessage, username string) DataResponseMsg {
	return DataResponseMsg{
		Type: "BROADCAST_REED",
		Data: DataResponseData{
			ReedID:   reedID,
			Data:     reedData,
			Username: username,
		},
	}
}

// NewPipeReedMsg builds a PIPE_REED delivery (pipe subscription push).
// Carries event_id so the viewer can DATA_ACK after verify+store (same as DATA_RESPONSE).
func NewPipeReedMsg(eventID, requestID, reedID string, data json.RawMessage) DataResponseMsg {
	return DataResponseMsg{
		Type: "PIPE_REED",
		Data: DataResponseData{
			EventID:   eventID,
			RequestID: requestID,
			ReedID:    reedID,
			Data:      data,
		},
	}
}

// NewFollowReedMsg builds a FOLLOW_REED delivery (followcast / follow catch-up push).
func NewFollowReedMsg(eventID, requestID, reedID string, data json.RawMessage) DataResponseMsg {
	return DataResponseMsg{
		Type: "FOLLOW_REED",
		Data: DataResponseData{
			EventID:   eventID,
			RequestID: requestID,
			ReedID:    reedID,
			Data:      data,
		},
	}
}

// NewReedReplyMsg builds a REED_REPLY delivery: pushed to subscribers of a
// reed (and its ancestors) when a new reply lands, distinct from FOLLOW_REED
// so it doesn't also feed the follow-feed cache — the recipient isn't
// necessarily following the reply's author, they're just viewing the thread.
func NewReedReplyMsg(eventID, requestID, reedID string, data json.RawMessage) DataResponseMsg {
	return DataResponseMsg{
		Type: "REED_REPLY",
		Data: DataResponseData{
			EventID:   eventID,
			RequestID: requestID,
			ReedID:    reedID,
			Data:      data,
		},
	}
}

// NewReedRemovedMsg builds a REED_REMOVED delivery with the full signed cert as data.
func NewReedRemovedMsg(eventID, requestID, reedID string, cert ReedRemovalWire) DataResponseMsg {
	raw, _ := json.Marshal(cert)
	return DataResponseMsg{
		Type: "REED_REMOVED",
		Data: DataResponseData{
			EventID:   eventID,
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
		Data: DataResponseData{
			EventID:   eventID,
			RequestID: requestID,
			UserID:    removedUserID,
			Data:      raw,
		},
	}
}

// RelayMissData is the parsed payload of an incoming RELAY_MISS message.
type RelayMissData struct {
	EventID string `json:"event_id"`
}

// DataAckData is the parsed payload of an incoming DATA_ACK message.
type DataAckData struct {
	EventID string `json:"event_id"`
}

// DataInvalidData is the parsed payload of an incoming DATA_INVALID message.
type DataInvalidData struct {
	EventID string `json:"event_id"`
}

// KeyFetchErrorData is the parsed payload of an incoming KEY_FETCH_ERROR
// message: the client tried to fetch userID's key (fingerprint) to verify
// signed content and the request failed (network error, non-2xx other than
// a legitimate "key not found"). The server was reachable enough to have
// delivered the content in the first place, so this is an anomaly worth
// logging, not a routine cache miss.
type KeyFetchErrorData struct {
	UserID      string `json:"user_id"`
	Fingerprint string `json:"fingerprint"`
}

// RevokedKeyUsedData is the parsed payload of an incoming REVOKED_KEY_USED
// message: the client fetched userID's key (fingerprint), found it revoked,
// and the signed content's timestamp was at or after the revocation time —
// i.e. content purportedly signed with an already-revoked key.
type RevokedKeyUsedData struct {
	UserID      string `json:"user_id"`
	Fingerprint string `json:"fingerprint"`
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
	AuthorID  string `json:"author_id"`
}

func NewReedNotHeldMsg(requestID, authorID, reedID string) ReedNotHeldMsg {
	return ReedNotHeldMsg{
		Type: "REED_NOT_HELD",
		Data: ReedNotHeldData{
			RequestID: requestID,
			ReedID:    reedID,
			AuthorID:  authorID,
		},
	}
}
