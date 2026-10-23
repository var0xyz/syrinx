//go:build !ops && !ripplescleanup && !challengescleanup

package main

import (
	"context"
	"testing"
)

// Only peers approved on both sides that haven't been disconnected are listed.
func TestListFederatedServers(t *testing.T) {
	db, rs, _ := newTeardownTestService(t)
	for _, stmt := range []string{
		`UPDATE servers SET base_url = 'https://api.peer.example', frontend_url = 'https://peer.example', key_id = 'fp@peer5678' WHERE id = 'peer5678'`,
		`UPDATE server_mesh SET connected = TRUE, peer_approved_at = NOW() WHERE server_id = 'peer5678'`,
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
	var ids []string
	for _, srv := range servers {
		ids = append(ids, srv.ID)
	}
	if len(servers) != 2 || servers[0].ID != "leav7890" || servers[1].ID != teardownPeerID {
		t.Fatalf("listed %v, want the pending-disconnect peer and the connected one, by name", ids)
	}
	if servers[1].FrontendURL != "https://peer.example" || servers[1].KeyID != "fp@peer5678" {
		t.Fatalf("peer = %+v, want its frontend url and key id", servers[1])
	}
}
