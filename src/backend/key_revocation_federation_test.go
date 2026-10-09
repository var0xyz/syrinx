//go:build !ops

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/mux"
)

const revHomeServerID = "Hm4kQ2zX"

// signedKeyRevocation is a user key on revHomeServerID, its home server's
// signing key, and the key's revocation, all with real signatures.
type signedKeyRevocation struct {
	serverKP, userKP *cryptoKeyPair
	userID, keyID    string
	serverKeyID      string
	keyCountersig    ServerSignature
	rev              KeyRevocation
}

func newSignedKeyRevocation(t *testing.T) signedKeyRevocation {
	t.Helper()
	cryptoSvc := newCryptoService()
	serverKP, err := cryptoSvc.createKeyPair(revHomeServerID, "", "")
	if err != nil {
		t.Fatal(err)
	}
	userKP, err := cryptoSvc.createKeyPair("alice", "", "")
	if err != nil {
		t.Fatal(err)
	}
	userID := string(canonicalID(revHomeServerID, "alice"))
	keyID := string(appendEntity(identityID(userID), userKP.Fingerprint))
	serverKeyID := string(canonicalID(revHomeServerID, serverKP.Fingerprint))
	signedAt := time.Now().UTC().Truncate(time.Second)

	sign := func(payload []byte, armor string) string {
		sig, err := cryptoSvc.sign(string(payload), armor)
		if err != nil {
			t.Fatal(err)
		}
		return sig
	}
	keyCountersig := ServerSignature{
		ID:       serverKeyID,
		Armor:    sign(buildPublicKeyPayload(revHomeServerID, userID, keyID, serverKP.Fingerprint, userKP.PublicKey, signedAt), serverKP.PrivateKey),
		SignedAt: signedAt,
	}
	userSig := sign(buildUserRevocationPayload(userID, keyID, "rotated"), userKP.PrivateKey)
	rev := KeyRevocation{
		ID:            keyID,
		UserID:        userID,
		Reason:        "rotated",
		UserSignature: UserSignature{ID: keyID, Armor: userSig},
		ServerSignature: ServerSignature{
			ID:       serverKeyID,
			Armor:    sign(buildServerRevocationPayload(userID, keyID, "rotated", revHomeServerID, serverKP.Fingerprint, userSig, signedAt), serverKP.PrivateKey),
			SignedAt: signedAt,
		},
	}
	return signedKeyRevocation{serverKP, userKP, userID, keyID, serverKeyID, keyCountersig, rev}
}

// fakeHome serves the home server's signing key and the user key, as GET
// /api/keys/{id} does, and counts the requests it gets.
func (s signedKeyRevocation) fakeHome(t *testing.T) (*httptest.Server, *int32) {
	t.Helper()
	var requests int32
	fake := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&requests, 1)
		switch strings.TrimPrefix(r.URL.Path, "/api/keys/") {
		case s.serverKeyID:
			writeResponse(w, http.StatusOK, pbKey(&Key{ID: s.serverKeyID, Armor: s.serverKP.PublicKey}))
		case s.keyID:
			writeResponse(w, http.StatusOK, pbKey(&Key{ID: s.keyID, UserID: s.userID, Armor: s.userKP.PublicKey, ServerSignature: s.keyCountersig}))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(fake.Close)
	return fake, &requests
}

// peerHandlers returns a server that has revHomeServerID pinned and reaches
// it at baseURL, plus a local user bob.
func (s signedKeyRevocation) peerHandlers(t *testing.T, fake *httptest.Server) (*Handlers, string, cryptoKeyPair) {
	t.Helper()
	db := newTestDatabase(t, InitDB)
	h := newInviteModeHandlers(t, db)
	h.federationHTTPClientOverride = fake.Client()

	var sigID int64
	if err := db.QueryRow(`INSERT INTO server_signatures (private_key_id, signature, signed_at) VALUES ('x', 'sig', NOW()) RETURNING id`).Scan(&sigID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO public_keys (id, armor, server_signature_id) VALUES ($1, $2, $3)`, s.serverKeyID, s.serverKP.PublicKey, sigID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		INSERT INTO servers (id, name, self, base_url, frontend_url, connected, key_id)
		VALUES ($1, 'home.example', FALSE, $2, $2, TRUE, $3)
	`, revHomeServerID, fake.URL, s.serverKeyID); err != nil {
		t.Fatal(err)
	}
	bobKP := signedUpUser(t, h, "bob", "bob")
	return h, string(canonicalID(h.services.db.GetServerID(), "bob")), bobKP
}

// cacheAndAllocate gives user the key cached, as fetching it through the
// peer would.
func (s signedKeyRevocation) cacheAndAllocate(t *testing.T, h *Handlers, user string) {
	t.Helper()
	if key, err := h.resolvePublicKey(context.Background(), s.keyID); err != nil || key == nil {
		t.Fatalf("cache the user key: %v", err)
	}
	if err := h.services.db.AllocatePublicKey(context.Background(), user, s.keyID); err != nil {
		t.Fatal(err)
	}
}

func postKeyRevocation(t *testing.T, h *Handlers, rev KeyRevocation) int {
	t.Helper()
	body, err := json.Marshal(rev)
	if err != nil {
		t.Fatal(err)
	}
	req := withPeer(httptest.NewRequest(http.MethodPost, "/api/federation/relay/key-revocation", bytes.NewReader(body)), revHomeServerID)
	rr := httptest.NewRecorder()
	h.KeyRevocationFromPeer(rr, req)
	return rr.Code
}

