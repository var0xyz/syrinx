package realtime

import (
	"database/sql"
	"encoding/json"
	"net/http"

	"syrinx/crypto"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/rs/zerolog/log"
	"google.golang.org/protobuf/proto"

	pb "syrinx/proto"
)

const maxPendingEventsPerClient = 100

// RealtimeService represents the main realtime service
type RealtimeService struct {
	connManager   *ConnectionManager
	dbService     *DBService
	authService   *AuthService
	crypto        *crypto.Service
	allowedOrigin string
}

// NewService creates a new realtime service
func NewService(db *sql.DB, crypto *crypto.Service, allowedOrigin string) *RealtimeService {
	dbService := NewDBService(db)
	authService := NewAuthService(db, crypto)
	connManager := NewConnectionManager()
	log.Info().Msg("[OK] Realtime services initialized successfully")

	return &RealtimeService{
		connManager:   connManager,
		dbService:     dbService,
		authService:   authService,
		crypto:        crypto,
		allowedOrigin: allowedOrigin,
	}
}

// Start starts the realtime service
func (rs *RealtimeService) Start(broadcastChan <-chan BroadcastMessage) {
	// Start the connection manager
	go rs.connManager.Start()

	// Start the broadcast handler
	go rs.handleBroadcasts(broadcastChan)

	// Start periodic cleanup
	go rs.startPeriodicCleanup()

	log.Info().Msg("[OK] Realtime service started")
}

// handleBroadcasts handles incoming broadcast messages from the main app
func (rs *RealtimeService) handleBroadcasts(broadcastChan <-chan BroadcastMessage) {
	for message := range broadcastChan {
		log.Debug().
			Str("type", message.Type.String()).
			Str("userID", message.UserID).
			Str("reedID", message.ReedID).
			Msg("Received broadcast message")

		// Log new reed notifications at Info level with user ID and reed ID
		if message.Type == NewReed {
			log.Info().
				Str("userID", message.UserID).
				Str("reedID", message.ReedID).
				Msg("New reed published")

			// Notify online users who follow the author
			followers, err := rs.dbService.GetOnlineFollowers(message.UserID)
			if err != nil {
				log.Error().
					Err(err).
					Msg("Failed to get online followers from database")
			}
			rs.notifyUsers(followers, &message)

			// Notify broadcast subscribers who do not follow the author (complementary group, no duplicates)
			broadcastRecipients, err := rs.dbService.GetBroadcastSubscribersNotFollowing(message.UserID)
			if err != nil {
				log.Error().
					Err(err).
					Msg("Failed to get broadcast subscribers from database")
			}
			rs.notifyUsers(broadcastRecipients, &message)
		}
	}
}

// notifyUsers notifies all active WebSocket connections for the given user IDs
func (rs *RealtimeService) notifyUsers(userIDs []string, message *BroadcastMessage) {
	for _, userID := range userIDs {
		rs.connManager.NotifyUser(userID, message)
	}
}

// startPeriodicCleanup runs periodic cleanup tasks
func (rs *RealtimeService) startPeriodicCleanup() {
	// TODO: Implement periodic cleanup of stale connections
	// This could run every 5 minutes to clean up old online_users entries
}

// HandleWebSocket handles WebSocket connections
func (rs *RealtimeService) HandleWebSocket(w http.ResponseWriter, r *http.Request) {
	log.Info().Msg("WebSocket connection attempt")

	// Authenticate the connection
	userID, err := rs.authService.AuthenticateWebSocket(r)
	if err != nil {
		log.Error().Err(err).Msg("WebSocket authentication failed")
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	log.Info().
		Str("userID", userID).
		Msg("WebSocket authentication successful")

	// Check if the response writer implements Hijacker
	if _, ok := w.(http.Hijacker); !ok {
		log.Error().Msg("Response writer does not implement http.Hijacker")
		http.Error(w, "WebSocket not supported", http.StatusInternalServerError)
		return
	}

	// Upgrade HTTP connection to WebSocket
	upgrader := websocket.Upgrader{
		CheckOrigin:     rs.checkOrigin,
		ReadBufferSize:  1024,
		WriteBufferSize: 1024,
	}

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Error().Err(err).Msg("Failed to upgrade connection to WebSocket")
		return
	}
	defer conn.Close()

	// Create client and register
	client := NewClient(conn, userID)
	rs.connManager.RegisterClient(client)

	// Mark user as online
	if err := rs.dbService.MarkUserOnline(userID); err != nil {
		log.Error().
			Str("userID", userID).
			Err(err).Msg("Failed to mark user as online")
		return
	}

	// Dispatch any pending relay requests for reeds this user holds
	rs.handleUserCameOnline(userID)

	log.Info().
		Str("userID", userID).
		Msg("WebSocket client connected")

	// Handle incoming messages
	rs.handleClientMessages(client)

	// Cleanup when connection closes
	rs.connManager.UnregisterClient(client)
	if err := rs.dbService.MarkUserOffline(userID); err != nil {
		log.Error().Err(err).Msg("Failed to mark user as offline")
	}

	// Remove broadcast subscription on disconnect
	if err := rs.dbService.UnsubscribeFromBroadcast(userID); err != nil {
		log.Error().
			Str("userID", userID).
			Err(err).
			Msg("Failed to remove broadcast subscription on disconnect")
	}

	// Discard all pending relay events for this requester
	if err := rs.dbService.DeletePendingEventsByUser(userID); err != nil {
		log.Error().
			Str("userID", userID).
			Err(err).
			Msg("Failed to delete pending events on disconnect")
	}

	log.Info().
		Str("userID", userID).
		Msg("WebSocket client disconnected")
}

