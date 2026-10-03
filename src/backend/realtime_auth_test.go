//go:build !ops && !ripplescleanup && !challengescleanup

package main

import (
	"net/http/httptest"
	"net/url"
	"strconv"
	"testing"
	"time"
)

func realtimeAuthRequest(t *testing.T, h *Handlers, kp cryptoKeyPair, keyID, payload, timestamp string) (string, error) {
	t.Helper()
	sig, err := h.services.crypto.sign(payload, kp.PrivateKey)
	if err != nil {
		t.Fatal(err)
	}
	q := url.Values{"publicKeyId": {keyID}, "timestamp": {timestamp}, "signature": {base64Encode(sig)}}
	r := httptest.NewRequest("GET", "/ws/?"+q.Encode(), nil)
	return authenticateWebSocket(r, h.services.db, h.services.crypto)
}

func TestAuthenticateWebSocket_BindsServerAndUser(t *testing.T) {
	h, _ := newInviteTestHandlers(t, "open", maxInvitesUnlimited)
	kp := signedUpUser(t, h, "u1", "alice")
	serverID := h.services.db.GetServerID()
	userID := string(canonicalID(serverID, "u1"))
	keyID := string(appendEntity(identityID(userID), kp.Fingerprint))
	ts := strconv.FormatInt(time.Now().Unix(), 10)

	got, err := realtimeAuthRequest(t, h, kp, keyID, string(buildRealtimeAuthPayload(serverID, userID, ts)), ts)
	if err != nil || got != userID {
		t.Fatalf("bound handshake: user=%q err=%v", got, err)
	}
	if _, err := realtimeAuthRequest(t, h, kp, keyID, ts, ts); err == nil {
		t.Fatal("a signature over the bare timestamp was accepted")
	}
	other := string(buildRealtimeAuthPayload("OtherServer", userID, ts))
	if _, err := realtimeAuthRequest(t, h, kp, keyID, other, ts); err == nil {
		t.Fatal("a handshake signed for another server was accepted")
	}
}
