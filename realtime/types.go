package realtime

import (
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// BroadcastType represents the type of broadcast message
type BroadcastType int

const (
	UserUpdate BroadcastType = iota
	ReedDeleted // legacy unused; prefer ReedRemoved
	ReedRemoved
	AccountRemoved
	EchoCountChanged // UserID/ReedID = echoed target; refresh REED_ECHOES for subscribers
	ReplyCountChanged // UserID/ReedID = ancestor reed; refresh REED_REPLIES subtree count for subscribers
	LikeCountChanged // UserID/ReedID = liked reed; refresh REED_LIKES for subscribers
	ReplyPosted // UserID/ReedID = ancestor reed to notify; ReplyUserID/ReplyReedID = the new reply (content holder)
	RipplePosted  // UserID/ReedID = parent reed; Ripple = the new ripple response (full signed payload)
	RippleUpdated // UserID/ReedID = parent reed; Ripple = the soft-deleted ripple response (deleted=true, content="[DELETED]")
)

// BroadcastMessage represents a message sent from the main app to the realtime service.
// Only the payload field matching Type is set.
type BroadcastMessage struct {
	Type     BroadcastType
	ServerID string
	UserID   string
	ReedID   string

	// ReplyPosted only: identifies the new reply itself, distinct from
	// UserID/ReedID above (the ancestor being notified).
	ReplyUserID string
	ReplyReedID string

	ReedRemoval    *ReedRemovalWire
	AccountRemoval *AccountRemovalWire
	UserUpdate     *UserUpdateBroadcast

	// RipplePosted/RippleUpdated only: the full signed ripple response.
	Ripple *RippleWire
}

// ReedKey identifies a reed-scoped subscription — the reed's own canonical
// id, which already self-describes (embeds the author).
type ReedKey string

// clientSubscriptionFlags tracks protobuf/legacy JSON subscription toggles.
type clientSubscriptionFlags struct {
	user      bool
	broadcast bool
}

// SubscriptionType represents the type of subscription
type SubscriptionType int

const (
	SubscribeUser SubscriptionType = iota
	SubscribeBroadcast
	UnsubscribeUser
	UnsubscribeBroadcast
)

// Client represents a connected WebSocket client
type Client struct {
	conn              *websocket.Conn
	userID            string
	subscriptions     clientSubscriptionFlags
	reedSubscriptions map[ReedKey]struct{}
	pipeSubscriptions map[string]struct{} // normalized tag → subscribed
	lastPing          time.Time
	writeMu           sync.Mutex
	wsRecordOutbound  func(messageType int, data []byte)
}

// ConnectionManager manages WebSocket connections and subscriptions
type ConnectionManager struct {
	userConnections map[string]map[*websocket.Conn]*Client
	reedSubscribers map[ReedKey]map[*Client]struct{}
	pipeSubscribers map[string]map[*Client]struct{}
	mutex           sync.RWMutex
}

// String returns the string representation of BroadcastType
func (bt BroadcastType) String() string {
	switch bt {
	case UserUpdate:
		return "UserUpdate"
	case ReedDeleted:
		return "ReedDeleted"
	case ReedRemoved:
		return "ReedRemoved"
	case AccountRemoved:
		return "AccountRemoved"
	case EchoCountChanged:
		return "EchoCountChanged"
	case ReplyCountChanged:
		return "ReplyCountChanged"
	case LikeCountChanged:
		return "LikeCountChanged"
	case ReplyPosted:
		return "ReplyPosted"
	case RipplePosted:
		return "RipplePosted"
	case RippleUpdated:
		return "RippleUpdated"
	default:
		return "Unknown"
	}
}

// NewClient creates a new client
func NewClient(conn *websocket.Conn, userID string) *Client {
	return &Client{
		conn:              conn,
		userID:            userID,
		reedSubscriptions: make(map[ReedKey]struct{}),
		pipeSubscriptions: make(map[string]struct{}),
		lastPing:          time.Now(),
	}
}

// IsSubscribed checks if a client is subscribed to a specific type
func (c *Client) IsSubscribed(subType SubscriptionType) bool {
	switch subType {
	case SubscribeUser:
		return c.subscriptions.user
	case SubscribeBroadcast:
		return c.subscriptions.broadcast
	default:
		return false
	}
}

// Subscribe adds a subscription for the client
func (c *Client) Subscribe(subType SubscriptionType) {
	switch subType {
	case SubscribeUser:
		c.subscriptions.user = true
	case SubscribeBroadcast:
		c.subscriptions.broadcast = true
	}
}

// Unsubscribe removes a subscription for the client
func (c *Client) Unsubscribe(subType SubscriptionType) {
	switch subType {
	case SubscribeUser:
		c.subscriptions.user = false
	case SubscribeBroadcast:
		c.subscriptions.broadcast = false
	}
}
