//go:build !ops

package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

// peerChainFixture is a peer server ("Ab3xY9pQ") that rotated its key: its
// key IDs oldest first, the first key's armor, and its chain from there.
type peerChainFixture struct {
	keys       []string
	firstArmor string
	chain      []serverKeyChainLink
}

func newPeerChainFixture(t *testing.T, compromised bool) peerChainFixture {
	t.Helper()
	db, ds, _ := newServerKeyTestDB(t)
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
	chain, ok, err := ds.GetServerKeyChain(ctx, first.KeyID, second.Successor)
	if err != nil || !ok {
		t.Fatalf("chain: ok=%v err=%v", ok, err)
	}
	return peerChainFixture{
		keys:       []string{first.KeyID, first.Successor, second.Successor},
		firstArmor: publicArmorOf(t, db, first.KeyID),
		chain:      chain,
	}
}

func TestFollowKeyChain(t *testing.T) {
	cryptoSvc := newCryptoService()
	peer := newPeerChainFixture(t, false)

	adopted, err := followKeyChain(cryptoSvc, "Ab3xY9pQ", peer.keys[0], peer.firstArmor, peer.chain)
	if err != nil || len(adopted) != 2 || adopted[1].ID != peer.keys[2] {
		t.Fatalf("from the first key: adopted=%+v err=%v", adopted, err)
	}

	// Already on the newest key: nothing to adopt.
	tip := peer.chain[1].Successor
	if adopted, err := followKeyChain(cryptoSvc, "Ab3xY9pQ", tip.ID, tip.Armor, peer.chain); err != nil || len(adopted) != 0 {
		t.Fatalf("from the newest key: adopted=%+v err=%v", adopted, err)
	}

	if _, err := followKeyChain(cryptoSvc, "Ab3xY9pQ", "unknown@Ab3xY9pQ", peer.firstArmor, peer.chain); !errors.Is(err, errKeyChainBroken) {
		t.Fatalf("from an unknown key: got %v", err)
	}
	if _, err := followKeyChain(cryptoSvc, "Other123", peer.keys[0], peer.firstArmor, peer.chain); !errors.Is(err, errKeyChainBroken) {
		t.Fatalf("chain read as another server's: got %v", err)
	}

	tampered := append([]serverKeyChainLink(nil), peer.chain...)
	tampered[1].Reason = "edited"
	if _, err := followKeyChain(cryptoSvc, "Ab3xY9pQ", peer.keys[0], peer.firstArmor, tampered); !errors.Is(err, errKeyChainBroken) {
		t.Fatalf("tampered link: got %v", err)
	}

	compromised := newPeerChainFixture(t, true)
	if _, err := followKeyChain(cryptoSvc, "Ab3xY9pQ", compromised.keys[0], compromised.firstArmor, compromised.chain); !errors.Is(err, errKeyChainCompromised) {
		t.Fatalf("compromised link: got %v", err)
	}
}

// pinnedPeerHandlers returns handlers for a server that has the fixture's
// peer pinned at its first key, reaching it at a fake peer that serves the
// chain and counts how often it is asked.
func pinnedPeerHandlers(t *testing.T, peer peerChainFixture) (*Handlers, *int32) {
	t.Helper()
	db := newTestDatabase(t, InitDB)
	h := newInviteModeHandlers(t, db)

	var fetches int32
	fake := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&fetches, 1)
		var req relayServerKeyChainRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.From != peer.keys[0] {
			http.Error(w, "bad from", http.StatusBadRequest)
			return
		}
		_ = json.NewEncoder(w).Encode(relayServerKeyChainResponse{Revocations: peer.chain})
	}))
	t.Cleanup(fake.Close)
	h.federationHTTPClientOverride = fake.Client()

	var sigID int64
	if err := db.QueryRow(`INSERT INTO server_signatures (private_key_id, signature, signed_at) VALUES ('x', 'sig', NOW()) RETURNING id`).Scan(&sigID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO public_keys (id, armor, server_signature_id) VALUES ($1, $2, $3)`,
		peer.keys[0], peer.firstArmor, sigID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		INSERT INTO servers (id, name, self, base_url, connected, key_id)
		VALUES ('Ab3xY9pQ', 'peer.example', FALSE, $1, TRUE, $2)
	`, fake.URL, peer.keys[0]); err != nil {
		t.Fatal(err)
	}
	return h, &fetches
}

func fingerprintOfKey(t *testing.T, keyID string) string {
	t.Helper()
	fp, _, ok := parseIdentityID(identityID(keyID))
	if !ok {
		t.Fatalf("malformed key ID %s", keyID)
	}
	return fp
}

func TestRepinPeerFollowsRotation(t *testing.T) {
	peer := newPeerChainFixture(t, false)
	h, fetches := pinnedPeerHandlers(t, peer)
	ctx := context.Background()
	newest := fingerprintOfKey(t, peer.keys[2])

	if !h.repinPeer(ctx, "Ab3xY9pQ", newest) {
		t.Fatal("repinPeer did not move the pin")
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

	// The pin already matches: no fetch.
	if h.repinPeer(ctx, "Ab3xY9pQ", newest) || atomic.LoadInt32(fetches) != 1 {
		t.Fatalf("second repin fetched again (%d fetches)", atomic.LoadInt32(fetches))
	}
}

func TestRepinPeerClearsPinOnCompromise(t *testing.T) {
	peer := newPeerChainFixture(t, true)
	h, _ := pinnedPeerHandlers(t, peer)
	ctx := context.Background()

	if h.repinPeer(ctx, "Ab3xY9pQ", fingerprintOfKey(t, peer.keys[2])) {
		t.Fatal("repinPeer followed a compromised chain")
	}
	if pin, _ := h.services.db.GetPeerPin(ctx, "Ab3xY9pQ"); pin != nil {
		t.Fatalf("pin kept after a compromise: %+v", pin)
	}
	if ok, _, _ := h.services.db.VerifyFederationPeer(ctx, "Ab3xY9pQ", fingerprintOfKey(t, peer.keys[0])); ok {
		t.Fatal("compromised key still accepted")
	}
}

func TestRepinPeerThrottlesUnknownKey(t *testing.T) {
	peer := newPeerChainFixture(t, false)
	h, fetches := pinnedPeerHandlers(t, peer)
	ctx := context.Background()

	// A key the chain never reaches: one fetch, then none within the window.
	for i := 0; i < 3; i++ {
		h.repinPeer(ctx, "Ab3xY9pQ", "deadbeef")
	}
	if got := atomic.LoadInt32(fetches); got != 1 {
		t.Fatalf("fetched the chain %d times, want 1", got)
	}
}
