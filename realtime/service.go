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

// RealtimeService represents the main realtime service
type RealtimeService struct {
	connManager   *ConnectionManager
	dbService     *DBService
	authService   *AuthService
	crypto        *crypto.Service
	allowedOrigin string
	ongoingCheck  func(userID string) (bool, error)
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

// SetOngoingCheck installs an optional import-gate check used after WebSocket
// auth succeeds. When the check returns true, the connection is rejected with 403.
func (rs *RealtimeService) SetOngoingCheck(check func(userID string) (bool, error)) {
	rs.ongoingCheck = check
}

// rejectOngoingImport writes an error response and returns true when the user
// must finish recovery import before opening a WebSocket.
func (rs *RealtimeService) rejectOngoingImport(w http.ResponseWriter, userID string) bool {
	if rs.ongoingCheck == nil {
		return false
	}
	ongoing, err := rs.ongoingCheck(userID)
	if err != nil {
		log.Error().Err(err).Str("userID", userID).Msg("Import gate check failed")
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return true
	}
	if ongoing {
		log.Info().Str("userID", userID).Msg("WebSocket rejected: ongoing recovery import")
		http.Error(w, "Forbidden: finish recovery import first", http.StatusForbidden)
		return true
	}
	return false
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

		if message.Type == NewReed {
			rs.fanoutNewReed(message.UserID, message.ReedID)
		}

		if message.Type == EchoCountChanged {
			rs.notifyReedEchoes(message.UserID, message.ReedID)
		}

		if message.Type == ReedRemoved {
			log.Info().
				Str("userID", message.UserID).
				Str("reedID", message.ReedID).
				Msg("Reed removed; fanout cert")

			cert := message.Data["cert"]
			followers, err := rs.dbService.GetOnlineFollowers(message.UserID)
			if err != nil {
				log.Error().Err(err).Msg("Failed to get online followers for reed removal")
			}
			broadcastRecipients, err := rs.dbService.GetBroadcastSubscribers(message.UserID)
			if err != nil {
				log.Error().Err(err).Msg("Failed to get broadcast subscribers for reed removal")
			}
			rs.dispatchRemovalMany(followers, message.UserID, message.ReedID, cert)
			rs.dispatchRemovalMany(broadcastRecipients, message.UserID, message.ReedID, cert)

			profileSubscribers, err := rs.dbService.GetProfileSubscribers(message.UserID)
			if err != nil {
				log.Error().Err(err).Msg("Failed to get profile subscribers for reed removal")
			}
			for _, sub := range profileSubscribers {
				rs.dispatchRemovalTo(sub.ViewerUserID, message.UserID, message.ReedID, cert)
			}
		}

		if message.Type == AccountRemoved {
			log.Info().
				Str("userID", message.UserID).
				Msg("Account removed; fanout cert")

			cert := message.Data["cert"]
			followers, err := rs.dbService.GetOnlineFollowers(message.UserID)
			if err != nil {
				log.Error().Err(err).Msg("Failed to get online followers for account removal")
			}
			broadcastRecipients, err := rs.dbService.GetBroadcastSubscribers(message.UserID)
			if err != nil {
				log.Error().Err(err).Msg("Failed to get broadcast subscribers for account removal")
			}
			rs.dispatchAccountRemovalMany(followers, message.UserID, cert)
			rs.dispatchAccountRemovalMany(broadcastRecipients, message.UserID, cert)

			profileSubscribers, err := rs.dbService.GetProfileSubscribers(message.UserID)
			if err != nil {
				log.Error().Err(err).Msg("Failed to get profile subscribers for account removal")
			}
			for _, sub := range profileSubscribers {
				rs.dispatchAccountRemovalTo(sub.ViewerUserID, message.UserID, cert)
			}
		}
	}
}

// fanoutNewReed dispatches a newly published reed to followers, broadcast subs, and profile subs.
func (rs *RealtimeService) fanoutNewReed(authorUserID, reedID string) {
	log.Info().
		Str("userID", authorUserID).
		Str("reedID", reedID).
		Msg("Fanning out new reed")

	broadcastRecipients, err := rs.dbService.GetBroadcastSubscribers(authorUserID)
	if err != nil {
		log.Error().
			Err(err).
			Msg("Failed to get broadcast subscribers from database")
	}

	rs.fanoutNewReedCore(authorUserID, reedID, broadcastRecipients)
}

