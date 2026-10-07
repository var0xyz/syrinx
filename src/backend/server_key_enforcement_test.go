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

	"github.com/gorilla/mux"
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

func TestGetServerKeyRevocation(t *testing.T) {
	_, ds, _ := newServerKeyTestDB(t)
	ctx := context.Background()
	rotated, err := revokeServerKey(ctx, ds.db, newCryptoService(), serverKeyTestPassphrase, true, "leaked")
	if err != nil {
		t.Fatal(err)
	}

	wire, err := ds.GetServerKeyRevocation(ctx, rotated.KeyID)
	if err != nil || wire == nil {
		t.Fatalf("revocation of the revoked key: %+v, err=%v", wire, err)
	}
	if wire.Type != identityTypeServerKeyRevocation || wire.ServerID != "Ab3xY9pQ" ||
		wire.Successor != rotated.Successor || !wire.Compromised || wire.Reason != "leaked" ||
		wire.Signature != rotated.Signature || wire.SuccessorSignature != rotated.SuccessorSignature {
		t.Fatalf("revocation wire = %+v", wire)
	}
	if wire, _ := ds.GetServerKeyRevocation(ctx, rotated.Successor); wire != nil {
		t.Fatalf("current key reported revoked: %+v", wire)
	}
	if wire, _ := ds.GetServerKeyRevocation(ctx, "unknown@Ab3xY9pQ"); wire != nil {
		t.Fatalf("unknown key reported revoked: %+v", wire)
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

// rotatedKeyHandlers returns handlers signing with the newest of three server
// keys stored in the DB, plus the three key IDs.
func rotatedKeyHandlers(t *testing.T) (*Handlers, [3]string) {
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

func TestGetKeyRevocationServesServerKeys(t *testing.T) {
	h, keys := rotatedKeyHandlers(t)
	kp := signedUpUser(t, h, "alice", "alice")
	serve := func(keyID string) *httptest.ResponseRecorder {
		req := signedRequest(t, h, http.MethodGet, "/api/keys/"+keyID+"/revocation",
			"alice@"+h.services.db.GetServerID(), kp.Fingerprint, kp.PrivateKey, nil)
		req = mux.SetURLVars(req, map[string]string{"id": keyID})
		rr := httptest.NewRecorder()
		h.signatureAuthMiddleware("/api")(http.HandlerFunc(h.GetKeyRevocation)).ServeHTTP(rr, req)
		return rr
	}

	rr := serve(keys[1])
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rr.Code, rr.Body.String())
	}
	var wire serverKeyRevocationWire
	if err := json.Unmarshal(rr.Body.Bytes(), &wire); err != nil {
		t.Fatal(err)
	}
	if wire.Type != identityTypeServerKeyRevocation || wire.KeyID != keys[1] || wire.Successor != keys[2] || !wire.Compromised {
		t.Fatalf("revocation = %+v", wire)
	}
	if rr := serve(keys[2]); rr.Code != http.StatusNotFound {
		t.Fatalf("current key: status = %d", rr.Code)
	}
}

func TestServerKeyProofRefusesRevokedKey(t *testing.T) {
	h, keys := rotatedKeyHandlers(t)
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
