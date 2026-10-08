//go:build !ops && !ripplescleanup && !challengescleanup

package main

import (
	"context"
	"testing"

	"google.golang.org/protobuf/proto"

	pb "syrinx/proto"
)

// blockFrames is every USER_BLOCKED and USER_UNBLOCKED a client received.
type blockFrames struct {
	blocked   []*pb.UserBlockedMessage
	unblocked []*pb.UserUnblockedMessage
}

func captureBlockFrames(t *testing.T, rs *realtimeService, userID string) (*realtimeClient, *blockFrames) {
	t.Helper()
	got := &blockFrames{}
	client := newEvictionTestClient(t, userID, func(_ int, data []byte) {
		var msg pb.WSMessage
		if err := proto.Unmarshal(data, &msg); err != nil {
			t.Errorf("unmarshal: %v", err)
			return
		}
		switch msg.Type {
		case pb.MessageType_USER_BLOCKED:
			got.blocked = append(got.blocked, msg.GetUserBlocked())
		case pb.MessageType_USER_UNBLOCKED:
			got.unblocked = append(got.unblocked, msg.GetUserUnblocked())
		}
	})
	rs.connManager.RegisterClient(client)
	return client, got
}

func TestBlockEventOwedUntilAcked(t *testing.T) {
	f := newBlockFixture(t)
	rs := newRealtimeService(f.ds, newCryptoService(), "")
	bob, got := captureBlockFrames(t, rs, f.bob)

	cert := f.storeBlock(t, f.alice, f.aliceKP, f.bob)
	rs.pushBlock(&cert)
	if len(got.blocked) != 1 || got.blocked[0].GetRequestId() != "" || got.blocked[0].GetBlock().GetUserId() != f.alice {
		t.Fatalf("push = %+v", got.blocked)
	}

	rs.catchUpBlocks(f.bob)
	if len(got.blocked) != 2 {
		t.Fatal("unacked block not resent on catch-up")
	}

	rs.ackBlockEvent(bob, f.alice, blockEventBlock)
	rs.catchUpBlocks(f.bob)
	if len(got.blocked) != 2 {
		t.Fatal("acked block resent on catch-up")
	}

	// bob's ack only ever drops bob's own events.
	f.storeBlock(t, f.alice, f.aliceKP, f.carol)
	rs.ackBlockEvent(bob, f.alice, blockEventBlock)
	if events, _ := f.ds.BlockEventsFor(context.Background(), f.carol); len(events) != 1 {
		t.Fatal("carol's block event dropped by bob's ack")
	}
}

func TestUnblockReplacesOwedBlock(t *testing.T) {
	f := newBlockFixture(t)
	ctx := context.Background()
	rs := newRealtimeService(f.ds, newCryptoService(), "")
	bob, got := captureBlockFrames(t, rs, f.bob)

	f.storeBlock(t, f.alice, f.aliceKP, f.bob)
	if deleted, err := f.ds.DeleteBlock(ctx, f.alice, f.bob); err != nil || !deleted {
		t.Fatalf("DeleteBlock: %v %v", deleted, err)
	}
	if deleted, _ := f.ds.DeleteBlock(ctx, f.alice, f.bob); deleted {
		t.Fatal("deleted a block twice")
	}

	// A late ack of the replaced block leaves the lift owed.
	rs.ackBlockEvent(bob, f.alice, blockEventBlock)
	rs.catchUpBlocks(f.bob)
	if len(got.blocked) != 0 || len(got.unblocked) != 1 || got.unblocked[0].GetUserId() != f.alice {
		t.Fatalf("catch-up: %d blocked, %+v unblocked", len(got.blocked), got.unblocked)
	}

	rs.ackBlockEvent(bob, f.alice, blockEventUnblock)
	rs.catchUpBlocks(f.bob)
	if len(got.unblocked) != 1 {
		t.Fatal("acked lift resent")
	}
}

func TestReblockReplacesOwedUnblock(t *testing.T) {
	f := newBlockFixture(t)
	ctx := context.Background()
	rs := newRealtimeService(f.ds, newCryptoService(), "")
	bob, got := captureBlockFrames(t, rs, f.bob)

	f.storeBlock(t, f.alice, f.aliceKP, f.bob)
	rs.ackBlockEvent(bob, f.alice, blockEventBlock)
	if _, err := f.ds.DeleteBlock(ctx, f.alice, f.bob); err != nil {
		t.Fatal(err)
	}
	f.storeBlock(t, f.alice, f.aliceKP, f.bob)

	rs.catchUpBlocks(f.bob)
	if len(got.unblocked) != 0 || len(got.blocked) != 1 {
		t.Fatalf("catch-up after re-block: %d blocked, %d unblocked", len(got.blocked), len(got.unblocked))
	}
}