// fanoutNewReedNoBroadcast dispatches to followers and profile subs only (no broadcast stream).
func (rs *RealtimeService) fanoutNewReedNoBroadcast(authorUserID, reedID string) {
	log.Info().
		Str("userID", authorUserID).
		Str("reedID", reedID).
		Msg("Fanning out new reed (no broadcast)")

	rs.fanoutNewReedCore(authorUserID, reedID, nil)
}

func (rs *RealtimeService) fanoutNewReedCore(authorUserID, reedID string, broadcastRecipients []string) {
	followers, err := rs.dbService.GetOnlineFollowers(authorUserID)
	if err != nil {
		log.Error().
			Err(err).
			Msg("Failed to get online followers from database")
	}

	log.Info().
		Str("userID", authorUserID).
		Int("followers", len(followers)).
		Int("broadcastSubscribers", len(broadcastRecipients)).
		Msg("Dispatching new reed to recipients")
	rs.dispatchMany(followers, NewReedEvent, reedID, authorUserID)
	rs.dispatchMany(broadcastRecipients, BroadcastReedEvent, reedID, authorUserID)

	profileSubscribers, err := rs.dbService.GetProfileSubscribers(authorUserID)
	if err != nil {
		log.Error().
			Err(err).
			Msg("Failed to get profile subscribers from database")
	}

	for _, sub := range profileSubscribers {
		eventID := generateEventID()
		requestID := generateEventID()
		if err := rs.dbService.CreateProfileSubscriptionEvent(eventID, requestID, sub.ViewerUserID, NewReedEvent, authorUserID, reedID, sub.SubscriptionID); err != nil {
			log.Error().
				Err(err).
				Str("viewerUserID", sub.ViewerUserID).
				Msg("Failed to create pending event for profile subscriber")
		}
	}

	rs.dispatchNextIfConnected(authorUserID)
}

// dispatchRemovalMany enqueues reed_removed pending events and delivers certs
// server-side (no holder relay).
func (rs *RealtimeService) dispatchRemovalMany(recipients []string, authorUserID, reedID string, cert interface{}) {
	for _, recipientID := range recipients {
		rs.dispatchRemovalTo(recipientID, authorUserID, reedID, cert)
	}
}

func (rs *RealtimeService) dispatchRemovalTo(recipientID, authorUserID, reedID string, cert interface{}) {
	requestID, err := rs.dbService.GetSyncRequestID(recipientID)
	if err != nil || requestID == "" {
		return
	}
	eventID := generateEventID()
	if err := rs.dbService.CreatePendingReedEvent(eventID, requestID, recipientID, ReedRemovedEvent, authorUserID, reedID); err != nil {
		log.Error().Err(err).Str("recipientID", recipientID).Msg("Failed to create reed_removed pending event")
		return
	}
	rs.deliverReedRemoved(eventID, requestID, recipientID, authorUserID, reedID, cert)
}

func (rs *RealtimeService) dispatchAccountRemovalMany(recipients []string, removedUserID string, cert interface{}) {
	for _, recipientID := range recipients {
		rs.dispatchAccountRemovalTo(recipientID, removedUserID, cert)
	}
}

func (rs *RealtimeService) dispatchAccountRemovalTo(recipientID, removedUserID string, cert interface{}) {
	requestID, err := rs.dbService.GetSyncRequestID(recipientID)
	if err != nil || requestID == "" {
		return
	}
	eventID := generateEventID()
	if err := rs.dbService.CreatePendingAccountEvent(eventID, requestID, recipientID, removedUserID); err != nil {
		log.Error().Err(err).Str("recipientID", recipientID).Msg("Failed to create account_removed pending event")
		return
	}
	rs.deliverAccountRemoved(eventID, requestID, recipientID, removedUserID, cert)
}

func (rs *RealtimeService) deliverAccountRemoved(eventID, requestID, recipientID, removedUserID string, cert interface{}) {
	if cert == nil {
		var err error
		cert, err = rs.dbService.GetAccountRemovalWire(removedUserID)
		if err != nil || cert == nil {
			log.Error().Err(err).Str("userID", removedUserID).Msg("Failed to load account removal cert for delivery")
			return
		}
	}
	ok, err := rs.dbService.MarkEventDispatched(eventID)
	if err != nil {
		log.Error().Err(err).Str("eventID", eventID).Msg("Failed to mark account_removed dispatched")
		return
	}
	if !ok {
		return
	}
	if err := rs.connManager.SendToUser(recipientID, NewAccountRemovedMsg(eventID, requestID, removedUserID, cert)); err != nil {
		log.Error().Err(err).Str("recipientID", recipientID).Str("userID", removedUserID).Msg("Failed to send ACCOUNT_REMOVED")
	}
}

