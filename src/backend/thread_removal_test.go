//go:build !ops && !ripplescleanup && !challengescleanup

package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gorilla/mux"

	pb "syrinx/proto"
)

// seedSignedThread stores an n-part thread and returns its parts and the
// author's signature over its record.
func (f *threadFixture) seedSignedThread(t *testing.T, n int) ([]string, string) {
	t.Helper()
	ids := f.ids(t, n)
	req := f.request(t, ids, ids)
	if rr := f.post(t, req); rr.Code != http.StatusCreated {
		t.Fatalf("status %d: %s", rr.Code, rr.Body.String())
	}
	return ids, req.ThreadSignature
}

func (f *threadFixture) deleteThread(t *testing.T, threadID, signature string) *httptest.ResponseRecorder {
	t.Helper()
	r := protoRequest(http.MethodDelete, "/api/threads/"+threadID, &pb.RemovalRequest{Signature: signature})
	r = mux.SetURLVars(withInviteUID(r, f.author), map[string]string{"threadID": threadID})
	rr := httptest.NewRecorder()
	f.h.DeleteThread(rr, r)
	return rr
}

func (f *threadFixture) removalSignature(t *testing.T, threadID, threadSignature string) string {
	t.Helper()
	payload := buildThreadRemovalUserPayload(f.h.services.db.GetServerID(), threadID, threadSignature)
	sig, err := f.h.services.crypto.sign(string(payload), f.kp.PrivateKey)
	if err != nil {
		t.Fatal(err)
	}
	return sig
}

func TestDeleteReed_RefusesThreadParts(t *testing.T) {
	f := newThreadFixture(t)
	ids, _ := f.seedSignedThread(t, 3)
	for _, id := range []string{ids[0], ids[1]} {
		_, _, bare, _ := parseKeyFingerprint(identityID(id))
		r := protoRequest(http.MethodDelete, "/api/reeds/x", &pb.RemovalRequest{Signature: "sig"})
		r = mux.SetURLVars(withInviteUID(r, f.author), map[string]string{"userID": f.author, "reedID": bare})
		rr := httptest.NewRecorder()
		f.h.DeleteReed(rr, r)
		if rr.Code != http.StatusBadRequest {
			t.Errorf("DeleteReed(%s) status %d, want 400 (%s)", id, rr.Code, rr.Body.String())
		}
	}
}

func TestDeleteThread_RemovesEveryPart(t *testing.T) {
	f := newThreadFixture(t)
	ctx := context.Background()
	ds := f.h.services.db
	ids, threadSig := f.seedSignedThread(t, 3)
	sig := f.removalSignature(t, ids[0], threadSig)

	rr := f.deleteThread(t, ids[0], sig)
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rr.Code, rr.Body.String())
	}
	var rm pb.ThreadRemoval
	decodeProto(t, rr.Body.Bytes(), &rm)
	if rm.GetCert().GetThreadId() != ids[0] || len(rm.GetRecord().GetReedIds()) != 3 {
		t.Fatalf("unexpected removal: %v", &rm)
	}
	for _, id := range ids {
		result, err := ds.GetReedOrRemovalCert(ctx, id)
		if err != nil || result.ThreadRemoval == nil || result.Reed != nil {
			t.Fatalf("part %s still served: %+v (%v)", id, result, err)
		}
	}

	if again := f.deleteThread(t, ids[0], sig); again.Code != http.StatusOK {
		t.Fatalf("replay status %d", again.Code)
	}
	if other := f.deleteThread(t, ids[0], f.removalSignature(t, ids[0], "another record")); other.Code != http.StatusConflict {
		t.Fatalf("conflicting removal status %d, want 409", other.Code)
	}
}

