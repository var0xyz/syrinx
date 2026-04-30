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

		if message.Type == NewReed {
			log.Info().
				Str("userID", message.UserID).
				Str("reedID", message.ReedID).
				Msg("New reed published")

			followers, err := rs.dbService.GetOnlineFollowers(message.UserID)
			if err != nil {
				log.Error().
					Err(err).
					Msg("Failed to get online followers from database")
			}

			broadcastRecipients, err := rs.dbService.GetBroadcastSubscribers(message.UserID)
			if err != nil {
				log.Error().
					Err(err).
					Msg("Failed to get broadcast subscribers from database")
			}

			log.Info().
				Str("userID", message.UserID).
				Int("followers", len(followers)).
				Int("broadcastSubscribers", len(broadcastRecipients)).
				Msg("Dispatching new reed to recipients")
			rs.dispatchMany(followers, NewReedEvent, message.ReedID, message.UserID)
			rs.dispatchMany(broadcastRecipients, BroadcastReedEvent, message.ReedID, message.UserID)

			profileSubscribers, err := rs.dbService.GetProfileSubscribers(message.UserID)
			if err != nil {
				log.Error().
					Err(err).
					Msg("Failed to get profile subscribers from database")
			}

			for _, sub := range profileSubscribers {
				eventID := generateEventID()
				requestID := generateEventID()
				if err := rs.dbService.CreateProfileSubscriptionEvent(eventID, requestID, sub.ViewerUserID, NewReedEvent, message.ReedID, sub.SubscriptionID); err != nil {
					log.Error().
						Err(err).
						Str("viewerUserID", sub.ViewerUserID).
						Msg("Failed to create pending event for profile subscriber")
				}
			}

			rs.dispatchNext(message.UserID)
		}
	}
}


// dispatchMany creates a pending event for each recipient and triggers relay dispatch to holderUserID.
func (rs *RealtimeService) dispatchMany(recipients []string, eventName EventName, reedID, holderUserID string) {
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
		if err := rs.dbService.CreatePendingEvent(eventID, requestID, recipientID, eventName, reedID); err != nil {
			log.Error().
				Err(err).
				Str("recipientID", recipientID).
				Msg("Failed to create pending event")
			continue
		}
		log.Debug().
			Str("recipientID", recipientID).
			Str("eventID", eventID).
			Str("holderUserID", holderUserID).
			Msg("Pending event created, dispatching to holder")
		rs.dispatchNext(holderUserID)
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
	rs.handleUserCameOnline(client)

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

	case "NOTIFY_CHAT_REQUEST":
		dataBytes, _ := json.Marshal(jsonMsg["data"])
		var data NotifyChatRequestData
		if err := json.Unmarshal(dataBytes, &data); err == nil {
			rs.handleNotifyChatRequest(client, data)
		}

	case "NOTIFY_CHAT_ACCEPTED":
		dataBytes, _ := json.Marshal(jsonMsg["data"])
		var data NotifyChatAcceptedData
		if err := json.Unmarshal(dataBytes, &data); err == nil {
			rs.handleNotifyChatAccepted(client, data)
		}

	case "DELIVER_CHAT_MESSAGE":
		dataBytes, _ := json.Marshal(jsonMsg["data"])
		var data DeliverChatMessageData
		if err := json.Unmarshal(dataBytes, &data); err == nil {
			rs.handleDeliverChatMessage(client, data)
		}

	case "CONFIRM_DELIVERY":
		dataBytes, _ := json.Marshal(jsonMsg["data"])
		var data ConfirmDeliveryData
		if err := json.Unmarshal(dataBytes, &data); err == nil {
			rs.handleConfirmDelivery(client, data)
		}

	case "NOTIFY_BLOCK":
		dataBytes, _ := json.Marshal(jsonMsg["data"])
		var data NotifyBlockData
		if err := json.Unmarshal(dataBytes, &data); err == nil {
			rs.handleNotifyBlock(client, data)
		}

	case "BLOCK_EVENT_ACK":
		dataBytes, _ := json.Marshal(jsonMsg["data"])
		var data BlockEventAckData
		if err := json.Unmarshal(dataBytes, &data); err == nil {
			rs.handleBlockEventAck(client, data)
		}

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
	} else {
		log.Debug().
			Str("holderUserID", holderUserID).
			Str("eventID", pe.EventID).
			Str("reedID", pe.ReedID).
			Msg("Relay request sent to holder")
	}
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

	exists, err := rs.dbService.ReedExists(reedID)
	if err != nil {
		log.Error().Err(err).Str("reedID", reedID).Msg("Failed to check reed existence")
		return
	}
	if !exists {
		log.Debug().Str("reedID", reedID).Msg("Requested reed does not exist, notifying requester")
		rs.connManager.SendToUser(client.userID, NewReedNotFoundMsg(requestID, reedID))
		return
	}

	eventID := generateEventID()
	if err := rs.dbService.CreatePendingEvent(eventID, requestID, client.userID, RequestReedEvent, reedID); err != nil {
		log.Error().Err(err).Msg("Failed to create pending event")
		return
	}

	rs.connManager.SendToUser(client.userID, NewRequestAckMsg(requestID, eventID, reedID))

	holder, err := rs.dbService.GetOnlineReedHolder(reedID)
	if err != nil {
		log.Error().Err(err).Str("reedID", reedID).Msg("Failed to get online holder for reed")
		return
	}
	if holder != "" {
		rs.dispatchNext(holder)
	}
}

