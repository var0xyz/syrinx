//go:build !ops && !ripplescleanup

package main

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"

	pb "syrinx/proto"
)

// A frame published for a user this replica holds is delivered locally.
func TestRealtimeBusHandleDeliversLocally(t *testing.T) {
	var gotUser string
	var gotFrame []byte
	bus := &realtimeBus{deliver: func(userID string, frame []byte) bool {
		gotUser, gotFrame = userID, frame
		return true
	}}

	frame := []byte("marshaled-frame")
	payload, err := json.Marshal(realtimeBusEnvelope{
		UserID: "viewer@testserver",
		Frame:  base64.StdEncoding.EncodeToString(frame),
	})
	if err != nil {
		t.Fatalf("marshal envelope: %v", err)
	}

	bus.handle(string(payload))

	if gotUser != "viewer@testserver" {
		t.Fatalf("userID = %q, want viewer@testserver", gotUser)
	}
	if string(gotFrame) != string(frame) {
		t.Fatalf("frame = %q, want %q", gotFrame, frame)
	}
}

// A wake-up envelope (no frame) is not delivered — the client's own
// catch-up path fetches the payload instead.
func TestRealtimeBusHandleIgnoresEmptyFrame(t *testing.T) {
	called := false
	bus := &realtimeBus{deliver: func(string, []byte) bool {
		called = true
		return true
	}}

	payload, err := json.Marshal(realtimeBusEnvelope{UserID: "viewer@testserver"})
	if err != nil {
		t.Fatalf("marshal envelope: %v", err)
	}
	bus.handle(string(payload))

	if called {
		t.Fatal("expected wake-up envelope not to be delivered")
	}
}

// Malformed payloads are dropped rather than panicking the listener loop.
func TestRealtimeBusHandleRejectsGarbage(t *testing.T) {
	called := false
	bus := &realtimeBus{deliver: func(string, []byte) bool {
		called = true
		return true
	}}

	bus.handle("not json")
	bus.handle(`{"userID":"viewer@testserver","frame":"!!!not-base64!!!"}`)

	if called {
		t.Fatal("expected malformed envelopes to be dropped")
	}
}

// A frame past the NOTIFY ceiling drops its payload and rides as a
// wake-up, so the notification itself still fits.
func TestRealtimeBusEnvelopeDropsOversizeFrame(t *testing.T) {
	big := base64.StdEncoding.EncodeToString([]byte(strings.Repeat("x", realtimeBusMaxPayload)))
	env := realtimeBusEnvelope{UserID: "viewer@testserver", Frame: big}
	if len(env.Frame) <= realtimeBusMaxPayload {
		t.Fatal("test setup: frame should exceed the payload ceiling")
	}

	// Mirrors Publish's ceiling check.
	if len(env.Frame) > realtimeBusMaxPayload {
		env.Frame = ""
	}

	payload, err := json.Marshal(env)
	if err != nil {
		t.Fatalf("marshal envelope: %v", err)
	}
	if len(payload) > realtimeBusMaxPayload {
		t.Fatalf("wake-up payload is %d bytes, over the ceiling", len(payload))
	}
}

// Round-trip a real protobuf frame, the shape SendToUser actually hands off.
func TestRealtimeBusRoundTripsProtobufFrame(t *testing.T) {
	msg := &pb.WSMessage{Type: pb.MessageType_PONG, Payload: &pb.WSMessage_Pong{Pong: &pb.PongMessage{Data: "hb"}}}
	frame, err := marshalWSMessage(msg)
	if err != nil {
		t.Fatalf("marshalWSMessage: %v", err)
	}

	var got []byte
	bus := &realtimeBus{deliver: func(_ string, frame []byte) bool {
		got = frame
		return true
	}}
	payload, err := json.Marshal(realtimeBusEnvelope{
		UserID: "viewer@testserver",
		Frame:  base64.StdEncoding.EncodeToString(frame),
	})
	if err != nil {
		t.Fatalf("marshal envelope: %v", err)
	}
	bus.handle(string(payload))

	if string(got) != string(frame) {
		t.Fatal("frame did not survive the bus round trip")
	}
}
