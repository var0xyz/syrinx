//go:build !ops && !ripplescleanup && !challengescleanup

package main

import (
	"context"
	"database/sql"
	"syrinx/observability/metrics"
	"testing"
	"time"
)

const (
	deliveryHomeID = "home1234"
	deliveryPeerID = "peer5678"
)

// deliveryFixture is a home server with one local author and one peer
// approved an hour ago, so everything seeded after that is owed to it.
type deliveryFixture struct {
	db       *sql.DB
	h        *Handlers
	author   string
	peer     PeerServer
	userSig  int64
	pubKeyID string
	nextReed int
}

func newDeliveryFixture(t *testing.T) *deliveryFixture {
	t.Helper()
	db := newTestDatabase(t, InitDB)
	userSig, _, pubKeyID := seedReedStatsServer(t, db, deliveryHomeID)
	if _, err := db.Exec(`
		INSERT INTO servers (id, name, self, base_url, frontend_url, created_at)
		VALUES ($1, $1, FALSE, 'https://peer.example', 'https://peer.example', NOW() - INTERVAL '1 hour')`, deliveryPeerID); err != nil {
		t.Fatalf("insert peer: %v", err)
	}
	peerTestMesh(t, db, deliveryPeerID)
	author := string(canonicalID(deliveryHomeID, "alice"))
	if _, err := db.Exec(`INSERT INTO identities (id, server_id) VALUES ($1, $2)`, author, deliveryHomeID); err != nil {
		t.Fatalf("insert author: %v", err)
	}
	ds := NewDataService(db, deliveryHomeID)
	ds.setServerIDForTest(deliveryHomeID)
	return &deliveryFixture{
		db:       db,
		h:        &Handlers{services: &Services{db: ds, log: NewLoggingService()}, metrics: metrics.Noop{}},
		author:   author,
		peer:     PeerServer{ID: deliveryPeerID, BaseURL: "https://peer.example"},
		userSig:  userSig,
		pubKeyID: pubKeyID,
	}
}

func (f *deliveryFixture) serverSig(t *testing.T, at time.Time) int64 {
	t.Helper()
	var id int64
	if err := f.db.QueryRow(`INSERT INTO server_signatures (private_key_id, signature, signed_at) VALUES ('sfp1', 'sig', $1) RETURNING id`, at).Scan(&id); err != nil {
		t.Fatalf("insert server signature: %v", err)
	}
	return id
}

// reed inserts a local reed by the author; publishedAt nil leaves it
// unpublished.
func (f *deliveryFixture) reed(t *testing.T, publishedAt *time.Time) string {
	t.Helper()
	f.nextReed++
	id := string(appendEntity(identityID(f.author), "01a026d4-406f-744b-b730-fcd241bf26"+string(rune('a'+f.nextReed))+"0"))
	if _, err := f.db.Exec(`INSERT INTO reed_identities (id, server_id, author_id) VALUES ($1, $2, $3)`, id, deliveryHomeID, f.author); err != nil {
		t.Fatalf("insert reed identity: %v", err)
	}
	signed := time.Now().UTC().Add(-30 * time.Minute)
	if _, err := f.db.Exec(`
		INSERT INTO reeds (id, user_id, signed_at, user_signature_id, server_signature_id, published_at)
		VALUES ($1, $2, $3, $4, $5, $6)`, id, f.author, signed, f.userSig, f.serverSig(t, signed), publishedAt); err != nil {
		t.Fatalf("insert reed: %v", err)
	}
	return id
}

func (f *deliveryFixture) remove(t *testing.T, reedID string, at time.Time) {
	t.Helper()
	if _, err := f.db.Exec(`
		INSERT INTO reed_removals (reed_id, public_key_id, user_signature_id, server_signature_id)
		VALUES ($1, $2, $3, $4)`, reedID, f.pubKeyID, f.userSig, f.serverSig(t, at)); err != nil {
		t.Fatalf("insert removal: %v", err)
	}
}

func at(minutesAgo int) *time.Time {
	t := time.Now().UTC().Add(-time.Duration(minutesAgo) * time.Minute)
	return &t
}

// queuedKinds lists what peerID is owed, oldest first, as kind:subject.
func (f *deliveryFixture) queuedKinds(t *testing.T, peerID string) []string {
	t.Helper()
	rows, err := f.db.Query(`SELECT kind, subject_id FROM peer_event_queue WHERE server_id = $1 ORDER BY id`, peerID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var kind, subject string
		if err := rows.Scan(&kind, &subject); err != nil {
			t.Fatal(err)
		}
		out = append(out, kind+":"+subject)
	}
	return out
}

