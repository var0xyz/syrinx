//go:build !ops && !ripplescleanup && !challengescleanup

package main

import (
	"context"
	"errors"
	"testing"
	"time"
)

// Purging a revoked peer deletes what it left here but keeps its record and
// revocation, and lets it connect again with the same key.
func TestPurgeKeepsTheRevokedRecord(t *testing.T) {
	db, rs, admin := newTeardownTestService(t)
	ds := rs.db
	ctx := context.Background()
	const fp = "aaaabbbbccccddddeeeeffff0000111122223333"
	keyID := string(canonicalID(teardownPeerID, fp))

	var sigID int64
	if err := db.QueryRow(`INSERT INTO server_signatures (private_key_id, signature, signed_at) VALUES ('x', 'sig', NOW()) RETURNING id`).Scan(&sigID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO public_keys (id, armor, server_signature_id) VALUES ($1, 'armor', $2)`, keyID, sigID); err != nil {
		t.Fatal(err)
	}
	pinTestPeerKey(t, db, teardownPeerID, keyID)
	insertTeardownIdentity(t, db, string(canonicalID(teardownPeerID, "bob")), teardownPeerID)
	peer := federationPeer{ServerID: teardownPeerID, ServerName: teardownPeerID, BaseURL: "https://peer.example", FrontendURL: "https://peer.example", Fingerprint: fp, PublicKeyArmor: "armor"}

	if err := ds.PurgeFederationServer(ctx, teardownPeerID); !errors.Is(err, errFederationServerNotRevoked) {
		t.Fatalf("purge of a live peer = %v, want not revoked", err)
	}
	if err := ds.RevokeFederationServer(ctx, teardownPeerID, nil, "gone", time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := ds.CreateFederationAttempt(ctx, peer, time.Now()); !errors.Is(err, errFederationServerAlreadyKnown) {
		t.Fatalf("handshake with a revoked, unpurged peer = %v, want already known", err)
	}

	if err := ds.PurgeFederationServer(ctx, teardownPeerID); err != nil {
		t.Fatal(err)
	}
	var identities int
	if err := db.QueryRow(`SELECT COUNT(*) FROM identities WHERE server_id = $1`, teardownPeerID).Scan(&identities); err != nil || identities != 0 {
		t.Fatalf("peer identities after purge = %d (%v)", identities, err)
	}
	servers, err := ds.ListFederationServers(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(servers) != 1 || !servers[0].Revoked || !servers[0].Purged || servers[0].RevokedReason != "gone" {
		t.Fatalf("peers after purge = %+v, want the revoked record, purged", servers)
	}

	attemptID, err := ds.CreateFederationAttempt(ctx, peer, time.Now())
	if err != nil {
		t.Fatalf("handshake with a purged peer: %v", err)
	}
	if _, err := ds.CreateFederationAttempt(ctx, peer, time.Now()); !errors.Is(err, errFederationServerAlreadyKnown) {
		t.Fatalf("second handshake while one is pending = %v, want already known", err)
	}
	if err := ds.RejectFederationAttempt(ctx, attemptID, admin, "not yet", time.Now(), nil); err != nil {
		t.Fatal(err)
	}
	if _, err := ds.CreateFederationAttempt(ctx, peer, time.Now()); err != nil {
		t.Fatalf("handshake after a rejected attempt: %v", err)
	}
}
