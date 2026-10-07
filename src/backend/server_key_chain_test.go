//go:build !ops

package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// rotatedServerKeys mints a first key and rotates twice, the second time as
// compromised. It returns the three key IDs, oldest first.
func rotatedServerKeys(t *testing.T, ds *DataService) [3]string {
	t.Helper()
	ctx := context.Background()
	cryptoSvc := newCryptoService()
	first, err := ds.InitServerKey(ctx, cryptoSvc, serverKeyTestPassphrase)
	if err != nil {
		t.Fatalf("first key: %v", err)
	}
	rotated, err := revokeServerKey(ctx, ds.db, cryptoSvc, serverKeyTestPassphrase, false, "")
	if err != nil {
		t.Fatalf("rotate: %v", err)
	}
	compromised, err := revokeServerKey(ctx, ds.db, cryptoSvc, serverKeyTestPassphrase, true, "leaked")
	if err != nil {
		t.Fatalf("rotate compromised: %v", err)
	}
	return [3]string{string(canonicalID(ds.GetServerID(), first.Fingerprint)), rotated.Successor, compromised.Successor}
}

func TestGetServerKeyChain(t *testing.T) {
	_, ds, _ := newServerKeyTestDB(t)
	ctx := context.Background()
	cryptoSvc := newCryptoService()
	rotated, err := revokeServerKey(ctx, ds.db, cryptoSvc, serverKeyTestPassphrase, false, "")
	if err != nil {
		t.Fatal(err)
	}
	compromised, err := revokeServerKey(ctx, ds.db, cryptoSvc, serverKeyTestPassphrase, true, "leaked")
	if err != nil {
		t.Fatal(err)
	}
	first, middle, current := rotated.KeyID, rotated.Successor, compromised.Successor

	links, ok, err := ds.GetServerKeyChain(ctx, first, current)
	if err != nil || !ok || len(links) != 2 {
		t.Fatalf("chain from first: %d links, ok=%v, err=%v", len(links), ok, err)
	}
	if links[0].KeyID != first || links[0].Successor.ID != middle || links[0].Compromised {
		t.Fatalf("first link = %+v", links[0])
	}
	if links[1].KeyID != middle || links[1].Successor.ID != current || !links[1].Compromised || links[1].Reason != "leaked" {
		t.Fatalf("second link = %+v", links[1])
	}
	if links[1].Successor.Armor != publicArmorOf(t, ds.db, current) {
		t.Fatal("successor armor doesn't match the stored public key")
	}

	if links, ok, _ := ds.GetServerKeyChain(ctx, current, current); !ok || len(links) != 0 {
		t.Fatalf("chain from current: %d links, ok=%v", len(links), ok)
	}
	if _, ok, _ := ds.GetServerKeyChain(ctx, "unknown@Ab3xY9pQ", current); ok {
		t.Fatal("chain from an unknown key was found")
	}
	// Rotated in the DB but not restarted: the chain stops at the key in use.
	if links, ok, _ := ds.GetServerKeyChain(ctx, first, middle); !ok || len(links) != 1 {
		t.Fatalf("chain to the key in use: %d links, ok=%v", len(links), ok)
	}
	if _, ok, _ := ds.GetServerKeyChain(ctx, current, middle); ok {
		t.Fatal("chain from a key newer than the one in use was found")
	}
}

func TestGetServerKeyArmorAtRefusesCompromisedKeyAfterRevocation(t *testing.T) {
	_, ds, _ := newServerKeyTestDB(t)
	ctx := context.Background()
	cryptoSvc := newCryptoService()
	rotated, err := revokeServerKey(ctx, ds.db, cryptoSvc, serverKeyTestPassphrase, false, "")
	if err != nil {
		t.Fatal(err)
	}
	compromised, err := revokeServerKey(ctx, ds.db, cryptoSvc, serverKeyTestPassphrase, true, "leaked")
	if err != nil {
		t.Fatal(err)
	}
	fingerprintOf := func(keyID string) string {
		fp, _, ok := parseIdentityID(identityID(keyID))
		if !ok {
			t.Fatalf("malformed key ID %s", keyID)
		}
		return fp
	}
	before := compromised.SignedAt.Add(-time.Second)
	after := compromised.SignedAt.Add(time.Second)

	if armor, err := ds.GetServerKeyArmorAt(ctx, fingerprintOf(compromised.KeyID), before); err != nil || armor == "" {
		t.Fatalf("compromised key before its revocation: armor=%q err=%v", armor, err)
	}
	if _, err := ds.GetServerKeyArmorAt(ctx, fingerprintOf(compromised.KeyID), after); !errors.Is(err, errCompromisedServerKey) {
		t.Fatalf("compromised key after its revocation: got %v, want errCompromisedServerKey", err)
	}
	if _, err := ds.GetServerKeyArmorAt(ctx, fingerprintOf(compromised.KeyID), compromised.SignedAt); !errors.Is(err, errCompromisedServerKey) {
		t.Fatalf("compromised key at its revocation: got %v, want errCompromisedServerKey", err)
	}
	// A key revoked without compromise keeps verifying, whatever the time.
	if armor, err := ds.GetServerKeyArmorAt(ctx, fingerprintOf(rotated.KeyID), after); err != nil || armor == "" {
		t.Fatalf("rotated key after its revocation: armor=%q err=%v", armor, err)
	}
}

