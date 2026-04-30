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
	EventID   string      `json:"event_id"`
	RequestID string      `json:"request_id"`
	ReedID    string      `json:"reed_id"`
	Data      interface{} `json:"data"`
}

func NewDataResponseMsg(eventID, requestID, reedID string, data interface{}) DataResponseMsg {
	return DataResponseMsg{Type: "DATA_RESPONSE", Data: DataResponseData{EventID: eventID, RequestID: requestID, ReedID: reedID, Data: data}}
}

// NewBroadcastReedMsg builds a BROADCAST_REED delivery message (no request_id needed).
func NewBroadcastReedMsg(reedID string, data interface{}) DataResponseMsg {
	return DataResponseMsg{Type: "BROADCAST_REED", Data: DataResponseData{ReedID: reedID, Data: data}}
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

// NotifyChatRequestData is the payload of an incoming NOTIFY_CHAT_REQUEST message.
type NotifyChatRequestData struct {
	ChatID      string `json:"chatId"`
	RecipientID string `json:"recipientId"`
	Message     string `json:"message"`
}

// NotifyChatAcceptedData is the payload of an incoming NOTIFY_CHAT_ACCEPTED message.
type NotifyChatAcceptedData struct {
	ChatID      string `json:"chatId"`
	InitiatorID string `json:"initiatorId"`
}

// DeliverChatMessageData is the payload of an incoming DELIVER_CHAT_MESSAGE message.
type DeliverChatMessageData struct {
	ChatID string `json:"chatId"`
}

// ConfirmDeliveryData is the payload of an incoming CONFIRM_DELIVERY message.
type ConfirmDeliveryData struct {
	MessageID string `json:"messageId"`
	SenderID  string `json:"senderId"`
}

// NotifyBlockData is the payload of an incoming NOTIFY_BLOCK message.
type NotifyBlockData struct {
	BlockedUserID string `json:"blockedUserId"`
}

// BlockEventAckData is the payload of an incoming BLOCK_EVENT_ACK message.
type BlockEventAckData struct {
	BlockerID string `json:"blockerId"`
}

// ChatMessageMsg is sent to a recipient to deliver a message.
type ChatMessageMsg struct {
	Type string          `json:"type"`
	Data ChatMessageData `json:"data"`
}

type ChatMessageData struct {
	ServerID  string `json:"serverId"`
	ClientID  string `json:"clientId"`
	ChatID    string `json:"chatId"`
	SenderID  string `json:"senderId"`
	Content   string `json:"content"`
	CreatedAt string `json:"createdAt"`
}

func NewChatMessageMsg(data ChatMessageData) ChatMessageMsg {
	return ChatMessageMsg{Type: "CHAT_MESSAGE", Data: data}
}

// ChatDeliveryConfirmationMsg is sent to the sender when the recipient ACKs.
type ChatDeliveryConfirmationMsg struct {
	Type string                       `json:"type"`
	Data ChatDeliveryConfirmationData `json:"data"`
}

type ChatDeliveryConfirmationData struct {
	MessageID string `json:"messageId"`
}

func NewChatDeliveryConfirmationMsg(messageID string) ChatDeliveryConfirmationMsg {
	return ChatDeliveryConfirmationMsg{Type: "CHAT_DELIVERY_CONFIRMATION", Data: ChatDeliveryConfirmationData{MessageID: messageID}}
}

// ChatSigVerifyFailedMsg is sent to the sender when the recipient rejects a message.
type ChatSigVerifyFailedMsg struct {
	Type string                  `json:"type"`
	Data ChatSigVerifyFailedData `json:"data"`
}

type ChatSigVerifyFailedData struct {
	MessageID string `json:"messageId"`
}

func NewChatSigVerifyFailedMsg(messageID string) ChatSigVerifyFailedMsg {
	return ChatSigVerifyFailedMsg{Type: "CHAT_SIG_VERIFY_FAILED", Data: ChatSigVerifyFailedData{MessageID: messageID}}
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

// ChatRequestMsg is sent to the recipient when a new chat request arrives.
type ChatRequestMsg struct {
	Type string          `json:"type"`
	Data ChatRequestData `json:"data"`
}

type ChatRequestData struct {
	ChatID   string `json:"chatId"`
	SenderID string `json:"senderId"`
	Message  string `json:"message"`
}

func NewChatRequestMsg(data ChatRequestData) ChatRequestMsg {
	return ChatRequestMsg{Type: "CHAT_REQUEST", Data: data}
}

// ChatRequestAcceptedMsg is sent to the initiator when their request is accepted.
type ChatRequestAcceptedMsg struct {
	Type string                  `json:"type"`
	Data ChatRequestAcceptedData `json:"data"`
}

type ChatRequestAcceptedData struct {
	ChatID string `json:"chatId"`
}

func NewChatRequestAcceptedMsg(chatID string) ChatRequestAcceptedMsg {
	return ChatRequestAcceptedMsg{Type: "CHAT_REQUEST_ACCEPTED", Data: ChatRequestAcceptedData{ChatID: chatID}}
}

// BlockEventMsg is sent to a blocked user to wipe data for the blocker.
type BlockEventMsg struct {
	Type string         `json:"type"`
	Data BlockEventData `json:"data"`
}

type BlockEventData struct {
	BlockerID string `json:"blockerId"`
}

func NewBlockEventMsg(blockerID string) BlockEventMsg {
	return BlockEventMsg{Type: "BLOCK_EVENT", Data: BlockEventData{BlockerID: blockerID}}
}
