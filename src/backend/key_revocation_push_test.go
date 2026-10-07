//go:build !ops

package main

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"github.com/gorilla/mux"
)

// keyRevocationFixture is alice, whose key aliceKey gets revoked, and the
// users around her: bob has aliceKey cached, carol has nothing cached.
type keyRevocationFixture struct {
	h                 *Handlers
	ds                *DataService
	db                *sql.DB
	alice, bob, carol string
	aliceKey          string
	bobKeyPair        cryptoKeyPair
}

func newKeyRevocationFixture(t *testing.T) keyRevocationFixture {
	t.Helper()
	db := newTestDatabase(t, InitDB)
	h := newInviteModeHandlers(t, db)
	ds := h.services.db
	srv := ds.GetServerID()

	f := keyRevocationFixture{h: h, ds: ds, db: db}
	in := signupInput("alice", "alice", nil)
	in.Fingerprint = string(appendEntity(canonicalID(srv, "alice"), "fp-alice"))
	if _, err := ds.Signup(context.Background(), in); err != nil {
		t.Fatalf("signup alice: %v", err)
	}
	f.alice, f.aliceKey = string(canonicalID(srv, "alice")), in.Fingerprint
	f.bobKeyPair = signedUpUser(t, h, "bob", "bob")
	f.bob = string(canonicalID(srv, "bob"))
	signedUpUser(t, h, "carol", "carol")
	f.carol = string(canonicalID(srv, "carol"))
	return f
}

// revoke records a revocation of key, as a rotation would.
func (f keyRevocationFixture) revoke(t *testing.T, key string) {
	t.Helper()
	var usID, ssID int64
	if err := f.db.QueryRow(`INSERT INTO user_signatures (public_key_id, signature) VALUES ($1, 'sig') RETURNING id`, key).Scan(&usID); err != nil {
		t.Fatal(err)
	}
	if err := f.db.QueryRow(`INSERT INTO server_signatures (private_key_id, signature, signed_at) VALUES ('x', 'sig', NOW()) RETURNING id`).Scan(&ssID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Exec(`
		INSERT INTO public_key_revocations (key_id, reason, user_signature_id, server_signature_id)
		VALUES ($1, 'rotated', $2, $3)
	`, key, usID, ssID); err != nil {
		t.Fatal(err)
	}
}

// getKeyAs fetches keyID through the real auth middleware, signed by bob.
func (f keyRevocationFixture) getKeyAs(t *testing.T, keyID string) int {
	t.Helper()
	req := signedRequest(t, f.h, http.MethodGet, "/api/keys/"+keyID, f.bob, f.bobKeyPair.Fingerprint, f.bobKeyPair.PrivateKey, nil)
	req = mux.SetURLVars(req, map[string]string{"id": keyID})
	rr := httptest.NewRecorder()
	f.h.signatureAuthMiddleware("/api")(http.HandlerFunc(f.h.GetKey)).ServeHTTP(rr, req)
	return rr.Code
}

func TestGetKeyAllocatesUserKeys(t *testing.T) {
	f := newKeyRevocationFixture(t)
	ctx := context.Background()

	if code := f.getKeyAs(t, f.aliceKey); code != http.StatusOK {
		t.Fatalf("GET alice's key: status %d", code)
	}
	if holders, _ := f.ds.PublicKeyHolders(ctx, f.aliceKey); !slices.Equal(holders, []string{f.bob}) {
		t.Fatalf("holders of alice's key = %v, want [%s]", holders, f.bob)
	}

	bobKey := string(appendEntity(identityID(f.bob), f.bobKeyPair.Fingerprint))
	if code := f.getKeyAs(t, bobKey); code != http.StatusOK {
		t.Fatalf("GET own key: status %d", code)
	}
	if holders, _ := f.ds.PublicKeyHolders(ctx, bobKey); len(holders) != 0 {
		t.Fatalf("own key allocated: %v", holders)
	}
}

func TestGetKeyDoesNotAllocateRevokedKey(t *testing.T) {
	f := newKeyRevocationFixture(t)
	f.revoke(t, f.aliceKey)

	if code := f.getKeyAs(t, f.aliceKey); code != http.StatusOK {
		t.Fatalf("GET revoked key: status %d", code)
	}
	if holders, _ := f.ds.PublicKeyHolders(context.Background(), f.aliceKey); len(holders) != 0 {
		t.Fatalf("revoked key allocated: %v", holders)
	}
}

func TestKeyRevocationOwedUntilAcknowledged(t *testing.T) {
	f := newKeyRevocationFixture(t)
	ctx := context.Background()
	if err := f.ds.AllocatePublicKey(ctx, f.bob, f.aliceKey); err != nil {
		t.Fatal(err)
	}

	if missing, _ := f.ds.GetMissingKeyRevocations(ctx, f.bob); len(missing) != 0 {
		t.Fatalf("owed before any revocation: %v", missing)
	}
	f.revoke(t, f.aliceKey)

	if missing, _ := f.ds.GetMissingKeyRevocations(ctx, f.bob); !slices.Equal(missing, []string{f.aliceKey}) {
		t.Fatalf("bob owed %v, want [%s]", missing, f.aliceKey)
	}
	if missing, _ := f.ds.GetMissingKeyRevocations(ctx, f.carol); len(missing) != 0 {
		t.Fatalf("carol, with nothing cached, owed %v", missing)
	}
	if holders, _ := f.ds.PublicKeyHolders(ctx, f.aliceKey); !slices.Equal(holders, []string{f.bob}) {
		t.Fatalf("live recipients = %v, want [%s]", holders, f.bob)
	}

	if err := f.ds.DeletePublicKeyAllocation(ctx, f.bob, f.aliceKey); err != nil {
		t.Fatal(err)
	}
	if missing, _ := f.ds.GetMissingKeyRevocations(ctx, f.bob); len(missing) != 0 {
		t.Fatalf("bob still owed %v after acknowledging", missing)
	}
}

func TestKeyRevokedEventAck(t *testing.T) {
	f := newKeyRevocationFixture(t)
	ctx := context.Background()

	if _, err := f.db.Exec(`INSERT INTO online_users (user_id) VALUES ($1) ON CONFLICT DO NOTHING`, f.bob); err != nil {
		t.Fatal(err)
	}
	if err := f.ds.CreatePendingKeyEvent(ctx, "ev-key", "req-1", f.bob, f.aliceKey); err != nil {
		t.Fatalf("CreatePendingKeyEvent: %v", err)
	}
	pe, err := f.ds.GetPendingSubject(ctx, "ev-key")
	if err != nil || pe == nil {
		t.Fatalf("GetPendingSubject: %+v, %v", pe, err)
	}
	if realtimeEventName(pe.EventName) != keyRevokedEvent || pe.KeyID != f.aliceKey {
		t.Fatalf("pending subject = %+v", pe)
	}
	if ackAcceptable(pe, f.bob) {
		t.Fatal("ack accepted before the revocation was sent")
	}
	if ok, err := f.ds.MarkEventDispatched(ctx, "ev-key", f.bob); err != nil || !ok {
		t.Fatalf("MarkEventDispatched: ok=%v err=%v", ok, err)
	}
	pe, _ = f.ds.GetPendingSubject(ctx, "ev-key")
	if !ackAcceptable(pe, f.bob) || ackAcceptable(pe, f.carol) {
		t.Fatal("only bob may ack once the revocation was sent")
	}
}
