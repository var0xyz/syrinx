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
	approved := time.Date(2026, 10, 1, 9, 30, 0, 0, time.UTC)
	full := peering{
		PeerID: "peer5678", Name: "Peer", BaseURL: "https://api.peer.example", FrontendURL: "https://peer.example",
		KeyID: "fp@peer5678", InvitedBy: "a1@home1234", InvitedAt: &approved, ApprovedBy: "a2@home1234",
		ApprovedAt: &approved, PeerApprovedAt: &approved, DisconnectRequestedBy: "a1@home1234",
		DisconnectRequestedAt: &at, DisconnectReason: "spam", RevokedBy: "a2@home1234", RevokedAt: &at,
		RevokedReason: "spam", PurgedAt: &at,
	}
	for name, tc := range map[string]struct {
		p    peering
		want string
	}{
		"every field": {full, `{"approvedAt":"2026-10-01T09:30:00Z","approvedBy":"a2@home1234","baseURL":"https://api.peer.example",` +
			`"disconnectReason":"spam","disconnectRequestedAt":"2026-10-23T12:00:00Z","disconnectRequestedBy":"a1@home1234",` +
			`"frontendURL":"https://peer.example","invitedAt":"2026-10-01T09:30:00Z","invitedBy":"a1@home1234","keyID":"fp@peer5678",` +
			`"name":"Peer","peerApprovedAt":"2026-10-01T09:30:00Z","peerID":"peer5678","purgedAt":"2026-10-23T12:00:00Z",` +
			`"revokedAt":"2026-10-23T12:00:00Z","revokedBy":"a2@home1234","revokedReason":"spam","serverID":"home1234",` +
			`"signedAt":"2026-10-23T12:00:00Z","type":"peer-record"}`},
		"required only": {peering{PeerID: "peer5678", Name: "Peer", FrontendURL: "https://peer.example", ApprovedAt: &approved},
			`{"approvedAt":"2026-10-01T09:30:00Z","frontendURL":"https://peer.example","name":"Peer","peerID":"peer5678",` +
				`"serverID":"home1234","signedAt":"2026-10-23T12:00:00Z","type":"peer-record"}`},
	} {
		if got := string(buildPeerRecordPayload("home1234", tc.p, at)); got != tc.want {
			t.Fatalf("%s payload\n got %s\nwant %s", name, got, tc.want)
		}
	}
}
