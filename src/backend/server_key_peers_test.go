//go:build !ops

package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// rotatedPeer is a peer server ("Ab3xY9pQ") that replaced its key twice: its
// database and key IDs, oldest first.
type rotatedPeer struct {
	db   *sql.DB
	keys []string
}

func newRotatedPeer(t *testing.T, compromised bool) rotatedPeer {
	t.Helper()
	db, _, _ := newServerKeyTestDB(t)
	ctx := context.Background()
	cryptoSvc := newCryptoService()
	first, err := revokeServerKey(ctx, db, cryptoSvc, serverKeyTestPassphrase, false, "")
	if err != nil {
		t.Fatal(err)
	}
	second, err := revokeServerKey(ctx, db, cryptoSvc, serverKeyTestPassphrase, compromised, "leaked")
	if err != nil {
		t.Fatal(err)
	}
	return rotatedPeer{db: db, keys: []string{first.KeyID, first.Successor, second.Successor}}
}

// fetcher reads the peer's revocations and keys straight from its database.
func (p rotatedPeer) fetcher(t *testing.T) serverKeySource {
	return serverKeySource{
		revocation: func(ctx context.Context, keyID string) (*serverKeyRevocation, error) {
			return loadServerKeyRevocation(ctx, p.db, keyID)
		},
		armor: func(ctx context.Context, keyID string) (string, error) {
			return publicArmorOf(t, p.db, keyID), nil
		},
	}
}

func TestWalkRevocationChain(t *testing.T) {
	ctx := context.Background()
	cryptoSvc := newCryptoService()
	peer := newRotatedPeer(t, false)
	firstArmor := publicArmorOf(t, peer.db, peer.keys[0])

	adopted, err := walkRevocationChain(ctx, cryptoSvc, "Ab3xY9pQ", peer.keys[0], firstArmor, peer.fetcher(t))
	if err != nil || len(adopted) != 2 || adopted[1].ID != peer.keys[2] {
		t.Fatalf("from the first key: adopted=%+v err=%v", adopted, err)
	}

	// Already on the current key: nothing to adopt.
	current := peer.keys[2]
	if adopted, err := walkRevocationChain(ctx, cryptoSvc, "Ab3xY9pQ", current, publicArmorOf(t, peer.db, current), peer.fetcher(t)); err != nil || len(adopted) != 0 {
		t.Fatalf("from the current key: adopted=%+v err=%v", adopted, err)
	}

	if _, err := walkRevocationChain(ctx, cryptoSvc, "Other123", peer.keys[0], firstArmor, peer.fetcher(t)); !errors.Is(err, errKeyHandoverBroken) {
		t.Fatalf("revocations read as another server's: got %v", err)
	}

	tampered := peer.fetcher(t)
	honest := tampered.revocation
	tampered.revocation = func(ctx context.Context, keyID string) (*serverKeyRevocation, error) {
		rev, err := honest(ctx, keyID)
		if rev != nil && keyID == peer.keys[1] {
			rev.Reason = "edited"
		}
		return rev, err
	}
	if _, err := walkRevocationChain(ctx, cryptoSvc, "Ab3xY9pQ", peer.keys[0], firstArmor, tampered); !errors.Is(err, errKeyHandoverBroken) {
		t.Fatalf("tampered revocation: got %v", err)
	}

	// A successor key served under the wrong ID.
	swapped := peer.fetcher(t)
	swapped.armor = func(ctx context.Context, keyID string) (string, error) {
		return firstArmor, nil
	}
	if _, err := walkRevocationChain(ctx, cryptoSvc, "Ab3xY9pQ", peer.keys[0], firstArmor, swapped); !errors.Is(err, errKeyHandoverBroken) {
		t.Fatalf("swapped successor key: got %v", err)
	}

	compromised := newRotatedPeer(t, true)
	if _, err := walkRevocationChain(ctx, cryptoSvc, "Ab3xY9pQ", compromised.keys[0], publicArmorOf(t, compromised.db, compromised.keys[0]), compromised.fetcher(t)); !errors.Is(err, errKeyHandoverCompromised) {
		t.Fatalf("compromised revocation: got %v", err)
	}
}

