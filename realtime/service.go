package realtime

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"

	"syrinx/crypto"
	"syrinx/observability/metrics"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/rs/zerolog/log"
	"google.golang.org/protobuf/proto"

	pb "syrinx/proto"
)

type errorMessage struct {
	Error string `json:"error"`
}

func rejectConnection(w http.ResponseWriter, reason string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusForbidden)
	_ = json.NewEncoder(w).Encode(errorMessage{Error: reason})
}

// RealtimeService represents the main realtime service
type RealtimeService struct {
	connManager   *ConnectionManager
	dbService     *DBService
	authService   *AuthService
	crypto        *crypto.Service
	allowedOrigin string
	metrics       metrics.Recorder
	ongoingCheck  func(userID string) (bool, error)
	deviceCheck   func(userID, deviceID string) error
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
		metrics:       metrics.Noop{},
	}
}

// SetMetrics installs the business-metrics recorder.
func (rs *RealtimeService) SetMetrics(rec metrics.Recorder) {
	if rec == nil {
		rs.metrics = metrics.Noop{}
		return
	}
	rs.metrics = rec
}

// SetOngoingCheck installs an optional import-gate check used after WebSocket
// auth succeeds. When the check returns true, the connection is rejected with 403.
func (rs *RealtimeService) SetOngoingCheck(check func(userID string) (bool, error)) {
	rs.ongoingCheck = check
}

// SetDeviceCheck installs the active-device check used after WebSocket auth succeeds.
func (rs *RealtimeService) SetDeviceCheck(check func(userID, deviceID string) error) {
	rs.deviceCheck = check
}

// DisconnectUser closes all WebSocket connections for a user (device rebind kick).
func (rs *RealtimeService) DisconnectUser(userID string) {
	rs.connManager.DisconnectUser(userID)
}

func (rs *RealtimeService) deviceMismatch(userID, deviceID string) bool {
	if rs.deviceCheck == nil {
		return false
	}
	return rs.deviceCheck(userID, deviceID) != nil
}

func (rs *RealtimeService) ongoingImport(userID string) (bool, error) {
	if rs.ongoingCheck == nil {
		return false, nil
	}
	return rs.ongoingCheck(userID)
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

		if message.Type == EchoCountChanged {
			rs.notifyReedEchoes(message.UserID, message.ReedID)
		}

		if message.Type == ReplyCountChanged {
			rs.notifyReedReplies(message.UserID, message.ReedID)
		}

		if message.Type == ReedRemoved {
			log.Info().
				Str("userID", message.UserID).
				Str("reedID", message.ReedID).
				Msg("Reed removed; fanout cert")

			cert := message.ReedRemoval
			followers, err := rs.dbService.GetOnlineFollowers(context.Background(), message.UserID)
			if err != nil {
				log.Error().Err(err).Msg("Failed to get online followers for reed removal")
			}
			broadcastRecipients, err := rs.dbService.GetBroadcastSubscribers(context.Background(), message.UserID)
			if err != nil {
				log.Error().Err(err).Msg("Failed to get broadcast subscribers for reed removal")
			}
			rs.dispatchRemovalMany(followers, message.UserID, message.ReedID, cert)
			rs.dispatchRemovalMany(broadcastRecipients, message.UserID, message.ReedID, cert)

			profileSubscribers, err := rs.dbService.GetProfileSubscribers(context.Background(), message.UserID)
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

			cert := message.AccountRemoval
			followers, err := rs.dbService.GetOnlineFollowers(context.Background(), message.UserID)
			if err != nil {
				log.Error().Err(err).Msg("Failed to get online followers for account removal")
			}
			broadcastRecipients, err := rs.dbService.GetBroadcastSubscribers(context.Background(), message.UserID)
			if err != nil {
				log.Error().Err(err).Msg("Failed to get broadcast subscribers for account removal")
			}
			rs.dispatchAccountRemovalMany(followers, message.UserID, cert)
			rs.dispatchAccountRemovalMany(broadcastRecipients, message.UserID, cert)

			profileSubscribers, err := rs.dbService.GetProfileSubscribers(context.Background(), message.UserID)
			if err != nil {
				log.Error().Err(err).Msg("Failed to get profile subscribers for account removal")
			}
			for _, sub := range profileSubscribers {
				rs.dispatchAccountRemovalTo(sub.ViewerUserID, message.UserID, cert)
			}
		}
	}
}

// fanoutNewReed dispatches a newly published reed to followers, broadcast subs,
// profile subs, and pipe listeners for the claimed tags.
func (rs *RealtimeService) fanoutNewReed(authorUserID, reedID string, tags []string) {
	log.Info().
		Str("userID", authorUserID).
		Str("reedID", reedID).
		Int("tags", len(tags)).
		Msg("Fanning out new reed")

	broadcastRecipients, err := rs.dbService.GetBroadcastSubscribers(context.Background(), authorUserID)
	if err != nil {
		log.Error().
			Err(err).
			Msg("Failed to get broadcast subscribers from database")
	}

	rs.fanoutNewReedCore(authorUserID, reedID, broadcastRecipients, tags)
}