func TestGetPublicKeyReportsServerKeyRevocation(t *testing.T) {
	_, ds, _ := newServerKeyTestDB(t)
	ctx := context.Background()
	cryptoSvc := newCryptoService()
	rotated, err := revokeServerKey(ctx, ds.db, cryptoSvc, serverKeyTestPassphrase, false, "")
	if err != nil {
		t.Fatal(err)
	}
	compromised, err := revokeServerKey(ctx, ds.db, cryptoSvc, serverKeyTestPassphrase, true, "leaked")
	if err != nil {
		t.Fatal(err)
	}

	key, err := ds.GetPublicKey(ctx, rotated.KeyID)
	if err != nil || key == nil {
		t.Fatalf("GetPublicKey: %v", err)
	}
	if !key.Revoked || key.RevokedAt == nil || key.Compromised {
		t.Fatalf("rotated key: revoked=%v revokedAt=%v compromised=%v", key.Revoked, key.RevokedAt, key.Compromised)
	}
	key, err = ds.GetPublicKey(ctx, compromised.KeyID)
	if err != nil || key == nil {
		t.Fatalf("GetPublicKey: %v", err)
	}
	if !key.Revoked || key.RevokedAt == nil || !key.Compromised {
		t.Fatalf("compromised key: revoked=%v revokedAt=%v compromised=%v", key.Revoked, key.RevokedAt, key.Compromised)
	}
	key, err = ds.GetPublicKey(ctx, compromised.Successor)
	if err != nil || key == nil {
		t.Fatalf("GetPublicKey: %v", err)
	}
	if key.Revoked || key.RevokedAt != nil {
		t.Fatalf("current key reported revoked: %+v", key)
	}
}

// keyChainHandlers returns handlers signing with the newest of three server
// keys stored in the DB, plus the three key IDs.
func keyChainHandlers(t *testing.T) (*Handlers, [3]string) {
	t.Helper()
	h := newInviteModeHandlers(t, newTestDatabase(t, InitDB))
	keys := rotatedServerKeys(t, h.services.db)
	current, err := h.services.db.InitServerKey(context.Background(), newCryptoService(), serverKeyTestPassphrase)
	if err != nil {
		t.Fatal(err)
	}
	h.signingKey = *current
	return h, keys
}

func TestGetServerKeyChainHandler(t *testing.T) {
	h, keys := keyChainHandlers(t)
	kp := signedUpUser(t, h, "alice", "alice")
	serve := func(req *http.Request) *httptest.ResponseRecorder {
		rr := httptest.NewRecorder()
		h.signatureAuthMiddleware("/api")(http.HandlerFunc(h.GetServerKeyChain)).ServeHTTP(rr, req)
		return rr
	}

	req := signedRequest(t, h, http.MethodGet, "/api/server/key-chain?from="+keys[0],
		"alice@"+h.services.db.GetServerID(), kp.Fingerprint, kp.PrivateKey, nil)
	rr := serve(req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rr.Code, rr.Body.String())
	}
	var body struct {
		Revocations []serverKeyChainLink `json:"revocations"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Revocations) != 2 || body.Revocations[1].Successor.ID != keys[2] {
		t.Fatalf("revocations = %+v", body.Revocations)
	}

	req = signedRequest(t, h, http.MethodGet, "/api/server/key-chain?from=unknown@x",
		"alice@"+h.services.db.GetServerID(), kp.Fingerprint, kp.PrivateKey, nil)
	if rr := serve(req); rr.Code != http.StatusNotFound {
		t.Fatalf("unknown from: status = %d", rr.Code)
	}

	// The auth middleware answers missing signature headers with 400.
	unsigned := httptest.NewRequest(http.MethodGet, "/api/server/key-chain?from="+keys[0], nil)
	if rr := serve(unsigned); rr.Code != http.StatusBadRequest {
		t.Fatalf("unsigned request: status = %d", rr.Code)
	}
}

func TestServerKeyProofRefusesRevokedKey(t *testing.T) {
	h, keys := keyChainHandlers(t)
	reached := false
	proofed := h.serverKeyProofMiddleware("/api")(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached = true
		w.WriteHeader(http.StatusNoContent)
	}))

	for _, keyID := range keys[:2] {
		fp, _, _ := parseIdentityID(identityID(keyID))
		req := httptest.NewRequest(http.MethodPost, "/api/users/signup", nil)
		req.Header.Set(serverKeyProofHeader, fp)
		rr := httptest.NewRecorder()
		proofed.ServeHTTP(rr, req)
		if rr.Code != http.StatusUnauthorized || reached {
			t.Fatalf("signup proved with revoked key %s: status = %d", keyID, rr.Code)
		}
	}

	req := httptest.NewRequest(http.MethodPost, "/api/users/signup", nil)
	req.Header.Set(serverKeyProofHeader, h.signingKey.Fingerprint)
	rr := httptest.NewRecorder()
	proofed.ServeHTTP(rr, req)
	if !reached {
		t.Fatalf("signup proved with the current key was refused: status = %d", rr.Code)
	}
}