func (f *deliveryFixture) publish(t *testing.T, reedID string) {
	t.Helper()
	if _, err := f.db.Exec(`INSERT INTO pending_fanout (reed_id, tags) VALUES ($1, '{}')`, reedID); err != nil {
		t.Fatalf("insert pending fanout: %v", err)
	}
	if _, _, err := f.h.services.db.ClaimPendingFanout(context.Background(), reedID); err != nil {
		t.Fatalf("ClaimPendingFanout: %v", err)
	}
}

func (f *deliveryFixture) removal(reedID string) reedRemovalCert {
	return reedRemovalCert{
		ReedID: reedID, UserID: f.author, UserKeyID: f.pubKeyID, UserSignature: "sig-" + reedID,
		ServerSignature: "ssig-" + reedID, ServerFingerprint: "sfp1", ServerSignedAt: time.Now().UTC(),
	}
}

// Publishing a reed owes every peer its announcement, and removing it
// afterwards its removal, in that order.
func TestPublishAndRemovalQueueForPeers(t *testing.T) {
	f := newDeliveryFixture(t)
	reedID := f.reed(t, nil)
	f.publish(t, reedID)
	if err := f.h.services.db.InsertReedRemoval(context.Background(), f.removal(reedID)); err != nil {
		t.Fatalf("InsertReedRemoval: %v", err)
	}
	want := []string{peerEventNewReed + ":" + reedID, peerEventReedRemoval + ":" + reedID}
	if got := f.queuedKinds(t, deliveryPeerID); !equalStrings(got, want) {
		t.Fatalf("queued %v, want %v", got, want)
	}
}

// A reed never published here was never announced, so its removal isn't
// either.
func TestRemovalOfUnpublishedReedQueuesNothing(t *testing.T) {
	f := newDeliveryFixture(t)
	reedID := f.reed(t, nil)
	if err := f.h.services.db.InsertReedRemoval(context.Background(), f.removal(reedID)); err != nil {
		t.Fatalf("InsertReedRemoval: %v", err)
	}
	if got := f.queuedKinds(t, deliveryPeerID); len(got) != 0 {
		t.Fatalf("queued %v, want nothing", got)
	}
}

// The real senders turn queued reeds and removals into their relay calls.
func TestDrainSendsReedEvents(t *testing.T) {
	f := newDeliveryFixture(t)
	reedID := f.reed(t, nil)
	f.publish(t, reedID)
	if err := f.h.services.db.InsertReedRemoval(context.Background(), f.removal(reedID)); err != nil {
		t.Fatalf("InsertReedRemoval: %v", err)
	}
	calls := &peerCalls{}
	f.h.drainPeerEventsWith(deliveryPeerID, f.h.peerEventSenderFor, calls.call)
	want := []string{"/api/federation/relay/new-reed", "/api/federation/relay/reed-removal"}
	if !equalStrings(calls.paths, want) || f.queued(t, deliveryPeerID) != 0 {
		t.Fatalf("sent %v, want %v", calls.paths, want)
	}
}

// The payload is rebuilt at send time from what is stored about the reed.
func TestBuildNewReedPayload(t *testing.T) {
	f := newDeliveryFixture(t)
	parent := f.reed(t, at(20))
	reply := f.reed(t, at(10))
	mentioned := string(canonicalID(deliveryPeerID, "bob"))
	if _, err := f.db.Exec(`INSERT INTO identities (id, server_id) VALUES ($1, $2)`, mentioned, deliveryPeerID); err != nil {
		t.Fatalf("insert mentioned identity: %v", err)
	}
	if _, err := f.db.Exec(`INSERT INTO reed_mentions (mentioning_reed_id, mentioned_user_id) VALUES ($1, $2)`, reply, mentioned); err != nil {
		t.Fatalf("insert mention: %v", err)
	}
	if _, err := f.db.Exec(`INSERT INTO reed_replies (root_id, reed_id, parent_reed_id, timestamp) VALUES ($1, $2, $1, NOW())`, parent, reply); err != nil {
		t.Fatalf("insert reply: %v", err)
	}

	payload, err := f.h.buildNewReedPayload(context.Background(), reply)
	if err != nil {
		t.Fatalf("buildNewReedPayload: %v", err)
	}
	if payload.AuthorId != f.author || len(payload.Mentions) != 1 || payload.Mentions[0] != mentioned {
		t.Fatalf("payload = %+v, want author %s mentioning %s", payload, f.author, mentioned)
	}
	if payload.Reply == nil || payload.Reply.ParentReedId != parent || payload.Echo != nil {
		t.Fatalf("payload reply = %+v echo = %+v, want a reply to %s", payload.Reply, payload.Echo, parent)
	}
}
