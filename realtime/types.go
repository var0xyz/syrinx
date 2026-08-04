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
)

// BroadcastMessage represents a message sent from the main app to the realtime service
type BroadcastMessage struct {
	Type     BroadcastType
	ServerID string
	UserID   string
	ReedID   string
	Data     map[string]interface{}
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
	subscriptions     map[SubscriptionType]bool
	reedSubscriptions map[string]bool
	pipeSubscriptions map[string]bool // normalized tag → subscribed
	lastPing          time.Time
	writeMu           sync.Mutex
}

// ConnectionManager manages WebSocket connections and subscriptions
type ConnectionManager struct {
	userConnections map[string]map[*websocket.Conn]*Client
	// Map of authorID/reedID -> subscribed clients
	reedSubscribers map[string]map[*Client]bool
	// Map of normalized tag -> subscribed clients
	pipeSubscribers map[string]map[*Client]bool
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
	default:
		return "Unknown"
	}
}

// NewClient creates a new client
func NewClient(conn *websocket.Conn, userID string) *Client {
	return &Client{
		conn:              conn,
		userID:            userID,
		subscriptions:     make(map[SubscriptionType]bool),
		reedSubscriptions: make(map[string]bool),
		pipeSubscriptions: make(map[string]bool),
		lastPing:          time.Now(),
	}
}

// IsSubscribed checks if a client is subscribed to a specific type
func (c *Client) IsSubscribed(subType SubscriptionType) bool {
	return c.subscriptions[subType]
}

// Subscribe adds a subscription for the client
func (c *Client) Subscribe(subType SubscriptionType) {
	c.subscriptions[subType] = true
}

// Unsubscribe removes a subscription for the client
func (c *Client) Unsubscribe(subType SubscriptionType) {
	c.subscriptions[subType] = false
}
