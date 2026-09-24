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

// jsonWSMessageType classifies a text frame for metrics only — text frames
// are otherwise rejected outright (only binary protobuf is accepted), so
// this just labels what a client sent right before that rejection.
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
	if msg.Type == pb.MessageType_UNKNOWN {
		return "unknown_protobuf"
	}
	return msg.Type.String()
}
