//go:build !ops && !ripplescleanup && !challengescleanup

package main

import (
	"context"
	"net/http"
	"testing"

	pb "syrinx/proto"
)

// statsFoldFixture is a home server with a local reed and two viewers on
// one peer subscribed to it.
func statsFoldFixture(t *testing.T) (*realtimeService, string) {
	t.Helper()
	db, rs, _ := newTeardownTestService(t)
	author := string(canonicalID(teardownHomeID, "alice"))
	insertTeardownIdentity(t, db, author, teardownHomeID)
	reedID := string(appendEntity(identityID(author), "01a026d4-406f-744b-b730-fcd241bf2700"))
	if err := rs.db.UpsertReedIdentity(context.Background(), reedID); err != nil {
		t.Fatalf("insert reed identity: %v", err)
	}
	for i, name := range []string{"carol", "dave"} {
		viewer := string(canonicalID(teardownPeerID, name))
		insertTeardownIdentity(t, db, viewer, teardownPeerID)
		if err := rs.db.CreateReedSubscription(context.Background(), "sub"+name, viewer, reedID); err != nil {
			t.Fatalf("subscribe viewer %d: %v", i, err)
		}
	}
	return rs, reedID
}

// Two viewers on the same peer cost one push, not two.
func TestReedStatsPushFoldsPerPeer(t *testing.T) {
	rs, reedID := statsFoldFixture(t)
	pushes := 0
	rs.SetForeignReedStatsHook(func(_ context.Context, peerServerID, gotReedID, _ string, _ *pb.WSMessage) (int, error) {
		pushes++
		if peerServerID != teardownPeerID || gotReedID != reedID {
			t.Errorf("push to %s for %s", peerServerID, gotReedID)
		}
		return http.StatusNoContent, nil
	})

	rs.notifyForeignReedSubscribers(reedID, newReedLikesMsg(reedID, 3))

	if pushes != 1 {
		t.Fatalf("pushes = %d, want 1", pushes)
	}
}

// A peer with nobody watching any more loses its subscriptions.
func TestReedStatsPushDropsGoneSubscribers(t *testing.T) {
	rs, reedID := statsFoldFixture(t)
	rs.SetForeignReedStatsHook(func(context.Context, string, string, string, *pb.WSMessage) (int, error) {
		return http.StatusNotFound, nil
	})

	rs.notifyForeignReedSubscribers(reedID, newReedLikesMsg(reedID, 3))

	subs, err := rs.db.GetReedSubscriberUserIDs(context.Background(), reedID, "")
	if err != nil {
		t.Fatalf("GetReedSubscriberUserIDs: %v", err)
	}
	if len(subs) != 0 {
		t.Fatalf("subscribers = %v, want the peer's dropped", subs)
	}
}

// The receiving side forwards only to its own users and says when none
// are subscribed.
func TestDeliverForeignReedStatsReportsSubscribers(t *testing.T) {
	db, rs, viewer := newTeardownTestService(t)
	author := string(canonicalID(teardownPeerID, "bob"))
	insertTeardownIdentity(t, db, author, teardownPeerID)
	reedID := string(appendEntity(identityID(author), "01a026d4-406f-744b-b730-fcd241bf2701"))
	if err := rs.db.UpsertReedIdentity(context.Background(), reedID); err != nil {
		t.Fatalf("insert reed identity: %v", err)
	}
	msg := newReedLikesMsg(reedID, 3)

	if rs.DeliverForeignReedStats(context.Background(), reedID, "", msg) {
		t.Fatal("with no subscribers: delivered, want not")
	}

	if err := rs.db.CreateReedSubscription(context.Background(), "sub1", viewer, reedID); err != nil {
		t.Fatalf("CreateReedSubscription: %v", err)
	}
	if !rs.DeliverForeignReedStats(context.Background(), reedID, "", msg) {
		t.Fatal("with a subscriber: not delivered, want delivered")
	}
}
