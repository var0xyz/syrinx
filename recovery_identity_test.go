//go:build !ops

package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
	if resp.Challenge <= 0 {
		t.Fatalf("challenge=%d", resp.Challenge)
	}
}

func TestClaimIdentity_StaleChallenge(t *testing.T) {
	db := openSignupTestDB(t)
	h := newSignupGateHandlers(t, db, AppConfig{ServerName: "test"})
	now := time.Now().UTC()
	body, _ := json.Marshal(recoveryClaimRequest{Challenge: now.Unix() - 120})
	rr := httptest.NewRecorder()
	h.ClaimIdentity(rr, httptest.NewRequest(http.MethodPost, "/api/recovery/identity/claim", bytes.NewReader(body)))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
}

func TestClaimIdentity_FutureChallenge(t *testing.T) {
	db := openSignupTestDB(t)
	h := newSignupGateHandlers(t, db, AppConfig{ServerName: "test"})
	now := time.Now().UTC()
	body, _ := json.Marshal(recoveryClaimRequest{Challenge: now.Unix() + 5})
	rr := httptest.NewRecorder()
	h.ClaimIdentity(rr, httptest.NewRequest(http.MethodPost, "/api/recovery/identity/claim", bytes.NewReader(body)))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status=%d", rr.Code)
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
	body, _ := json.Marshal(recoveryClaimRequest{
		Challenge: now.Unix(),
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
