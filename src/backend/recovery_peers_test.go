//go:build !ops && !ripplescleanup && !challengescleanup

package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	pb "syrinx/proto"
)

// peeredWith makes b a connected peer of a, as approving a handshake would,
// and returns the record a's /server/info countersigns for it.
func peeredWith(t *testing.T, a, b *e2eServer) *pb.FederatedServerInfo {
	t.Helper()
	ctx := context.Background()
	bID := b.h.services.db.GetServerID()
	bKeyID := string(canonicalID(bID, b.h.signingKey.Fingerprint))
	bKey, err := b.h.services.db.GetPublicKey(ctx, bKeyID)
	if err != nil || bKey == nil {
		t.Fatalf("b's key: %v", err)
	}
	var sigID int64
	if err := a.db.QueryRow(`INSERT INTO server_signatures (private_key_id, signature, signed_at) VALUES ('x', 'sig', NOW()) RETURNING id`).Scan(&sigID); err != nil {
		t.Fatal(err)
	}
	if _, err := a.db.Exec(`INSERT INTO public_keys (id, armor, server_signature_id) VALUES ($1, $2, $3)`, bKeyID, bKey.Armor, sigID); err != nil {
		t.Fatal(err)
	}
	if _, err := a.db.Exec(`INSERT INTO servers (id, name, key_id, base_url, frontend_url) VALUES ($1, 'Bravo', $2, 'https://b.example', 'https://b.example')`, bID, bKeyID); err != nil {
		t.Fatal(err)
	}
	peerTestMesh(t, a.db, bID)

	rr := httptest.NewRecorder()
	a.h.GetServerInfo(rr, httptest.NewRequest(http.MethodGet, "/api/server/info", nil))
	var info pb.ServerInfo
	decodeProto(t, rr.Body.Bytes(), &info)
	if len(info.GetFederation()) != 1 || info.GetFederation()[0].GetKeyArmor() == "" {
		t.Fatalf("a's peer records = %+v", info.GetFederation())
	}
	return info.GetFederation()[0]
}