// fanoutNewReedNoBroadcast dispatches to followers, profile subs, and pipe
// listeners only (no broadcast stream).
func (rs *RealtimeService) fanoutNewReedNoBroadcast(authorUserID, reedID string, tags []string) {
	log.Info().
		Str("userID", authorUserID).
		Str("reedID", reedID).
		Int("tags", len(tags)).
		Msg("Fanning out new reed (no broadcast)")

	rs.fanoutNewReedCore(authorUserID, reedID, nil, tags)
}

func (rs *RealtimeService) fanoutNewReedCore(authorUserID, reedID string, broadcastRecipients, tags []string) {
	followers, err := rs.dbService.GetOnlineFollowers(context.Background(), authorUserID)
	if err != nil {
		log.Error().
			Err(err).
			Msg("Failed to get online followers from database")
	}

	pipeListeners := rs.connManager.PipeListenerUserIDs(tags, authorUserID)
	// Pipe listeners always get PIPE_REED (push). Followers who are not on the
	// pipe get FOLLOW_REED. Overlap prefers PIPE_REED (one event).
	followersOnly := subtractUserIDs(followers, pipeListeners)

	durable := unionUserIDs(followersOnly, pipeListeners)
	broadcastOnly := subtractUserIDs(broadcastRecipients, durable)

	log.Info().
		Str("userID", authorUserID).
		Int("followersOnly", len(followersOnly)).
		Int("pipeListeners", len(pipeListeners)).
		Int("broadcastSubscribers", len(broadcastOnly)).
		Msg("Dispatching new reed to recipients")
	rs.dispatchMany(followersOnly, FollowReedEvent, reedID, authorUserID)
	rs.dispatchMany(pipeListeners, PipeReedEvent, reedID, authorUserID)
	rs.dispatchMany(broadcastOnly, BroadcastReedEvent, reedID, authorUserID)

	profileSubscribers, err := rs.dbService.GetProfileSubscribers(context.Background(), authorUserID)
	if err != nil {
		log.Error().
			Err(err).
			Msg("Failed to get profile subscribers from database")
	}

	for _, sub := range profileSubscribers {
		eventID := generateEventID()
		requestID := generateEventID()
		if err := rs.dbService.CreateProfileSubscriptionEvent(context.Background(), eventID, requestID, sub.ViewerUserID, ProfileSubscriptionEvent, authorUserID, reedID, sub.SubscriptionID); err != nil {
			log.Error().
				Err(err).
				Str("viewerUserID", sub.ViewerUserID).
				Msg("Failed to create pending event for profile subscriber")
		}
	}

	rs.dispatchNextIfConnected(authorUserID)
}

