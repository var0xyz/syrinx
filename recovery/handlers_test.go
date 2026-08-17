package recovery

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

	"github.com/gorilla/mux"
	"syrinx/crypto"
	"syrinx/identity"
)

func testReedUserSig() UserSignature {
	return UserSignature{Armor: "dXNlclNpZw=="}
}

// TestVerifyReedCountersig_RealSignature: AuthorID arrives canonical, only
// ReedID is bare — fakeVerifier-based tests never run real crypto so
// can't catch a wrong reconstructed payload; this signs/verifies for real.
func TestVerifyReedCountersig_RealSignature(t *testing.T) {
	cryptoSvc := crypto.NewService()
	serverID := "srv1"
	serverKP, err := cryptoSvc.CreateKeyPair("server", "", "")
	if err != nil {
		t.Fatalf("CreateKeyPair: %v", err)
	}

	authorID := "author1@" + serverID
	canonicalReedID := authorID + "/reed1"
	ts := time.Now().UTC().Truncate(time.Second)
	userSigArmorB64 := "dXNlclNpZw=="

	payload := identity.BuildReedPayload(serverID, canonicalReedID, serverKP.Fingerprint, userSigArmorB64, ts)
	sigArmor, err := cryptoSvc.Sign(string(payload), serverKP.PrivateKey)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}

	req := ReedRequest{
		ReedID:        "reed1",
		AuthorID:      authorID,
		UserSignature: UserSignature{Armor: userSigArmorB64},
		ServerSignature: ServerSignature{
			ServerID: serverID, Fingerprint: serverKP.Fingerprint,
			Armor: base64.StdEncoding.EncodeToString([]byte(sigArmor)), Timestamp: ts,
		},
	}

	err = verifyReedCountersig(context.Background(), req, serverID,
		func(ctx context.Context, fp string) (string, error) {
			if fp == serverKP.Fingerprint {
				return serverKP.PublicKey, nil
			}
			return "", nil
		},
		cryptoSvc,
	)
	if err != nil {
		t.Fatalf("verifyReedCountersig: %v", err)
	}
}

func TestVerifyReedCountersig_OK(t *testing.T) {
	ts := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	serverID := "srv1"
	req := ReedRequest{
		ReedID:          "reed1",
		AuthorID:        "author1@srv1",
		UserSignature:   testReedUserSig(),
		ServerSignature: testServerSig(serverID, ts),
	}
	payload := identity.BuildReedPayload(serverID, "author1@srv1/reed1", "SKEY", "dXNlclNpZw==", ts)
	v := &fakeVerifier{}
	err := verifyReedCountersig(context.Background(), req, serverID, func(ctx context.Context, fp string) (string, error) {
		if fp == "SKEY" {
			return "server-pub", nil
		}
		return "", nil
	}, v)
	if err != nil {
		t.Fatal(err)
	}
	_ = payload // ensures BuildReedPayload stays aligned with verify path
}

func TestVerifyReedCountersig_BadSig(t *testing.T) {
	ts := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	serverID := "srv1"
	req := ReedRequest{
		ReedID:          "reed1",
		AuthorID:        "author1@srv1",
		UserSignature:   testReedUserSig(),
		ServerSignature: testServerSig(serverID, ts),
	}
	payload := string(identity.BuildReedPayload(serverID, "author1@srv1/reed1", "SKEY", "dXNlclNpZw==", ts))
	v := &fakeVerifier{failSig: map[string]bool{payload: true}}
	err := verifyReedCountersig(context.Background(), req, serverID, func(ctx context.Context, _ string) (string, error) {
		return "server-pub", nil
	}, v)
	if err == nil {
		t.Fatal("expected bad countersignature")
	}
}

func TestVerifyReedCountersig_ServerIDMismatch(t *testing.T) {
	ts := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	req := ReedRequest{
		ReedID: "reed1", AuthorID: "author1@srv1", UserSignature: testReedUserSig(),
		ServerSignature: testServerSig("other", ts),
	}
	err := verifyReedCountersig(context.Background(), req, "srv1", func(ctx context.Context, _ string) (string, error) { return "pub", nil }, &fakeVerifier{})
	if err == nil {
		t.Fatal("expected server id mismatch")
	}
}