// A rebuilt server gets its peers back from devices, then what devices hold
// of those peers' users, reeds, follows and blocks, checked against the
// peers' keys the records name.
func TestRecoveryRestoresPeersAndTheirContent(t *testing.T) {
	ctx := context.Background()
	a := newE2EServer(t, newTestDatabase(t, InitDB), false)
	b := newE2EServer(t, newTestDatabase(t, InitDB), false)
	record := peeredWith(t, a, b)
	bID := b.h.services.db.GetServerID()

	alice := a.signup(t, "alice")
	aliceSnap := a.snapshot(t, alice.id)
	bob := b.signup(t, "bob")
	bobSnap := b.snapshot(t, bob.id)
	reed := b.post(t, bob, "", "")
	peeredWith(t, b, a)
	if err := b.h.services.db.UpsertRemoteIdentity(ctx, alice.id, a.h.services.db.GetServerID()); err != nil {
		t.Fatal(err)
	}
	b.block(t, bob, alice)

	att, err := b.h.services.db.GetReedAttestation(ctx, reed)
	if err != nil || att == nil {
		t.Fatalf("attestation: %v", err)
	}
	reedReport := proto.Clone(b.reedLinks(t, reed)).(*pb.RecoveryReedRequest)
	reedReport.ReedId = reed
	reedReport.UserSignature = &pb.UserSignature{Id: att.UserKeyID, Armor: att.UserSignature}
	reedReport.ServerSignature = pbServerSignature(ServerSignature{ID: att.ServerFingerprint, Armor: att.ServerSignature, SignedAt: att.ServerSignedAt})
	block, err := b.h.services.db.GetBlock(ctx, bob.id, alice.id)
	if err != nil || block == nil {
		t.Fatalf("bob's block: %v", err)
	}

	a2 := restoreIdentity(t, a)
	a2.claim(t, alice, aliceSnap)

	rr := a2.authed(t, alice, http.MethodPost, "/api/recovery/servers", &pb.RecoveryServersRequest{Servers: []*pb.FederatedServerInfo{record}}, nil, a2.h.ReportServers)
	expectStatus(t, "servers", rr, http.StatusOK)
	var res pb.RecoveryRecordsResponse
	decodeProto(t, rr.Body.Bytes(), &res)
	if len(res.GetResults()) != 1 || res.GetResults()[0].GetStatus() != recoveryRecordStored {
		t.Fatalf("servers result = %+v", res.GetResults())
	}
	if active, err := a2.h.services.db.IsActivePeer(ctx, bID); err != nil || !active {
		t.Fatalf("b restored as an active peer = %v (%v)", active, err)
	}

	msg := &pb.PeerIdentityRequest{Profile: spaRecoveryProfile(bobSnap.profile), Key: spaKeyNest(t, bobSnap)}
	expectStatus(t, "bob", a2.authed(t, alice, http.MethodPost, "/api/recovery/identity", msg, nil, a2.h.ReportPeerIdentity), http.StatusOK)
	if key, err := a2.h.services.db.GetPublicKey(ctx, bob.keyID); err != nil || key == nil {
		t.Fatalf("bob's key after report = %v (%v)", key, err)
	}
	var bobAccounts int
	if err := a2.db.QueryRow(`SELECT COUNT(*) FROM users WHERE id = $1`, bob.id).Scan(&bobAccounts); err != nil || bobAccounts != 0 {
		t.Fatalf("bob is an account here: %d (%v)", bobAccounts, err)
	}

	expectStatus(t, "reed", a2.authed(t, alice, http.MethodPost, "/api/recovery/reeds", reedReport, nil, a2.h.ReportReed), http.StatusNoContent)
	var held bool
	if err := a2.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM reed_allocations WHERE reed_id = $1 AND holder_user_id = $2)`, reed, alice.id).Scan(&held); err != nil || !held {
		t.Fatalf("alice holds bob's reed = %v (%v)", held, err)
	}

	expectStatus(t, "following", a2.authed(t, alice, http.MethodPost, "/api/recovery/following", &pb.RecoveryFollowingRequest{UserIds: []string{bob.id}}, nil, a2.h.ReportFollowing), http.StatusNoContent)
	var follows, pending int
	if err := a2.db.QueryRow(`SELECT (SELECT COUNT(*) FROM user_following WHERE user_id = $1 AND following_user_id = $2), (SELECT COUNT(*) FROM pending_follows)`, alice.id, bob.id).Scan(&follows, &pending); err != nil || follows != 1 || pending != 0 {
		t.Fatalf("follow of bob = %d, pending = %d (%v)", follows, pending, err)
	}

	a2.reportRecords(t, alice, func(yield func(int, *pb.RecoveryRecord) bool) {
		yield(0, &pb.RecoveryRecord{Record: &pb.RecoveryRecord_Block{Block: pbBlockCert(block)}})
	})
	if got, err := a2.h.services.db.GetBlock(ctx, bob.id, alice.id); err != nil || got == nil {
		t.Fatalf("bob's block of alice after report = %v (%v)", got, err)
	}
	// The block ends the follow it covers.
	if err := a2.db.QueryRow(`SELECT COUNT(*) FROM user_following WHERE user_id = $1`, alice.id).Scan(&follows); err != nil || follows != 0 {
		t.Fatalf("alice still follows bob: %d (%v)", follows, err)
	}
}

// A revoked peer's record comes back, and its users and reeds are refused.
func TestRecoveryRefusesARevokedPeersUsers(t *testing.T) {
	ctx := context.Background()
	a := newE2EServer(t, newTestDatabase(t, InitDB), false)
	b := newE2EServer(t, newTestDatabase(t, InitDB), false)
	bID := b.h.services.db.GetServerID()
	peeredWith(t, a, b)
	if err := a.h.services.db.RevokeFederationServer(ctx, bID, nil, "gone", time.Now()); err != nil {
		t.Fatal(err)
	}
	rr := httptest.NewRecorder()
	a.h.GetServerInfo(rr, httptest.NewRequest(http.MethodGet, "/api/server/info", nil))
	var info pb.ServerInfo
	decodeProto(t, rr.Body.Bytes(), &info)
	revoked := info.GetFederation()[0]

	alice := a.signup(t, "alice")
	aliceSnap := a.snapshot(t, alice.id)
	bob := b.signup(t, "bob")
	bobSnap := b.snapshot(t, bob.id)

	a2 := restoreIdentity(t, a)
	a2.claim(t, alice, aliceSnap)
	expectStatus(t, "servers", a2.authed(t, alice, http.MethodPost, "/api/recovery/servers", &pb.RecoveryServersRequest{Servers: []*pb.FederatedServerInfo{revoked}}, nil, a2.h.ReportServers), http.StatusOK)
	if active, err := a2.h.services.db.IsActivePeer(ctx, bID); err != nil || active {
		t.Fatalf("revoked peer restored as active = %v (%v)", active, err)
	}
	msg := &pb.PeerIdentityRequest{Profile: spaRecoveryProfile(bobSnap.profile), Key: spaKeyNest(t, bobSnap)}
	expectStatus(t, "bob", a2.authed(t, alice, http.MethodPost, "/api/recovery/identity", msg, nil, a2.h.ReportPeerIdentity), http.StatusUnprocessableEntity)
}
