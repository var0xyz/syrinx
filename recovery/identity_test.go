package recovery

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/gorilla/mux"
)

const testUserIDKey = "userID"

func TestIssueChallenge(t *testing.T) {
	fixed := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	deps := Deps{Now: func() time.Time { return fixed }}
	rr := httptest.NewRecorder()
	deps.IssueChallenge(rr, httptest.NewRequest(http.MethodGet, "/api/recovery/identity/claim", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d", rr.Code)
	}
	var resp ChallengeResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if resp.Challenge != fixed.Unix() {
		t.Fatalf("challenge=%d", resp.Challenge)
	}
}

func TestClaimIdentity_StaleChallenge(t *testing.T) {
	fixed := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	deps := Deps{
		Now:      func() time.Time { return fixed },
		ServerID: "srv1",
		Crypto:   &fakeVerifier{},
		Lookup:   func(string) (string, error) { return "pub", nil },
	}
	body, _ := json.Marshal(ClaimRequest{Challenge: fixed.Unix() - 120})
	rr := httptest.NewRecorder()
	deps.ClaimIdentity(rr, httptest.NewRequest(http.MethodPost, "/api/recovery/identity/claim", bytes.NewReader(body)))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
}

func TestClaimIdentity_FutureChallenge(t *testing.T) {
	fixed := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	deps := Deps{
		Now:      func() time.Time { return fixed },
		ServerID: "srv1",
		Crypto:   &fakeVerifier{},
	}
	body, _ := json.Marshal(ClaimRequest{Challenge: fixed.Unix() + 5})
	rr := httptest.NewRecorder()
	deps.ClaimIdentity(rr, httptest.NewRequest(http.MethodPost, "/api/recovery/identity/claim", bytes.NewReader(body)))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status=%d", rr.Code)
	}
}

func TestClaimIdentity_BadChallengeSignature(t *testing.T) {
	fixed := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	ts := fixed
	serverID := "srv1"
	profile := Profile{
		ID:              "user1",
		Username:        "alice",
		MemberSince:     ts,
		UserSignature:   testUserSig("AAA"),
		ServerSignature: testServerSig(serverID, ts),
	}
	root := KeyNode{
		KeyWire: KeyWire{
			Fingerprint: "AAA", Armor: "armor-a", UserID: "user1", CreatedAt: ts,
			ServerSignature: testServerSig(serverID, ts),
		},
	}
	challengeMsg := strconv.FormatInt(fixed.Unix(), 10)
	deps := Deps{
		Now:      func() time.Time { return fixed },
		ServerID: serverID,
		Crypto: &fakeVerifier{
			failSig: map[string]bool{challengeMsg: true},
		},
		Lookup: func(fp string) (string, error) {
			if fp == "SKEY" {
				return "server-pub", nil
			}
			return "", nil
		},
	}
	body, _ := json.Marshal(ClaimRequest{
		Challenge: fixed.Unix(),
		Signature: "Y2hhbGxlbmdl",
		Profile:   profile,
		Key:       root,
	})
	rr := httptest.NewRecorder()
	deps.ClaimIdentity(rr, httptest.NewRequest(http.MethodPost, "/api/recovery/identity/claim", bytes.NewReader(body)))
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
}

func TestReportPeerIdentity_Unauthenticated(t *testing.T) {
	deps := Deps{UserIDKey: testUserIDKey}
	rr := httptest.NewRecorder()
	deps.ReportPeerIdentity(rr, httptest.NewRequest(http.MethodPost, "/api/recovery/identity", bytes.NewReader([]byte(`{}`))))
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
}

func TestReportPeerIdentity_SelfSubmit(t *testing.T) {
	deps := Deps{UserIDKey: testUserIDKey}
	body, _ := json.Marshal(PeerIdentityRequest{
		Profile: Profile{ID: "caller1"},
	})
	req := httptest.NewRequest(http.MethodPost, "/api/recovery/identity", bytes.NewReader(body))
	req = req.WithContext(context.WithValue(req.Context(), testUserIDKey, "caller1"))
	rr := httptest.NewRecorder()
	deps.ReportPeerIdentity(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
}

func TestReportPeerIdentity_BrokenNest(t *testing.T) {
	serverID := "srv1"
	ts := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	profile := Profile{
		ID:              "peer1",
		Username:        "bob",
		MemberSince:     ts,
		UserSignature:   testUserSig("BBB"),
		ServerSignature: testServerSig(serverID, ts),
	}
	root := KeyNode{
		KeyWire: KeyWire{
			Fingerprint: "BBB", Armor: "armor-b", UserID: "peer1", CreatedAt: ts,
			ServerSignature: testServerSig(serverID, ts),
		},
		Predecessor: &KeyNode{
			Signature: "bad-pred-sig",
			KeyWire: KeyWire{
				Fingerprint: "AAA", Armor: "armor-a", UserID: "peer1", CreatedAt: ts,
				ServerSignature: testServerSig(serverID, ts),
			},
		},
	}
	deps := Deps{
		UserIDKey: testUserIDKey,
		ServerID:  serverID,
		Crypto:    &fakeVerifier{failChallenge: map[string]bool{"bad-pred-sig": true}},
		Lookup: func(fp string) (string, error) {
			if fp == "SKEY" {
				return "server-pub", nil
			}
			return "", nil
		},
	}
	body, _ := json.Marshal(PeerIdentityRequest{Profile: profile, Key: root})
	req := httptest.NewRequest(http.MethodPost, "/api/recovery/identity", bytes.NewReader(body))
	req = req.WithContext(context.WithValue(req.Context(), testUserIDKey, "caller1"))
	rr := httptest.NewRecorder()
	deps.ReportPeerIdentity(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
}

func TestRegisterRoutes(t *testing.T) {
	r := mux.NewRouter()
	api := r.PathPrefix("/api").Subrouter()
	RegisterRoutes(api, Deps{Now: time.Now, UserIDKey: testUserIDKey})

	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/recovery/identity/claim", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("claim GET status=%d", rr.Code)
	}

	rr = httptest.NewRecorder()
	r.ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/api/recovery/identity", bytes.NewReader([]byte(`{}`))))
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("peer POST status=%d body=%s", rr.Code, rr.Body.String())
	}
}
