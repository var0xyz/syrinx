//go:build !ops && !ripplescleanup && !challengescleanup

package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/mux"
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
		url.Values{"signature": {"s"}, "fingerprint": {f.bobKP.Fingerprint}}, vars)
	if like.Code != http.StatusForbidden {
		t.Fatalf("bob likes alice's reed: status %d", like.Code)
	}

	ref := string(canonicalID(f.ds.GetServerID(), "alice", reedID))
	for _, field := range []string{"replyingTo", "echoing"} {
		form := url.Values{"signature": {"s"}, "reedID": {uuid.Must(uuid.NewV7()).String()}, field: {ref}}
		rr := f.serve(t, f.h.SignReed, http.MethodPost, "/api/reeds", f.bob, f.bobKP, form, nil)
		var cert BlockCert
		if rr.Code != http.StatusForbidden || json.Unmarshal(rr.Body.Bytes(), &cert) != nil || cert.UserID != f.alice {
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
	body, _ := json.Marshal(map[string]any{"content": "hi", "threadID": uuid.Must(uuid.NewV7()).String()})

	timestamp := strconv.FormatInt(time.Now().Unix(), 10)
	sig, err := f.h.services.crypto.sign(http.MethodPost+" "+path+"\n\n"+string(body)+"\n\n"+timestamp, f.bobKP.PrivateKey)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Syrinx-Public-Key-Id", f.keyID(f.bob, f.bobKP))
	req.Header.Set("X-Syrinx-Signature", base64.StdEncoding.EncodeToString([]byte(sig)))
	req.Header.Set("X-Syrinx-Signature-Scope", "body")
	req.Header.Set("X-Syrinx-Timestamp", timestamp)
	req = mux.SetURLVars(req, map[string]string{"userID": f.alice, "reedID": reedID})
	rr := httptest.NewRecorder()
	f.h.signatureAuthMiddleware("/api")(http.HandlerFunc(f.h.PostRipple)).ServeHTTP(rr, req)
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
