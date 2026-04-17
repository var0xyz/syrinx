package realtime

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/gorilla/websocket"
	"github.com/rs/zerolog/log"
	"google.golang.org/protobuf/proto"

	pb "syrinx/proto"
)

// NewConnectionManager creates a new connection manager
func NewConnectionManager() *ConnectionManager {
	return &ConnectionManager{
		userConnections: make(map[string]map[*websocket.Conn]*Client),
		register:        make(chan *Client),
		unregister:      make(chan *Client),
	}
}

// Start starts the connection manager
func (cm *ConnectionManager) Start() {
	ticker := time.NewTicker(30 * time.Second) // Ping ticker
	defer ticker.Stop()

	for {
		select {
		case client := <-cm.register:
			cm.registerClient(client)

		case client := <-cm.unregister:
			cm.unregisterClient(client)

		case <-ticker.C:
			cm.pingClients()
		}
	}
}

// RegisterClient registers a new client
func (cm *ConnectionManager) RegisterClient(client *Client) {
	cm.register <- client
}

// UnregisterClient unregisters a client
func (cm *ConnectionManager) UnregisterClient(client *Client) {
	cm.unregister <- client
}

// registerClient handles client registration
func (cm *ConnectionManager) registerClient(client *Client) {
	log.Info().Msg("Registering client " + client.userID + " with connection " + client.conn.RemoteAddr().String())
	cm.mutex.Lock()
	defer cm.mutex.Unlock()

	// Add to user connections
	if cm.userConnections[client.userID] == nil {
		cm.userConnections[client.userID] = make(map[*websocket.Conn]*Client)
	}
	cm.userConnections[client.userID][client.conn] = client

	log.Info().
		Str("userID", client.userID).
		Int("totalConnections", len(cm.userConnections[client.userID])).
		Msg("Client registered")
}

// unregisterClient handles client unregistration
func (cm *ConnectionManager) unregisterClient(client *Client) {
	cm.mutex.Lock()
	defer cm.mutex.Unlock()

	// Remove from user connections
	if userConns, exists := cm.userConnections[client.userID]; exists {
		delete(userConns, client.conn)
		if len(userConns) == 0 {
			delete(cm.userConnections, client.userID)
		}
	}

	// Close the connection
	client.conn.Close()

	log.Info().
		Str("userID", client.userID).
		Msg("Client unregistered")
}

// NotifyUser sends a reed notification to all active connections for a specific user
func (cm *ConnectionManager) NotifyUser(userID string, message *BroadcastMessage) {
	cm.mutex.RLock()
	defer cm.mutex.RUnlock()

	if userConns, exists := cm.userConnections[userID]; exists {
		for conn := range userConns {
			if message.Type == NewReed {
				err := cm.sendReedNotification(conn, message)
				if err != nil {
					log.Error().
						Err(err).
						Msg("Failed to send notification")
				}
			}
		}
	}
}

// SendToUser sends a JSON-encoded message to any one active connection for the given user
func (cm *ConnectionManager) SendToUser(userID string, msg any) error {
	cm.mutex.RLock()
	defer cm.mutex.RUnlock()

	userConns, exists := cm.userConnections[userID]
	if !exists || len(userConns) == 0 {
		return fmt.Errorf("no active connection for user %s", userID)
	}

	jsonBytes, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("failed to marshal message: %w", err)
	}

	// Send to the first available connection
	for conn := range userConns {
		if err := conn.WriteMessage(websocket.TextMessage, jsonBytes); err != nil {
			return fmt.Errorf("failed to send message: %w", err)
		}
		return nil
	}
	return nil
}

// pingClients sends ping messages to all clients
func (cm *ConnectionManager) pingClients() {
	cm.mutex.RLock()
	defer cm.mutex.RUnlock()

	now := time.Now()

	for _, userConns := range cm.userConnections {
		for conn, client := range userConns {
			if now.Sub(client.lastPing) > 30*time.Second {
				cm.sendPing(conn)
				client.lastPing = now
			}
		}
	}
}

// sendReedNotification sends a reed notification to a specific connection
func (cm *ConnectionManager) sendReedNotification(conn *websocket.Conn, message *BroadcastMessage) error {
	// For now, send as JSON since frontend doesn't parse protobuf yet
	// TODO: Switch to protobuf once frontend parsing is implemented
	jsonMsg := map[string]interface{}{
		"type": "reed_notification",
		"data": map[string]interface{}{
			"serverId": message.ServerID,
			"userId":   message.UserID,
			"reedId":   message.ReedID,
			"iceServers": []map[string]interface{}{
				{"urls": "stun:stun.l.google.com:19302"},
			},
		},
	}

	jsonBytes, err := json.Marshal(jsonMsg)
	if err != nil {
		return fmt.Errorf("Failed to marshal JSON notification: %w", err)
	}

	if err := conn.WriteMessage(websocket.TextMessage, jsonBytes); err != nil {
		return fmt.Errorf("Failed to send JSON notification: %w", err)
	}
	return nil
}

// sendPing sends a ping message to a connection
func (cm *ConnectionManager) sendPing(conn *websocket.Conn) {
	ping := &pb.WSMessage{
		Type: pb.MessageType_PING,
		Payload: &pb.WSMessage_Ping{
			Ping: &pb.PingMessage{
				Data: "ping",
			},
		},
	}

	cm.sendProtobufMessage(conn, ping)
}

// sendProtobufMessage sends a protobuf message to a connection
func (cm *ConnectionManager) sendProtobufMessage(conn *websocket.Conn, msg *pb.WSMessage) {
	data, err := proto.Marshal(msg)
	if err != nil {
		log.Error().Err(err).Msg("Failed to marshal protobuf message")
		return
	}

	if err := conn.WriteMessage(websocket.BinaryMessage, data); err != nil {
		log.Error().Err(err).Msg("Failed to write protobuf message")
	}
}

// GetConnectionCount returns the total number of active connections
func (cm *ConnectionManager) GetConnectionCount() int {
	cm.mutex.RLock()
	defer cm.mutex.RUnlock()

	total := 0
	for _, userConns := range cm.userConnections {
		total += len(userConns)
	}
	return total
}

// GetOnlineUsers returns a list of online user IDs
func (cm *ConnectionManager) GetOnlineUsers() []string {
	cm.mutex.RLock()
	defer cm.mutex.RUnlock()

	users := make([]string, 0, len(cm.userConnections))
	for userID := range cm.userConnections {
		users = append(users, userID)
	}
	return users
}