// checkOrigin validates the Origin header against the allowed origin
func (rs *RealtimeService) checkOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")

	// If no origin is provided, reject the connection for security
	if origin == "" {
		log.Warn().Msg("WebSocket connection rejected: missing Origin header")
		return false
	}

	// If no allowed origin is configured, reject all connections
	if rs.allowedOrigin == "" {
		log.Warn().
			Str("origin", origin).
			Msg("WebSocket connection rejected: no allowed origin configured")
		return false
	}

	// Check if the origin matches the allowed origin
	if origin == rs.allowedOrigin {
		log.Debug().
			Str("origin", origin).
			Msg("WebSocket origin validated")
		return true
	}

	log.Warn().
		Str("origin", origin).
		Str("allowedOrigin", rs.allowedOrigin).
		Msg("WebSocket connection rejected: origin not allowed")
	return false
}

// handleClientMessages handles incoming messages from a client
func (rs *RealtimeService) handleClientMessages(client *Client) {
	for {
		messageType, data, err := client.conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				log.Error().Err(err).Msg("WebSocket error")
			} else {
				log.Info().Msg("Client disconnected")
			}
			break
		}

		// Handle both binary (protobuf) and text (JSON) messages
		switch messageType {
		case websocket.BinaryMessage:
			rs.handleProtobufMessage(client, data)
		case websocket.TextMessage:
			rs.handleJSONMessage(client, data)
		default:
			log.Warn().Int("messageType", messageType).Msg("Received unsupported message type, ignoring")
			continue
		}
	}
}

// handleProtobufMessage handles protobuf messages
func (rs *RealtimeService) handleProtobufMessage(client *Client, data []byte) {
	var msg pb.WSMessage
	if err := proto.Unmarshal(data, &msg); err != nil {
		log.Error().Err(err).Msg("Failed to unmarshal protobuf message")
		return
	}

	log.Debug().Str("type", msg.Type.String()).Msg("Received protobuf WebSocket message")

	// Handle different message types
	switch msg.Type {
	case pb.MessageType_PING:
		rs.handlePing(client, msg.GetPing())

	case pb.MessageType_SUBSCRIBE_USER:
		rs.handleSubscribeUser(client, msg.GetSubscribe())

	case pb.MessageType_SUBSCRIBE_BROADCAST:
		rs.handleSubscribeBroadcast(client, msg.GetSubscribe())

	case pb.MessageType_UNSUBSCRIBE_USER:
		rs.handleUnsubscribeUser(client, msg.GetSubscribe())

	case pb.MessageType_UNSUBSCRIBE_BROADCAST:
		rs.handleUnsubscribeBroadcast(client, msg.GetSubscribe())

	default:
		log.Warn().Str("type", msg.Type.String()).Msg("Unknown protobuf WebSocket message type")
	}
}

