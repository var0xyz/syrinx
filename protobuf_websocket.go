package main

import (
	"net/http"
	"time"

	"github.com/gorilla/websocket"
	"github.com/rs/zerolog/log"
	"google.golang.org/protobuf/proto"

	pb "syrinx/proto"
)

// authenticateWebSocket authenticates WebSocket connections
func (h *Handlers) authenticateWebSocket(r *http.Request) (string, error) {
	userID := h.getUserID(r)
	return userID, nil
}

// ProtobufWebSocketHandler handles WebSocket connections with protobuf messages
func (h *Handlers) ProtobufWebSocketHandler(w http.ResponseWriter, r *http.Request) {
	log.Info().Msg("Protobuf WebSocket connection attempt")
	log.Info().Str("method", r.Method).Str("url", r.URL.String()).Msg("Protobuf WebSocket request details")

	// Authenticate user before upgrading to WebSocket
	userID, err := h.authenticateWebSocket(r)
	if err != nil {
		log.Error().Err(err).Msg("WebSocket authentication failed")
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	log.Info().Str("userID", userID).Msg("WebSocket authenticated user")

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
	log.Info().Msg("Protobuf WebSocket client connected")

	// Handle incoming messages
	for {
		messageType, data, err := conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				log.Error().Err(err).Msg("WebSocket error")
			} else {
				log.Info().Msg("Client disconnected")
			}
			break
		}

		// Handle both binary (protobuf) and text (JSON) messages
		if messageType == websocket.BinaryMessage {
			// Parse protobuf message
			var msg pb.WSMessage
			if err := proto.Unmarshal(data, &msg); err != nil {
				log.Error().Err(err).Msg("Failed to unmarshal protobuf message")
				continue
			}
			h.handleProtobufMessage(conn, &msg)
		} else if messageType == websocket.TextMessage {
			// Parse JSON message for testing
			h.handleJSONMessage(conn, data)
		} else {
			log.Warn().Int("messageType", messageType).Msg("Received unsupported message type, ignoring")
			continue
		}
	}

	// Unregister the connection when done
	h.wsManager.unregister <- conn
}

// handleProtobufMessage handles protobuf messages
func (h *Handlers) handleProtobufMessage(conn *websocket.Conn, msg *pb.WSMessage) {
	log.Debug().Str("type", msg.Type.String()).Msg("Received protobuf WebSocket message")

	// Handle different message types
	switch msg.Type {
	case pb.MessageType_PING:
		h.handleProtobufPing(conn, msg.GetPing())

	case pb.MessageType_SUBSCRIBE:
		h.handleProtobufSubscribe(conn, msg.GetSubscribe())

	default:
		log.Warn().Str("type", msg.Type.String()).Msg("Unknown protobuf WebSocket message type")
	}
}

// handleJSONMessage handles JSON messages for testing
func (h *Handlers) handleJSONMessage(conn *websocket.Conn, data []byte) {
	log.Debug().Str("data", string(data)).Msg("Received JSON WebSocket message")

	// Send JSON response
	if err := conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"pong","payload":{"data":"pong"}}`)); err != nil {
		log.Error().Err(err).Msg("Failed to send JSON response")
	}
}

// handleProtobufPing handles ping messages
func (h *Handlers) handleProtobufPing(conn *websocket.Conn, ping *pb.PingMessage) {
	response := &pb.WSMessage{
		Type: pb.MessageType_PONG,
		Payload: &pb.WSMessage_Pong{
			Pong: &pb.PongMessage{
				Data: ping.GetData(),
			},
		},
	}

	h.sendProtobufMessage(conn, response)
}

// handleProtobufSubscribe handles subscribe messages
func (h *Handlers) handleProtobufSubscribe(conn *websocket.Conn, subscribe *pb.SubscribeMessage) {
	response := &pb.WSMessage{
		Type: pb.MessageType_SUBSCRIBED,
		Payload: &pb.WSMessage_Subscribed{
			Subscribed: &pb.SubscribedMessage{
				Data: "You are now subscribed to updates",
			},
		},
	}

	h.sendProtobufMessage(conn, response)
}

// sendProtobufMessage sends a protobuf message over WebSocket
func (h *Handlers) sendProtobufMessage(conn *websocket.Conn, msg *pb.WSMessage) {
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

// BroadcastProtobufReedNotification broadcasts a reed notification using protobuf
func (h *Handlers) BroadcastProtobufReedNotification(reedID, userID, username, content string) {
	notification := &pb.WSMessage{
		Type: pb.MessageType_REED_NOTIFICATION,
		Payload: &pb.WSMessage_ReedNotification{
			ReedNotification: &pb.ReedNotificationMessage{
				ReedId:    reedID,
				UserId:    userID,
				Username:  username,
				Content:   content,
				Timestamp: time.Now().Unix(),
			},
		},
	}

	h.broadcastProtobufMessage(notification)
}

// BroadcastProtobufUserUpdate broadcasts a user update using protobuf
func (h *Handlers) BroadcastProtobufUserUpdate(userID string, updateType string) {
	update := &pb.WSMessage{
		Type: pb.MessageType_USER_UPDATE,
		Payload: &pb.WSMessage_UserUpdate{
			UserUpdate: &pb.UserUpdateMessage{
				UserId:     userID,
				UpdateType: updateType,
				Timestamp:  time.Now().Unix(),
			},
		},
	}

	h.broadcastProtobufMessage(update)
}

// broadcastProtobufMessage broadcasts a protobuf message to all connected clients
func (h *Handlers) broadcastProtobufMessage(msg *pb.WSMessage) {
	// Marshal the message once
	data, err := proto.Marshal(msg)
	if err != nil {
		log.Error().Err(err).Msg("Failed to marshal protobuf message for broadcast")
		return
	}

	h.wsManager.mutex.RLock()
	defer h.wsManager.mutex.RUnlock()

	for conn := range h.wsManager.connections {
		// Send as binary message
		if err := conn.WriteMessage(websocket.BinaryMessage, data); err != nil {
			log.Error().Err(err).Msg("Failed to write broadcast protobuf message")
			conn.Close()
			delete(h.wsManager.connections, conn)
			continue
		}
	}
}
