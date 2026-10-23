//go:build !ops && !ripplescleanup && !challengescleanup

package main

import (
	"context"
	"fmt"
	"testing"
	"time"

	pb "syrinx/proto"
)

// Only peers approved on both sides that haven't been disconnected are listed.
func TestListFederatedServers(t *testing.T) {
	db, rs, _ := newTeardownTestService(t)
	seedTestKey(t, db, "fp@peer5678")
	for _, stmt := range []string{
		`UPDATE servers SET base_url = 'https://api.peer.example', frontend_url = 'https://peer.example' WHERE id = 'peer5678'`,
		`UPDATE server_mesh SET connected = TRUE, peer_approved_at = NOW(), key_id = 'fp@peer5678' WHERE server_id = 'peer5678'`,
		`INSERT INTO servers (id, name, self, base_url, frontend_url) VALUES ('gone9012', 'gone', FALSE, 'https://gone.example', 'https://gone.example')`,
		`INSERT INTO server_mesh (server_id, revoked_at, peer_approved_at) VALUES ('gone9012', NOW(), NOW())`,
		`INSERT INTO servers (id, name, self, base_url, frontend_url) VALUES ('wait3456', 'waiting', FALSE, 'https://wait.example', 'https://wait.example')`,
		`INSERT INTO server_mesh (server_id, connected) VALUES ('wait3456', FALSE)`,
		`INSERT INTO servers (id, name, self, base_url, frontend_url) VALUES ('leav7890', 'leaving', FALSE, 'https://leaving.example', 'https://leaving.example')`,
		`INSERT INTO server_mesh (server_id, disconnect_requested_at, peer_approved_at) VALUES ('leav7890', NOW(), NOW())`,
		`INSERT INTO servers (id, name, self, base_url, frontend_url) VALUES ('half1234', 'halfway', FALSE, 'https://half.example', 'https://half.example')`,
		`INSERT INTO server_mesh (server_id) VALUES ('half1234')`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}

	servers, err := rs.db.ListFederatedServers(context.Background())
	if err != nil {
		t.Fatalf("ListFederatedServers: %v", err)
	}
	type row struct {
		id                   string
		revoked, established bool
	}
	var got []row
	for _, srv := range servers {
		got = append(got, row{srv.ID, srv.Revoked, srv.Established})
	}
	want := []row{
		{"gone9012", true, false},
		{"half1234", false, false},
		{"leav7890", false, true},
		{teardownPeerID, false, true},
		{"wait3456", false, false},
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("listed %v, want every peer by name %v", got, want)
	}
	if servers[3].FrontendURL != "https://peer.example" || servers[3].KeyID != "fp@peer5678" {
		t.Fatalf("peer = %+v, want its frontend url and key id", servers[3])
	}
}

// /server/info countersigns a record per peer, signs it again only when
// what it covers changes, and says when a peer was revoked or purged.
func TestServerInfoSignsPeerRecords(t *testing.T) {
	a := newE2EServer(t, newTestDatabase(t, InitDB), false)
	ctx := context.Background()
	const peerID = "peer5678"
	if _, err := a.db.Exec(`INSERT INTO servers (id, name, base_url, frontend_url) VALUES ($1, 'Peer', 'https://api.peer.example', 'https://peer.example')`, peerID); err != nil {
		t.Fatal(err)
	}
	peerTestMesh(t, a.db, peerID)
	seedTestKey(t, a.db, "fp@peer5678")
	pinTestPeerKey(t, a.db, peerID, "fp@peer5678")

	if anon := a.serverInfo(t, nil); len(anon.GetFederation()) != 0 {
		t.Fatalf("anonymous server info lists peers: %+v", anon.GetFederation())
	}
	viewer := a.signup(t, "viewer")
	fetch := func() *pb.FederatedServerInfo {
		t.Helper()
		info := a.serverInfo(t, viewer)
		if len(info.GetFederation()) != 1 {
			t.Fatalf("federation = %+v", info.GetFederation())
		}
		return info.GetFederation()[0]
	}
	verifies := func(rec *pb.FederatedServerInfo) {
		t.Helper()
		sig := rec.GetServerSignature()
		key, err := a.h.services.db.GetPublicKey(ctx, sig.GetId())
		if err != nil || key == nil {
			t.Fatalf("signing key %q: %v", sig.GetId(), err)
		}
		payload := buildPeerRecordPayload(a.h.services.db.GetServerID(), rec.GetId(), rec.GetName(), rec.GetKeyId(),
			rec.GetBaseUrl(), rec.GetFrontendUrl(), rec.GetStatus(), timeFromUnix(sig.GetSignedAt()))
		if err := a.h.services.crypto.verifySignature(ctx, string(payload), sig.GetArmor(), key.Armor); err != nil {
			t.Fatalf("record does not verify: %v", err)
		}
	}

	first := fetch()
	verifies(first)
	if first.GetStatus() != peerRecordConnected || !first.GetEstablished() {
		t.Fatalf("record = %+v, want connected and established", first)
	}
	if again := fetch(); again.GetServerSignature().GetArmor() != first.GetServerSignature().GetArmor() {
		t.Fatal("record signed again although nothing changed")
	}
	var firstSigID int64
	if err := a.db.QueryRow(`SELECT record_signature_id FROM server_mesh WHERE server_id = $1`, peerID).Scan(&firstSigID); err != nil {
		t.Fatal(err)
	}

	if err := a.h.services.db.RevokeFederationServer(ctx, peerID, nil, "gone", time.Now()); err != nil {
		t.Fatal(err)
	}
	revoked := fetch()
	verifies(revoked)
	if revoked.GetStatus() != peerRecordRevoked || revoked.GetServerSignature().GetArmor() == first.GetServerSignature().GetArmor() {
		t.Fatalf("after revocation record = %+v, want a new, revoked one", revoked)
	}
	var kept bool
	if err := a.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM server_signatures WHERE id = $1)`, firstSigID).Scan(&kept); err != nil || kept {
		t.Fatalf("replaced record signature kept = %v (%v), want it deleted", kept, err)
	}

	if err := a.h.services.db.PurgeFederationServer(ctx, peerID); err != nil {
		t.Fatal(err)
	}
	purged := fetch()
	verifies(purged)
	if purged.GetStatus() != peerRecordPurged {
		t.Fatalf("after purge record = %+v, want a purged one", purged)
	}
}
