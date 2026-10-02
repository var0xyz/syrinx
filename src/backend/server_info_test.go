//go:build !ops && !ripplescleanup

package main

import (
	"context"
	"testing"
)

// Only approved, connected peers that haven't been disconnected are listed.
func TestListFederatedServers(t *testing.T) {
	db, rs, _ := newTeardownTestService(t)
	for _, stmt := range []string{
		`UPDATE servers SET connected = TRUE, base_url = 'https://api.peer.example', frontend_url = 'https://peer.example', key_id = 'fp@peer5678' WHERE id = 'peer5678'`,
		`INSERT INTO servers (id, name, self, connected, base_url, frontend_url, revoked_at) VALUES ('gone9012', 'gone', FALSE, TRUE, 'https://gone.example', 'https://gone.example', NOW())`,
		`INSERT INTO servers (id, name, self, connected, base_url, frontend_url) VALUES ('wait3456', 'waiting', FALSE, FALSE, 'https://wait.example', 'https://wait.example')`,
		`INSERT INTO servers (id, name, self, connected, base_url, frontend_url, disconnect_requested_at) VALUES ('leav7890', 'leaving', FALSE, TRUE, 'https://leaving.example', 'https://leaving.example', NOW())`,
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
