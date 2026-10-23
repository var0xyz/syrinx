//go:build !ops

package main

import (
	"testing"
	"time"
)

// Golden bytes shared with scripts/test-peer-record-payload.mjs: the SPA
// must build the same record it verifies.
func TestBuildPeerRecordPayload(t *testing.T) {
	at := time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)
	got := string(buildPeerRecordPayload("home1234", "peer5678", "Peer", "fp@peer5678",
		"https://api.peer.example", "https://peer.example", peerRecordRevoked, at))
	want := `{"baseURL":"https://api.peer.example","frontendURL":"https://peer.example","keyID":"fp@peer5678",` +
		`"name":"Peer","peerID":"peer5678","serverID":"home1234","signedAt":"2026-10-23T12:00:00Z",` +
		`"status":"revoked","type":"peer-record"}`
	if got != want {
		t.Fatalf("payload\n got %s\nwant %s", got, want)
	}
}
