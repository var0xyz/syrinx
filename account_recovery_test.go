//go:build !ops

package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestAccountRecoveryChallenge(t *testing.T) {
	h := &Handlers{}
	before := time.Now().UTC().Unix()
	rr := httptest.NewRecorder()
	h.AccountRecoveryChallenge(rr, httptest.NewRequest(http.MethodGet, "/api/account-recovery/challenge", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d", rr.Code)
	}
	var resp accountRecoveryChallengeResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	after := time.Now().UTC().Unix()
	if resp.Challenge < before || resp.Challenge > after {
		t.Fatalf("challenge=%d outside [%d,%d]", resp.Challenge, before, after)
	}
}

func TestBootstrapAccountRecovery_staleChallenge(t *testing.T) {
	h := &Handlers{services: &Services{}}
	fixed := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	body, _ := json.Marshal(bootstrapAccountRecoveryRequest{
		Challenge: fixed.Unix() - 120,
		UserID:    "u1",
		KeyID:     "AAA",
		Signature: "c2ln",
	})
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/account-recovery/bootstrap", bytes.NewReader(body))
	req.Header.Set("X-Syrinx-Device-Id", "550e8400-e29b-41d4-a716-446655440000")
	h.BootstrapAccountRecovery(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
}

func TestBootstrapAccountRecovery_missingDeviceHeader(t *testing.T) {
	h := &Handlers{services: &Services{}}
	body, _ := json.Marshal(bootstrapAccountRecoveryRequest{
		Challenge: time.Now().Unix(),
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
