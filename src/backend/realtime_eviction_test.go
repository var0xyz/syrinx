//go:build !ops && !ripplescleanup

package main

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gorilla/websocket"
	"google.golang.org/protobuf/proto"

	pb "syrinx/proto"

	"syrinx/observability/metrics"
)

// newEvictionTestClient returns a realtimeClient on a real in-process
// websocket, since the ack goes out over client.conn. Outbound frames
// are captured via wsRecordOutbound.
func newEvictionTestClient(t *testing.T, userID string, record func(int, []byte)) *realtimeClient {
	t.Helper()

	upgrader := websocket.Upgrader{}
	serverConn := make(chan *websocket.Conn, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade: %v", err)
			return
		}
		serverConn <- conn
	}))
	t.Cleanup(srv.Close)

	clientConn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(srv.URL, "http"), nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { clientConn.Close() })

	conn := <-serverConn
	t.Cleanup(func() { conn.Close() })

	return &realtimeClient{userID: userID, conn: conn, wsRecordOutbound: record}
}

// captureEvictionAcks drives handleEviction and returns the reed ids of
// every EVICTION_ACK the client received.
func captureEvictionAcks(t *testing.T, rs *realtimeService, userID, reedID string) []string {
	t.Helper()

	var acked []string
	client := newEvictionTestClient(t, userID, func(_ int, data []byte) {
		var msg pb.WSMessage
		if err := proto.Unmarshal(data, &msg); err != nil {
			t.Errorf("unmarshal outbound frame: %v", err)
			return
		}
		if msg.Type == pb.MessageType_EVICTION_ACK {
			acked = append(acked, msg.GetEvictionAck().GetReedId())
		}
	})

	rs.handleEviction(client, reedID)
	return acked
}

func seedEvictionUser(t *testing.T, db *sql.DB, userID string, userSigID, serverSigID int64) {
	t.Helper()
	seedReedStatsIdentity(t, db, userID, "testserver")
	if _, err := db.Exec(`
		INSERT INTO users (id, username, user_signature_id, server_signature_id)
		VALUES ($1, $2, $3, $4)
	`, userID+"@testserver", userID, userSigID, serverSigID); err != nil {
		t.Fatal(err)
	}
}

// Covers the contract in one pass: the first eviction clears the
// allocation and acks, and repeats still ack with no row left.
func TestHandleEvictionDropsAllocationAndAcks(t *testing.T) {
	db := openReedStatsTestDB(t)
	ds := &DataService{db: db, serverID: "testserver"}
	rs := &realtimeService{db: ds, connManager: newRealtimeConnectionManager(), metrics: metrics.Noop{}}
	ctx := context.Background()

	userSigID, serverSigID, pubKeyID := seedReedStatsServer(t, db, "testserver")
	seedEvictionUser(t, db, "alice", userSigID, serverSigID)
	seedEvictionUser(t, db, "bob", userSigID, serverSigID)

	reedID := "alice@testserver/root"
	seedReedStatsReed(t, db, reedID, "alice@testserver", pubKeyID, userSigID, serverSigID)

	if _, err := ds.AllocateReed(ctx, reedID, "bob@testserver"); err != nil {
		t.Fatal(err)
	}

	acked := captureEvictionAcks(t, rs, "bob@testserver", reedID)
	if len(acked) != 1 || acked[0] != reedID {
		t.Fatalf("first eviction acked %v, want [%s]", acked, reedID)
	}

	var holders int
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM reed_allocations WHERE reed_id = $1 AND holder_user_id = $2`,
		reedID, "bob@testserver",
	).Scan(&holders); err != nil {
		t.Fatal(err)
	}
	if holders != 0 {
		t.Fatalf("allocation rows after eviction = %d, want 0", holders)
	}

	// Idempotent: a retry after a lost ack must still be acked.
	acked = captureEvictionAcks(t, rs, "bob@testserver", reedID)
	if len(acked) != 1 || acked[0] != reedID {
		t.Fatalf("repeat eviction acked %v, want [%s]", acked, reedID)
	}
}

// A holder the server never recorded still gets an ack, so the client
// can go ahead and delete.
func TestHandleEvictionAcksUnknownAllocation(t *testing.T) {
	db := openReedStatsTestDB(t)
	ds := &DataService{db: db, serverID: "testserver"}
	rs := &realtimeService{db: ds, connManager: newRealtimeConnectionManager(), metrics: metrics.Noop{}}

	userSigID, serverSigID, pubKeyID := seedReedStatsServer(t, db, "testserver")
	seedEvictionUser(t, db, "alice", userSigID, serverSigID)
	seedEvictionUser(t, db, "bob", userSigID, serverSigID)

	reedID := "alice@testserver/root"
	seedReedStatsReed(t, db, reedID, "alice@testserver", pubKeyID, userSigID, serverSigID)

	acked := captureEvictionAcks(t, rs, "bob@testserver", reedID)
	if len(acked) != 1 || acked[0] != reedID {
		t.Fatalf("eviction of unheld reed acked %v, want [%s]", acked, reedID)
	}
}

// TestHandleEvictionIgnoresEmptyReedID guards the malformed-payload path.
func TestHandleEvictionIgnoresEmptyReedID(t *testing.T) {
	t.Parallel()

	rs := &realtimeService{connManager: newRealtimeConnectionManager(), metrics: metrics.Noop{}}
	if acked := captureEvictionAcks(t, rs, "bob@testserver", ""); len(acked) != 0 {
		t.Fatalf("empty reed id acked %v, want none", acked)
	}
}
