//go:build !ops && !ripplescleanup && !challengescleanup

package main

import (
	"context"
	"database/sql"
	"errors"
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
		INSERT INTO servers (id, name, self, connected, base_url, frontend_url, created_at)
		VALUES ($1, $1, FALSE, TRUE, 'https://peer.example', 'https://peer.example', NOW() - INTERVAL '1 hour')`, deliveryPeerID); err != nil {
		t.Fatalf("insert peer: %v", err)
	}
	author := string(canonicalID(deliveryHomeID, "alice"))
	if _, err := db.Exec(`INSERT INTO identities (id, server_id) VALUES ($1, $2)`, author, deliveryHomeID); err != nil {
		t.Fatalf("insert author: %v", err)
	}
	ds := NewDataService(db, deliveryHomeID)
	ds.setServerIDForTest(deliveryHomeID)
	return &deliveryFixture{
		db:       db,
		h:        &Handlers{services: &Services{db: ds, log: NewLoggingService()}},
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

// recorder stands in for the peer: it answers each item with the next
// scripted status and records what it was sent.
type recorder struct {
	sent     []peerStreamItem
	statuses []int
}

func (r *recorder) send(_ context.Context, _ PeerServer, item peerStreamItem) (int, error) {
	r.sent = append(r.sent, item)
	status := 204
	if len(r.statuses) > 0 {
		status, r.statuses = r.statuses[0], r.statuses[1:]
	}
	if status == 0 {
		return 0, errors.New("connection refused")
	}
	return status, nil
}

func at(minutesAgo int) *time.Time {
	t := time.Now().UTC().Add(-time.Duration(minutesAgo) * time.Minute)
	return &t
}

// A stream goes out in publish order, and a removal after its creation.
func TestDrainDeliversInOrder(t *testing.T) {
	f := newDeliveryFixture(t)
	second := f.reed(t, at(10))
	first := f.reed(t, at(20))
	f.remove(t, first, *at(5))
	f.reed(t, nil) // unpublished: never streamed

	rec := &recorder{}
	f.h.drainPeerStreamWith(f.peer, f.author, rec.send)

	if len(rec.sent) != 3 {
		t.Fatalf("sent %d items, want 3: %+v", len(rec.sent), rec.sent)
	}
	want := []struct {
		reed string
		kind int
	}{{first, streamKindCreation}, {second, streamKindCreation}, {first, streamKindRemoval}}
	for i, w := range want {
		if rec.sent[i].ReedID != w.reed || rec.sent[i].Kind != w.kind {
			t.Fatalf("item %d = %+v, want %s kind %d", i, rec.sent[i], w.reed, w.kind)
		}
	}
}

// A failure stops the stream; the next drain resumes where it stopped.
func TestDrainStopsOnFailureAndResumes(t *testing.T) {
	f := newDeliveryFixture(t)
	f.reed(t, at(20))
	second := f.reed(t, at(10))

	rec := &recorder{statuses: []int{204, 503}}
	f.h.drainPeerStreamWith(f.peer, f.author, rec.send)
	if len(rec.sent) != 2 {
		t.Fatalf("first drain sent %d items, want 2 (the second failed)", len(rec.sent))
	}

	rec = &recorder{}
	f.h.drainPeerStreamWith(f.peer, f.author, rec.send)
	if len(rec.sent) != 1 || rec.sent[0].ReedID != second {
		t.Fatalf("resumed drain sent %+v, want only %s", rec.sent, second)
	}
}

// A transport error behaves like a 5xx; a 4xx is skipped for good.
func TestDrainSkipsRefusedItem(t *testing.T) {
	f := newDeliveryFixture(t)
	f.reed(t, at(20))
	f.reed(t, at(10))

	rec := &recorder{statuses: []int{400, 204}}
	f.h.drainPeerStreamWith(f.peer, f.author, rec.send)
	if len(rec.sent) != 2 {
		t.Fatalf("sent %d items, want both (the refused one skipped)", len(rec.sent))
	}
	behind, err := f.h.services.db.BehindAuthors(context.Background(), deliveryPeerID)
	if err != nil {
		t.Fatalf("BehindAuthors: %v", err)
	}
	if len(behind) != 0 {
		t.Fatalf("behind = %v, want nothing left owed", behind)
	}

	f.reed(t, at(1))
	rec = &recorder{statuses: []int{0}}
	f.h.drainPeerStreamWith(f.peer, f.author, rec.send)
	behind, _ = f.h.services.db.BehindAuthors(context.Background(), deliveryPeerID)
	if len(behind) != 1 {
		t.Fatalf("after a transport error behind = %v, want the author still owed", behind)
	}
}

// Only one drain holds a stream; a stale claim is taken over; a peer that
// is down can't be claimed.
func TestClaimPeerStream(t *testing.T) {
	f := newDeliveryFixture(t)
	ctx := context.Background()
	db := f.h.services.db

	if ok, err := db.ClaimPeerStream(ctx, deliveryPeerID, f.author); err != nil || !ok {
		t.Fatalf("first claim = %v, %v; want true", ok, err)
	}
	if ok, _ := db.ClaimPeerStream(ctx, deliveryPeerID, f.author); ok {
		t.Fatal("a live claim was taken twice")
	}
	if _, err := f.db.Exec(`UPDATE peer_author_cursors SET claimed_at = NOW() - INTERVAL '2 minutes'`); err != nil {
		t.Fatalf("age claim: %v", err)
	}
	if ok, _ := db.ClaimPeerStream(ctx, deliveryPeerID, f.author); !ok {
		t.Fatal("a stale claim was not taken over")
	}

	if err := db.ReleasePeerStream(ctx, deliveryPeerID, f.author); err != nil {
		t.Fatalf("ReleasePeerStream: %v", err)
	}
	if err := db.SetPeerDown(ctx, deliveryPeerID, true); err != nil {
		t.Fatalf("SetPeerDown: %v", err)
	}
	if ok, _ := db.ClaimPeerStream(ctx, deliveryPeerID, f.author); ok {
		t.Fatal("claimed a stream to a peer that is down")
	}
}

// A reed from before the peer was approved is not owed to it.
func TestNewPeerGetsNoHistory(t *testing.T) {
	f := newDeliveryFixture(t)
	f.reed(t, at(120))

	behind, err := f.h.services.db.BehindAuthors(context.Background(), deliveryPeerID)
	if err != nil {
		t.Fatalf("BehindAuthors: %v", err)
	}
	if len(behind) != 0 {
		t.Fatalf("behind = %v, want nothing from before approval", behind)
	}
}

// Publishing is what enters a reed into the stream.
func TestClaimPendingFanoutPublishes(t *testing.T) {
	f := newDeliveryFixture(t)
	reedID := f.reed(t, nil)
	if _, err := f.db.Exec(`INSERT INTO pending_fanout (reed_id, tags) VALUES ($1, '{}')`, reedID); err != nil {
		t.Fatalf("insert pending fanout: %v", err)
	}
	if _, _, err := f.h.services.db.ClaimPendingFanout(context.Background(), reedID); err != nil {
		t.Fatalf("ClaimPendingFanout: %v", err)
	}
	behind, err := f.h.services.db.BehindAuthors(context.Background(), deliveryPeerID)
	if err != nil {
		t.Fatalf("BehindAuthors: %v", err)
	}
	if len(behind) != 1 || behind[0] != f.author {
		t.Fatalf("behind = %v, want the author once published", behind)
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
