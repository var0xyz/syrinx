//go:build !ops && !ripplescleanup

package main

import (
	"encoding/json"
	"testing"

	"google.golang.org/protobuf/proto"

	pb "syrinx/proto"
)

func TestNewReedNotHeldMsg(t *testing.T) {
	t.Parallel()

	msg := newReedNotHeldMsg("req-1", "reed-1")
	if msg.Type != "REED_NOT_HELD" {
		t.Fatalf("Type = %q, want REED_NOT_HELD", msg.Type)
	}
	if msg.Data.RequestID != "req-1" || msg.Data.ReedID != "reed-1" {
		t.Fatalf("unexpected data: %+v", msg.Data)
	}

	raw, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("Marshal() error: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("Unmarshal() error: %v", err)
	}
	if decoded["type"] != "REED_NOT_HELD" {
		t.Fatalf("wire type = %v", decoded["type"])
	}
	data, ok := decoded["data"].(map[string]any)
	if !ok {
		t.Fatal("expected data object")
	}
	if data["request_id"] != "req-1" || data["reed_id"] != "reed-1" {
		t.Fatalf("unexpected wire data: %+v", data)
	}
}

func TestReedNotHeldDistinctFromReedNotFound(t *testing.T) {
	t.Parallel()

	held := newReedNotHeldMsg("req-1", "reed-1")
	found := newReedNotFoundMsg("req-1", "reed-1")
	if held.Type == found.Type {
		t.Fatalf("REED_NOT_HELD and REED_NOT_FOUND must differ on the wire")
	}
}

// TestRequestReedProtobufRoundTrip mirrors what handleProtobufMessage does
// with an inbound binary REQUEST_REED frame: marshal a WSMessage the way a
// client would, unmarshal it the way the server does, and confirm the
// fields handleRequestReed reads survive the trip.
func TestRequestReedProtobufRoundTrip(t *testing.T) {
	t.Parallel()

	sent := &pb.WSMessage{
		Type:     pb.MessageType_REQUEST_REED,
		TypeName: pb.MessageType_REQUEST_REED.String(),
		Payload: &pb.WSMessage_RequestReed{
			RequestReed: &pb.RequestReedMessage{
				RequestId: "req-1",
				ReedId:    "1@server/reed-1",
			},
		},
	}
	raw, err := proto.Marshal(sent)
	if err != nil {
		t.Fatalf("Marshal() error: %v", err)
	}

	var received pb.WSMessage
	if err := proto.Unmarshal(raw, &received); err != nil {
		t.Fatalf("Unmarshal() error: %v", err)
	}
	if received.Type != pb.MessageType_REQUEST_REED {
		t.Fatalf("Type = %v, want REQUEST_REED", received.Type)
	}
	if received.TypeName != "REQUEST_REED" {
		t.Fatalf("TypeName = %q, want REQUEST_REED", received.TypeName)
	}
	req := received.GetRequestReed()
	if req.GetRequestId() != "req-1" || req.GetReedId() != "1@server/reed-1" {
		t.Fatalf("unexpected request_reed payload: %+v", req)
	}
}

// TestDataResponseProtobufRoundTrip mirrors deliverOrForwardDataResponse's
// construction of a binary DATA_RESPONSE frame, then decodes it the way a
// client would, checking every field the SPA's DataResponse handler reads
// (WSMessage.id as the relay event id, plus request_id/ciphertext).
func TestDataResponseProtobufRoundTrip(t *testing.T) {
	t.Parallel()

	sent := &pb.WSMessage{
		Type:     pb.MessageType_DATA_RESPONSE,
		TypeName: pb.MessageType_DATA_RESPONSE.String(),
		Id:       "event-1",
		Payload: &pb.WSMessage_DataResponse{
			DataResponse: &pb.DataResponseMessage{
				RequestId:  "req-1",
				Ciphertext: "ciphertext-blob",
			},
		},
	}
	raw, err := proto.Marshal(sent)
	if err != nil {
		t.Fatalf("Marshal() error: %v", err)
	}

	var received pb.WSMessage
	if err := proto.Unmarshal(raw, &received); err != nil {
		t.Fatalf("Unmarshal() error: %v", err)
	}
	if received.Type != pb.MessageType_DATA_RESPONSE {
		t.Fatalf("Type = %v, want DATA_RESPONSE", received.Type)
	}
	if received.TypeName != "DATA_RESPONSE" {
		t.Fatalf("TypeName = %q, want DATA_RESPONSE", received.TypeName)
	}
	if received.GetId() != "event-1" {
		t.Fatalf("Id = %q, want event-1", received.GetId())
	}
	data := received.GetDataResponse()
	if data.GetRequestId() != "req-1" || data.GetCiphertext() != "ciphertext-blob" {
		t.Fatalf("unexpected data_response payload: %+v", data)
	}
}
