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

// UnregisterClient removes a client. Returns true when the user still has at
// least one other active WebSocket (partial disconnect — skip offline cleanup).
func (cm *ConnectionManager) UnregisterClient(client *Client) bool {
	return cm.unregisterClient(client)
}

// registerClient handles client registration. A user keeps a single active
// session: any prior sockets are closed so RELAY_REQUEST / fanout cannot land
// on a zombie connection the SPA no longer reads.
func (cm *ConnectionManager) registerClient(client *Client) {
	log.Info().Msg("Registering client " + client.userID + " with connection " + client.conn.RemoteAddr().String())
	cm.mutex.Lock()

	var stale []*Client
	if existing := cm.userConnections[client.userID]; existing != nil {
		for conn, old := range existing {
			if conn == client.conn {
				continue
			}
			delete(existing, conn)
			cm.clearReedSubscriptions(old)
			stale = append(stale, old)
		}
	}
	if cm.userConnections[client.userID] == nil {
		cm.userConnections[client.userID] = make(map[*websocket.Conn]*Client)
	}
	cm.userConnections[client.userID][client.conn] = client

	log.Info().
		Str("userID", client.userID).
		Int("totalConnections", len(cm.userConnections[client.userID])).
		Int("replaced", len(stale)).
		Msg("Client registered")
	cm.mutex.Unlock()

	for _, old := range stale {
		old.conn.Close()
	}
}

// unregisterClient removes the client. Returns true if the user still has
// another registered connection.
func (cm *ConnectionManager) unregisterClient(client *Client) bool {
	cm.mutex.Lock()
	defer cm.mutex.Unlock()

	if userConns, exists := cm.userConnections[client.userID]; exists {
		delete(userConns, client.conn)
		if len(userConns) == 0 {
			delete(cm.userConnections, client.userID)
		}
	}

	cm.clearReedSubscriptions(client)
	client.conn.Close()

	remaining := cm.userConnections[client.userID]
	stillOnline := len(remaining) > 0

	log.Info().
		Str("userID", client.userID).
		Bool("stillOnline", stillOnline).
		Msg("Client unregistered")
	return stillOnline
}

// SendToUser sends a JSON-encoded message to every active connection for the user.
// Delivering to all avoids losing messages when a superseded socket is still
// briefly registered; clients that already closed a socket simply drop the write.
func (cm *ConnectionManager) SendToUser(userID string, msg any) error {
	cm.mutex.RLock()
	userConns, exists := cm.userConnections[userID]
	if !exists || len(userConns) == 0 {
		cm.mutex.RUnlock()
		return fmt.Errorf("no active connection for user %s", userID)
	}
	clients := make([]*Client, 0, len(userConns))
	for _, c := range userConns {
		clients = append(clients, c)
	}
	cm.mutex.RUnlock()

	jsonBytes, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("failed to marshal message: %w", err)
	}

	var lastErr error
	sent := 0
	for _, c := range clients {
		if err := c.writeMessage(websocket.TextMessage, jsonBytes); err != nil {
			lastErr = err
			continue
		}
		sent++
	}
	if sent == 0 {
		if lastErr != nil {
			return fmt.Errorf("failed to send message: %w", lastErr)
		}
		return fmt.Errorf("no active connection for user %s", userID)
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
	clients := make([]*Client, 0)
	for _, userConns := range cm.userConnections {
		for _, c := range userConns {
			clients = append(clients, c)
		}
	}
	cm.mutex.RUnlock()

	for _, c := range clients {
		cm.sendPing(c)
	}
}

// sendPing sends a ping message to a connection
func (cm *ConnectionManager) sendPing(client *Client) {
	ping := &pb.WSMessage{
		Type: pb.MessageType_PING,
		Payload: &pb.WSMessage_Ping{
			Ping: &pb.PingMessage{
				Data: "ping",
			},
		},
	}

	cm.sendProtobufMessage(client, ping)
}

// sendProtobufMessage sends a protobuf message to a connection
func (cm *ConnectionManager) sendProtobufMessage(client *Client, msg *pb.WSMessage) {
	data, err := proto.Marshal(msg)
	if err != nil {
		log.Error().Err(err).Msg("Failed to marshal protobuf message")
		return
	}

	if err := client.writeMessage(websocket.BinaryMessage, data); err != nil {
		log.Error().Err(err).Msg("Failed to write protobuf message")
	}
}

func (c *Client) writeMessage(messageType int, data []byte) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	return c.conn.WriteMessage(messageType, data)
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
		if err := client.writeMessage(websocket.TextMessage, jsonBytes); err != nil {
			log.Error().Err(err).Str("userID", client.userID).Msg("Failed to send REED_COVERAGE")
		}
	}
	return nil
}