func TestDeleteThread_RefusesRemovalOfAnotherRecord(t *testing.T) {
	f := newThreadFixture(t)
	ids, _ := f.seedSignedThread(t, 2)
	if rr := f.deleteThread(t, ids[0], f.removalSignature(t, ids[0], "not the record")); rr.Code != http.StatusUnauthorized {
		t.Fatalf("status %d, want 401", rr.Code)
	}
}

// Someone holding only a middle part still learns of the removal, on
// catch-up, and acking it clears what they held.
func TestThreadRemoval_ReachesHolderOfOnePart(t *testing.T) {
	f := newThreadFixture(t)
	ctx := context.Background()
	ds := f.h.services.db
	rs := newRealtimeService(ds, newCryptoService(), "")
	ids, threadSig := f.seedSignedThread(t, 3)
	signedUpUser(t, f.h, "u2", "bob")
	bob := string(canonicalID(ds.GetServerID(), "u2"))
	if _, err := ds.AllocateReed(ctx, ids[1], bob); err != nil {
		t.Fatal(err)
	}
	if rr := f.deleteThread(t, ids[0], f.removalSignature(t, ids[0], threadSig)); rr.Code != http.StatusOK {
		t.Fatalf("status %d", rr.Code)
	}

	missing, err := ds.GetMissingThreadRemovals(ctx, bob)
	if err != nil || len(missing) != 1 || missing[0] != ids[0] {
		t.Fatalf("missing thread removals = %v (%v), want the thread", missing, err)
	}
	if err := ds.MarkUserOnline(ctx, bob); err != nil {
		t.Fatal(err)
	}
	holders, err := ds.OnlineThreadHolders(ctx, ids[0])
	if err != nil || len(holders) != 1 || holders[0] != bob {
		t.Fatalf("online holders = %v (%v), want bob", holders, err)
	}

	eventID := generateRealtimeEventID(bob)
	if err := rs.createPendingReedEvent(ctx, eventID, generateRealtimeEventID(bob), bob, threadRemovedEvent, ids[0]); err != nil {
		t.Fatal(err)
	}
	if _, err := ds.MarkEventDispatched(ctx, eventID, bob); err != nil {
		t.Fatal(err)
	}
	rs.handleDataAck(&realtimeClient{userID: bob}, eventID)
	if got := allocatedTo(t, ds, bob); len(got) != 0 {
		t.Fatalf("bob still holds %v after acking the removal", got)
	}
}

// signedThreadRemoval is a thread by the fixture's user on revHomeServerID
// and its removal, all with real signatures.
func signedThreadRemoval(t *testing.T, s signedKeyRevocation, ids []string, boundTo string) threadRemoval {
	t.Helper()
	cryptoSvc := newCryptoService()
	sign := func(payload []byte, armor string) string {
		sig, err := cryptoSvc.sign(string(payload), armor)
		if err != nil {
			t.Fatal(err)
		}
		return sig
	}
	at := time.Now().UTC().Truncate(time.Second)
	recordSig := sign(buildThreadUserPayload(revHomeServerID, ids[0], ids), s.userKP.PrivateKey)
	if boundTo == "" {
		boundTo = recordSig
	}
	certSig := sign(buildThreadRemovalUserPayload(revHomeServerID, ids[0], boundTo), s.userKP.PrivateKey)
	return threadRemoval{
		Cert: threadRemovalWire{
			Type: identityTypeThreadRemoval, ServerID: revHomeServerID, UserID: s.userID, ThreadID: ids[0],
			UserSignature: UserSignature{ID: s.keyID, Armor: certSig},
			ServerSignature: ServerSignature{
				ID:       s.serverKeyID,
				Armor:    sign(buildThreadRemovalServerPayload(revHomeServerID, ids[0], s.keyID, s.serverKP.Fingerprint, certSig, at), s.serverKP.PrivateKey),
				SignedAt: at,
			},
		},
		Record: threadRecordWire{
			Type: identityTypeThread, ServerID: revHomeServerID, UserID: s.userID, ThreadID: ids[0], ReedIDs: ids,
			UserSignature: UserSignature{ID: s.keyID, Armor: recordSig},
			ServerSignature: ServerSignature{
				ID:       s.serverKeyID,
				Armor:    sign(buildThreadServerPayload(revHomeServerID, ids[0], s.keyID, s.serverKP.Fingerprint, recordSig, at), s.serverKP.PrivateKey),
				SignedAt: at,
			},
		},
	}
}

