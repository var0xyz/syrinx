package realtime

import (
	"encoding/json"
	"time"

	"github.com/gorilla/websocket"
	"github.com/rs/zerolog/log"
	"google.golang.org/protobuf/proto"

	pb "syrinx/proto"
)

// NewConnectionManager creates a new connection manager
func NewConnectionManager() *ConnectionManager {
	return &ConnectionManager{
		userConnections:      make(map[string]map[*websocket.Conn]*Client),
		broadcastSubscribers: make(map[*websocket.Conn]*Client),
		register:             make(chan *Client),
		unregister:           make(chan *Client),
		broadcast:            make(chan *BroadcastMessage),
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

		case message := <-cm.broadcast:
			cm.handleBroadcast(message)

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

// BroadcastMessage broadcasts a message to appropriate subscribers
func (cm *ConnectionManager) BroadcastMessage(message *BroadcastMessage) {
	cm.broadcast <- message
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

	// Remove from broadcast subscribers
	delete(cm.broadcastSubscribers, client.conn)

	// Close the connection
	client.conn.Close()

	log.Info().
		Str("userID", client.userID).
		Msg("Client unregistered")
}

// handleBroadcast routes broadcast messages to appropriate subscribers
func (cm *ConnectionManager) handleBroadcast(message *BroadcastMessage) {
	cm.mutex.RLock()
	defer cm.mutex.RUnlock()

	switch message.Type {
	case NewReed:
		cm.broadcastReedNotification(message)
	case UserUpdate:
		cm.broadcastUserUpdate(message)
	case ReedDeleted:
		cm.broadcastReedDeletion(message)
	}
}

// broadcastReedNotification broadcasts a reed notification
func (cm *ConnectionManager) broadcastReedNotification(message *BroadcastMessage) {
	// Send to broadcast subscribers
	for conn, client := range cm.broadcastSubscribers {
		if client.IsSubscribed(SubscribeBroadcast) {
			cm.sendReedNotification(conn, message)
		}
	}

	// Send to author's followers (if they have user subscriptions)
	if userConns, exists := cm.userConnections[message.UserID]; exists {
		for conn, client := range userConns {
			if client.IsSubscribed(SubscribeUser) {
				cm.sendReedNotification(conn, message)
			}
		}
	}
}

// broadcastUserUpdate broadcasts a user update
func (cm *ConnectionManager) broadcastUserUpdate(message *BroadcastMessage) {
	// Send to broadcast subscribers
	for conn, client := range cm.broadcastSubscribers {
		if client.IsSubscribed(SubscribeBroadcast) {
			cm.sendUserUpdate(conn, message)
		}
	}

	// Send to the user's own connections
	if userConns, exists := cm.userConnections[message.UserID]; exists {
		for conn, client := range userConns {
			if client.IsSubscribed(SubscribeUser) {
				cm.sendUserUpdate(conn, message)
			}
		}
	}
}

// broadcastReedDeletion broadcasts a reed deletion
func (cm *ConnectionManager) broadcastReedDeletion(message *BroadcastMessage) {
	// Send to broadcast subscribers
	for conn, client := range cm.broadcastSubscribers {
		if client.IsSubscribed(SubscribeBroadcast) {
			cm.sendReedDeletion(conn, message)
		}
	}

	// Send to author's connections
	if userConns, exists := cm.userConnections[message.UserID]; exists {
		for conn, client := range userConns {
			if client.IsSubscribed(SubscribeUser) {
				cm.sendReedDeletion(conn, message)
			}
		}
	}
}

// sendReedNotification sends a reed notification to a specific connection
func (cm *ConnectionManager) sendReedNotification(conn *websocket.Conn, message *BroadcastMessage) {
	username, _ := message.Data["username"].(string)
	content, _ := message.Data["content"].(string)

	// For now, send as JSON since frontend doesn't parse protobuf yet
	// TODO: Switch to protobuf once frontend parsing is implemented
	jsonMsg := map[string]interface{}{
		"type": "reed_notification",
		"data": map[string]interface{}{
			"reedId":    message.ReedID,
			"userId":    message.UserID,
			"username":  username,
			"content":   content,
			"timestamp": time.Now().Unix(),
		},
	}

	jsonBytes, err := json.Marshal(jsonMsg)
	if err != nil {
		log.Error().Err(err).Msg("Failed to marshal JSON notification")
		return
	}

	if err := conn.WriteMessage(websocket.TextMessage, jsonBytes); err != nil {
		log.Error().Err(err).Msg("Failed to send JSON notification")
		return
	}
}

// sendUserUpdate sends a user update to a specific connection
func (cm *ConnectionManager) sendUserUpdate(conn *websocket.Conn, message *BroadcastMessage) {
	update := &pb.WSMessage{
		Type: pb.MessageType_USER_UPDATE,
		Payload: &pb.WSMessage_UserUpdate{
			UserUpdate: &pb.UserUpdateMessage{
				UserId:     message.UserID,
				UpdateType: "profile_update",
				Timestamp:  time.Now().Unix(),
			},
		},
	}

	cm.sendProtobufMessage(conn, update)
}

// sendReedDeletion sends a reed deletion to a specific connection
func (cm *ConnectionManager) sendReedDeletion(conn *websocket.Conn, message *BroadcastMessage) {
	// For reed deletion, we can send a simple notification
	// In a more sophisticated system, you might want a specific message type
	notification := &pb.WSMessage{
		Type: pb.MessageType_REED_NOTIFICATION,
		Payload: &pb.WSMessage_ReedNotification{
			ReedNotification: &pb.ReedNotificationMessage{
				ReedId:    message.ReedID,
				UserId:    message.UserID,
				Username:  "System",
				Content:   "Reed deleted",
				Timestamp: time.Now().Unix(),
			},
		},
	}

	cm.sendProtobufMessage(conn, notification)
}

// pingClients sends ping messages to all clients
func (cm *ConnectionManager) pingClients() {
	cm.mutex.RLock()
	defer cm.mutex.RUnlock()

	now := time.Now()

	// Ping all user connections
	for _, userConns := range cm.userConnections {
		for conn, client := range userConns {
			if now.Sub(client.lastPing) > 30*time.Second {
				cm.sendPing(conn)
				client.lastPing = now
			}
		}
	}

	// Ping all broadcast subscribers
	for conn, client := range cm.broadcastSubscribers {
		if now.Sub(client.lastPing) > 30*time.Second {
			cm.sendPing(conn)
			client.lastPing = now
		}
	}
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
	// Marshal the protobuf message
	data, err := proto.Marshal(msg)
	if err != nil {
		log.Error().Err(err).Msg("Failed to marshal protobuf message")
		return
	}

	// Send as binary message
	if err := conn.WriteMessage(websocket.BinaryMessage, data); err != nil {
		log.Error().Err(err).Msg("Failed to write protobuf message")
		return
	}
}

// SubscribeToBroadcast adds a client to broadcast subscribers
func (cm *ConnectionManager) SubscribeToBroadcast(client *Client) {
	cm.mutex.Lock()
	defer cm.mutex.Unlock()

	cm.broadcastSubscribers[client.conn] = client
	client.Subscribe(SubscribeBroadcast)

	log.Info().
		Str("userID", client.userID).
		Msg("Client subscribed to broadcast")
}

// UnsubscribeFromBroadcast removes a client from broadcast subscribers
func (cm *ConnectionManager) UnsubscribeFromBroadcast(client *Client) {
	cm.mutex.Lock()
	defer cm.mutex.Unlock()

	delete(cm.broadcastSubscribers, client.conn)
	client.Unsubscribe(SubscribeBroadcast)

	log.Info().
		Str("userID", client.userID).
		Msg("Client unsubscribed from broadcast")
}

// NotifyUser sends a reed notification to all active connections for a specific user
func (cm *ConnectionManager) NotifyUser(userID string, message *BroadcastMessage) {
	cm.mutex.RLock()
	defer cm.mutex.RUnlock()

	if userConns, exists := cm.userConnections[userID]; exists {
		for conn := range userConns {
			if message.Type == NewReed {
				cm.sendReedNotification(conn, message)
			}
		}
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
