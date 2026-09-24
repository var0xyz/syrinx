//go:build !ops && !ripplescleanup

package main

import (
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	pb "syrinx/proto"
)

func TestNewReedNotHeldMsg(t *testing.T) {
	t.Parallel()

	msg := newReedNotHeldMsg("req-1", "reed-1")
	if msg.Type != pb.MessageType_REED_NOT_HELD {
		t.Fatalf("Type = %v, want REED_NOT_HELD", msg.Type)
	}
	data := msg.GetReedNotHeld()
	if data.GetRequestId() != "req-1" || data.GetReedId() != "reed-1" {
		t.Fatalf("unexpected data: %+v", data)
	}

	raw, err := proto.Marshal(msg)
	if err != nil {
		t.Fatalf("Marshal() error: %v", err)
	}
	var decoded pb.WSMessage
	if err := proto.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("Unmarshal() error: %v", err)
	}
	if decoded.Type != pb.MessageType_REED_NOT_HELD {
		t.Fatalf("wire type = %v", decoded.Type)
	}
	wireData := decoded.GetReedNotHeld()
	if wireData.GetRequestId() != "req-1" || wireData.GetReedId() != "reed-1" {
		t.Fatalf("unexpected wire data: %+v", wireData)
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

// TestNewMailboxMsg checks the canonical ref (userID@serverID/id), not the
// bare row id, is what ends up on the wire.
func TestNewMailboxMsg(t *testing.T) {
	t.Parallel()

	msg := newMailboxMsg("alice@server-a", "msg-1", "ciphertext-blob")
	if msg.Type != pb.MessageType_MAILBOX {
		t.Fatalf("Type = %v, want MAILBOX", msg.Type)
	}
	data := msg.GetMailbox()
	if data.GetId() != "alice@server-a/msg-1" {
		t.Fatalf("Id = %q, want canonical ref", data.GetId())
	}
	if data.GetCiphertext() != "ciphertext-blob" {
		t.Fatalf("unexpected ciphertext: %+v", data)
	}
}

// TestNewReedRemovedMsgCarriesCert checks the signed removal cert survives
// the JSON-wire-type (reedRemovalWire) to protobuf conversion intact.
func TestNewReedRemovedMsgCarriesCert(t *testing.T) {
	t.Parallel()

	signedAt := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	wire := reedRemovalWire{
		Type:     identityTypeReed,
		ServerID: "server-a",
		UserID:   "alice@server-a",
		ReedID:   "alice@server-a/reed-1",
		UserSignature: UserSignature{
			ID:    "key-1",
			Armor: "user-sig-armor",
		},
		ServerSignature: ServerSignature{
			ID:       "server-key-1",
			Armor:    "server-sig-armor",
			SignedAt: signedAt,
		},
	}

	msg := newReedRemovedMsg("event-1", "req-1", wire)
	raw, err := proto.Marshal(msg)
	if err != nil {
		t.Fatalf("Marshal() error: %v", err)
	}
	var received pb.WSMessage
	if err := proto.Unmarshal(raw, &received); err != nil {
		t.Fatalf("Unmarshal() error: %v", err)
	}

	cert := received.GetReedRemoved().GetCert()
	if cert.GetServerId() != "server-a" || cert.GetUserId() != "alice@server-a" || cert.GetReedId() != "alice@server-a/reed-1" {
		t.Fatalf("unexpected cert identity fields: %+v", cert)
	}
	if cert.GetUserSignature().GetId() != "key-1" || cert.GetUserSignature().GetArmor() != "user-sig-armor" {
		t.Fatalf("unexpected user signature: %+v", cert.GetUserSignature())
	}
	if cert.GetServerSignature().GetId() != "server-key-1" || cert.GetServerSignature().GetArmor() != "server-sig-armor" {
		t.Fatalf("unexpected server signature: %+v", cert.GetServerSignature())
	}
	if cert.GetServerSignature().GetSignedAt() != signedAt.Unix() {
		t.Fatalf("SignedAt = %d, want %d", cert.GetServerSignature().GetSignedAt(), signedAt.Unix())
	}
}

// TestNewRipplePostedMsgCarriesRipple checks a full Ripple payload (with a
// ReplyingTo pointer) survives the RippleWire-to-protobuf conversion.
func TestNewRipplePostedMsgCarriesRipple(t *testing.T) {
	t.Parallel()

	replyingTo := "hash-parent"
	postedAt := time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC)
	ripple := RippleWire{
		Hash:       "hash-1",
		ThreadID:   "thread-1",
		UserID:     "bob@server-b",
		Content:    "hello",
		ReplyingTo: &replyingTo,
		Deleted:    false,
		PostedAt:   postedAt,
		UserSignature: UserSignature{
			ID:    "key-2",
			Armor: "armor-2",
		},
		ServerSignature: ServerSignature{
			ID:       "server-key-2",
			Armor:    "server-armor-2",
			SignedAt: postedAt,
		},
	}

	msg := newRipplePostedMsg("alice@server-a", "alice@server-a/reed-1", ripple)
	raw, err := proto.Marshal(msg)
	if err != nil {
		t.Fatalf("Marshal() error: %v", err)
	}
	var received pb.WSMessage
	if err := proto.Unmarshal(raw, &received); err != nil {
		t.Fatalf("Unmarshal() error: %v", err)
	}

	got := received.GetRipplePosted()
	if got.GetUserId() != "alice@server-a" || got.GetReedId() != "alice@server-a/reed-1" {
		t.Fatalf("unexpected envelope fields: %+v", got)
	}
	gotRipple := got.GetRipple()
	if gotRipple.GetHash() != "hash-1" || gotRipple.GetReplyingTo() != "hash-parent" {
		t.Fatalf("unexpected ripple fields: %+v", gotRipple)
	}
	if gotRipple.GetPostedAt() != postedAt.Unix() {
		t.Fatalf("PostedAt = %d, want %d", gotRipple.GetPostedAt(), postedAt.Unix())
	}
}
