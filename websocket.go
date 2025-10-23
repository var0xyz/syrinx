package main

import (
	"encoding/json"
	"net/http"
	"sync"

	"github.com/gorilla/websocket"
	"github.com/rs/zerolog/log"
)

// WebSocket upgrader
var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		// Allow connections from any origin for now
		// In production, you should implement proper origin checking
		return true
	},
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
}

// WebSocket connection manager
type ConnectionManager struct {
	connections map[*websocket.Conn]bool
	register    chan *websocket.Conn
	unregister  chan *websocket.Conn
	broadcast   chan []byte
	mutex       sync.RWMutex
}

// NewConnectionManager creates a new connection manager
func NewConnectionManager() *ConnectionManager {
	return &ConnectionManager{
		connections: make(map[*websocket.Conn]bool),
		register:    make(chan *websocket.Conn),
		unregister:  make(chan *websocket.Conn),
		broadcast:   make(chan []byte),
	}
}

// Start starts the connection manager
func (cm *ConnectionManager) Start() {
	for {
		select {
		case conn := <-cm.register:
			cm.mutex.Lock()
			cm.connections[conn] = true
			cm.mutex.Unlock()
			log.Info().Msg("WebSocket client connected")

		case conn := <-cm.unregister:
			cm.mutex.Lock()
			if _, ok := cm.connections[conn]; ok {
				delete(cm.connections, conn)
				conn.Close()
			}
			cm.mutex.Unlock()
			log.Info().Msg("WebSocket client disconnected")

		case message := <-cm.broadcast:
			cm.mutex.RLock()
			for conn := range cm.connections {
				err := conn.WriteMessage(websocket.TextMessage, message)
				if err != nil {
					log.Error().Err(err).Msg("Failed to send WebSocket message")
					conn.Close()
					delete(cm.connections, conn)
				}
			}
			cm.mutex.RUnlock()
		}
	}
}

// Broadcast sends a message to all connected clients
func (cm *ConnectionManager) Broadcast(message []byte) {
	cm.broadcast <- message
}

// WebSocket message types
type WSMessage struct {
	Type string      `json:"type"`
	Data interface{} `json:"data"`
}

type ReedNotification struct {
	ReedID   string `json:"reedId"`
	UserID   string `json:"userId"`
	Username string `json:"username"`
	Content  string `json:"content"`
}

// WebSocketHandler handles WebSocket connections
func (h *Handlers) WebSocketHandler(w http.ResponseWriter, r *http.Request) {
	// Use a simple logger for WebSocket connections
	log.Info().Msg("WebSocket connection attempt")
	log.Info().Str("method", r.Method).Str("url", r.URL.String()).Msg("WebSocket request details")

	// Check if the response writer implements Hijacker
	if _, ok := w.(http.Hijacker); !ok {
		log.Error().Msg("Response writer does not implement http.Hijacker")
		http.Error(w, "WebSocket not supported", http.StatusInternalServerError)
		return
	}

	// Upgrade HTTP connection to WebSocket
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Error().Err(err).Msg("Failed to upgrade connection to WebSocket")
		return
	}
	defer conn.Close()

	// Register the connection
	h.wsManager.register <- conn
	log.Info().Msg("WebSocket client connected")

	// Handle incoming messages
	for {
		var msg WSMessage
		err := conn.ReadJSON(&msg)
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				log.Error().Err(err).Msg("WebSocket error")
			}
			break
		}

		log.Debug().Str("type", msg.Type).Msg("Received WebSocket message")

		// Handle different message types
		switch msg.Type {
		case "ping":
			// Respond to ping with pong
			response := WSMessage{
				Type: "pong",
				Data: "pong",
			}
			if err := conn.WriteJSON(response); err != nil {
				log.Error().Err(err).Msg("Failed to send pong response")
				break
			}

		case "subscribe":
			// Client wants to subscribe to updates
			// For now, we just acknowledge the subscription
			response := WSMessage{
				Type: "subscribed",
				Data: "You are now subscribed to updates",
			}
			if err := conn.WriteJSON(response); err != nil {
				log.Error().Err(err).Msg("Failed to send subscription response")
				break
			}

		default:
			log.Warn().Str("type", msg.Type).Msg("Unknown WebSocket message type")
		}
	}

	// Unregister the connection when done
	h.wsManager.unregister <- conn
	log.Info().Msg("WebSocket client disconnected")
}

// BroadcastReedNotification broadcasts a reed notification to all connected clients
func (h *Handlers) BroadcastReedNotification(reedID, userID, username, content string) {
	notification := ReedNotification{
		ReedID:   reedID,
		UserID:   userID,
		Username: username,
		Content:  content,
	}

	message := WSMessage{
		Type: "reed_notification",
		Data: notification,
	}

	messageBytes, err := json.Marshal(message)
	if err != nil {
		log.Error().Err(err).Msg("Failed to marshal reed notification")
		return
	}

	h.wsManager.Broadcast(messageBytes)
}

// BroadcastUserUpdate broadcasts a user update to all connected clients
func (h *Handlers) BroadcastUserUpdate(userID string, updateType string) {
	message := WSMessage{
		Type: "user_update",
		Data: map[string]interface{}{
			"userId": userID,
			"type":   updateType,
		},
	}

	messageBytes, err := json.Marshal(message)
	if err != nil {
		log.Error().Err(err).Msg("Failed to marshal user update")
		return
	}

	h.wsManager.Broadcast(messageBytes)
}

// GetConnectionCount returns the number of active WebSocket connections
func (h *Handlers) GetConnectionCount() int {
	h.wsManager.mutex.RLock()
	defer h.wsManager.mutex.RUnlock()
	return len(h.wsManager.connections)
}