func (rs *RealtimeService) handleRelayResponse(client *Client, msg map[string]interface{}) {
	data, _ := msg["data"].(map[string]interface{})
	eventID, _ := data["event_id"].(string)
	if eventID == "" {
		return
	}

	pe, err := rs.dbService.GetPendingReedRequest(eventID)
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
		if err := rs.connManager.SendToUser(pe.RequesterUserID, NewBroadcastReedMsg(pe.ReedID, data["data"])); err != nil {
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

// handleRelayMiss is called when a holder receives a RELAY_REQUEST but no longer has the reed.
// It removes the holder's allocation so they won't be selected again, resets the pending event
// so it can be re-dispatched, and looks for another online holder to take over.
func (rs *RealtimeService) handleRelayMiss(client *Client, data RelayMissData) {
	if data.EventID == "" {
		return
	}
	eventID := data.EventID

	pe, err := rs.dbService.GetPendingReedRequest(eventID)
	if err != nil {
		log.Error().Err(err).Str("eventID", eventID).Msg("Failed to get pending event for relay miss")
		rs.dispatchNext(client.userID)
		return
	}
	if pe == nil {
		rs.dispatchNext(client.userID)
		return
	}

	if err := rs.dbService.DeleteReedAllocation(pe.ReedID, client.userID); err != nil {
		log.Error().Err(err).Str("reedID", pe.ReedID).Str("userID", client.userID).Msg("Failed to remove allocation on relay miss")
	}

	if err := rs.dbService.ResetDispatchedAt(eventID); err != nil {
		log.Error().Err(err).Str("eventID", eventID).Msg("Failed to reset dispatched_at on relay miss")
	}

	rs.dispatchNext(client.userID)

	newHolder, err := rs.dbService.GetOnlineReedHolder(pe.ReedID)
	if err != nil {
		log.Error().Err(err).Str("reedID", pe.ReedID).Msg("Failed to find new holder after relay miss")
		return
	}
	if newHolder != "" {
		rs.dispatchNext(newHolder)
	}
}

// handleDataAck is called when the viewer has received and verified a reed successfully.
// It allocates the reed to the viewer and removes the pending event.
func (rs *RealtimeService) handleDataAck(client *Client, data DataAckData) {
	if data.EventID == "" {
		return
	}
	eventID := data.EventID

	pe, err := rs.dbService.GetPendingReedRequest(eventID)
	if err != nil {
		log.Error().Err(err).Str("eventID", eventID).Msg("Failed to get pending event for data ack")
		return
	}
	if pe == nil {
		return
	}

	if err := rs.dbService.AllocateReed(pe.ReedID, client.userID); err != nil {
		log.Error().Err(err).Str("reedID", pe.ReedID).Str("userID", client.userID).Msg("Failed to allocate reed on data ack")
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
		if err := rs.dbService.CreateProfileSubscriptionEvent(eventID, requestID, client.userID, ProfileSubscriptionEvent, reedID, subscriptionID); err != nil {
			log.Error().Err(err).Str("reedID", reedID).Msg("Failed to create profile subscription event")
			continue
		}
		holder, err := rs.dbService.GetOnlineReedHolder(reedID)
		if err != nil || holder == "" {
			continue
		}
		rs.dispatchNext(holder)
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
		holder, err := rs.dbService.GetOnlineReedHolder(req.ReedID)
		if err != nil {
			log.Error().Err(err).Str("reedID", req.ReedID).Msg("Failed to get holder for pending request on reconnect")
			continue
		}
		if holder != "" {
			rs.dispatchNext(holder)
		}
	}
}

func (rs *RealtimeService) handleUserCameOnline(client *Client) {
	// Nothing sent until client signals readiness via SYNC_REQUEST

	// Deliver any undelivered chat requests (recipient was offline when sender initiated).
	chatReqs, err := rs.dbService.GetPendingChatRequests(client.userID)
	if err != nil {
		log.Error().Err(err).Str("userID", client.userID).Msg("Failed to get pending chat requests")
	} else {
		for _, req := range chatReqs {
			msg := NewChatRequestMsg(ChatRequestData{
				ChatID:   req.ChatID,
				SenderID: req.SenderID,
				Message:  req.Message,
			})
			if err := rs.connManager.SendToUser(client.userID, msg); err != nil {
				log.Error().Err(err).Str("chatID", req.ChatID).Msg("Failed to deliver pending chat request")
			}
		}
	}

	// Deliver the oldest pending chat message (one at a time; next is sent after ACK).
	rs.deliverNextChatMessage(client.userID)

	// Deliver any pending block events.
	blockers, err := rs.dbService.GetUnnotifiedBlockers(client.userID)
	if err != nil {
		log.Error().Err(err).Str("userID", client.userID).Msg("Failed to get unnotified blockers")
	} else {
		for _, blockerID := range blockers {
			if err := rs.connManager.SendToUser(client.userID, NewBlockEventMsg(blockerID)); err != nil {
				log.Error().Err(err).Str("blockerID", blockerID).Msg("Failed to send block event")
				continue
			}
			if err := rs.dbService.MarkBlockerNotified(blockerID, client.userID); err != nil {
				log.Error().Err(err).Str("blockerID", blockerID).Msg("Failed to mark blocker notified")
			}
		}
	}
}

// handleNotifyChatRequest routes a CHAT_REQUEST event to the recipient.
// The client sends this after a successful POST /api/chat/initiate.
func (rs *RealtimeService) handleNotifyChatRequest(client *Client, data NotifyChatRequestData) {
	if data.ChatID == "" || data.RecipientID == "" {
		return
	}
	ok, err := rs.dbService.ChatRequestExists(data.ChatID, client.userID)
	if err != nil || !ok {
		return
	}
	_ = rs.connManager.SendToUser(data.RecipientID, NewChatRequestMsg(ChatRequestData{
		ChatID:   data.ChatID,
		SenderID: client.userID,
		Message:  data.Message,
	}))
}

// handleNotifyChatAccepted sends CHAT_REQUEST_ACCEPTED to the initiator.
// Called after POST /api/chat/accept.
func (rs *RealtimeService) handleNotifyChatAccepted(client *Client, data NotifyChatAcceptedData) {
	if data.ChatID == "" || data.InitiatorID == "" {
		return
	}
	_ = rs.connManager.SendToUser(data.InitiatorID, NewChatRequestAcceptedMsg(data.ChatID))
}

func (rs *RealtimeService) deliverNextChatMessage(userID string) {
	m, err := rs.dbService.GetNextPendingChatMessage(userID)
	if err != nil {
		log.Error().Err(err).Str("userID", userID).Msg("Failed to query next pending chat message")
		return
	}
	if m == nil {
		return
	}
	msg := NewChatMessageMsg(ChatMessageData{
		ServerID:  m.ServerID,
		ClientID:  m.ClientID,
		ChatID:    m.ChatID,
		SenderID:  m.SenderID,
		Content:   m.Content,
		CreatedAt: m.CreatedAt.UTC().Format("2006-01-02T15:04:05.999Z"),
	})
	if err := rs.connManager.SendToUser(userID, msg); err != nil {
		log.Error().Err(err).Str("userID", userID).Msg("Failed to deliver pending chat message")
	}
}

// handleDeliverChatMessage triggers delivery to each recipient. Called after POST /api/chat/message.
func (rs *RealtimeService) handleDeliverChatMessage(client *Client, data DeliverChatMessageData) {
	if data.ChatID == "" {
		return
	}
	recipients, err := rs.dbService.GetChatParticipants(data.ChatID, client.userID)
	if err != nil {
		log.Error().Err(err).Str("chatID", data.ChatID).Msg("Failed to get chat participants")
		return
	}
	for _, recipientID := range recipients {
		rs.deliverNextChatMessage(recipientID)
	}
}

// handleConfirmDelivery sends CHAT_DELIVERY_CONFIRMATION and delivers the next message.
// Called after POST /api/chat/messages/{id}/ack.
func (rs *RealtimeService) handleConfirmDelivery(client *Client, data ConfirmDeliveryData) {
	if data.MessageID == "" || data.SenderID == "" {
		return
	}
	_ = rs.connManager.SendToUser(data.SenderID, NewChatDeliveryConfirmationMsg(data.MessageID))
	rs.deliverNextChatMessage(client.userID)
}

// handleNotifyBlock sends a BLOCK_EVENT to the blocked user and marks it delivered.
// Called after POST /api/users/{userID}/block.
func (rs *RealtimeService) handleNotifyBlock(client *Client, data NotifyBlockData) {
	if data.BlockedUserID == "" {
		return
	}
	ok, err := rs.dbService.BlockExists(client.userID, data.BlockedUserID)
	if err != nil || !ok {
		return
	}
	if err := rs.connManager.SendToUser(data.BlockedUserID, NewBlockEventMsg(client.userID)); err != nil {
		return
	}
	if err := rs.dbService.MarkBlockerNotified(client.userID, data.BlockedUserID); err != nil {
		log.Error().Err(err).Msg("Failed to mark block event notified")
	}
}

// handleBlockEventAck marks the block event as delivered. Called when the client
// sends BLOCK_EVENT_ACK after wiping the blocker's data.
func (rs *RealtimeService) handleBlockEventAck(client *Client, data BlockEventAckData) {
	if data.BlockerID == "" {
		return
	}
	if err := rs.dbService.MarkBlockerNotified(data.BlockerID, client.userID); err != nil {
		log.Error().Err(err).Str("blockerID", data.BlockerID).Msg("Failed to mark block event notified")
	}
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
		if err := rs.dbService.CreatePendingEvent(eventID, requestID, userID, NewReedEvent, reed.ReedID); err != nil {
			log.Error().
				Err(err).
				Str("reedID", reed.ReedID).
				Msg("Failed to create catch-up pending event")
			continue
		}
		holder, err := rs.dbService.GetOnlineReedHolder(reed.ReedID)
		if err != nil {
			log.Error().
				Err(err).
				Str("reedID", reed.ReedID).
				Msg("Failed to get online holder for catch-up reed")
			continue
		}
		if holder != "" {
			rs.dispatchNext(holder)
		}
	}
}
