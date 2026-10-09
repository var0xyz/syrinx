//go:build !ops && !ripplescleanup && !challengescleanup

package main

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"google.golang.org/protobuf/proto"

	pb "syrinx/proto"
)

func TestBlockedProfileAnswers403WithCert(t *testing.T) {
	f := newBlockFixture(t)
	cert := f.storeBlock(t, f.alice, f.aliceKP, f.bob)

	get := func(handler http.HandlerFunc, path, viewer string, kp cryptoKeyPair, vars map[string]string) (int, []byte) {
		rr := f.serve(t, handler, http.MethodGet, path, viewer, kp, nil, vars)
		return rr.Code, rr.Body.Bytes()
	}
	vars := map[string]string{"userID": f.alice}

	code, body := get(f.h.GetUserProfile, "/api/users/"+f.alice+"/profile", f.bob, f.bobKP, vars)
	if code != http.StatusForbidden {
		t.Fatalf("blocked profile: status %d", code)
	}
	got := refusalBlock(body)
	if got == nil || got.Type != identityTypeBlock ||
		got.UserSignature != cert.UserSignature || got.ServerSignature.Armor != cert.ServerSignature.Armor {
		t.Fatalf("403 body = %s", body)
	}
	if code, _ := get(f.h.GetUserInfo, "/api/users/"+f.alice+"/info", f.bob, f.bobKP, vars); code != http.StatusForbidden {
		t.Fatalf("blocked info: status %d", code)
	}
	if code, _ := get(f.h.GetUserFollowers, "/api/users/"+f.alice+"/followers", f.bob, f.bobKP, vars); code != http.StatusForbidden {
		t.Fatalf("blocked followers: status %d", code)
	}
	reedVars := map[string]string{"userID": f.alice, "reedID": "r1"}
	if code, _ := get(f.h.GetReed, "/api/reeds/"+f.alice+"/r1", f.bob, f.bobKP, reedVars); code != http.StatusForbidden {
		t.Fatalf("blocked reed: status %d", code)
	}

	if code, _ := get(f.h.GetUserProfile, "/api/users/"+f.alice+"/profile", f.carol, f.carolKP, vars); code != http.StatusOK {
		t.Fatalf("carol: status %d", code)
	}
	if code, _ := get(f.h.GetUserProfile, "/api/users/"+f.bob+"/profile", f.alice, f.aliceKP, map[string]string{"userID": f.bob}); code != http.StatusOK {
		t.Fatalf("blocking user viewing the blocked user: status %d", code)
	}
}

// blockCapture is a registered client whose USER_BLOCKED frames are kept.
func blockCapture(t *testing.T, rs *realtimeService, userID string) (*realtimeClient, *[]*pb.UserBlockedMessage) {
	t.Helper()
	var got []*pb.UserBlockedMessage
	client := newEvictionTestClient(t, userID, func(_ int, data []byte) {
		var msg pb.WSMessage
		if err := proto.Unmarshal(data, &msg); err != nil {
			t.Errorf("unmarshal: %v", err)
			return
		}
		if msg.Type == pb.MessageType_USER_BLOCKED {
			got = append(got, msg.GetUserBlocked())
		}
	})
	rs.connManager.RegisterClient(client)
	return client, &got
}

func TestBlockedRequestsAnsweredWithUserBlocked(t *testing.T) {
	f := newBlockFixture(t)
	f.storeBlock(t, f.alice, f.aliceKP, f.bob)
	reedID := f.allocate(t, f.alice, "r1", f.carol)
	rs := newRealtimeService(f.ds, newCryptoService(), "")

	bob, got := blockCapture(t, rs, f.bob)
	requestID := generateRealtimeEventID(f.bob)
	rs.requestReed(bob, requestID, reedID)
	rs.handleProfilePage(bob, f.alice, 1)
	rs.handleSubscribeReed(bob, reedID)

	if len(*got) != 3 {
		t.Fatalf("USER_BLOCKED frames = %d, want 3", len(*got))
	}
	first := (*got)[0]
	if first.GetRequestId() != requestID || first.GetBlock().GetUserId() != f.alice || first.GetBlock().GetBlockedUserId() != f.bob {
		t.Fatalf("REQUEST_REED answer = %+v", first)
	}
	if n := f.count(t, `SELECT COUNT(*) FROM pending_events WHERE requester_user_id = $1`, f.bob); n != 0 {
		t.Fatalf("bob has %d pending events", n)
	}

	carol, carolGot := blockCapture(t, rs, f.carol)
	rs.requestReed(carol, generateRealtimeEventID(f.carol), reedID)
	if len(*carolGot) != 0 {
		t.Fatal("carol was refused")
	}
}

// online marks users online, which pending events require.
func (f blockFixture) online(t *testing.T, users ...string) {
	t.Helper()
	for _, u := range users {
		if _, err := f.db.Exec(`INSERT INTO online_users (user_id) VALUES ($1) ON CONFLICT DO NOTHING`, u); err != nil {
			t.Fatal(err)
		}
	}
}

func TestFanoutSkipsBlockedRecipient(t *testing.T) {
	f := newBlockFixture(t)
	f.online(t, f.bob, f.carol)
	f.storeBlock(t, f.alice, f.aliceKP, f.bob)
	reedID := f.allocate(t, f.alice, "r1", f.carol)
	rs := newRealtimeService(f.ds, newCryptoService(), "")
	ctx := context.Background()

	if err := rs.createPendingReedEvent(ctx, "ev-bob", "req-bob", f.bob, followReedEvent, reedID); !errors.Is(err, errRecipientBlocked) {
		t.Fatalf("bob: err = %v, want errRecipientBlocked", err)
	}
	if err := rs.createPendingReedEvent(ctx, "ev-carol", "req-carol", f.carol, followReedEvent, reedID); err != nil {
		t.Fatalf("carol: %v", err)
	}
	if err := rs.createPendingReedEvent(ctx, "ev-bob-rm", "req-bob", f.bob, reedRemovedEvent, reedID); err != nil {
		t.Fatalf("removal to bob: %v", err)
	}
}

func TestBlockDropsInFlightEvents(t *testing.T) {
	f := newBlockFixture(t)
	f.online(t, f.bob)
	reedID := f.allocate(t, f.alice, "r1", f.carol)
	other := f.allocate(t, f.carol, "r2", f.carol)
	rs := newRealtimeService(f.ds, newCryptoService(), "")
	ctx := context.Background()
	for i, id := range []string{reedID, other} {
		if err := rs.createPendingReedEvent(ctx, "ev"+string(rune('a'+i)), "req", f.bob, requestReedEvent, id); err != nil {
			t.Fatal(err)
		}
	}

	f.storeBlock(t, f.alice, f.aliceKP, f.bob)
	if n := f.count(t, `SELECT COUNT(*) FROM pending_events WHERE requester_user_id = $1`, f.bob); n != 1 {
		t.Fatalf("bob has %d pending events, want only carol's reed", n)
	}
}
