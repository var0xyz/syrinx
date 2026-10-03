//go:build !ops

package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestIssueChallenge(t *testing.T) {
	db := openSignupTestDB(t)
	h := newSignupGateHandlers(t, db, AppConfig{ServerName: "test"})
	rr := httptest.NewRecorder()
	h.IssueChallenge(rr, httptest.NewRequest(http.MethodGet, "/api/recovery/identity/claim", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d", rr.Code)
	}
	var resp recoveryChallengeResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if resp.Challenge == "" {
		t.Fatal("empty challenge")
	}
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM recovery_challenges WHERE nonce = $1`, resp.Challenge).Scan(&n); err != nil || n != 1 {
		t.Fatalf("challenge not stored: n=%d err=%v", n, err)
	}
}

func claimWithChallenge(h *Handlers, challenge string) *httptest.ResponseRecorder {
	body, _ := json.Marshal(map[string]string{"challenge": challenge})
	rr := httptest.NewRecorder()
	h.ClaimIdentity(rr, httptest.NewRequest(http.MethodPost, "/api/recovery/identity/claim", bytes.NewReader(body)))
	return rr
}

func TestClaimIdentity_UnknownChallenge(t *testing.T) {
	db := openSignupTestDB(t)
	h := newSignupGateHandlers(t, db, AppConfig{ServerName: "test"})
	if rr := claimWithChallenge(h, "never-issued"); rr.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
}

func TestClaimIdentity_ExpiredChallenge(t *testing.T) {
	db := openSignupTestDB(t)
	h := newSignupGateHandlers(t, db, AppConfig{ServerName: "test"})
	nonce, err := h.services.db.IssueRecoveryChallenge(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE recovery_challenges SET issued_at = NOW() - INTERVAL '2 minutes' WHERE nonce = $1`, nonce); err != nil {
		t.Fatal(err)
	}
	if rr := claimWithChallenge(h, nonce); rr.Code != http.StatusBadRequest || !strings.Contains(rr.Body.String(), "challenge") {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
}

func TestClaimIdentity_ChallengeIsSingleUse(t *testing.T) {
	db := openSignupTestDB(t)
	h := newSignupGateHandlers(t, db, AppConfig{ServerName: "test"})
	nonce, err := h.services.db.IssueRecoveryChallenge(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	// The first claim fails later (empty profile) but still spends the nonce.
	if rr := claimWithChallenge(h, nonce); strings.Contains(rr.Body.String(), "challenge") {
		t.Fatalf("first use rejected the challenge: %s", rr.Body.String())
	}
	if rr := claimWithChallenge(h, nonce); rr.Code != http.StatusBadRequest || !strings.Contains(rr.Body.String(), "challenge") {
		t.Fatalf("reused challenge: status=%d body=%s", rr.Code, rr.Body.String())
	}
}

func TestClaimIdentity_BadChallengeSignature(t *testing.T) {
	db := openSignupTestDB(t)
	h := newSignupGateHandlers(t, db, AppConfig{ServerName: "test"})
	serverID := h.services.db.GetServerID()
	now := time.Now().UTC().Truncate(time.Second)
	ts := now
	profile := recoveryProfile{
		ID:              "user1@" + serverID,
		Username:        "alice",
		Role:            "user",
		MemberSince:     ts,
		UserSignature:   testRecoveryUserSig("user1@" + serverID + "/AAA"),
		ServerSignature: testRecoveryServerSig(serverID, ts),
	}
	root := recoveryKeyNode{
		recoveryKeyWire: recoveryKeyWire{
			Fingerprint: "AAA", Armor: base64.StdEncoding.EncodeToString([]byte("armor-a")), UserID: "user1@" + serverID, CreatedAt: ts,
			ServerSignature: testRecoveryServerSig(serverID, ts),
		},
	}
	nonce, err := h.services.db.IssueRecoveryChallenge(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(recoveryClaimRequest{
		Challenge: nonce,
		Signature: "Y2hhbGxlbmdl",
		Profile:   profile,
		Key:       root,
	})
	rr := httptest.NewRecorder()
	h.ClaimIdentity(rr, httptest.NewRequest(http.MethodPost, "/api/recovery/identity/claim", bytes.NewReader(body)))
	if rr.Code != http.StatusBadRequest && rr.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
}

func TestReportPeerIdentity_Unauthenticated(t *testing.T) {
	db := openSignupTestDB(t)
	h := newSignupGateHandlers(t, db, AppConfig{ServerName: "test"})
	rr := httptest.NewRecorder()
	h.ReportPeerIdentity(rr, httptest.NewRequest(http.MethodPost, "/api/recovery/identity", bytes.NewReader([]byte(`{}`))))
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
}

func TestReportPeerIdentity_SelfSubmit(t *testing.T) {
	db := openSignupTestDB(t)
	h := newSignupGateHandlers(t, db, AppConfig{ServerName: "test"})
	body, _ := json.Marshal(recoveryPeerIdentityRequest{
		Profile: recoveryProfile{ID: "caller1"},
	})
	req := httptest.NewRequest(http.MethodPost, "/api/recovery/identity", bytes.NewReader(body))
	req = req.WithContext(context.WithValue(req.Context(), userIDKey, "caller1"))
	rr := httptest.NewRecorder()
	h.ReportPeerIdentity(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
}

func TestReportPeerIdentity_BrokenNest(t *testing.T) {
	db := openSignupTestDB(t)
	h := newSignupGateHandlers(t, db, AppConfig{ServerName: "test"})
	serverID := h.services.db.GetServerID()
	ts := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	profile := recoveryProfile{
		ID:              "peer1",
		Username:        "bob",
		Role:            "user",
		MemberSince:     ts,
		UserSignature:   testRecoveryUserSig("BBB"),
		ServerSignature: testRecoveryServerSig(serverID, ts),
	}
	root := recoveryKeyNode{
		recoveryKeyWire: recoveryKeyWire{
			Fingerprint: "BBB", Armor: base64.StdEncoding.EncodeToString([]byte("armor-b")), UserID: "peer1", CreatedAt: ts,
			ServerSignature: testRecoveryServerSig(serverID, ts),
		},
		Predecessor: &recoveryKeyNode{
			Signature: "bad-pred-sig",
			recoveryKeyWire: recoveryKeyWire{
				Fingerprint: "AAA", Armor: base64.StdEncoding.EncodeToString([]byte("armor-a")), UserID: "peer1", CreatedAt: ts,
				ServerSignature: testRecoveryServerSig(serverID, ts),
			},
		},
	}
	body, _ := json.Marshal(recoveryPeerIdentityRequest{Profile: profile, Key: root})
	req := httptest.NewRequest(http.MethodPost, "/api/recovery/identity", bytes.NewReader(body))
	req = req.WithContext(context.WithValue(req.Context(), userIDKey, "caller1"))
	rr := httptest.NewRecorder()
	h.ReportPeerIdentity(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
}