// handleJSONMessage handles JSON messages for testing
func (rs *RealtimeService) handleJSONMessage(client *Client, data []byte) {
	log.Debug().Str("data", string(data)).Msg("Received JSON WebSocket message")

	// Parse JSON message
	var jsonMsg map[string]interface{}
	if err := json.Unmarshal(data, &jsonMsg); err != nil {
		log.Error().Err(err).Str("data", string(data)).Msg("Failed to unmarshal JSON message")
		return
	}

	// Extract message type
	msgType, ok := jsonMsg["type"].(string)
	if !ok {
		log.Warn().Str("data", string(data)).Msg("JSON message missing type field")
		return
	}

	// Handle different message types
	switch msgType {
	case "ping":
		// Send pong response
		response := map[string]interface{}{
			"type": "pong",
			"data": jsonMsg["data"],
		}
		if jsonBytes, err := json.Marshal(response); err == nil {
			client.conn.WriteMessage(websocket.TextMessage, jsonBytes)
		}

	case "SUBSCRIBE_USER":
		rs.handleSubscribeUserJSON(client)

	case "SUBSCRIBE_BROADCAST":
		rs.handleSubscribeBroadcastJSON(client)

	case "UNSUBSCRIBE_USER":
		rs.handleUnsubscribeUserJSON(client)

	case "UNSUBSCRIBE_BROADCAST":
		rs.handleUnsubscribeBroadcastJSON(client)

	case "REQUEST_REED":
		rs.handleRequestReed(client, jsonMsg)

	case "RELAY_RESPONSE":
		rs.handleRelayResponse(client, jsonMsg)

	default:
		log.Warn().Str("type", msgType).Msg("Unknown JSON WebSocket message type")
	}
}

// handleSubscribeUserJSON handles JSON user subscription requests
func (rs *RealtimeService) handleSubscribeUserJSON(client *Client) {
	rs.handleSubscribeUser(client, nil)

	// Send JSON response
	response := map[string]interface{}{
		"type": "subscribed",
		"data": "Subscribed to user notifications",
	}
	if jsonBytes, err := json.Marshal(response); err == nil {
		client.conn.WriteMessage(websocket.TextMessage, jsonBytes)
	}
}

// handleSubscribeBroadcastJSON handles JSON broadcast subscription requests
func (rs *RealtimeService) handleSubscribeBroadcastJSON(client *Client) {
	rs.handleSubscribeBroadcast(client, nil)

	// Send JSON response
	response := map[string]interface{}{
		"type": "subscribed",
		"data": "Subscribed to broadcast notifications",
	}
	if jsonBytes, err := json.Marshal(response); err == nil {
		client.conn.WriteMessage(websocket.TextMessage, jsonBytes)
	}
}

// handleUnsubscribeUserJSON handles JSON user unsubscription requests
func (rs *RealtimeService) handleUnsubscribeUserJSON(client *Client) {
	rs.handleUnsubscribeUser(client, nil)

	// No response needed for unsubscribe
}

// handleUnsubscribeBroadcastJSON handles JSON broadcast unsubscription requests
func (rs *RealtimeService) handleUnsubscribeBroadcastJSON(client *Client) {
	rs.handleUnsubscribeBroadcast(client, nil)

	// No response needed for unsubscribe
}

// handlePing handles ping messages
func (rs *RealtimeService) handlePing(client *Client, ping *pb.PingMessage) {
	response := &pb.WSMessage{
		Type: pb.MessageType_PONG,
		Payload: &pb.WSMessage_Pong{
			Pong: &pb.PongMessage{
				Data: ping.GetData(),
			},
		},
	}

	rs.sendProtobufMessage(client, response)
}

// handleSubscribeUser handles user subscription requests
func (rs *RealtimeService) handleSubscribeUser(client *Client, subscribe *pb.SubscribeMessage) {
	client.Subscribe(SubscribeUser)

	response := &pb.WSMessage{
		Type: pb.MessageType_SUBSCRIBED,
		Payload: &pb.WSMessage_Subscribed{
			Subscribed: &pb.SubscribedMessage{
				Data: "Subscribed to user notifications",
			},
		},
	}

	rs.sendProtobufMessage(client, response)
}

// handleSubscribeBroadcast handles broadcast subscription requests
func (rs *RealtimeService) handleSubscribeBroadcast(client *Client, subscribe *pb.SubscribeMessage) {
	// Persist to database
	err := rs.dbService.SubscribeToBroadcast(client.userID)
	if err != nil {
		log.Error().
			Str("userID", client.userID).
			Err(err).
			Msg("Failed to persist broadcast subscription to database")
		// Continue anyway - in-memory subscription is active
	}

	response := &pb.WSMessage{
		Type: pb.MessageType_SUBSCRIBED,
		Payload: &pb.WSMessage_Subscribed{
			Subscribed: &pb.SubscribedMessage{
				Data: "Subscribed to broadcast notifications",
			},
		},
	}

	rs.sendProtobufMessage(client, response)
}

// handleUnsubscribeUser handles user unsubscription requests
func (rs *RealtimeService) handleUnsubscribeUser(client *Client, subscribe *pb.SubscribeMessage) {
	client.Unsubscribe(SubscribeUser)
}