func unionUserIDs(a, b []string) []string {
	seen := make(map[string]struct{}, len(a)+len(b))
	out := make([]string, 0, len(a)+len(b))
	for _, id := range a {
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	for _, id := range b {
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out
}

func subtractUserIDs(from, remove []string) []string {
	if len(from) == 0 {
		return nil
	}
	drop := make(map[string]struct{}, len(remove))
	for _, id := range remove {
		drop[id] = struct{}{}
	}
	out := make([]string, 0, len(from))
	for _, id := range from {
		if _, ok := drop[id]; ok {
			continue
		}
		out = append(out, id)
	}
	return out
}

// dispatchRemovalMany enqueues reed_removed pending events and delivers certs
// server-side (no holder relay).
func (rs *RealtimeService) dispatchRemovalMany(recipients []string, authorUserID, reedID string, cert *ReedRemovalWire) {
	for _, recipientID := range recipients {
		rs.dispatchRemovalTo(recipientID, authorUserID, reedID, cert)
	}
}

func (rs *RealtimeService) dispatchRemovalTo(recipientID, authorUserID, reedID string, cert *ReedRemovalWire) {
	requestID, err := rs.dbService.GetSyncRequestID(context.Background(), recipientID)
	if err != nil || requestID == "" {
		return
	}
	eventID := generateEventID()
	if err := rs.dbService.CreatePendingReedEvent(context.Background(), eventID, requestID, recipientID, ReedRemovedEvent, authorUserID, reedID); err != nil {
		log.Error().Err(err).Str("recipientID", recipientID).Msg("Failed to create reed_removed pending event")
		return
	}
	rs.deliverReedRemoved(eventID, requestID, recipientID, authorUserID, reedID, cert)
}

func (rs *RealtimeService) dispatchAccountRemovalMany(recipients []string, removedUserID string, cert *AccountRemovalWire) {
	for _, recipientID := range recipients {
		rs.dispatchAccountRemovalTo(recipientID, removedUserID, cert)
	}
}

func (rs *RealtimeService) dispatchAccountRemovalTo(recipientID, removedUserID string, cert *AccountRemovalWire) {
	requestID, err := rs.dbService.GetSyncRequestID(context.Background(), recipientID)
	if err != nil || requestID == "" {
		return
	}
	eventID := generateEventID()
	if err := rs.dbService.CreatePendingAccountEvent(context.Background(), eventID, requestID, recipientID, removedUserID); err != nil {
		log.Error().Err(err).Str("recipientID", recipientID).Msg("Failed to create account_removed pending event")
		return
	}
	rs.deliverAccountRemoved(eventID, requestID, recipientID, removedUserID, cert)
}

func (rs *RealtimeService) deliverAccountRemoved(eventID, requestID, recipientID, removedUserID string, cert *AccountRemovalWire) {
	wire := AccountRemovalWire{}
	if cert != nil {
		wire = *cert
	} else {
		var err error
		wire, err = rs.dbService.GetAccountRemovalWire(context.Background(), removedUserID)
		if err != nil || wire.UserID == "" {
			log.Error().Err(err).Str("userID", removedUserID).Msg("Failed to load account removal cert for delivery")
			return
		}
	}
	ok, err := rs.dbService.MarkEventDispatched(context.Background(), eventID)
	if err != nil {
		log.Error().Err(err).Str("eventID", eventID).Msg("Failed to mark account_removed dispatched")
		return
	}
	if !ok {
		return
	}
	if err := rs.connManager.SendToUser(recipientID, NewAccountRemovedMsg(eventID, requestID, removedUserID, wire)); err != nil {
		log.Error().Err(err).Str("recipientID", recipientID).Str("userID", removedUserID).Msg("Failed to send ACCOUNT_REMOVED")
	}
}

func (rs *RealtimeService) deliverReedRemoved(eventID, requestID, recipientID, authorID, reedID string, cert *ReedRemovalWire) {
	wire := ReedRemovalWire{}
	if cert != nil {
		wire = *cert
	} else {
		var err error
		wire, err = rs.dbService.GetReedRemovalWire(context.Background(), authorID, reedID)
		if err != nil || wire.UserID == "" {
			log.Error().Err(err).Str("reedID", reedID).Str("authorID", authorID).Msg("Failed to load reed removal cert for delivery")
			return
		}
	}
	ok, err := rs.dbService.MarkEventDispatched(context.Background(), eventID)
	if err != nil {
		log.Error().Err(err).Str("eventID", eventID).Msg("Failed to mark reed_removed dispatched")
		return
	}
	if !ok {
		return
	}
	if err := rs.connManager.SendToUser(recipientID, NewReedRemovedMsg(eventID, requestID, reedID, wire)); err != nil {
		log.Error().Err(err).Str("recipientID", recipientID).Str("reedID", reedID).Msg("Failed to send REED_REMOVED")
	}
}

// dispatchMany creates a pending reed event for each recipient and triggers relay dispatch to holderUserID.
// holderUserID is also the reed author for published-reed fanout.
func (rs *RealtimeService) dispatchMany(recipients []string, eventName EventName, reedID, authorUserID string) {
	for _, recipientID := range recipients {
		requestID, err := rs.dbService.GetSyncRequestID(context.Background(), recipientID)
		if err != nil || requestID == "" {
			// User hasn't sent SYNC_REQUEST yet; they'll receive this via catchUp on connect.
			log.Debug().
				Str("recipientID", recipientID).
				Str("eventName", string(eventName)).
				Msg("Skipping recipient: no sync_request_id set")
			continue
		}
		eventID := generateEventID()
		if err := rs.dbService.CreatePendingReedEvent(context.Background(), eventID, requestID, recipientID, eventName, authorUserID, reedID); err != nil {
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

	deviceID := r.URL.Query().Get("deviceId")
	if rs.deviceMismatch(userID, deviceID) {
		rejectConnection(w, "Device mismatch: this session is not bound to the active device.")
		return
	}

	ongoing, err := rs.ongoingImport(userID)
	if err != nil {
		log.Error().Err(err).Str("userID", userID).Msg("Import gate check failed")
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	if ongoing {
		log.Info().Str("userID", userID).Msg("WebSocket rejected: ongoing recovery import")
		rejectConnection(w, "Finish recovery import first.")
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
	client.wsRecordOutbound = func(messageType int, data []byte) {
		rs.metrics.WSMessage(context.Background(), metrics.DirectionOut, metrics.WSMessageType(messageType, data))
	}
	rs.connManager.RegisterClient(client)

	// Mark user as online
	if err := rs.dbService.MarkUserOnline(context.Background(), userID); err != nil {
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

	if err := rs.dbService.MarkUserOffline(context.Background(), userID); err != nil {
		log.Error().Err(err).Msg("Failed to mark user as offline")
	}

	// Remove broadcast subscription on disconnect
	if err := rs.dbService.UnsubscribeFromBroadcast(context.Background(), userID); err != nil {
		log.Error().
			Str("userID", userID).
			Err(err).
			Msg("Failed to remove broadcast subscription on disconnect")
	}

	// Discard all pending relay events for this requester (cascades from profile_subscriptions too)
	if err := rs.dbService.DeletePendingEventsByUser(context.Background(), userID); err != nil {
		log.Error().
			Str("userID", userID).
			Err(err).
			Msg("Failed to delete pending events on disconnect")
	}

	// Clean up any active profile subscriptions for this viewer
	if err := rs.dbService.DeleteProfileSubscriptionsByViewer(context.Background(), userID); err != nil {
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
		rs.metrics.WSMessage(context.Background(), metrics.DirectionIn, metrics.WSMessageType(messageType, data))

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

	var jsonMsg InboundJSONMsg
	if err := json.Unmarshal(data, &jsonMsg); err != nil {
		log.Error().Err(err).Str("data", string(data)).Msg("Failed to unmarshal JSON message")
		return
	}

	if jsonMsg.Type == "" {
		log.Warn().Str("data", string(data)).Msg("JSON message missing type field")
		return
	}

	// Handle different message types
	switch jsonMsg.Type {
	case "ping":
		response := PongMsg{Type: "pong", Data: jsonMsg.Data}
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
		var syncData SyncRequestData
		if err := json.Unmarshal(jsonMsg.Data, &syncData); err == nil {
			rs.handleSyncRequest(client, syncData)
		}

	case "REQUEST_REED":
		rs.handleRequestReed(client, jsonMsg.Data)

	case "RELAY_RESPONSE":
		rs.handleRelayResponse(client, jsonMsg.Data)

	case "RELAY_MISS":
		var d RelayMissData
		if err := json.Unmarshal(jsonMsg.Data, &d); err == nil {
			rs.handleRelayMiss(client, d)
		}

	case "DATA_ACK":
		var d DataAckData
		if err := json.Unmarshal(jsonMsg.Data, &d); err == nil {
			rs.handleDataAck(client, d)
		}

	case "DATA_INVALID":
		var d DataInvalidData
		if err := json.Unmarshal(jsonMsg.Data, &d); err == nil {
			rs.handleDataInvalid(client, d)
		}

	case "SUBSCRIBE_PROFILE":
		rs.handleSubscribeProfile(client, jsonMsg.Data)

	case "UNSUBSCRIBE_PROFILE":
		rs.handleUnsubscribeProfile(client, jsonMsg.Data)

	case "PUBLISH_READY":
		rs.handlePublishReady(client, jsonMsg.Data)

	case "SUBSCRIBE_REED":
		rs.handleSubscribeReed(client, jsonMsg)

	case "UNSUBSCRIBE_REED":
		rs.handleUnsubscribeReed(client, jsonMsg)

	case "SUBSCRIBE_PIPE":
		rs.handleSubscribePipe(client, jsonMsg.Data)

	case "UNSUBSCRIBE_PIPE":
		rs.handleUnsubscribePipe(client, jsonMsg.Data)

	default:
		log.Warn().Str("type", jsonMsg.Type).Msg("Unknown JSON WebSocket message type")
	}
}

// handleSubscribeUserJSON handles JSON user subscription requests
func (rs *RealtimeService) handleSubscribeUserJSON(client *Client) {
	rs.handleSubscribeUser(client, nil)

	response := SubscribedMsg{Type: "subscribed", Data: "Subscribed to user notifications"}
	if jsonBytes, err := json.Marshal(response); err == nil {
		client.writeMessage(websocket.TextMessage, jsonBytes)
	}
}

// handleSubscribeBroadcastJSON handles JSON broadcast subscription requests
func (rs *RealtimeService) handleSubscribeBroadcastJSON(client *Client) {
	rs.handleSubscribeBroadcast(client, nil)

	response := SubscribedMsg{Type: "subscribed", Data: "Subscribed to broadcast notifications"}
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
	err := rs.dbService.SubscribeToBroadcast(context.Background(), client.userID)
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
	if err := rs.dbService.UnsubscribeFromBroadcast(context.Background(), client.userID); err != nil {
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
	pe, err := rs.dbService.GetNextPendingForHolder(context.Background(), holderUserID)
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
	ok, err := rs.dbService.MarkEventDispatched(context.Background(), pe.EventID)
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
		if resetErr := rs.dbService.ResetDispatchedAt(context.Background(), pe.EventID); resetErr != nil {
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

func (rs *RealtimeService) handleRequestReed(client *Client, data json.RawMessage) {
	var req RequestReedData
	if err := json.Unmarshal(data, &req); err != nil {
		return
	}
	if req.RequestID == "" || req.ReedID == "" || req.AuthorID == "" {
		return
	}
	requestID, reedID, authorID := req.RequestID, req.ReedID, req.AuthorID

	exists, err := rs.dbService.ReedExists(context.Background(), authorID, reedID)
	if err != nil {
		log.Error().Err(err).Str("reedID", reedID).Str("authorID", authorID).Msg("Failed to check reed existence")
		return
	}
	if !exists {
		log.Debug().Str("reedID", reedID).Str("authorID", authorID).Msg("Requested reed does not exist, notifying requester")
		rs.connManager.SendToUser(client.userID, NewReedNotFoundMsg(requestID, reedID))
		return
	}

	// dropRequesterAllocation removes a stale holder row when the requester asks for
	// a reed the server thought they held — they clearly do not have the body locally.
	if _, err = rs.dbService.DeleteReedAllocation(context.Background(), authorID, reedID, client.userID); err != nil {
		log.Error().Err(err).
			Str("authorID", authorID).
			Str("reedID", reedID).
			Str("requesterID", client.userID).
			Msg("Failed to drop requester holder allocation")
		return
	}

	hasHolders, holder, err := rs.dbService.GetOnlineHolders(context.Background(), authorID, reedID)
	if err != nil {
		log.Error().Err(err).Str("reedID", reedID).Str("authorID", authorID).Msg("Failed to check reed holders")
		return
	}
	if !hasHolders {
		log.Debug().
			Str("reedID", reedID).
			Str("authorID", authorID).
			Str("requesterID", client.userID).
			Msg("Requested reed is unheld, notifying requester")
		rs.connManager.SendToUser(client.userID, NewReedNotHeldMsg(requestID, authorID, reedID))
		return
	}

	eventID := generateEventID()
	if err := rs.dbService.CreatePendingReedEvent(context.Background(), eventID, requestID, client.userID, RequestReedEvent, authorID, reedID); err != nil {
		log.Error().Err(err).Msg("Failed to create pending event")
		return
	}

	rs.connManager.SendToUser(client.userID, NewRequestAckMsg(requestID, eventID, reedID))

	if holder != "" {
		rs.dispatchNextIfConnected(holder)
	}
}

// handlePublishReady runs new-reed fanout when a pending_fanout row exists.
func (rs *RealtimeService) handlePublishReady(client *Client, data json.RawMessage) {
	var ready PublishReadyData
	if err := json.Unmarshal(data, &ready); err != nil {
		return
	}
	reedID := ready.ReedID
	if reedID == "" {
		return
	}
	authorUserID := client.userID

	claimed, tags, err := rs.dbService.ClaimPendingFanout(context.Background(), authorUserID, reedID)
	if err != nil {
		log.Error().Err(err).Str("reedID", reedID).Str("userID", authorUserID).Msg("Failed to claim pending fanout")
		return
	}

	if claimed {
		if shouldBroadcast(ready) {
			go rs.fanoutNewReed(authorUserID, reedID, tags)
		} else {
			go rs.fanoutNewReedNoBroadcast(authorUserID, reedID, tags)
		}
	} else {
		exists, err := rs.dbService.ReedExists(context.Background(), authorUserID, reedID)
		if err != nil {
			log.Error().Err(err).Str("reedID", reedID).Str("userID", authorUserID).Msg("Failed to check reed for publish ready ack")
			return
		}
		if !exists {
			return
		}
	}

	ack := PublishReadyAckMsg{
		Type: "PUBLISH_READY_ACK",
		Data: PublishReadyAckData{ReedID: reedID},
	}
	if jsonBytes, err := json.Marshal(ack); err == nil {
		client.writeMessage(websocket.TextMessage, jsonBytes)
	}
}

func (rs *RealtimeService) handleRelayResponse(client *Client, data json.RawMessage) {
	var relay RelayResponseData
	if err := json.Unmarshal(data, &relay); err != nil {
		return
	}
	eventID := relay.EventID
	if eventID == "" {
		return
	}

	pe, err := rs.dbService.GetPendingReedEvent(context.Background(), eventID)
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
		if name, err := rs.dbService.GetUsername(context.Background(), pe.UserID); err != nil {
			log.Error().Err(err).Str("userID", pe.UserID).Msg("Failed to load author username for broadcast reed")
		} else {
			username = name
		}
		if err := rs.connManager.SendToUser(pe.RequesterUserID, NewBroadcastReedMsg(pe.ReedID, relay.Data, username)); err != nil {
			log.Error().Err(err).Str("requesterID", pe.RequesterUserID).Msg("Failed to deliver broadcast reed")
		}
		if err := rs.dbService.DeletePendingEvent(context.Background(), eventID); err != nil {
			log.Error().Err(err).Str("eventID", eventID).Msg("Failed to delete pending event")
		}
	} else if pe.EventName == string(PipeReedEvent) {
		log.Info().Str("requesterID", pe.RequesterUserID).Str("reedID", pe.ReedID).Msg("Delivering pipe reed to subscriber")
		if err := rs.connManager.SendToUser(pe.RequesterUserID, NewPipeReedMsg(pe.EventID, pe.RequestID, pe.ReedID, relay.Data)); err != nil {
			log.Error().Err(err).Str("requesterID", pe.RequesterUserID).Msg("Failed to deliver pipe reed")
		}
		// Allocation and deletion deferred until viewer sends DATA_ACK or DATA_INVALID.
	} else if pe.EventName == string(FollowReedEvent) {
		log.Info().Str("requesterID", pe.RequesterUserID).Str("reedID", pe.ReedID).Msg("Delivering follow reed to subscriber")
		if err := rs.connManager.SendToUser(pe.RequesterUserID, NewFollowReedMsg(pe.EventID, pe.RequestID, pe.ReedID, relay.Data)); err != nil {
			log.Error().Err(err).Str("requesterID", pe.RequesterUserID).Msg("Failed to deliver follow reed")
		}
		// Allocation and deletion deferred until viewer sends DATA_ACK or DATA_INVALID.
	} else {
		if err := rs.connManager.SendToUser(pe.RequesterUserID, NewDataResponseMsg(pe.EventID, pe.RequestID, pe.ReedID, relay.Data)); err != nil {
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

	pe, err := rs.dbService.GetPendingReedEvent(context.Background(), data.EventID)
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

	if _, err := rs.dbService.DeleteReedAllocation(context.Background(), pe.UserID, pe.ReedID, client.userID); err != nil {
		log.Error().Err(err).Str("reedID", pe.ReedID).Str("holderID", client.userID).Msg("Failed to delete allocation on relay miss")
	}

	if err := rs.dbService.ResetDispatchedAt(context.Background(), data.EventID); err != nil {
		log.Error().Err(err).Str("eventID", data.EventID).Msg("Failed to reset dispatched_at on relay miss")
	}

	hasHolders, holder, err := rs.dbService.GetOnlineHolders(context.Background(), pe.UserID, pe.ReedID)
	if err != nil {
		log.Error().Err(err).Str("reedID", pe.ReedID).Msg("Failed to check reed holders on relay miss")
	} else if !hasHolders {
		rs.failReedNotHeld(pe)
	} else if holder != "" {
		rs.dispatchNextIfConnected(holder)
	}

	rs.dispatchNext(client.userID)
}

func (rs *RealtimeService) failReedNotHeld(pe *PendingReedEvent) {
	log.Info().
		Str("eventID", pe.EventID).
		Str("requesterID", pe.RequesterUserID).
		Str("authorID", pe.UserID).
		Str("reedID", pe.ReedID).
		Msg("No reed holders remain; notifying requester")
	if err := rs.connManager.SendToUser(pe.RequesterUserID, NewReedNotHeldMsg(pe.RequestID, pe.UserID, pe.ReedID)); err != nil {
		log.Error().Err(err).Str("requesterID", pe.RequesterUserID).Msg("Failed to send reed not held")
	}
	if err := rs.dbService.DeletePendingEvent(context.Background(), pe.EventID); err != nil {
		log.Error().Err(err).Str("eventID", pe.EventID).Msg("Failed to delete pending event on reed not held")
	}
}

// handleDataAck is called when the viewer has received and verified a delivery successfully.
// New reeds: allocate. Reed removals: clear allocation. Account removals: clear peer state.
func (rs *RealtimeService) handleDataAck(client *Client, data DataAckData) {
	if data.EventID == "" {
		return
	}
	eventID := data.EventID

	pe, err := rs.dbService.GetPendingSubject(context.Background(), eventID)
	if err != nil {
		log.Error().Err(err).Str("eventID", eventID).Msg("Failed to get pending event for data ack")
		return
	}
	if pe == nil {
		return
	}

	if EventName(pe.EventName) == ReedRemovedEvent {
		changed, err := rs.dbService.DeleteReedAllocation(context.Background(), pe.UserID, pe.ReedID, client.userID)
		if err != nil {
			log.Error().Err(err).Str("reedID", pe.ReedID).Str("userID", client.userID).Msg("Failed to clear allocation on reed_removed ack")
		} else if changed {
			rs.notifyReedCoverage(pe.UserID, pe.ReedID)
		}
	} else if EventName(pe.EventName) == AccountRemovedEvent {
		targets, err := rs.dbService.ClearPeerStateForRemovedAccount(context.Background(), client.userID, pe.UserID)
		if err != nil {
			log.Error().Err(err).Str("removedUserID", pe.UserID).Str("viewer", client.userID).Msg("Failed to clear peer state on account_removed ack")
		} else {
			for _, t := range targets {
				rs.notifyReedCoverage(t.AuthorUserID, t.ReedID)
			}
		}
	} else {
		changed, err := rs.dbService.AllocateReed(context.Background(), pe.ReedID, client.userID, pe.UserID)
		if err != nil {
			log.Error().Err(err).Str("reedID", pe.ReedID).Str("userID", client.userID).Msg("Failed to allocate reed on data ack")
		} else if changed {
			rs.notifyReedCoverage(pe.UserID, pe.ReedID)
		}
	}

	if err := rs.dbService.DeletePendingEvent(context.Background(), eventID); err != nil {
		log.Error().Err(err).Str("eventID", eventID).Msg("Failed to delete pending event on data ack")
	}
}

// handleDataInvalid is called when the viewer received a reed but its signature failed verification.
// The pending event is removed without allocating the reed to the viewer.
func (rs *RealtimeService) handleDataInvalid(client *Client, data DataInvalidData) {
	if data.EventID == "" {
		return
	}

	if err := rs.dbService.DeletePendingEvent(context.Background(), data.EventID); err != nil {
		log.Error().Err(err).Str("eventID", data.EventID).Msg("Failed to delete pending event on data invalid")
	}
}

func (rs *RealtimeService) handleSubscribeProfile(client *Client, data json.RawMessage) {
	var profile SubscribeProfileData
	if err := json.Unmarshal(data, &profile); err != nil || profile.UserID == "" {
		return
	}

	subscriptionID := generateEventID()
	if err := rs.dbService.CreateProfileSubscription(context.Background(), subscriptionID, client.userID, profile.UserID); err != nil {
		log.Error().Err(err).Msg("Failed to create profile subscription")
		return
	}

	missingIDs, err := rs.dbService.GetUnallocatedReeds(context.Background(), profile.UserID, client.userID)
	if err != nil {
		log.Error().Err(err).Msg("Failed to get unallocated reeds for viewer")
		return
	}

	for _, reedID := range missingIDs {
		eventID := generateEventID()
		requestID := generateEventID()
		if err := rs.dbService.CreateProfileSubscriptionEvent(context.Background(), eventID, requestID, client.userID, ProfileSubscriptionEvent, profile.UserID, reedID, subscriptionID); err != nil {
			log.Error().Err(err).Str("reedID", reedID).Msg("Failed to create profile subscription event")
			continue
		}
		holder, err := rs.dbService.GetOnlineReedHolder(context.Background(), profile.UserID, reedID)
		if err != nil || holder == "" {
			continue
		}
		rs.dispatchNextIfConnected(holder)
	}
}

func (rs *RealtimeService) handleUnsubscribeProfile(client *Client, data json.RawMessage) {
	var profile UnsubscribeProfileData
	if err := json.Unmarshal(data, &profile); err != nil || profile.UserID == "" {
		return
	}

	subscriptionID, err := rs.dbService.GetProfileSubscription(context.Background(), client.userID, profile.UserID)
	if err != nil {
		log.Error().Err(err).Msg("Failed to get profile subscription")
		return
	}
	if subscriptionID == "" {
		return
	}

	if err := rs.dbService.DeleteProfileSubscription(context.Background(), subscriptionID); err != nil {
		log.Error().Err(err).Str("subscriptionID", subscriptionID).Msg("Failed to delete profile subscription")
	}
}

func (rs *RealtimeService) handleSubscribeReed(client *Client, msg InboundJSONMsg) {
	authorID, reedID := msg.UserID, msg.ReedID
	if authorID == "" || reedID == "" {
		return
	}

	exists, err := rs.dbService.ReedExists(context.Background(), authorID, reedID)
	if err != nil {
		log.Error().Err(err).Str("userID", authorID).Str("reedID", reedID).Msg("Failed to check reed for subscribe")
		return
	}
	if !exists {
		return
	}

	echoes, coveragePercent, replies, err := rs.dbService.GetReedStatsSnapshot(context.Background(), authorID, reedID)
	if err != nil {
		log.Error().Err(err).Str("userID", authorID).Str("reedID", reedID).Msg("Failed to load reed stats for subscribe")
		return
	}

	rs.connManager.SubscribeReed(client, authorID, reedID)
	stats := ReedStatsMsg{
		Type:            "REED_STATS",
		UserID:          authorID,
		ReedID:          reedID,
		Echoes:          echoes,
		CoveragePercent: coveragePercent,
		Replies:         replies,
	}
	if err := rs.connManager.SendToClient(client, stats); err != nil {
		log.Error().Err(err).Str("userID", client.userID).Str("reedID", reedID).Msg("Failed to send REED_STATS")
	}
}

func (rs *RealtimeService) handleUnsubscribeReed(client *Client, msg InboundJSONMsg) {
	authorID, reedID := msg.UserID, msg.ReedID
	if authorID == "" || reedID == "" {
		return
	}
	rs.connManager.UnsubscribeReed(client, authorID, reedID)
}

func (rs *RealtimeService) handleSubscribePipe(client *Client, data json.RawMessage) {
	var pipe SubscribePipeData
	if err := json.Unmarshal(data, &pipe); err != nil {
		return
	}
	tag := NormalizePipeTag(pipe.Tag)
	if tag == "" {
		return
	}
	rs.connManager.SubscribePipe(client, tag)
	log.Debug().Str("userID", client.userID).Str("tag", tag).Msg("Client subscribed to pipe")
}

func (rs *RealtimeService) handleUnsubscribePipe(client *Client, data json.RawMessage) {
	var pipe SubscribePipeData
	if err := json.Unmarshal(data, &pipe); err != nil {
		return
	}
	tag := NormalizePipeTag(pipe.Tag)
	if tag == "" {
		return
	}
	rs.connManager.UnsubscribePipe(client, tag)
	log.Debug().Str("userID", client.userID).Str("tag", tag).Msg("Client unsubscribed from pipe")
}

// FilterSubscribedPipeTags returns extracted tags that currently have ≥1 listener.
// Used by SignReed to stash only relevant tags on pending_fanout.
func (rs *RealtimeService) FilterSubscribedPipeTags(tags []string) []string {
	return rs.connManager.FilterTagsWithListeners(tags)
}

func (rs *RealtimeService) notifyReedCoverage(authorUserID, reedID string) {
	holders, percent, err := rs.dbService.GetReedCoverage(context.Background(), authorUserID, reedID)
	if err != nil {
		log.Error().
			Err(err).
			Str("userID", authorUserID).
			Str("reedID", reedID).
			Msg("Failed to load reed coverage for notify")
		return
	}

	rs.metrics.ReedCoverage(context.Background(), authorUserID, reedID, holders, percent)

	if err := rs.connManager.BroadcastReedCoverage(ReedCoverageMsg{
		Type:            "REED_COVERAGE",
		UserID:          authorUserID,
		ReedID:          reedID,
		CoveragePercent: percent,
	}); err != nil {
		log.Error().Err(err).Str("userID", authorUserID).Str("reedID", reedID).Msg("Failed to broadcast REED_COVERAGE")
	}
}

func (rs *RealtimeService) notifyReedEchoes(authorUserID, reedID string) {
	echoes, err := rs.dbService.CountEchoes(context.Background(), authorUserID, reedID)
	if err != nil {
		log.Error().
			Err(err).
			Str("userID", authorUserID).
			Str("reedID", reedID).
			Msg("Failed to load reed echoes for notify")
		return
	}

	if err := rs.connManager.SendToReedSubscribers(authorUserID, reedID, ReedEchoesMsg{
		Type:   "REED_ECHOES",
		UserID: authorUserID,
		ReedID: reedID,
		Echoes: echoes,
	}); err != nil {
		log.Error().Err(err).Str("userID", authorUserID).Str("reedID", reedID).Msg("Failed to broadcast REED_ECHOES")
	}
}

func (rs *RealtimeService) notifyReedReplies(authorUserID, reedID string) {
	replies, err := rs.dbService.GetSubtreeReplyCount(context.Background(), authorUserID, reedID)
	if err != nil {
		log.Error().
			Err(err).
			Str("userID", authorUserID).
			Str("reedID", reedID).
			Msg("Failed to load subtree replies for notify")
		return
	}

	if err := rs.connManager.SendToReedSubscribers(authorUserID, reedID, ReedRepliesMsg{
		Type:    "REED_REPLIES",
		UserID:  authorUserID,
		ReedID:  reedID,
		Replies: replies,
	}); err != nil {
		log.Error().Err(err).Str("userID", authorUserID).Str("reedID", reedID).Msg("Failed to broadcast REED_REPLIES")
	}
}

func (rs *RealtimeService) handleSyncRequest(client *Client, data SyncRequestData) {
	if data.RequestID == "" {
		return
	}
	if err := rs.dbService.SetSyncRequestID(context.Background(), client.userID, data.RequestID); err != nil {
		log.Error().Err(err).Msg("Failed to store sync request ID")
		return
	}
	rs.catchUp(client.userID, data.RequestID)
	rs.dispatchNext(client.userID)
	rs.redispatchPendingRequests(client.userID)
}

func (rs *RealtimeService) redispatchPendingRequests(requesterUserID string) {
	requests, err := rs.dbService.GetPendingRequestsForRequester(context.Background(), requesterUserID)
	if err != nil {
		log.Error().Err(err).Str("requesterUserID", requesterUserID).Msg("Failed to get pending requests for requester")
		return
	}
	for _, req := range requests {
		if err := rs.dbService.ResetDispatchedAt(context.Background(), req.EventID); err != nil {
			log.Error().Err(err).Str("eventID", req.EventID).Msg("Failed to reset dispatched_at for pending request")
			continue
		}
		holder, err := rs.dbService.GetOnlineReedHolder(context.Background(), req.UserID, req.ReedID)
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
	unallocated, err := rs.dbService.GetMissingOut(context.Background(), userID)
	if err != nil {
		log.Error().
			Err(err).
			Str("userID", userID).
			Msg("Failed to get unallocated reeds from followings")
		return
	}

	for _, reed := range unallocated {
		eventID := generateEventID()
		if err := rs.dbService.CreatePendingReedEvent(context.Background(), eventID, requestID, userID, FollowReedEvent, reed.AuthorID, reed.ReedID); err != nil {
			log.Error().
				Err(err).
				Str("reedID", reed.ReedID).
				Msg("Failed to create catch-up pending event")
			continue
		}
		holder, err := rs.dbService.GetOnlineReedHolder(context.Background(), reed.AuthorID, reed.ReedID)
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

	removals, err := rs.dbService.GetMissingRemovals(context.Background(), userID)
	if err != nil {
		log.Error().Err(err).Str("userID", userID).Msg("Failed to get missing reed removals")
		return
	}
	for _, rem := range removals {
		eventID := generateEventID()
		if err := rs.dbService.CreatePendingReedEvent(context.Background(), eventID, requestID, userID, ReedRemovedEvent, rem.UserID, rem.ReedID); err != nil {
			log.Error().Err(err).Str("reedID", rem.ReedID).Msg("Failed to create catch-up reed_removed event")
			continue
		}
		rs.deliverReedRemoved(eventID, requestID, userID, rem.UserID, rem.ReedID, &rem.Cert)
	}

	accountRemovals, err := rs.dbService.GetMissingAccountRemovals(context.Background(), userID)
	if err != nil {
		log.Error().Err(err).Str("userID", userID).Msg("Failed to get missing account removals")
		return
	}
	for _, rem := range accountRemovals {
		eventID := generateEventID()
		if err := rs.dbService.CreatePendingAccountEvent(context.Background(), eventID, requestID, userID, rem.UserID); err != nil {
			log.Error().Err(err).Str("removedUserID", rem.UserID).Msg("Failed to create catch-up account_removed event")
			continue
		}
		rs.deliverAccountRemoved(eventID, requestID, userID, rem.UserID, &rem.Cert)
	}
}

// shouldBroadcast reports whether PUBLISH_READY should fan out to the broadcast stream.
// Absent or true means include broadcast; only explicit false opts out.
func shouldBroadcast(data PublishReadyData) bool {
	if len(data.Broadcast) == 0 || string(data.Broadcast) == "null" {
		return true
	}
	var include bool
	if err := json.Unmarshal(data.Broadcast, &include); err != nil {
		return true
	}
	return include
}
