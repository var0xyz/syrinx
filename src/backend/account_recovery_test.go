//go:build !ops

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAccountRecoveryChallenge(t *testing.T) {
	db := openSignupTestDB(t)
	h := newSignupGateHandlers(t, db, AppConfig{ServerName: "test"})
	rr := httptest.NewRecorder()
	h.AccountRecoveryChallenge(rr, httptest.NewRequest(http.MethodGet, "/api/account-recovery/challenge", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d", rr.Code)
	}
	var resp accountRecoveryChallengeResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM account_recovery_challenges WHERE nonce = $1`, resp.Challenge).Scan(&n); err != nil || n != 1 {
		t.Fatalf("challenge not stored: n=%d err=%v", n, err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM recovery_challenges`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("account recovery wrote a server-recovery challenge: n=%d err=%v", n, err)
	}
}

func bootstrapWithChallenge(h *Handlers, challenge string) *httptest.ResponseRecorder {
	body, _ := json.Marshal(bootstrapAccountRecoveryRequest{
		Challenge: challenge,
		UserID:    "u1@nowhere",
		KeyID:     "AAA",
		Signature: "c2ln",
	})
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/account-recovery/bootstrap", bytes.NewReader(body))
	req.Header.Set("X-Syrinx-Device-Id", "550e8400-e29b-41d4-a716-446655440000")
	h.BootstrapAccountRecovery(rr, req)
	return rr
}

func TestBootstrapAccountRecovery_unknownChallenge(t *testing.T) {
	db := openSignupTestDB(t)
	h := newSignupGateHandlers(t, db, AppConfig{ServerName: "test"})
	if rr := bootstrapWithChallenge(h, "never-issued"); rr.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
}

func TestBootstrapAccountRecovery_expiredChallenge(t *testing.T) {
	db := openSignupTestDB(t)
	h := newSignupGateHandlers(t, db, AppConfig{ServerName: "test"})
	nonce, err := h.services.db.IssueAccountRecoveryChallenge(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE account_recovery_challenges SET issued_at = NOW() - INTERVAL '2 minutes' WHERE nonce = $1`, nonce); err != nil {
		t.Fatal(err)
	}
	if rr := bootstrapWithChallenge(h, nonce); rr.Code != http.StatusBadRequest || !strings.Contains(rr.Body.String(), "challenge") {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
}

func TestBootstrapAccountRecovery_challengeIsSingleUse(t *testing.T) {
	db := openSignupTestDB(t)
	h := newSignupGateHandlers(t, db, AppConfig{ServerName: "test"})
	nonce, err := h.services.db.IssueAccountRecoveryChallenge(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	// The first bootstrap fails later (unknown account) but still spends the nonce.
	if rr := bootstrapWithChallenge(h, nonce); strings.Contains(rr.Body.String(), "challenge") {
		t.Fatalf("first use rejected the challenge: %s", rr.Body.String())
	}
	if rr := bootstrapWithChallenge(h, nonce); rr.Code != http.StatusBadRequest || !strings.Contains(rr.Body.String(), "challenge") {
		t.Fatalf("reused challenge: status=%d body=%s", rr.Code, rr.Body.String())
	}
}

func TestBootstrapAccountRecovery_serverRecoveryNonceRejected(t *testing.T) {
	db := openSignupTestDB(t)
	h := newSignupGateHandlers(t, db, AppConfig{ServerName: "test"})
	nonce, err := h.services.db.IssueRecoveryChallenge(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if rr := bootstrapWithChallenge(h, nonce); rr.Code != http.StatusBadRequest || !strings.Contains(rr.Body.String(), "challenge") {
		t.Fatalf("server-recovery nonce accepted: status=%d body=%s", rr.Code, rr.Body.String())
	}
}

func TestBootstrapAccountRecovery_missingDeviceHeader(t *testing.T) {
	h := &Handlers{services: &Services{}}
	body, _ := json.Marshal(bootstrapAccountRecoveryRequest{
		Challenge: "nonce",
		UserID:    "u1",
		KeyID:     "AAA",
		Signature: "c2ln",
	})
	rr := httptest.NewRecorder()
	h.BootstrapAccountRecovery(rr, httptest.NewRequest(http.MethodPost, "/api/account-recovery/bootstrap", bytes.NewReader(body)))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
}