func postThreadRemoval(t *testing.T, h *Handlers, rm threadRemoval) int {
	t.Helper()
	r := protoRequest(http.MethodPost, "/api/federation/relay/thread-removal", pbThreadRemoval(&rm))
	r = r.WithContext(context.WithValue(r.Context(), peerServerIDKey, revHomeServerID))
	rr := httptest.NewRecorder()
	h.ThreadRemovalFromPeer(rr, r)
	return rr.Code
}

func TestThreadRemovalFromPeer(t *testing.T) {
	s := newSignedKeyRevocation(t)
	fake, _ := s.fakeHome(t)
	parts := func() []string {
		ids := make([]string, 3)
		for i := range ids {
			ids[i] = s.userID + "/" + newTestReedID(t)
		}
		return ids
	}

	t.Run("accepted", func(t *testing.T) {
		h, _, _ := s.peerHandlers(t, fake)
		ids := parts()
		if code := postThreadRemoval(t, h, signedThreadRemoval(t, s, ids, "")); code != http.StatusNoContent {
			t.Fatalf("status %d, want 204", code)
		}
		for _, id := range ids {
			if threadID, err := h.services.db.RemovedThreadOf(context.Background(), id); err != nil || threadID != ids[0] {
				t.Fatalf("part %s not removed: %q (%v)", id, threadID, err)
			}
		}
	})

	refused := map[string]func() threadRemoval{
		"bound to another signature": func() threadRemoval {
			return signedThreadRemoval(t, s, parts(), "some other record's signature")
		},
		"record with a reed of another user": func() threadRemoval {
			ids := parts()
			ids[1] = string(canonicalID(revHomeServerID, "eve")) + "/" + newTestReedID(t)
			return signedThreadRemoval(t, s, ids, "")
		},
	}
	for name, build := range refused {
		t.Run(name, func(t *testing.T) {
			h, _, _ := s.peerHandlers(t, fake)
			rm := build()
			if code := postThreadRemoval(t, h, rm); code != http.StatusBadRequest {
				t.Fatalf("status %d, want 400", code)
			}
			if got, _ := h.services.db.GetThreadRemoval(context.Background(), rm.Cert.ThreadID); got != nil {
				t.Fatal("refused removal was stored")
			}
		})
	}
}

// A published thread and its removal each cross to peers once, as the head.
func TestThreadRemoval_PeerStreamCarriesHeadOnce(t *testing.T) {
	f := newThreadFixture(t)
	ctx := context.Background()
	ds := f.h.services.db
	ids, threadSig := f.seedSignedThread(t, 3)
	if _, _, err := ds.ClaimPendingFanout(ctx, ids[0]); err != nil {
		t.Fatal(err)
	}
	if rr := f.deleteThread(t, ids[0], f.removalSignature(t, ids[0], threadSig)); rr.Code != http.StatusOK {
		t.Fatalf("status %d", rr.Code)
	}

	rows, err := ds.db.Query(peerStreamSQL+` SELECT kind, reed_id FROM stream WHERE author_id = $1 ORDER BY at, kind`, f.author)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var items []string
	for rows.Next() {
		var kind int
		var reedID string
		if err := rows.Scan(&kind, &reedID); err != nil {
			t.Fatal(err)
		}
		if reedID != ids[0] {
			t.Fatalf("stream carries part %s, want only the head", reedID)
		}
		items = append(items, reedID)
	}
	if len(items) != 2 {
		t.Fatalf("stream = %v, want the head's creation and its removal", items)
	}
}