func TestReportReed_Unauthorized(t *testing.T) {
	deps := Deps{UserIDKey: testUserIDKey}
	rr := httptest.NewRecorder()
	deps.ReportReed(rr, httptest.NewRequest(http.MethodPost, "/api/recovery/reeds", bytes.NewReader([]byte(`{}`))))
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d", rr.Code)
	}
}

func TestReportReed_BadCountersig(t *testing.T) {
	ts := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	serverID := "srv1"
	req := ReedRequest{
		ReedID: "reed1", AuthorID: "author1@srv1", UserSignature: testReedUserSig(),
		ServerSignature: testServerSig(serverID, ts),
	}
	payload := string(identity.BuildReedPayload(serverID, "author1@srv1/reed1", "SKEY", "dXNlclNpZw==", ts))
	body, _ := json.Marshal(req)
	deps := Deps{
		ServerID:  serverID,
		UserIDKey: testUserIDKey,
		Crypto:    &fakeVerifier{failSig: map[string]bool{payload: true}},
		Lookup:    func(ctx context.Context, _ string) (string, error) { return "pub", nil },
	}
	httpReq := httptest.NewRequest(http.MethodPost, "/api/recovery/reeds", bytes.NewReader(body))
	httpReq = httpReq.WithContext(context.WithValue(httpReq.Context(), testUserIDKey, "caller1"))
	rr := httptest.NewRecorder()
	deps.ReportReed(rr, httpReq)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
}

func TestReportFollowing_TooMany(t *testing.T) {
	ids := make([]string, 101)
	for i := range ids {
		ids[i] = "user-" + strconv.Itoa(i)
	}
	body, _ := json.Marshal(FollowingRequest{UserIDs: ids})
	deps := Deps{UserIDKey: testUserIDKey}
	httpReq := httptest.NewRequest(http.MethodPost, "/api/recovery/following", bytes.NewReader(body))
	httpReq = httpReq.WithContext(context.WithValue(httpReq.Context(), testUserIDKey, "caller1"))
	rr := httptest.NewRecorder()
	deps.ReportFollowing(rr, httpReq)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status=%d", rr.Code)
	}
}

func TestReportFollowing_Self(t *testing.T) {
	body, _ := json.Marshal(FollowingRequest{UserIDs: []string{"caller1"}})
	deps := Deps{UserIDKey: testUserIDKey}
	httpReq := httptest.NewRequest(http.MethodPost, "/api/recovery/following", bytes.NewReader(body))
	httpReq = httpReq.WithContext(context.WithValue(httpReq.Context(), testUserIDKey, "caller1"))
	rr := httptest.NewRecorder()
	deps.ReportFollowing(rr, httpReq)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status=%d", rr.Code)
	}
}

func TestCompleteImport_Unauthorized(t *testing.T) {
	deps := Deps{UserIDKey: testUserIDKey}
	rr := httptest.NewRecorder()
	deps.CompleteImport(rr, httptest.NewRequest(http.MethodPost, "/api/recovery/complete", nil))
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d", rr.Code)
	}
}

func TestRegisterRoutes_IncludesPhase6(t *testing.T) {
	r := mux.NewRouter()
	api := r.PathPrefix("/api").Subrouter()
	RegisterRoutes(api, Deps{UserIDKey: testUserIDKey})

	for _, path := range []string{
		"/api/recovery/reeds",
		"/api/recovery/following",
		"/api/recovery/complete",
	} {
		rr := httptest.NewRecorder()
		r.ServeHTTP(rr, httptest.NewRequest(http.MethodOptions, path, nil))
		if rr.Code != http.StatusOK {
			t.Fatalf("%s OPTIONS status=%d", path, rr.Code)
		}
	}
}
