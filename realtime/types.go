package realtime

import (
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// BroadcastType represents the type of broadcast message
type BroadcastType int

const (
	NewReed BroadcastType = iota
	UserUpdate
	ReedDeleted // legacy unused; prefer ReedRemoved
	ReedRemoved
	AccountRemoved
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
	conn          *websocket.Conn
	userID        string
	subscriptions map[SubscriptionType]bool
	lastPing      time.Time
}

// ConnectionManager manages WebSocket connections and subscriptions
type ConnectionManager struct {
	// Map of userID -> connections for user-specific notifications
	userConnections map[string]map[*websocket.Conn]*Client
	// Channels for operations
	register   chan *Client
	unregister chan *Client
	mutex      sync.RWMutex
}

// String returns the string representation of BroadcastType
func (bt BroadcastType) String() string {
	switch bt {
	case NewReed:
		return "NewReed"
	case UserUpdate:
		return "UserUpdate"
	case ReedDeleted:
		return "ReedDeleted"
	case ReedRemoved:
		return "ReedRemoved"
	case AccountRemoved:
		return "AccountRemoved"
	default:
		return "Unknown"
	}
}

// NewClient creates a new client
func NewClient(conn *websocket.Conn, userID string) *Client {
	return &Client{
		conn:          conn,
		userID:        userID,
		subscriptions: make(map[SubscriptionType]bool),
		lastPing:      time.Now(),
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
