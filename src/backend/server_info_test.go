//go:build !ops && !ripplescleanup && !challengescleanup

package main

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"

	pb "syrinx/proto"
)

// fakePeeringSigner signs nothing, for tests about what is listed.
func fakePeeringSigner(payload []byte, ts time.Time) (ServerSignature, error) {
	return ServerSignature{ID: "fp@home", Armor: fmt.Sprintf("sig %d", len(payload)), SignedAt: ts}, nil
}

func signTestPeering(t *testing.T, ds *DataService, serverID string, countersign func([]byte, time.Time) (ServerSignature, error)) {
	t.Helper()
	tx, err := ds.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if err := ds.signPeeringTx(context.Background(), tx, serverID, countersign); err != nil {
		t.Fatalf("sign %s: %v", serverID, err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

// Every signed peering is listed by name, revoked ones included, with
// what it was signed over; one never signed isn't.
func TestListFederatedServers(t *testing.T) {
	db, rs, _ := newTeardownTestService(t)
	seedTestKey(t, db, "fp@peer5678")
	for _, stmt := range []string{
		`UPDATE servers SET base_url = 'https://api.peer.example', frontend_url = 'https://peer.example' WHERE id = 'peer5678'`,
		`UPDATE server_mesh SET peer_approved_at = NOW(), key_id = 'fp@peer5678' WHERE server_id = 'peer5678'`,
		`INSERT INTO servers (id, name, self, base_url, frontend_url) VALUES ('gone9012', 'gone', FALSE, 'https://gone.example', 'https://gone.example')`,
		`INSERT INTO server_mesh (server_id, revoked_at, revoked_reason, peer_approved_at) VALUES ('gone9012', NOW(), 'bye', NOW())`,
		`INSERT INTO servers (id, name, self, base_url, frontend_url) VALUES ('half1234', 'halfway', FALSE, 'https://half.example', 'https://half.example')`,
		`INSERT INTO server_mesh (server_id) VALUES ('half1234')`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
	signTestPeering(t, rs.db, teardownPeerID, fakePeeringSigner)
	signTestPeering(t, rs.db, "gone9012", fakePeeringSigner)

	servers, err := rs.db.ListFederatedServers(context.Background())
	if err != nil {
		t.Fatalf("ListFederatedServers: %v", err)
	}
	var got []string
	for _, srv := range servers {
		got = append(got, fmt.Sprintf("%s revoked=%v reason=%q", srv.PeerID, srv.RevokedAt != nil, srv.RevokedReason))
	}
	want := []string{`gone9012 revoked=true reason="bye"`, teardownPeerID + ` revoked=false reason=""`}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("listed %v, want %v", got, want)
	}
	if servers[1].FrontendURL != "https://peer.example" || servers[1].KeyID != "fp@peer5678" || servers[1].PeerApprovedAt == nil {
		t.Fatalf("peer = %+v, want its frontend url, key id and approval", servers[1])
	}
}

// assertPeeringSigned checks the stored signature verifies against the
// peering as it now stands.
func assertPeeringSigned(t *testing.T, a *e2eServer, serverID string) {
	t.Helper()
	ctx := context.Background()
	var sigID sql.NullInt64
	p, err := scanPeering(a.db.QueryRow(`SELECT `+peeringColumns+`, m.server_signature_id
		FROM servers s JOIN server_mesh m ON m.server_id = s.id WHERE s.id = $1`, serverID).Scan, &sigID)
	if err != nil || !sigID.Valid {
		t.Fatalf("peering %s: %v, signed %v", serverID, err, sigID.Valid)
	}
	var keyID, armor string
	var at time.Time
	if err := a.db.QueryRow(`SELECT private_key_id, signature, signed_at FROM server_signatures WHERE id = $1`, sigID.Int64).Scan(&keyID, &armor, &at); err != nil {
		t.Fatal(err)
	}
	key, err := a.h.services.db.GetPublicKey(ctx, keyID)
	if err != nil || key == nil {
		t.Fatalf("signing key %q: %v", keyID, err)
	}
	payload := buildPeerRecordPayload(a.h.services.db.GetServerID(), *p, at.UTC())
	if err := a.h.services.crypto.verifySignature(ctx, string(payload), armor, key.Armor); err != nil {
		t.Fatalf("peering %s is signed stale: %v", serverID, err)
	}
}

// /server/info lists each peering as signed, never signing on a read;
// each write signs it again, and a purge keeps every decision made.
func TestPeeringSignedOnEveryWrite(t *testing.T) {
	a := newE2EServer(t, newTestDatabase(t, InitDB), false)
	ctx := context.Background()
	ds := a.h.services.db
	sign := a.h.signerFor(ctx)
	const peerID = "peer5678"
	if _, err := a.db.Exec(`INSERT INTO servers (id, name, base_url, frontend_url) VALUES ($1, 'Peer', 'https://api.peer.example', 'https://peer.example')`, peerID); err != nil {
		t.Fatal(err)
	}
	if _, err := a.db.Exec(`INSERT INTO server_mesh (server_id, approved_by, approved_at) VALUES ($1, 'root', NOW() - INTERVAL '1 hour')`, peerID); err != nil {
		t.Fatal(err)
	}
	seedTestKey(t, a.db, "fp@peer5678")
	pinTestPeerKey(t, a.db, peerID, "fp@peer5678")
	signTestPeering(t, ds, peerID, sign)

	viewer := a.signup(t, "viewer")
	fetch := func() *pb.FederatedServerInfo {
		t.Helper()
		info := a.serverInfo(t, viewer)
		if len(info.GetFederation()) != 1 {
			t.Fatalf("federation = %+v", info.GetFederation())
		}
		return info.GetFederation()[0]
	}
	first := fetch()
	if again := fetch(); again.GetServerSignature().GetArmor() != first.GetServerSignature().GetArmor() {
		t.Fatal("a read signed the peering again")
	}
	if first.GetApprovedBy() != "root" || first.GetApprovedAt() == 0 {
		t.Fatalf("record = %+v, want its approval", first)
	}

	steps := []struct {
		name  string
		write func() error
	}{
		{"peer approval", func() error { return ds.MarkPeerApproved(ctx, peerID, sign) }},
		{"disconnect request", func() error {
			return ds.RequestFederationServerDisconnect(ctx, peerID, "admin", "testing", time.Now(), sign)
		}},
		{"disconnect cancel", func() error { return ds.CancelFederationServerDisconnect(ctx, peerID, sign) }},
		{"pin cleared", func() error { return ds.ClearPeerPin(ctx, peerID, sign) }},
		{"revocation", func() error { return ds.RevokeFederationServer(ctx, peerID, nil, "gone", time.Now(), sign) }},
		{"purge", func() error { return ds.PurgeFederationServer(ctx, peerID, sign) }},
	}
	for _, step := range steps {
		before := fetch().GetServerSignature().GetArmor()
		if err := step.write(); err != nil {
			t.Fatalf("%s: %v", step.name, err)
		}
		assertPeeringSigned(t, a, peerID)
		if fetch().GetServerSignature().GetArmor() == before {
			t.Fatalf("%s didn't sign the peering again", step.name)
		}
	}
	purged := fetch()
	if purged.GetPurgedAt() == 0 || purged.GetRevokedReason() != "gone" || purged.GetApprovedBy() != "root" || purged.GetPeerApprovedAt() == 0 {
		t.Fatalf("purged record = %+v, want its revocation, approval and peer approval kept", purged)
	}
	var signatures int
	if err := a.db.QueryRow(`SELECT COUNT(*) FROM server_mesh m JOIN server_signatures ss ON ss.id = m.server_signature_id`).Scan(&signatures); err != nil || signatures != 1 {
		t.Fatalf("peering signatures = %d (%v), want the replaced ones deleted", signatures, err)
	}
}
