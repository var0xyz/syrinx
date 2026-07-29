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
		reedSubscribers: make(map[string]map[*Client]bool),
	}
}

// Start starts the connection manager
func (cm *ConnectionManager) Start() {
	ticker := time.NewTicker(30 * time.Second) // Ping ticker
	defer ticker.Stop()

	for range ticker.C {
		cm.pingClients()
	}
}

// RegisterClient registers a new client (synchronous so delivery can run immediately).
func (cm *ConnectionManager) RegisterClient(client *Client) {
	cm.registerClient(client)
}

// UnregisterClient unregisters a client
func (cm *ConnectionManager) UnregisterClient(client *Client) {
	cm.unregisterClient(client)
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

	cm.clearReedSubscriptions(client)

	// Close the connection
	client.conn.Close()

	log.Info().
		Str("userID", client.userID).
		Msg("Client unregistered")
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

// HasConnection reports whether any active WebSocket is registered for the user.
func (cm *ConnectionManager) HasConnection(userID string) bool {
	cm.mutex.RLock()
	defer cm.mutex.RUnlock()

	userConns, exists := cm.userConnections[userID]
	return exists && len(userConns) > 0
}

// pingClients sends ping messages to all clients
func (cm *ConnectionManager) pingClients() {
	cm.mutex.RLock()
	defer cm.mutex.RUnlock()

	for _, userConns := range cm.userConnections {
		for conn, _ := range userConns {
			cm.sendPing(conn)
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

func reedSubKey(authorUserID, reedID string) string {
	return authorUserID + "/" + reedID
}

// SubscribeReed adds a reed-scoped subscription for the client.
func (cm *ConnectionManager) SubscribeReed(client *Client, authorUserID, reedID string) {
	cm.mutex.Lock()
	defer cm.mutex.Unlock()

	key := reedSubKey(authorUserID, reedID)
	client.reedSubscriptions[key] = true
	if cm.reedSubscribers[key] == nil {
		cm.reedSubscribers[key] = make(map[*Client]bool)
	}
	cm.reedSubscribers[key][client] = true
}

// UnsubscribeReed removes a reed-scoped subscription for the client.
func (cm *ConnectionManager) UnsubscribeReed(client *Client, authorUserID, reedID string) {
	cm.mutex.Lock()
	defer cm.mutex.Unlock()

	key := reedSubKey(authorUserID, reedID)
	delete(client.reedSubscriptions, key)
	if subs, ok := cm.reedSubscribers[key]; ok {
		delete(subs, client)
		if len(subs) == 0 {
			delete(cm.reedSubscribers, key)
		}
	}
}

func (cm *ConnectionManager) clearReedSubscriptions(client *Client) {
	for key := range client.reedSubscriptions {
		if subs, ok := cm.reedSubscribers[key]; ok {
			delete(subs, client)
			if len(subs) == 0 {
				delete(cm.reedSubscribers, key)
			}
		}
	}
	client.reedSubscriptions = make(map[string]bool)
}

// BroadcastReedCoverage sends a coverage update to all subscribers of a reed.
func (cm *ConnectionManager) BroadcastReedCoverage(payload map[string]interface{}) error {
	cm.mutex.RLock()
	defer cm.mutex.RUnlock()

	authorUserID, _ := payload["userID"].(string)
	reedID, _ := payload["reedID"].(string)
	if authorUserID == "" || reedID == "" {
		return fmt.Errorf("reed coverage payload missing userID or reedID")
	}

	subs := cm.reedSubscribers[reedSubKey(authorUserID, reedID)]
	if len(subs) == 0 {
		return nil
	}

	jsonBytes, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	for client := range subs {
		if err := client.conn.WriteMessage(websocket.TextMessage, jsonBytes); err != nil {
			log.Error().Err(err).Str("userID", client.userID).Msg("Failed to send REED_COVERAGE")
		}
	}
	return nil
}