// pinnedPeerHandlers returns handlers for a server that has the peer pinned at
// its first key, reaching it at a fake peer that serves /api/keys/{id} and
// /api/keys/{id}/revocation from the peer's database and counts the requests.
func pinnedPeerHandlers(t *testing.T, peer rotatedPeer) (*Handlers, *int32) {
	t.Helper()
	db := newTestDatabase(t, InitDB)
	h := newInviteModeHandlers(t, db)
	peerDS := NewDataService(peer.db, "test")
	peerDS.setServerIDForTest("Ab3xY9pQ")

	var requests int32
	fake := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&requests, 1)
		rest := strings.TrimPrefix(r.URL.Path, "/api/keys/")
		if keyID, ok := strings.CutSuffix(rest, "/revocation"); ok {
			wire, err := peerDS.GetServerKeyRevocation(r.Context(), keyID)
			if err != nil || wire == nil {
				http.NotFound(w, r)
				return
			}
			_ = json.NewEncoder(w).Encode(wire)
			return
		}
		key, err := peerDS.GetPublicKey(r.Context(), rest)
		if err != nil || key == nil {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(key)
	}))
	t.Cleanup(fake.Close)
	h.federationHTTPClientOverride = fake.Client()

	var sigID int64
	if err := db.QueryRow(`INSERT INTO server_signatures (private_key_id, signature, signed_at) VALUES ('x', 'sig', NOW()) RETURNING id`).Scan(&sigID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO public_keys (id, armor, server_signature_id) VALUES ($1, $2, $3)`,
		peer.keys[0], publicArmorOf(t, peer.db, peer.keys[0]), sigID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		INSERT INTO servers (id, name, self, base_url, frontend_url, connected, key_id)
		VALUES ('Ab3xY9pQ', 'peer.example', FALSE, $1, $1, TRUE, $2)
	`, fake.URL, peer.keys[0]); err != nil {
		t.Fatal(err)
	}
	return h, &requests
}

func fingerprintOfKey(t *testing.T, keyID string) string {
	t.Helper()
	fp, _, ok := parseIdentityID(identityID(keyID))
	if !ok {
		t.Fatalf("malformed key ID %s", keyID)
	}
	return fp
}

func TestUpdatePeerKeyAfterRotation(t *testing.T) {
	peer := newRotatedPeer(t, false)
	h, requests := pinnedPeerHandlers(t, peer)
	ctx := context.Background()
	newest := fingerprintOfKey(t, peer.keys[2])

	if !h.updatePeerKey(ctx, "Ab3xY9pQ", newest) {
		t.Fatal("updatePeerKey did not move the pin")
	}
	pin, err := h.services.db.GetPeerPin(ctx, "Ab3xY9pQ")
	if err != nil || pin == nil || pin.KeyID != peer.keys[2] {
		t.Fatalf("pin after rotation = %+v, err=%v", pin, err)
	}
	if ok, _, err := h.services.db.VerifyFederationPeer(ctx, "Ab3xY9pQ", newest); err != nil || !ok {
		t.Fatalf("new key not accepted: ok=%v err=%v", ok, err)
	}
	if ok, _, _ := h.services.db.VerifyFederationPeer(ctx, "Ab3xY9pQ", fingerprintOfKey(t, peer.keys[0])); ok {
		t.Fatal("old key still accepted after the re-pin")
	}

	// The pin already matches: nothing is fetched.
	before := atomic.LoadInt32(requests)
	if h.updatePeerKey(ctx, "Ab3xY9pQ", newest) || atomic.LoadInt32(requests) != before {
		t.Fatal("a second repin fetched again")
	}
}

func TestUpdatePeerKeyClearsPinOnCompromise(t *testing.T) {
	peer := newRotatedPeer(t, true)
	h, _ := pinnedPeerHandlers(t, peer)
	ctx := context.Background()

	if h.updatePeerKey(ctx, "Ab3xY9pQ", fingerprintOfKey(t, peer.keys[2])) {
		t.Fatal("updatePeerKey moved past a compromised revocation")
	}
	if pin, _ := h.services.db.GetPeerPin(ctx, "Ab3xY9pQ"); pin != nil {
		t.Fatalf("pin kept after a compromise: %+v", pin)
	}
	if ok, _, _ := h.services.db.VerifyFederationPeer(ctx, "Ab3xY9pQ", fingerprintOfKey(t, peer.keys[0])); ok {
		t.Fatal("compromised key still accepted")
	}
}

func TestUpdatePeerKeyThrottlesUnknownKey(t *testing.T) {
	peer := newRotatedPeer(t, false)
	h, requests := pinnedPeerHandlers(t, peer)
	ctx := context.Background()

	h.updatePeerKey(ctx, "Ab3xY9pQ", "deadbeef")
	first := atomic.LoadInt32(requests)
	for i := 0; i < 3; i++ {
		h.updatePeerKey(ctx, "Ab3xY9pQ", "deadbeef")
	}
	if first == 0 || atomic.LoadInt32(requests) != first {
		t.Fatalf("fetched %d times after the first round of %d", atomic.LoadInt32(requests)-first, first)
	}
}
