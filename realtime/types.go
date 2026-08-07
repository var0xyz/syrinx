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
)

// BroadcastMessage represents a message sent from the main app to the realtime service.
// Only the payload field matching Type is set.
type BroadcastMessage struct {
	Type     BroadcastType
	ServerID string
	UserID   string
	ReedID   string

	ReedRemoval    *ReedRemovalWire
	AccountRemoval *AccountRemovalWire
	UserUpdate     *UserUpdateBroadcast
}

// ReedKey identifies a reed-scoped subscription.
type ReedKey struct {
	AuthorUserID string
	ReedID       string
}

func makeReedKey(authorUserID, reedID string) ReedKey {
	return ReedKey{AuthorUserID: authorUserID, ReedID: reedID}
}

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
