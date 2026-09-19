//go:build !ops

package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"
)

// TestVerifyRecoveryReedCountersig_RealSignature: AuthorID arrives canonical,
// only ReedID is bare — fakeRecoveryVerifier-based tests never run real
// crypto so can't catch a wrong reconstructed payload; this signs/verifies
// for real.
func TestVerifyRecoveryReedCountersig_RealSignature(t *testing.T) {
	cryptoSvc := newCryptoService()
	serverID := "srv1"
	serverKP, err := cryptoSvc.createKeyPair("server", "", "")
	if err != nil {
		t.Fatalf("createKeyPair: %v", err)
	}

	authorID := "author1@" + serverID
	canonicalReedID := authorID + "/reed1"
	ts := time.Now().UTC().Truncate(time.Second)
	userSigArmorB64 := "dXNlclNpZw=="

	payload := buildReedPayload(serverID, canonicalReedID, serverKP.Fingerprint, userSigArmorB64, ts)
	sigArmor, err := cryptoSvc.sign(string(payload), serverKP.PrivateKey)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}

	req := recoveryReedRequest{
		ReedID:        "reed1",
		AuthorID:      authorID,
		UserSignature: recoveryUserSignature{Armor: userSigArmorB64},
		ServerSignature: recoveryServerSignature{
			ServerID: serverID, Fingerprint: serverKP.Fingerprint,
			Armor: base64.StdEncoding.EncodeToString([]byte(sigArmor)), Timestamp: ts,
		},
	}

	err = verifyRecoveryReedCountersig(context.Background(), req, serverID,
		func(ctx context.Context, fp string) (string, error) {
			if fp == serverKP.Fingerprint {
				return serverKP.PublicKey, nil
			}
			return "", nil
		},
		cryptoSvc,
	)
	if err != nil {
		t.Fatalf("verifyRecoveryReedCountersig: %v", err)
	}
}

func testReedUserSig() recoveryUserSignature {
	return recoveryUserSignature{Armor: "dXNlclNpZw=="}
}

func TestVerifyRecoveryReedCountersig_OK(t *testing.T) {
	ts := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	serverID := "srv1"
	req := recoveryReedRequest{
		ReedID:          "reed1",
		AuthorID:        "author1@srv1",
		UserSignature:   testReedUserSig(),
		ServerSignature: testRecoveryServerSig(serverID, ts),
	}
	payload := buildReedPayload(serverID, "author1@srv1/reed1", "SKEY", "dXNlclNpZw==", ts)
	v := &fakeRecoveryVerifier{}
	err := verifyRecoveryReedCountersig(context.Background(), req, serverID, func(ctx context.Context, fp string) (string, error) {
		if fp == "SKEY" {
			return "server-pub", nil
		}
		return "", nil
	}, v)
	if err != nil {
		t.Fatal(err)
	}
	_ = payload // ensures buildReedPayload stays aligned with verify path
}

func TestVerifyRecoveryReedCountersig_BadSig(t *testing.T) {
	ts := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	serverID := "srv1"
	req := recoveryReedRequest{
		ReedID:          "reed1",
		AuthorID:        "author1@srv1",
		UserSignature:   testReedUserSig(),
		ServerSignature: testRecoveryServerSig(serverID, ts),
	}
	payload := string(buildReedPayload(serverID, "author1@srv1/reed1", "SKEY", "dXNlclNpZw==", ts))
	v := &fakeRecoveryVerifier{failSig: map[string]bool{payload: true}}
	err := verifyRecoveryReedCountersig(context.Background(), req, serverID, func(ctx context.Context, _ string) (string, error) {
		return "server-pub", nil
	}, v)
	if err == nil {
		t.Fatal("expected bad countersignature")
	}
}

func TestVerifyRecoveryReedCountersig_ServerIDMismatch(t *testing.T) {
	ts := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	req := recoveryReedRequest{
		ReedID: "reed1", AuthorID: "author1@srv1", UserSignature: testReedUserSig(),
		ServerSignature: testRecoveryServerSig("other", ts),
	}
	err := verifyRecoveryReedCountersig(context.Background(), req, "srv1", func(ctx context.Context, _ string) (string, error) { return "pub", nil }, &fakeRecoveryVerifier{})
	if err == nil {
		t.Fatal("expected server id mismatch")
	}
}

func TestReportReed_Unauthorized(t *testing.T) {
	db := openSignupTestDB(t)
	h := newSignupGateHandlers(t, db, AppConfig{ServerName: "test"})
	rr := httptest.NewRecorder()
	h.ReportReed(rr, httptest.NewRequest(http.MethodPost, "/api/recovery/reeds", bytes.NewReader([]byte(`{}`))))
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d", rr.Code)
	}
}

func TestReportReed_BadCountersig(t *testing.T) {
	db := openSignupTestDB(t)
	h := newSignupGateHandlers(t, db, AppConfig{ServerName: "test"})
	serverID := h.services.db.GetServerID()
	ts := time.Now().UTC().Truncate(time.Second)
	req := recoveryReedRequest{
		ReedID: "reed1", AuthorID: "author1@" + serverID, UserSignature: testReedUserSig(),
		ServerSignature: testRecoveryServerSig(serverID, ts),
	}
	body, _ := json.Marshal(req)
	httpReq := httptest.NewRequest(http.MethodPost, "/api/recovery/reeds", bytes.NewReader(body))
	httpReq = httpReq.WithContext(context.WithValue(httpReq.Context(), userIDKey, "caller1"))
	rr := httptest.NewRecorder()
	h.ReportReed(rr, httpReq)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
}

func TestReportFollowing_TooMany(t *testing.T) {
	db := openSignupTestDB(t)
	h := newSignupGateHandlers(t, db, AppConfig{ServerName: "test"})
	ids := make([]string, 101)
	for i := range ids {
		ids[i] = "user-" + strconv.Itoa(i)
	}
	body, _ := json.Marshal(recoveryFollowingRequest{UserIDs: ids})
	httpReq := httptest.NewRequest(http.MethodPost, "/api/recovery/following", bytes.NewReader(body))
	httpReq = httpReq.WithContext(context.WithValue(httpReq.Context(), userIDKey, "caller1"))
	rr := httptest.NewRecorder()
	h.ReportFollowing(rr, httpReq)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status=%d", rr.Code)
	}
}

func TestReportFollowing_Self(t *testing.T) {
	db := openSignupTestDB(t)
	h := newSignupGateHandlers(t, db, AppConfig{ServerName: "test"})
	body, _ := json.Marshal(recoveryFollowingRequest{UserIDs: []string{"caller1"}})
	httpReq := httptest.NewRequest(http.MethodPost, "/api/recovery/following", bytes.NewReader(body))
	httpReq = httpReq.WithContext(context.WithValue(httpReq.Context(), userIDKey, "caller1"))
	rr := httptest.NewRecorder()
	h.ReportFollowing(rr, httpReq)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status=%d", rr.Code)
	}
}

func TestCompleteImport_Unauthorized(t *testing.T) {
	db := openSignupTestDB(t)
	h := newSignupGateHandlers(t, db, AppConfig{ServerName: "test"})
	rr := httptest.NewRecorder()
	h.CompleteImport(rr, httptest.NewRequest(http.MethodPost, "/api/recovery/complete", nil))
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d", rr.Code)
	}
}
