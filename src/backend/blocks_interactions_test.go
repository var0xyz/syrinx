//go:build !ops && !ripplescleanup && !challengescleanup

package main

import (
	"context"
	"net/http"
	"testing"

	"github.com/google/uuid"

	pb "syrinx/proto"
)

func TestBlockedUserCannotInteract(t *testing.T) {
	f := newBlockFixture(t)
	f.storeBlock(t, f.alice, f.aliceKP, f.bob)
	reedID := uuid.Must(uuid.NewV7()).String()
	vars := map[string]string{"userID": f.alice, "reedID": reedID}

	follow := func(user string, kp cryptoKeyPair) int {
		return f.serve(t, f.h.FollowUser, http.MethodPost, "/api/users/"+f.alice+"/follow", user, kp, nil, map[string]string{"userID": f.alice}).Code
	}
	if code := follow(f.bob, f.bobKP); code != http.StatusForbidden {
		t.Fatalf("bob follows alice: status %d", code)
	}
	if code := follow(f.carol, f.carolKP); code != http.StatusNoContent {
		t.Fatalf("carol follows alice: status %d", code)
	}

	like := f.serve(t, f.h.LikeReed, http.MethodPost, "/api/reeds/"+f.alice+"/"+reedID+"/like", f.bob, f.bobKP,
		&pb.LikeRequest{Signature: "s", Fingerprint: f.bobKP.Fingerprint}, vars)
	if like.Code != http.StatusForbidden {
		t.Fatalf("bob likes alice's reed: status %d", like.Code)
	}

	ref := string(canonicalID(f.ds.GetServerID(), "alice", reedID))
	for _, field := range []string{"replyingTo", "echoing"} {
		body := &pb.SignReedRequest{Signature: "s", ReedId: uuid.Must(uuid.NewV7()).String()}
		if field == "replyingTo" {
			body.ReplyingTo = ref
		} else {
			body.Echoing = ref
		}
		rr := f.serve(t, f.h.SignReed, http.MethodPost, "/api/reeds", f.bob, f.bobKP, body, nil)
		if cert := refusalBlock(rr.Body.Bytes()); rr.Code != http.StatusForbidden || cert == nil || cert.UserID != f.alice {
			t.Fatalf("bob %s alice's reed: status %d %s", field, rr.Code, rr.Body.String())
		}
	}

	// Unfollowing and blocking back are never refused.
	if rr := f.serve(t, f.h.UnfollowUser, http.MethodDelete, "/api/users/"+f.alice+"/follow", f.bob, f.bobKP, nil, map[string]string{"userID": f.alice}); rr.Code != http.StatusNoContent {
		t.Fatalf("bob unfollows alice: status %d", rr.Code)
	}
	if rr := f.postBlock(t, f.bob, f.bobKP, f.alice); rr.Code != http.StatusOK {
		t.Fatalf("bob blocks alice back: status %d", rr.Code)
	}
}

func TestBlockedUserCannotRipple(t *testing.T) {
	f := newBlockFixture(t)
	f.storeBlock(t, f.alice, f.aliceKP, f.bob)
	reedID := uuid.Must(uuid.NewV7()).String()
	path := "/api/reeds/" + f.alice + "/" + reedID + "/ripples"
	body := &pb.PostRippleRequest{Content: "hi", ThreadId: uuid.Must(uuid.NewV7()).String()}
	rr := f.serve(t, f.h.PostRipple, http.MethodPost, path, f.bob, f.bobKP, body, map[string]string{"userID": f.alice, "reedID": reedID})
	if rr.Code != http.StatusForbidden {
		t.Fatalf("bob ripples on alice's reed: status %d %s", rr.Code, rr.Body.String())
	}
}

func TestMentionNotSentToUserBlockingTheAuthor(t *testing.T) {
	f := newBlockFixture(t)
	f.storeBlock(t, f.alice, f.aliceKP, f.bob)

	got, err := f.h.dropMentionsBlockingAuthor(context.Background(), []string{f.alice, f.carol}, f.bob)
	if err != nil || len(got) != 1 || got[0] != f.carol {
		t.Fatalf("mentions sent = %v, %v; want only carol", got, err)
	}
	got, _ = f.h.dropMentionsBlockingAuthor(context.Background(), []string{f.bob}, f.carol)
	if len(got) != 1 {
		t.Fatal("a mention by someone nobody blocked was dropped")
	}
}
