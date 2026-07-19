package recovery

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/gorilla/mux"
)

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
		ID:                   "user1",
		Username:             "alice",
		MemberSince:          ts,
		SignatureFingerprint: "AAA",
		Signature:            "dXNlcg==",
		Server: ServerSignature{
			ID: serverID, Fingerprint: "SKEY", Timestamp: ts, Signature: "c2VydmVy",
		},
	}
	root := KeyNode{
		KeyWire: KeyWire{
			Fingerprint: "AAA", Armor: "armor-a", UserID: "user1", CreatedAt: ts,
			Server: ServerSignature{ID: serverID, Fingerprint: "SKEY", Timestamp: ts, Signature: "c2VydmVy"},
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

func TestRegisterRoutes(t *testing.T) {
	r := mux.NewRouter()
	api := r.PathPrefix("/api").Subrouter()
	RegisterRoutes(api, Deps{Now: time.Now})
	req := httptest.NewRequest(http.MethodGet, "/api/recovery/identity/claim", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d", rr.Code)
	}
}