// handleUnsubscribeBroadcast handles broadcast unsubscription requests
func (rs *RealtimeService) handleUnsubscribeBroadcast(client *Client, subscribe *pb.SubscribeMessage) {
	// Remove from database
	if err := rs.dbService.UnsubscribeFromBroadcast(client.userID); err != nil {
		log.Error().
			Str("userID", client.userID).
			Err(err).
			Msg("Failed to remove broadcast subscription from database")
		// Continue anyway - in-memory subscription is removed
	}
}

// sendProtobufMessage sends a protobuf message to a client
func (rs *RealtimeService) sendProtobufMessage(client *Client, msg *pb.WSMessage) {
	// Marshal the protobuf message
	data, err := proto.Marshal(msg)
	if err != nil {
		log.Error().Err(err).Msg("Failed to marshal protobuf message")
		return
	}

	// Send as binary message
	if err := client.conn.WriteMessage(websocket.BinaryMessage, data); err != nil {
		log.Error().Err(err).Msg("Failed to write protobuf message")
		return
	}
}

// GetConnectionCount returns the number of active connections
func (rs *RealtimeService) GetConnectionCount() int {
	return rs.connManager.GetConnectionCount()
}

// GetOnlineUsers returns a list of online user IDs
func (rs *RealtimeService) GetOnlineUsers() []string {
	return rs.connManager.GetOnlineUsers()
}

func generateEventID() string {
	return uuid.New().String()
}

func (rs *RealtimeService) handleRequestReed(client *Client, msg map[string]interface{}) {
	data, _ := msg["data"].(map[string]interface{})
	requestID, _ := data["request_id"].(string)
	reedID, _ := data["reed_id"].(string)
	if requestID == "" || reedID == "" {
		return
	}

	count, err := rs.dbService.CountPendingEventsByUser(client.userID)
	if err != nil {
		log.Error().Err(err).Str("userID", client.userID).Msg("Failed to count pending events")
		return
	}
	if count >= maxPendingEventsPerClient {
		rs.connManager.SendToUser(client.userID, map[string]interface{}{
			"type": "ERROR",
			"data": map[string]interface{}{"request_id": requestID, "message": "too many pending requests"},
		})
		return
	}

	eventID := generateEventID()
	if err := rs.dbService.CreatePendingEvent(eventID, requestID, client.userID, "request_reed", reedID); err != nil {
		log.Error().Err(err).Msg("Failed to create pending event")
		return
	}

	rs.connManager.SendToUser(client.userID, map[string]interface{}{
		"type": "REQUEST_ACK",
		"data": map[string]interface{}{"request_id": requestID, "event_id": eventID},
	})

	holders, err := rs.dbService.GetOnlineUsersWithReed(reedID)
	if err != nil {
		log.Error().Err(err).Str("reedID", reedID).Msg("Failed to get online users with reed")
		return
	}
	if len(holders) > 0 {
		rs.connManager.SendToUser(holders[0], map[string]interface{}{
			"type": "RELAY_REQUEST",
			"data": map[string]interface{}{"event_id": eventID, "reed_id": reedID},
		})
	}
}

func (rs *RealtimeService) handleRelayResponse(client *Client, msg map[string]interface{}) {
	data, _ := msg["data"].(map[string]interface{})
	eventID, _ := data["event_id"].(string)
	if eventID == "" {
		return
	}

	pe, err := rs.dbService.GetPendingEvent(eventID)
	if err != nil {
		log.Error().Err(err).Str("eventID", eventID).Msg("Failed to get pending event")
		return
	}
	if pe == nil {
		return
	}

	rs.connManager.SendToUser(pe.RequesterUserID, map[string]interface{}{
		"type": "DATA_RESPONSE",
		"data": map[string]interface{}{"request_id": pe.RequestID, "data": data["data"]},
	})

	if err := rs.dbService.DeletePendingEvent(eventID); err != nil {
		log.Error().Err(err).Str("eventID", eventID).Msg("Failed to delete pending event")
	}
}

func (rs *RealtimeService) handleUserCameOnline(userID string) {
	events, err := rs.dbService.GetPendingEventsForUser(userID)
	if err != nil {
		log.Error().Err(err).Str("userID", userID).Msg("Failed to get pending events for new user")
		return
	}
	for _, e := range events {
		rs.connManager.SendToUser(userID, map[string]interface{}{
			"type": "RELAY_REQUEST",
			"data": map[string]interface{}{"event_id": e.EventID, "reed_id": e.ReedID},
		})
	}
}
