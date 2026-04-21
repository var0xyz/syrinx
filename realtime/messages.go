package realtime

// EventName identifies the reason a pending relay event was created.
type EventName string

const (
	RequestReedEvent         EventName = "request_reed"
	ProfileSubscriptionEvent EventName = "profile_subscription"
	NewReedEvent             EventName = "new_reed"
	BroadcastReedEvent       EventName = "broadcast_reed"
)

// RelayRequestMsg is sent from the server to a holder to request reed content.
type RelayRequestMsg struct {
	Type string          `json:"type"`
	Data RelayRequestData `json:"data"`
}

type RelayRequestData struct {
	EventID string `json:"event_id"`
	ReedID  string `json:"reed_id"`
}

func NewRelayRequestMsg(eventID, reedID string) RelayRequestMsg {
	return RelayRequestMsg{Type: "RELAY_REQUEST", Data: RelayRequestData{EventID: eventID, ReedID: reedID}}
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
	RequestID string      `json:"request_id"`
	ReedID    string      `json:"reed_id"`
	Data      interface{} `json:"data"`
}

func NewDataResponseMsg(requestID, reedID string, data interface{}) DataResponseMsg {
	return DataResponseMsg{Type: "DATA_RESPONSE", Data: DataResponseData{RequestID: requestID, ReedID: reedID, Data: data}}
}

// NewBroadcastReedMsg builds a BROADCAST_REED delivery message (no request_id needed).
func NewBroadcastReedMsg(reedID string, data interface{}) DataResponseMsg {
	return DataResponseMsg{Type: "BROADCAST_REED", Data: DataResponseData{ReedID: reedID, Data: data}}
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
	return ReedNotFoundMsg{Type: "REED_NOT_FOUND", Data: ReedNotFoundData{RequestID: requestID, ReedID: reedID}}
}
