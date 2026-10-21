//go:build !ops && !ripplescleanup && !challengescleanup

package main

import (
	"context"
	"errors"
	"net/http/httptest"
	"net/url"
	"strconv"
	"testing"
	"time"
)

func realtimeAuthRequest(t *testing.T, h *Handlers, kp cryptoKeyPair, keyID, payload, timestamp, nonce string) (string, error) {
	t.Helper()
	sig, err := h.services.crypto.sign(context.Background(), payload, kp.PrivateKey)
	if err != nil {
		t.Fatal(err)
	}
	q := url.Values{"publicKeyId": {keyID}, "timestamp": {timestamp}, "nonce": {nonce}, "signature": {base64Encode(sig)}}
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

	nonce := "bound-handshake-nonce-1"
	got, err := realtimeAuthRequest(t, h, kp, keyID, string(buildRealtimeAuthPayload(serverID, userID, ts, nonce)), ts, nonce)
	if err != nil || got != userID {
		t.Fatalf("bound handshake: user=%q err=%v", got, err)
	}
	if _, err := realtimeAuthRequest(t, h, kp, keyID, ts, ts, "bare-timestamp-nonce"); err == nil {
		t.Fatal("a signature over the bare timestamp was accepted")
	}
	other := string(buildRealtimeAuthPayload("OtherServer", userID, ts, "other-server-nonce"))
	if _, err := realtimeAuthRequest(t, h, kp, keyID, other, ts, "other-server-nonce"); err == nil {
		t.Fatal("a handshake signed for another server was accepted")
	}
}

func TestAuthenticateWebSocket_RefusesReplayedNonce(t *testing.T) {
	h, _ := newInviteTestHandlers(t, "open", maxInvitesUnlimited)
	kp := signedUpUser(t, h, "u1", "alice")
	serverID := h.services.db.GetServerID()
	userID := string(canonicalID(serverID, "u1"))
	keyID := string(appendEntity(identityID(userID), kp.Fingerprint))
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	nonce := "replayed-handshake-nonce"
	payload := string(buildRealtimeAuthPayload(serverID, userID, ts, nonce))

	if _, err := realtimeAuthRequest(t, h, kp, keyID, payload, ts, nonce); err != nil {
		t.Fatalf("first handshake: %v", err)
	}
	_, err := realtimeAuthRequest(t, h, kp, keyID, payload, ts, nonce)
	var refusal *wsAuthRefusal
	if !errors.As(err, &refusal) || refusal.reason != "replayed_nonce" {
		t.Fatalf("replayed handshake: err=%v, want replayed_nonce", err)
	}
	unsigned := "unsigned-handshake-nonce"
	if _, err := realtimeAuthRequest(t, h, kp, keyID, payload, ts, unsigned); err == nil {
		t.Fatal("a nonce outside the signed payload was accepted")
	}
	if _, err := realtimeAuthRequest(t, h, kp, keyID, payload, ts, "short"); err == nil {
		t.Fatal("a malformed nonce was accepted")
	}
}