func TestKeyRevocationFromPeerMarksHolders(t *testing.T) {
	s := newSignedKeyRevocation(t)
	fake, _ := s.fakeHome(t)
	h, bob, _ := s.peerHandlers(t, fake)
	ctx := context.Background()
	s.cacheAndAllocate(t, h, bob)

	if code := postKeyRevocation(t, h, s.rev); code != http.StatusNoContent {
		t.Fatalf("status = %d", code)
	}
	if missing, _ := h.services.db.GetMissingKeyRevocations(ctx, bob); !slices.Equal(missing, []string{s.keyID}) {
		t.Fatalf("bob owed %v, want [%s]", missing, s.keyID)
	}
}

func TestKeyRevocationFromPeerRefusesBadRevocations(t *testing.T) {
	s := newSignedKeyRevocation(t)
	fake, _ := s.fakeHome(t)
	h, bob, _ := s.peerHandlers(t, fake)
	ctx := context.Background()
	s.cacheAndAllocate(t, h, bob)

	tampered := s.rev
	tampered.Reason = "edited"
	notOurs := s.rev
	notOurs.ID = "carol@Other999/" + s.userKP.Fingerprint
	notOurs.UserID = "carol@Other999"
	forgedCountersig := s.rev
	forgedCountersig.ServerSignature.Armor = s.rev.UserSignature.Armor

	for name, rev := range map[string]KeyRevocation{
		"tampered reason":         tampered,
		"key of another server":   notOurs,
		"forged countersignature": forgedCountersig,
	} {
		if code := postKeyRevocation(t, h, rev); code != http.StatusBadRequest {
			t.Errorf("%s: status = %d, want 400", name, code)
		}
	}
	if missing, _ := h.services.db.GetMissingKeyRevocations(ctx, bob); len(missing) != 0 {
		t.Fatalf("a refused revocation marked bob's key: %v", missing)
	}
}

func TestGetKeyThroughPeerAllocatesToLocalUser(t *testing.T) {
	s := newSignedKeyRevocation(t)
	fake, _ := s.fakeHome(t)
	h, bob, bobKP := s.peerHandlers(t, fake)

	req := signedRequest(t, h, http.MethodGet, "/api/keys/"+s.keyID, bob, bobKP.Fingerprint, bobKP.PrivateKey, nil)
	req = mux.SetURLVars(req, map[string]string{"id": s.keyID})
	rr := httptest.NewRecorder()
	h.signatureAuthMiddleware("/api")(http.HandlerFunc(h.GetKey)).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rr.Code, rr.Body.String())
	}
	if holders, _ := h.services.db.PublicKeyHolders(context.Background(), s.keyID); !slices.Equal(holders, []string{bob}) {
		t.Fatalf("holders = %v, want [%s]", holders, bob)
	}
}

// homeWithPeer returns a home server with a local user alice, and a peer
// reached at a fake that answers revocation notices with status.
func homeWithPeer(t *testing.T, status int) (*Handlers, string, *int32) {
	t.Helper()
	var notices int32
	fake := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/federation/relay/key-revocation" {
			atomic.AddInt32(&notices, 1)
		}
		w.WriteHeader(status)
	}))
	t.Cleanup(fake.Close)

	f := newKeyRevocationFixture(t)
	f.h.federationHTTPClientOverride = fake.Client()
	if _, err := f.db.Exec(`
		INSERT INTO servers (id, name, self, base_url, frontend_url, connected) VALUES ('peer5678', 'peer.example', FALSE, $1, $1, TRUE)
	`, fake.URL); err != nil {
		t.Fatal(err)
	}

	req := withPeer(httptest.NewRequest(http.MethodGet, "/api/keys/"+f.aliceKey, nil), "peer5678")
	req = mux.SetURLVars(req, map[string]string{"id": f.aliceKey})
	rr := httptest.NewRecorder()
	f.h.GetKey(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("peer GET key: status %d", rr.Code)
	}
	if holders, _ := f.ds.PublicKeyServerHolders(context.Background(), f.aliceKey); !slices.Equal(holders, []string{"peer5678"}) {
		t.Fatalf("peer holders = %v", holders)
	}
	f.revoke(t, f.aliceKey)
	return f.h, f.aliceKey, &notices
}

func TestNotifyPeersOfKeyRevocation(t *testing.T) {
	h, key, notices := homeWithPeer(t, http.StatusNoContent)
	h.notifyPeersOfKeyRevocation(key)
	if atomic.LoadInt32(notices) != 1 {
		t.Fatalf("sent %d notices, want 1", atomic.LoadInt32(notices))
	}
	if holders, _ := h.services.db.PublicKeyServerHolders(context.Background(), key); len(holders) != 0 {
		t.Fatalf("peer allocation kept after it accepted: %v", holders)
	}
}

func TestNotifyPeersOfKeyRevocationRetriesUntilAccepted(t *testing.T) {
	h, key, notices := homeWithPeer(t, http.StatusInternalServerError)
	h.notifyPeersOfKeyRevocation(key)
	if atomic.LoadInt32(notices) != 1 {
		t.Fatalf("sent %d notices, want 1", atomic.LoadInt32(notices))
	}
	if owed, _ := h.services.db.RevokedKeysOwedToServer(context.Background(), "peer5678"); !slices.Equal(owed, []string{key}) {
		t.Fatalf("owed to peer = %v, want [%s]", owed, key)
	}
}