func (rs *RealtimeService) deliverReedRemoved(eventID, requestID, recipientID, authorID, reedID string, cert interface{}) {
	if cert == nil {
		var err error
		cert, err = rs.dbService.GetReedRemovalWire(authorID, reedID)
		if err != nil || cert == nil {
			log.Error().Err(err).Str("reedID", reedID).Str("authorID", authorID).Msg("Failed to load reed removal cert for delivery")
			return
		}
	}
	ok, err := rs.dbService.MarkEventDispatched(eventID)
	if err != nil {
		log.Error().Err(err).Str("eventID", eventID).Msg("Failed to mark reed_removed dispatched")
		return
	}
	if !ok {
		return
	}
	if err := rs.connManager.SendToUser(recipientID, NewReedRemovedMsg(eventID, requestID, reedID, cert)); err != nil {
		log.Error().Err(err).Str("recipientID", recipientID).Str("reedID", reedID).Msg("Failed to send REED_REMOVED")
	}
}

// dispatchMany creates a pending reed event for each recipient and triggers relay dispatch to holderUserID.
// holderUserID is also the reed author for new-reed fanout.
func (rs *RealtimeService) dispatchMany(recipients []string, eventName EventName, reedID, authorUserID string) {
	for _, recipientID := range recipients {
		requestID, err := rs.dbService.GetSyncRequestID(recipientID)
		if err != nil || requestID == "" {
			// User hasn't sent SYNC_REQUEST yet; they'll receive this via catchUp on connect.
			log.Debug().
				Str("recipientID", recipientID).
				Str("eventName", string(eventName)).
				Msg("Skipping recipient: no sync_request_id set")
			continue
		}
		eventID := generateEventID()
		if err := rs.dbService.CreatePendingReedEvent(eventID, requestID, recipientID, eventName, authorUserID, reedID); err != nil {
			log.Error().
				Err(err).
				Str("recipientID", recipientID).
				Msg("Failed to create pending event")
			continue
		}
		log.Debug().
			Str("recipientID", recipientID).
			Str("eventID", eventID).
			Str("holderUserID", authorUserID).
			Msg("Pending event created, dispatching to holder")
		rs.dispatchNextIfConnected(authorUserID)
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

	if rs.rejectOngoingImport(w, userID) {
		return
	}

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
	rs.handleUserCameOnline(client)

	log.Info().
		Str("userID", userID).
		Msg("WebSocket client connected")

	// Handle incoming messages
	rs.handleClientMessages(client)

	// Cleanup when connection closes. Only tear down DB presence when this
	// was the user's last socket — a superseded connection must not wipe
	// online_users, broadcast subscriptions, or in-flight pending events.
	stillOnline := rs.connManager.UnregisterClient(client)
	if stillOnline {
		log.Info().
			Str("userID", userID).
			Msg("WebSocket client replaced; keeping online state")
		return
	}

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

	// Discard all pending relay events for this requester (cascades from profile_subscriptions too)
	if err := rs.dbService.DeletePendingEventsByUser(userID); err != nil {
		log.Error().
			Str("userID", userID).
			Err(err).
			Msg("Failed to delete pending events on disconnect")
	}

	// Clean up any active profile subscriptions for this viewer
	if err := rs.dbService.DeleteProfileSubscriptionsByViewer(userID); err != nil {
		log.Error().
			Str("userID", userID).
			Err(err).
			Msg("Failed to delete profile subscriptions on disconnect")
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
			client.writeMessage(websocket.TextMessage, jsonBytes)
		}

	case "SUBSCRIBE_USER":
		rs.handleSubscribeUserJSON(client)

	case "SUBSCRIBE_BROADCAST":
		rs.handleSubscribeBroadcastJSON(client)

	case "UNSUBSCRIBE_USER":
		rs.handleUnsubscribeUserJSON(client)

	case "UNSUBSCRIBE_BROADCAST":
		rs.handleUnsubscribeBroadcastJSON(client)

	case "SYNC_REQUEST":
		dataBytes, _ := json.Marshal(jsonMsg["data"])
		var syncData SyncRequestData
		if err := json.Unmarshal(dataBytes, &syncData); err == nil {
			rs.handleSyncRequest(client, syncData)
		}

	case "REQUEST_REED":
		rs.handleRequestReed(client, jsonMsg)

	case "RELAY_RESPONSE":
		rs.handleRelayResponse(client, jsonMsg)

	case "RELAY_MISS":
		dataBytes, _ := json.Marshal(jsonMsg["data"])
		var d RelayMissData
		if err := json.Unmarshal(dataBytes, &d); err == nil {
			rs.handleRelayMiss(client, d)
		}

	case "DATA_ACK":
		dataBytes, _ := json.Marshal(jsonMsg["data"])
		var d DataAckData
		if err := json.Unmarshal(dataBytes, &d); err == nil {
			rs.handleDataAck(client, d)
		}

	case "DATA_INVALID":
		dataBytes, _ := json.Marshal(jsonMsg["data"])
		var d DataInvalidData
		if err := json.Unmarshal(dataBytes, &d); err == nil {
			rs.handleDataInvalid(client, d)
		}

	case "SUBSCRIBE_PROFILE":
		rs.handleSubscribeProfile(client, jsonMsg)

	case "UNSUBSCRIBE_PROFILE":
		rs.handleUnsubscribeProfile(client, jsonMsg)

	case "PUBLISH_READY":
		rs.handlePublishReady(client, jsonMsg)

	case "SUBSCRIBE_REED":
		rs.handleSubscribeReed(client, jsonMsg)

	case "UNSUBSCRIBE_REED":
		rs.handleUnsubscribeReed(client, jsonMsg)

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
		client.writeMessage(websocket.TextMessage, jsonBytes)
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
		client.writeMessage(websocket.TextMessage, jsonBytes)
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
	if err := client.writeMessage(websocket.BinaryMessage, data); err != nil {
		log.Error().Err(err).Msg("Failed to write protobuf message")
		return
	}
}

// GetConnectionCount returns the number of active connections
func (rs *RealtimeService) GetConnectionCount() int {
	return rs.connManager.GetConnectionCount()
}

func (rs *RealtimeService) dispatchNext(holderUserID string) {
	pe, err := rs.dbService.GetNextPendingForHolder(holderUserID)
	if err != nil {
		log.Error().Err(err).Str("holderUserID", holderUserID).Msg("Failed to get next pending event for holder")
		return
	}
	if pe == nil {
		log.Debug().Str("holderUserID", holderUserID).Msg("No pending events for holder")
		return
	}
	if EventName(pe.EventName) == ReedRemovedEvent {
		rs.deliverReedRemoved(pe.EventID, pe.RequestID, pe.RequesterUserID, pe.UserID, pe.ReedID, nil)
		rs.dispatchNext(holderUserID)
		return
	}
	ok, err := rs.dbService.MarkEventDispatched(pe.EventID)
	if err != nil {
		log.Error().Err(err).Str("eventID", pe.EventID).Msg("Failed to mark event dispatched")
		return
	}
	if !ok {
		return // another replica claimed it
	}
	if err := rs.connManager.SendToUser(holderUserID, NewRelayRequestMsg(pe.EventID, pe.ReedID)); err != nil {
		log.Error().
			Err(err).
			Str("holderUserID", holderUserID).
			Str("eventID", pe.EventID).
			Str("reedID", pe.ReedID).
			Msg("Failed to send relay request to holder")
		if resetErr := rs.dbService.ResetDispatchedAt(pe.EventID); resetErr != nil {
			log.Error().Err(resetErr).Str("eventID", pe.EventID).Msg("Failed to reset dispatched_at after relay send failure")
		}
		return
	}
	log.Debug().
		Str("holderUserID", holderUserID).
		Str("eventID", pe.EventID).
		Str("reedID", pe.ReedID).
		Msg("Relay request sent to holder")
}

func (rs *RealtimeService) dispatchNextIfConnected(holderUserID string) {
	if holderUserID == "" {
		return
	}
	if !rs.connManager.HasConnection(holderUserID) {
		log.Debug().Str("holderUserID", holderUserID).Msg("Holder has no active WebSocket; skipping relay dispatch")
		return
	}
	rs.dispatchNext(holderUserID)
}

func generateEventID() string {
	return uuid.New().String()
}

func (rs *RealtimeService) handleRequestReed(client *Client, msg map[string]interface{}) {
	data, _ := msg["data"].(map[string]interface{})
	requestID, _ := data["request_id"].(string)
	reedID, _ := data["reed_id"].(string)
	authorID, _ := data["author_id"].(string)
	if requestID == "" || reedID == "" || authorID == "" {
		return
	}

	exists, err := rs.dbService.ReedExists(authorID, reedID)
	if err != nil {
		log.Error().Err(err).Str("reedID", reedID).Str("authorID", authorID).Msg("Failed to check reed existence")
		return
	}
	if !exists {
		log.Debug().Str("reedID", reedID).Str("authorID", authorID).Msg("Requested reed does not exist, notifying requester")
		rs.connManager.SendToUser(client.userID, NewReedNotFoundMsg(requestID, reedID))
		return
	}

	eventID := generateEventID()
	if err := rs.dbService.CreatePendingReedEvent(eventID, requestID, client.userID, RequestReedEvent, authorID, reedID); err != nil {
		log.Error().Err(err).Msg("Failed to create pending event")
		return
	}

	rs.connManager.SendToUser(client.userID, NewRequestAckMsg(requestID, eventID, reedID))

	holder, err := rs.dbService.GetOnlineReedHolder(authorID, reedID)
	if err != nil {
		log.Error().Err(err).Str("reedID", reedID).Str("authorID", authorID).Msg("Failed to get online holder for reed")
		return
	}
	if holder != "" {
		rs.dispatchNextIfConnected(holder)
	}
}

// handlePublishReady runs new-reed fanout when a pending_fanout row exists.
func (rs *RealtimeService) handlePublishReady(client *Client, jsonMsg map[string]interface{}) {
	data, _ := jsonMsg["data"].(map[string]interface{})
	reedID, _ := data["reed_id"].(string)
	if reedID == "" {
		return
	}
	authorUserID := client.userID

	claimed, tags, err := rs.dbService.ClaimPendingFanout(authorUserID, reedID)
	if err != nil {
		log.Error().Err(err).Str("reedID", reedID).Str("userID", authorUserID).Msg("Failed to claim pending fanout")
		return
	}
	_ = tags // pipe fanout uses claimed tags in pipes 02

	if claimed {
		if shouldBroadcast(data) {
			go rs.fanoutNewReed(authorUserID, reedID)
		} else {
			go rs.fanoutNewReedNoBroadcast(authorUserID, reedID)
		}
	} else {
		exists, err := rs.dbService.ReedExists(authorUserID, reedID)
		if err != nil {
			log.Error().Err(err).Str("reedID", reedID).Str("userID", authorUserID).Msg("Failed to check reed for publish ready ack")
			return
		}
		if !exists {
			return
		}
	}

	ack := map[string]interface{}{
		"type": "PUBLISH_READY_ACK",
		"data": map[string]string{"reed_id": reedID},
	}
	if jsonBytes, err := json.Marshal(ack); err == nil {
		client.writeMessage(websocket.TextMessage, jsonBytes)
	}
}

func (rs *RealtimeService) handleRelayResponse(client *Client, msg map[string]interface{}) {
	data, _ := msg["data"].(map[string]interface{})
	eventID, _ := data["event_id"].(string)
	if eventID == "" {
		return
	}

	pe, err := rs.dbService.GetPendingReedEvent(eventID)
	if err != nil {
		log.Error().Err(err).Str("eventID", eventID).Msg("Failed to get pending event")
		rs.dispatchNext(client.userID)
		return
	}
	if pe == nil {
		// Event was cancelled (e.g. UNSUBSCRIBE_PROFILE) — still advance the holder's queue.
		rs.dispatchNext(client.userID)
		return
	}

	if pe.EventName == string(BroadcastReedEvent) {
		log.Info().Str("requesterID", pe.RequesterUserID).Str("reedID", pe.ReedID).Msg("Delivering broadcast reed to subscriber")
		username := ""
		if name, err := rs.dbService.GetUsername(pe.UserID); err != nil {
			log.Error().Err(err).Str("userID", pe.UserID).Msg("Failed to load author username for broadcast reed")
		} else {
			username = name
		}
		if err := rs.connManager.SendToUser(pe.RequesterUserID, NewBroadcastReedMsg(pe.ReedID, data["data"], username)); err != nil {
			log.Error().Err(err).Str("requesterID", pe.RequesterUserID).Msg("Failed to deliver broadcast reed")
		}
		if err := rs.dbService.DeletePendingEvent(eventID); err != nil {
			log.Error().Err(err).Str("eventID", eventID).Msg("Failed to delete pending event")
		}
	} else {
		if err := rs.connManager.SendToUser(pe.RequesterUserID, NewDataResponseMsg(pe.EventID, pe.RequestID, pe.ReedID, data["data"])); err != nil {
			log.Error().Err(err).Str("requesterID", pe.RequesterUserID).Msg("Failed to deliver data response")
		}
		// Allocation and deletion deferred until viewer sends DATA_ACK or DATA_INVALID.
	}

	rs.dispatchNext(client.userID)
}

// handleRelayMiss drops the reporting holder's allocation and retries another online holder.
func (rs *RealtimeService) handleRelayMiss(client *Client, data RelayMissData) {
	if data.EventID == "" {
		return
	}

	pe, err := rs.dbService.GetPendingReedEvent(data.EventID)
	if err != nil {
		log.Error().Err(err).Str("eventID", data.EventID).Msg("Failed to get pending event for relay miss")
		rs.dispatchNext(client.userID)
		return
	}
	if pe == nil {
		rs.dispatchNext(client.userID)
		return
	}

	log.Info().
		Str("eventID", data.EventID).
		Str("holderID", client.userID).
		Str("authorID", pe.UserID).
		Str("reedID", pe.ReedID).
		Msg("Relay miss; dropping holder allocation and retrying")

	if _, err := rs.dbService.DeleteReedAllocation(pe.UserID, pe.ReedID, client.userID); err != nil {
		log.Error().Err(err).Str("reedID", pe.ReedID).Str("holderID", client.userID).Msg("Failed to delete allocation on relay miss")
	}

	if err := rs.dbService.ResetDispatchedAt(data.EventID); err != nil {
		log.Error().Err(err).Str("eventID", data.EventID).Msg("Failed to reset dispatched_at on relay miss")
	}

	holder, err := rs.dbService.GetOnlineReedHolderExcluding(pe.UserID, pe.ReedID, client.userID)
	if err != nil {
		log.Error().Err(err).Str("reedID", pe.ReedID).Msg("Failed to find alternate holder on relay miss")
	} else if holder != "" {
		rs.dispatchNextIfConnected(holder)
	}

	rs.dispatchNext(client.userID)
}

// handleDataAck is called when the viewer has received and verified a delivery successfully.
// New reeds: allocate. Reed removals: clear allocation. Account removals: clear peer state.
func (rs *RealtimeService) handleDataAck(client *Client, data DataAckData) {
	if data.EventID == "" {
		return
	}
	eventID := data.EventID

	pe, err := rs.dbService.GetPendingSubject(eventID)
	if err != nil {
		log.Error().Err(err).Str("eventID", eventID).Msg("Failed to get pending event for data ack")
		return
	}
	if pe == nil {
		return
	}

	if EventName(pe.EventName) == ReedRemovedEvent {
		changed, err := rs.dbService.DeleteReedAllocation(pe.UserID, pe.ReedID, client.userID)
		if err != nil {
			log.Error().Err(err).Str("reedID", pe.ReedID).Str("userID", client.userID).Msg("Failed to clear allocation on reed_removed ack")
		} else if changed {
			rs.notifyReedCoverage(pe.UserID, pe.ReedID)
		}
	} else if EventName(pe.EventName) == AccountRemovedEvent {
		targets, err := rs.dbService.ClearPeerStateForRemovedAccount(client.userID, pe.UserID)
		if err != nil {
			log.Error().Err(err).Str("removedUserID", pe.UserID).Str("viewer", client.userID).Msg("Failed to clear peer state on account_removed ack")
		} else {
			for _, t := range targets {
				rs.notifyReedCoverage(t.AuthorUserID, t.ReedID)
			}
		}
	} else {
		changed, err := rs.dbService.AllocateReed(pe.ReedID, client.userID, pe.UserID)
		if err != nil {
			log.Error().Err(err).Str("reedID", pe.ReedID).Str("userID", client.userID).Msg("Failed to allocate reed on data ack")
		} else if changed {
			rs.notifyReedCoverage(pe.UserID, pe.ReedID)
		}
	}

	if err := rs.dbService.DeletePendingEvent(eventID); err != nil {
		log.Error().Err(err).Str("eventID", eventID).Msg("Failed to delete pending event on data ack")
	}
}

// handleDataInvalid is called when the viewer received a reed but its signature failed verification.
// The pending event is removed without allocating the reed to the viewer.
func (rs *RealtimeService) handleDataInvalid(client *Client, data DataInvalidData) {
	if data.EventID == "" {
		return
	}

	if err := rs.dbService.DeletePendingEvent(data.EventID); err != nil {
		log.Error().Err(err).Str("eventID", data.EventID).Msg("Failed to delete pending event on data invalid")
	}
}

func (rs *RealtimeService) handleSubscribeProfile(client *Client, msg map[string]interface{}) {
	dataBytes, err := json.Marshal(msg["data"])
	if err != nil {
		return
	}
	var data SubscribeProfileData
	if err := json.Unmarshal(dataBytes, &data); err != nil || data.UserID == "" {
		return
	}

	subscriptionID := generateEventID()
	if err := rs.dbService.CreateProfileSubscription(subscriptionID, client.userID, data.UserID); err != nil {
		log.Error().Err(err).Msg("Failed to create profile subscription")
		return
	}

	missingIDs, err := rs.dbService.GetUnallocatedReeds(data.UserID, client.userID)
	if err != nil {
		log.Error().Err(err).Msg("Failed to get unallocated reeds for viewer")
		return
	}

	for _, reedID := range missingIDs {
		eventID := generateEventID()
		requestID := generateEventID()
		if err := rs.dbService.CreateProfileSubscriptionEvent(eventID, requestID, client.userID, ProfileSubscriptionEvent, data.UserID, reedID, subscriptionID); err != nil {
			log.Error().Err(err).Str("reedID", reedID).Msg("Failed to create profile subscription event")
			continue
		}
		holder, err := rs.dbService.GetOnlineReedHolder(data.UserID, reedID)
		if err != nil || holder == "" {
			continue
		}
		rs.dispatchNextIfConnected(holder)
	}
}

func (rs *RealtimeService) handleUnsubscribeProfile(client *Client, msg map[string]interface{}) {
	dataBytes, err := json.Marshal(msg["data"])
	if err != nil {
		return
	}
	var data UnsubscribeProfileData
	if err := json.Unmarshal(dataBytes, &data); err != nil || data.UserID == "" {
		return
	}

	subscriptionID, err := rs.dbService.GetProfileSubscription(client.userID, data.UserID)
	if err != nil {
		log.Error().Err(err).Msg("Failed to get profile subscription")
		return
	}
	if subscriptionID == "" {
		return
	}

	if err := rs.dbService.DeleteProfileSubscription(subscriptionID); err != nil {
		log.Error().Err(err).Str("subscriptionID", subscriptionID).Msg("Failed to delete profile subscription")
	}
}

func (rs *RealtimeService) handleSubscribeReed(client *Client, msg map[string]interface{}) {
	authorID, _ := msg["userID"].(string)
	reedID, _ := msg["reedID"].(string)
	if authorID == "" || reedID == "" {
		return
	}

	exists, err := rs.dbService.ReedExists(authorID, reedID)
	if err != nil {
		log.Error().Err(err).Str("userID", authorID).Str("reedID", reedID).Msg("Failed to check reed for subscribe")
		return
	}
	if !exists {
		return
	}

	echoes, coveragePercent, err := rs.dbService.GetReedStatsSnapshot(authorID, reedID)
	if err != nil {
		log.Error().Err(err).Str("userID", authorID).Str("reedID", reedID).Msg("Failed to load reed stats for subscribe")
		return
	}

	rs.connManager.SubscribeReed(client, authorID, reedID)
	if err := rs.connManager.SendToClient(client, map[string]interface{}{
		"type":            "REED_STATS",
		"userID":          authorID,
		"reedID":          reedID,
		"echoes":          echoes,
		"coveragePercent": coveragePercent,
	}); err != nil {
		log.Error().Err(err).Str("userID", client.userID).Str("reedID", reedID).Msg("Failed to send REED_STATS")
	}
}

func (rs *RealtimeService) handleUnsubscribeReed(client *Client, msg map[string]interface{}) {
	authorID, _ := msg["userID"].(string)
	reedID, _ := msg["reedID"].(string)
	if authorID == "" || reedID == "" {
		return
	}
	rs.connManager.UnsubscribeReed(client, authorID, reedID)
}

func (rs *RealtimeService) notifyReedCoverage(authorUserID, reedID string) {
	percent, err := rs.dbService.GetReedCoveragePercent(authorUserID, reedID)
	if err != nil {
		log.Error().
			Err(err).
			Str("userID", authorUserID).
			Str("reedID", reedID).
			Msg("Failed to load reed coverage for notify")
		return
	}

	if err := rs.connManager.BroadcastReedCoverage(map[string]interface{}{
		"type":            "REED_COVERAGE",
		"userID":          authorUserID,
		"reedID":          reedID,
		"coveragePercent": percent,
	}); err != nil {
		log.Error().Err(err).Str("userID", authorUserID).Str("reedID", reedID).Msg("Failed to broadcast REED_COVERAGE")
	}
}

func (rs *RealtimeService) notifyReedEchoes(authorUserID, reedID string) {
	echoes, err := rs.dbService.CountEchoes(authorUserID, reedID)
	if err != nil {
		log.Error().
			Err(err).
			Str("userID", authorUserID).
			Str("reedID", reedID).
			Msg("Failed to load reed echoes for notify")
		return
	}

	if err := rs.connManager.SendToReedSubscribers(authorUserID, reedID, map[string]interface{}{
		"type":   "REED_ECHOES",
		"userID": authorUserID,
		"reedID": reedID,
		"echoes": echoes,
	}); err != nil {
		log.Error().Err(err).Str("userID", authorUserID).Str("reedID", reedID).Msg("Failed to broadcast REED_ECHOES")
	}
}

func (rs *RealtimeService) handleSyncRequest(client *Client, data SyncRequestData) {
	if data.RequestID == "" {
		return
	}
	if err := rs.dbService.SetSyncRequestID(client.userID, data.RequestID); err != nil {
		log.Error().Err(err).Msg("Failed to store sync request ID")
		return
	}
	rs.catchUp(client.userID, data.RequestID)
	rs.dispatchNext(client.userID)
	rs.redispatchPendingRequests(client.userID)
}

func (rs *RealtimeService) redispatchPendingRequests(requesterUserID string) {
	requests, err := rs.dbService.GetPendingRequestsForRequester(requesterUserID)
	if err != nil {
		log.Error().Err(err).Str("requesterUserID", requesterUserID).Msg("Failed to get pending requests for requester")
		return
	}
	for _, req := range requests {
		if err := rs.dbService.ResetDispatchedAt(req.EventID); err != nil {
			log.Error().Err(err).Str("eventID", req.EventID).Msg("Failed to reset dispatched_at for pending request")
			continue
		}
		holder, err := rs.dbService.GetOnlineReedHolder(req.UserID, req.ReedID)
		if err != nil {
			log.Error().Err(err).Str("reedID", req.ReedID).Msg("Failed to get holder for pending request on reconnect")
			continue
		}
		if holder != "" {
			rs.dispatchNextIfConnected(holder)
		}
	}
}

func (rs *RealtimeService) handleUserCameOnline(client *Client) {
	// Nothing sent until client signals readiness via SYNC_REQUEST
}

func (rs *RealtimeService) catchUp(userID, requestID string) {
	unallocated, err := rs.dbService.GetMissingOut(userID)
	if err != nil {
		log.Error().
			Err(err).
			Str("userID", userID).
			Msg("Failed to get unallocated reeds from followings")
		return
	}

	for _, reed := range unallocated {
		eventID := generateEventID()
		if err := rs.dbService.CreatePendingReedEvent(eventID, requestID, userID, NewReedEvent, reed.AuthorID, reed.ReedID); err != nil {
			log.Error().
				Err(err).
				Str("reedID", reed.ReedID).
				Msg("Failed to create catch-up pending event")
			continue
		}
		holder, err := rs.dbService.GetOnlineReedHolder(reed.AuthorID, reed.ReedID)
		if err != nil {
			log.Error().
				Err(err).
				Str("reedID", reed.ReedID).
				Msg("Failed to get online holder for catch-up reed")
			continue
		}
		if holder != "" {
			rs.dispatchNextIfConnected(holder)
		}
	}

	removals, err := rs.dbService.GetMissingRemovals(userID)
	if err != nil {
		log.Error().Err(err).Str("userID", userID).Msg("Failed to get missing reed removals")
		return
	}
	for _, rem := range removals {
		eventID := generateEventID()
		if err := rs.dbService.CreatePendingReedEvent(eventID, requestID, userID, ReedRemovedEvent, rem.UserID, rem.ReedID); err != nil {
			log.Error().Err(err).Str("reedID", rem.ReedID).Msg("Failed to create catch-up reed_removed event")
			continue
		}
		rs.deliverReedRemoved(eventID, requestID, userID, rem.UserID, rem.ReedID, rem.Cert)
	}

	accountRemovals, err := rs.dbService.GetMissingAccountRemovals(userID)
	if err != nil {
		log.Error().Err(err).Str("userID", userID).Msg("Failed to get missing account removals")
		return
	}
	for _, rem := range accountRemovals {
		eventID := generateEventID()
		if err := rs.dbService.CreatePendingAccountEvent(eventID, requestID, userID, rem.UserID); err != nil {
			log.Error().Err(err).Str("removedUserID", rem.UserID).Msg("Failed to create catch-up account_removed event")
			continue
		}
		rs.deliverAccountRemoved(eventID, requestID, userID, rem.UserID, rem.Cert)
	}
}

// shouldBroadcast reports whether PUBLISH_READY should fan out to the broadcast stream.
// Absent or true means include broadcast; only explicit false opts out.
func shouldBroadcast(data map[string]interface{}) bool {
	if data == nil {
		return true
	}
	v, ok := data["broadcast"]
	if !ok || v == nil {
		return true
	}
	b, ok := v.(bool)
	if !ok {
		return true
	}
	return b
}
