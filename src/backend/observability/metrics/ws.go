package metrics

import (
	"encoding/json"

	"github.com/gorilla/websocket"
	"google.golang.org/protobuf/proto"

	pb "syrinx/proto"
)

// WSMessageType classifies an inbound or outbound WebSocket frame.
func WSMessageType(frameType int, data []byte) string {
	if frameType == websocket.BinaryMessage {
		return protobufWSMessageType(data)
	}
	if frameType == websocket.TextMessage {
		return jsonWSMessageType(data)
	}
	return "unknown"
}

func jsonWSMessageType(data []byte) string {
	var msg struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(data, &msg); err != nil || msg.Type == "" {
		return "unknown_json"
	}
	return msg.Type
}

func protobufWSMessageType(data []byte) string {
	var msg pb.WSMessage
	if err := proto.Unmarshal(data, &msg); err != nil {
		return "unknown_protobuf"
	}
	switch msg.Type {
	case pb.MessageType_PING:
		return "PING"
	case pb.MessageType_PONG:
		return "PONG"
	case pb.MessageType_SUBSCRIBE:
		return "SUBSCRIBE"
	case pb.MessageType_SUBSCRIBED:
		return "subscribed"
	case pb.MessageType_REED_NOTIFICATION:
		return "REED_NOTIFICATION"
	case pb.MessageType_USER_UPDATE:
		return "USER_UPDATE"
	case pb.MessageType_ERROR:
		return "ERROR"
	case pb.MessageType_SUBSCRIBE_USER:
		return "SUBSCRIBE_USER"
	case pb.MessageType_SUBSCRIBE_BROADCAST:
		return "SUBSCRIBE_BROADCAST"
	case pb.MessageType_UNSUBSCRIBE_USER:
		return "UNSUBSCRIBE_USER"
	case pb.MessageType_UNSUBSCRIBE_BROADCAST:
		return "UNSUBSCRIBE_BROADCAST"
	default:
		return "unknown_protobuf"
	}
}
