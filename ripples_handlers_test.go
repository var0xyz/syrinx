//go:build !ops

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"syrinx/crypto"
	"syrinx/realtime"

	"github.com/google/uuid"
	"github.com/gorilla/mux"
)

func withRippleUID(r *http.Request, uid string) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), userIDKey, uid))
}

func withRippleVars(r *http.Request, vars map[string]string) *http.Request {
	return mux.SetURLVars(r, vars)
}

// ripplesTestHandlers builds *Handlers with a real, freshly generated
// server signing key so h.countersign works end to end in HTTP-level
// tests. broadcastChan is a real buffered channel (matching main.go's
// capacity) rather than the zero value — PostRipple/DeleteRipple send
// onto it synchronously, and a nil channel would block that send forever
// since nothing here runs RealtimeService to drain it.
func ripplesTestHandlers(db *DataService) *Handlers {
	svc := crypto.NewService()
	kp, err := svc.CreateKeyPair("test-server", "", "")
	if err != nil {
		panic(err)
	}
	return &Handlers{
		services:      &Services{db: db, crypto: svc, log: NewLoggingService()},
		broadcastChan: make(chan realtime.BroadcastMessage, 100),
		signingKey: ServerSigningKey{
			Fingerprint: kp.Fingerprint,
			Armor:       kp.PrivateKey,
		},
	}
}

// postRippleRequestBody builds the JSON body PostRipple expects, signing
// content client-side exactly as the SPA would. If replyingTo is set and
// threadID is empty, threadID is resolved from replyingTo's ripple. reedID
// is canonical.
func postRippleRequestBody(t *testing.T, db *DataService, key rippleTestKey, reedID, rippleAuthorID, content, threadID string, replyingTo *string) postRippleRequest {
	t.Helper()
	if threadID == "" {
		if replyingTo != nil {
			threadID = mustRippleThreadID(t, db, *replyingTo)
		} else {
			threadID = uuid.NewString()
		}
	}
	replyingToVal := ""
	if replyingTo != nil {
		replyingToVal = *replyingTo
	}
	userSig := signRippleUserPayload(t, key, reedID, rippleAuthorID, threadID, replyingToVal, content)
	return postRippleRequest{
		Content:       content,
		ThreadID:      threadID,
		ReplyingTo:    replyingTo,
		Proof:         testReedServerSignature,
		Fingerprint:   key.Fingerprint,
		UserSignature: userSig,
	}
}

func postRipple(h *Handlers, uid, userID, reedID string, body any) *httptest.ResponseRecorder {
	var buf bytes.Buffer
	_ = json.NewEncoder(&buf).Encode(body)
	req := httptest.NewRequest(http.MethodPost, "/api/reeds/"+userID+"/"+reedID+"/ripples", &buf)
	req = withRippleVars(req, map[string]string{"userID": userID, "reedID": reedID})
	if uid != "" {
		req = withRippleUID(req, uid)
	}
	rr := httptest.NewRecorder()
	h.PostRipple(rr, req)
	return rr
}

func decodeRippleWire(t *testing.T, rr *httptest.ResponseRecorder) RippleWire {
	t.Helper()
	var w RippleWire
	if err := json.Unmarshal(rr.Body.Bytes(), &w); err != nil {
		t.Fatalf("decode response wire: %v (body: %s)", err, rr.Body.String())
	}
	return w
}

func TestPostRipple_Handler_Success(t *testing.T) {
	db := openRipplesTestDB(t)
	insertRipplesTestUser(t, db, "author1", "author1")
	insertRipplesTestUser(t, db, "commenter1", "commenter1")
	insertRipplesTestReed(t, db, "author1", "reed1")
	key := newRippleTestKey(t, db, "commenter1")
	svc := &DataService{db: db, serverID: ripplesTestServerID}
	h := ripplesTestHandlers(svc)

	body := postRippleRequestBody(t, svc, key, reed1ID, canonicalCommenter1, "hello", "", nil)
	rr := postRipple(h, canonicalCommenter1, canonicalAuthor1, "reed1", body)
	if rr.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201 (body: %s)", rr.Code, rr.Body.String())
	}
	w := decodeRippleWire(t, rr)
	if w.Hash == "" || w.ThreadID == "" {
		t.Errorf("expected non-empty Hash/ThreadID, got %+v", w)
	}
	if w.UserID != canonicalCommenter1 || w.Content != "hello" || w.Deleted {
		t.Errorf("unexpected wire shape: %+v", w)
	}
	if w.UserSignature.Fingerprint != key.CanonicalFingerprint || w.UserSignature.Armor != body.UserSignature {
		t.Errorf("userSignature not echoed correctly: %+v", w.UserSignature)
	}
	if w.ServerSignature.Armor == "" || w.ServerSignature.Fingerprint == "" {
		t.Errorf("serverSignature missing: %+v", w.ServerSignature)
	}
}

func TestPostRipple_Handler_InvalidSignature(t *testing.T) {
	db := openRipplesTestDB(t)
	insertRipplesTestUser(t, db, "author1", "author1")
	insertRipplesTestUser(t, db, "commenter1", "commenter1")
	insertRipplesTestReed(t, db, "author1", "reed1")
	key := newRippleTestKey(t, db, "commenter1")
	svc := &DataService{db: db, serverID: ripplesTestServerID}
	h := ripplesTestHandlers(svc)

	body := postRippleRequestBody(t, svc, key, reed1ID, canonicalCommenter1, "hello", "", nil)
	body.Content = "tampered after signing"
	rr := postRipple(h, canonicalCommenter1, canonicalAuthor1, "reed1", body)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rr.Code)
	}
}

func TestPostRipple_Handler_UnknownFingerprint(t *testing.T) {
	db := openRipplesTestDB(t)
	insertRipplesTestUser(t, db, "author1", "author1")
	insertRipplesTestUser(t, db, "commenter1", "commenter1")
	insertRipplesTestReed(t, db, "author1", "reed1")
	svc := &DataService{db: db, serverID: ripplesTestServerID}
	h := ripplesTestHandlers(svc)

	rr := postRipple(h, canonicalCommenter1, canonicalAuthor1, "reed1", postRippleRequest{
		Content:       "hello",
		ThreadID:      uuid.NewString(),
		Proof:         testReedServerSignature,
		Fingerprint:   "nonexistent-fingerprint",
		UserSignature: "bm90LWEtcmVhbC1zaWc=",
	})
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rr.Code)
	}
}

func TestPostRipple_Handler_TooLong(t *testing.T) {
	db := openRipplesTestDB(t)
	insertRipplesTestUser(t, db, "author1", "author1")
	insertRipplesTestUser(t, db, "commenter1", "commenter1")
	insertRipplesTestReed(t, db, "author1", "reed1")
	svc := &DataService{db: db, serverID: ripplesTestServerID}
	h := ripplesTestHandlers(svc)

	long := make([]byte, 141)
	for i := range long {
		long[i] = 'a'
	}
	rr := postRipple(h, canonicalCommenter1, canonicalAuthor1, "reed1", postRippleRequest{
		Content:  string(long),
		ThreadID: uuid.NewString(),
		Proof:    testReedServerSignature,
	})
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rr.Code)
	}
}

func TestPostRipple_Handler_Empty(t *testing.T) {
	db := openRipplesTestDB(t)
	insertRipplesTestUser(t, db, "author1", "author1")
	insertRipplesTestUser(t, db, "commenter1", "commenter1")
	insertRipplesTestReed(t, db, "author1", "reed1")
	svc := &DataService{db: db, serverID: ripplesTestServerID}
	h := ripplesTestHandlers(svc)

	rr := postRipple(h, canonicalCommenter1, canonicalAuthor1, "reed1", postRippleRequest{
		Content:  "   ",
		ThreadID: uuid.NewString(),
		Proof:    testReedServerSignature,
	})
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rr.Code)
	}
}

func TestPostRipple_Handler_InvalidThreadID(t *testing.T) {
	db := openRipplesTestDB(t)
	insertRipplesTestUser(t, db, "author1", "author1")
	insertRipplesTestUser(t, db, "commenter1", "commenter1")
	insertRipplesTestReed(t, db, "author1", "reed1")
	svc := &DataService{db: db, serverID: ripplesTestServerID}
	h := ripplesTestHandlers(svc)

	rr := postRipple(h, canonicalCommenter1, canonicalAuthor1, "reed1", postRippleRequest{
		Content:  "hi",
		ThreadID: "not-a-uuid",
		Proof:    testReedServerSignature,
	})
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rr.Code)
	}
}

func TestPostRipple_Handler_MissingParent(t *testing.T) {
	db := openRipplesTestDB(t)
	svc := &DataService{db: db, serverID: ripplesTestServerID}
	h := ripplesTestHandlers(svc)

	rr := postRipple(h, canonicalCommenter1, canonicalAuthor1, "reed1", postRippleRequest{
		Content:  "hi",
		ThreadID: uuid.NewString(),
	})
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rr.Code)
	}
}

func TestPostRipple_Handler_RemovedParentReed(t *testing.T) {
	db := openRipplesTestDB(t)
	insertRipplesTestUser(t, db, "author1", "author1")
	insertRipplesTestUser(t, db, "commenter1", "commenter1")
	insertRipplesTestReed(t, db, "author1", "reed1")
	insertReedRemoval(t, db, "author1", "reed1")
	svc := &DataService{db: db, serverID: ripplesTestServerID}
	h := ripplesTestHandlers(svc)

	rr := postRipple(h, canonicalCommenter1, canonicalAuthor1, "reed1", postRippleRequest{
		Content:  "hi",
		ThreadID: uuid.NewString(),
	})
	if rr.Code != http.StatusGone {
		t.Fatalf("status = %d, want 410 (body: %s)", rr.Code, rr.Body.String())
	}
}

func TestPostRipple_Handler_BlankEchoParent(t *testing.T) {
	db := openRipplesTestDB(t)
	insertRipplesTestUser(t, db, "author1", "author1")
	insertRipplesTestUser(t, db, "commenter1", "commenter1")
	insertRipplesTestReed(t, db, "author1", "reed1")
	markReedBlankEcho(t, db, "author1", "reed1")
	svc := &DataService{db: db, serverID: ripplesTestServerID}
	h := ripplesTestHandlers(svc)

	rr := postRipple(h, canonicalCommenter1, canonicalAuthor1, "reed1", postRippleRequest{
		Content:  "hi",
		ThreadID: uuid.NewString(),
	})
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (body: %s)", rr.Code, rr.Body.String())
	}
}

func TestPostRipple_Handler_MissingProof(t *testing.T) {
	db := openRipplesTestDB(t)
	insertRipplesTestUser(t, db, "author1", "author1")
	insertRipplesTestUser(t, db, "commenter1", "commenter1")
	insertRipplesTestReed(t, db, "author1", "reed1")
	svc := &DataService{db: db, serverID: ripplesTestServerID}
	h := ripplesTestHandlers(svc)

	rr := postRipple(h, canonicalCommenter1, canonicalAuthor1, "reed1", postRippleRequest{
		Content:  "hi",
		ThreadID: uuid.NewString(),
	})
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (body: %s)", rr.Code, rr.Body.String())
	}
}

func TestPostRipple_Handler_WrongProof(t *testing.T) {
	db := openRipplesTestDB(t)
	insertRipplesTestUser(t, db, "author1", "author1")
	insertRipplesTestUser(t, db, "commenter1", "commenter1")
	insertRipplesTestReed(t, db, "author1", "reed1")
	svc := &DataService{db: db, serverID: ripplesTestServerID}
	h := ripplesTestHandlers(svc)

	rr := postRipple(h, canonicalCommenter1, canonicalAuthor1, "reed1", postRippleRequest{
		Content:  "hi",
		ThreadID: uuid.NewString(),
		Proof:    "not-the-right-signature",
	})
	if rr.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 (body: %s)", rr.Code, rr.Body.String())
	}
}

func TestGetRipples_Handler_BlankEchoParent(t *testing.T) {
	db := openRipplesTestDB(t)
	insertRipplesTestUser(t, db, "author1", "author1")
	insertRipplesTestReed(t, db, "author1", "reed1")
	markReedBlankEcho(t, db, "author1", "reed1")
	svc := &DataService{db: db, serverID: ripplesTestServerID}
	h := ripplesTestHandlers(svc)

	rr := getRipples(h, canonicalCommenter1, canonicalAuthor1, "reed1", "", testReedServerSignature)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (body: %s)", rr.Code, rr.Body.String())
	}
}

func TestGetRipples_Handler_MissingProof(t *testing.T) {
	db := openRipplesTestDB(t)
	insertRipplesTestUser(t, db, "author1", "author1")
	insertRipplesTestReed(t, db, "author1", "reed1")
	svc := &DataService{db: db, serverID: ripplesTestServerID}
	h := ripplesTestHandlers(svc)

	rr := getRipples(h, canonicalCommenter1, canonicalAuthor1, "reed1", "", "")
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (body: %s)", rr.Code, rr.Body.String())
	}
}

func TestGetRipples_Handler_WrongProof(t *testing.T) {
	db := openRipplesTestDB(t)
	insertRipplesTestUser(t, db, "author1", "author1")
	insertRipplesTestReed(t, db, "author1", "reed1")
	svc := &DataService{db: db, serverID: ripplesTestServerID}
	h := ripplesTestHandlers(svc)

	rr := getRipples(h, canonicalCommenter1, canonicalAuthor1, "reed1", "", "not-the-right-signature")
	if rr.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 (body: %s)", rr.Code, rr.Body.String())
	}
}

func TestGetRipples_Handler_MissingReedNotFound(t *testing.T) {
	db := openRipplesTestDB(t)
	insertRipplesTestUser(t, db, "author1", "author1")
	svc := &DataService{db: db, serverID: ripplesTestServerID}
	h := ripplesTestHandlers(svc)

	rr := getRipples(h, canonicalCommenter1, canonicalAuthor1, "no-such-reed", "", testReedServerSignature)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (body: %s)", rr.Code, rr.Body.String())
	}
}

func TestPostRipple_Handler_RemovedAccountParent(t *testing.T) {
	db := openRipplesTestDB(t)
	insertRipplesTestUser(t, db, "author1", "author1")
	insertRipplesTestUser(t, db, "commenter1", "commenter1")
	insertAccountRemoval(t, db, "author1")
	svc := &DataService{db: db, serverID: ripplesTestServerID}
	h := ripplesTestHandlers(svc)

	rr := postRipple(h, canonicalCommenter1, canonicalAuthor1, "reed1", postRippleRequest{
		Content:  "hi",
		ThreadID: uuid.NewString(),
	})
	if rr.Code != http.StatusGone {
		t.Fatalf("status = %d, want 410 (body: %s)", rr.Code, rr.Body.String())
	}
}

func TestPostRipple_Handler_ReplyingToDifferentReed(t *testing.T) {
	db := openRipplesTestDB(t)
	insertRipplesTestUser(t, db, "author1", "author1")
	insertRipplesTestUser(t, db, "author2", "author2")
	insertRipplesTestUser(t, db, "commenter1", "commenter1")
	insertRipplesTestReed(t, db, "author1", "reed1")
	insertRipplesTestReed(t, db, "author2", "reed2")
	key := newRippleTestKey(t, db, "commenter1")
	svc := &DataService{db: db, serverID: ripplesTestServerID}
	h := ripplesTestHandlers(svc)

	other := postTestRipple(t, svc, key, reed2ID, canonicalCommenter1, "elsewhere", nil, time.Now())

	body := postRippleRequestBody(t, svc, key, reed1ID, canonicalCommenter1, "reply", "", &other.ID)
	rr := postRipple(h, canonicalCommenter1, canonicalAuthor1, "reed1", body)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (reply targets a different reed)", rr.Code)
	}
}

func TestPostRipple_Handler_ThreadMismatch(t *testing.T) {
	db := openRipplesTestDB(t)
	insertRipplesTestUser(t, db, "author1", "author1")
	insertRipplesTestUser(t, db, "commenter1", "commenter1")
	insertRipplesTestReed(t, db, "author1", "reed1")
	key := newRippleTestKey(t, db, "commenter1")
	svc := &DataService{db: db, serverID: ripplesTestServerID}
	h := ripplesTestHandlers(svc)

	root := postTestRipple(t, svc, key, reed1ID, canonicalCommenter1, "root", nil, time.Now())

	body := postRippleRequestBody(t, svc, key, reed1ID, canonicalCommenter1, "reply", uuid.NewString(), &root.ID)
	rr := postRipple(h, canonicalCommenter1, canonicalAuthor1, "reed1", body)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (threadID mismatch with parent)", rr.Code)
	}
}

func TestPostRipple_Handler_ReplyingToSoftDeletedResponse(t *testing.T) {
	db := openRipplesTestDB(t)
	insertRipplesTestUser(t, db, "author1", "author1")
	insertRipplesTestUser(t, db, "commenter1", "commenter1")
	insertRipplesTestReed(t, db, "author1", "reed1")
	key := newRippleTestKey(t, db, "commenter1")
	svc := &DataService{db: db, serverID: ripplesTestServerID}
	h := ripplesTestHandlers(svc)

	root := postTestRipple(t, svc, key, reed1ID, canonicalCommenter1, "root", nil, time.Now())
	if _, _, err := svc.SoftDeleteRipple(context.Background(), root.ID, canonicalCommenter1); err != nil {
		t.Fatalf("seed SoftDeleteRipple: %v", err)
	}

	body := postRippleRequestBody(t, svc, key, reed1ID, canonicalCommenter1, "reply", "", &root.ID)
	rr := postRipple(h, canonicalCommenter1, canonicalAuthor1, "reed1", body)
	if rr.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201 (replying to a tombstone is allowed) (body: %s)", rr.Code, rr.Body.String())
	}
}

// testReedServerSignature is the server-signature armor
// insertRipplesTestReed/insertRipplesTestUser always store (both hardcode
// the literal 'sig' row) — the correct proof-of-possession value for any
// reed built by this test file's helpers.
const testReedServerSignature = "sig"

// getRipples issues a QUERY request against GetRipples with proof as the
// body (proof-of-possession of the parent reed — see checkReedPossession).
// Pass testReedServerSignature for the happy path, or an empty/wrong value
// to exercise the 400/403 cases.
func getRipples(h *Handlers, uid, userID, reedID, query, proof string) *httptest.ResponseRecorder {
	url := "/api/reeds/" + userID + "/" + reedID + "/ripples"
	if query != "" {
		url += "?" + query
	}
	req := httptest.NewRequest("QUERY", url, bytes.NewBufferString(proof))
	req = withRippleVars(req, map[string]string{"userID": userID, "reedID": reedID})
	if uid != "" {
		req = withRippleUID(req, uid)
	}
	rr := httptest.NewRecorder()
	h.GetRipples(rr, req)
	return rr
}

func TestGetRipples_Handler_Empty(t *testing.T) {
	db := openRipplesTestDB(t)
	insertRipplesTestUser(t, db, "author1", "author1")
	insertRipplesTestReed(t, db, "author1", "reed1")
	svc := &DataService{db: db, serverID: ripplesTestServerID}
	h := ripplesTestHandlers(svc)

	rr := getRipples(h, canonicalCommenter1, canonicalAuthor1, "reed1", "", testReedServerSignature)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", rr.Code, rr.Body.String())
	}
	var body rippleListResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.Responses) != 0 || body.HasMore {
		t.Errorf("unexpected non-empty list: %+v", body)
	}
}

func TestGetRipples_Handler_IncludesTombstonesAndRemovedAccounts(t *testing.T) {
	db := openRipplesTestDB(t)
	insertRipplesTestUser(t, db, "author1", "author1")
	insertRipplesTestUser(t, db, "commenter1", "commenter1")
	insertRipplesTestUser(t, db, "commenter2", "commenter2")
	insertRipplesTestReed(t, db, "author1", "reed1")
	key1 := newRippleTestKey(t, db, "commenter1")
	key2 := newRippleTestKey(t, db, "commenter2")
	svc := &DataService{db: db, serverID: ripplesTestServerID}
	h := ripplesTestHandlers(svc)

	base := time.Now().Add(-1 * time.Hour)
	deleted := postTestRipple(t, svc, key1, reed1ID, canonicalCommenter1, "will be deleted", nil, base)
	if _, _, err := svc.SoftDeleteRipple(context.Background(), deleted.ID, canonicalCommenter1); err != nil {
		t.Fatalf("seed delete: %v", err)
	}
	postTestRipple(t, svc, key2, reed1ID, canonicalCommenter2, "removed account", nil, base.Add(time.Second))
	insertAccountRemoval(t, db, "commenter2")

	rr := getRipples(h, canonicalCommenter1, canonicalAuthor1, "reed1", "", testReedServerSignature)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", rr.Code, rr.Body.String())
	}
	var body rippleListResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.Responses) != 2 {
		t.Fatalf("got %d responses, want 2", len(body.Responses))
	}
	if !body.Responses[0].Deleted || body.Responses[0].Content != "[DELETED]" {
		t.Errorf("first response not a tombstone: %+v", body.Responses[0])
	}
	if body.Responses[0].UserSignature.Armor == "" {
		t.Error("tombstoned response must still carry its original userSignature")
	}
	if body.Responses[1].UserID != canonicalCommenter2 || body.Responses[1].Deleted {
		t.Errorf("removed-account response not rendered unfiltered: %+v", body.Responses[1])
	}
}

func TestGetRipples_Handler_OnRemovedAccountParent(t *testing.T) {
	db := openRipplesTestDB(t)
	insertRipplesTestUser(t, db, "author1", "author1")
	insertAccountRemoval(t, db, "author1")
	svc := &DataService{db: db, serverID: ripplesTestServerID}
	h := ripplesTestHandlers(svc)

	rr := getRipples(h, canonicalCommenter1, canonicalAuthor1, "reed1", "", "")
	if rr.Code != http.StatusGone {
		t.Fatalf("status = %d, want 410", rr.Code)
	}
}

func TestGetRipples_Handler_Pagination(t *testing.T) {
	db := openRipplesTestDB(t)
	insertRipplesTestUser(t, db, "author1", "author1")
	insertRipplesTestUser(t, db, "commenter1", "commenter1")
	insertRipplesTestReed(t, db, "author1", "reed1")
	key := newRippleTestKey(t, db, "commenter1")
	svc := &DataService{db: db, serverID: ripplesTestServerID}
	h := ripplesTestHandlers(svc)

	base := time.Now().Add(-1 * time.Hour)
	for i := 0; i < 3; i++ {
		postTestRipple(t, svc, key, reed1ID, canonicalCommenter1, "msg", nil, base.Add(time.Duration(i)*time.Second))
	}

	rr1 := getRipples(h, canonicalCommenter1, canonicalAuthor1, "reed1", "limit=2", testReedServerSignature)
	var page1 rippleListResponse
	if err := json.Unmarshal(rr1.Body.Bytes(), &page1); err != nil {
		t.Fatalf("decode page1: %v", err)
	}
	if len(page1.Responses) != 2 || !page1.HasMore || page1.NextCursor == "" {
		t.Fatalf("page1 = %+v, want 2 items/hasMore=true/non-empty cursor", page1)
	}

	rr2 := getRipples(h, canonicalCommenter1, canonicalAuthor1, "reed1", "limit=2&before="+page1.NextCursor, testReedServerSignature)
	if rr2.Code != http.StatusOK {
		t.Fatalf("page2 status = %d (body: %s)", rr2.Code, rr2.Body.String())
	}
	var page2 rippleListResponse
	if err := json.Unmarshal(rr2.Body.Bytes(), &page2); err != nil {
		t.Fatalf("decode page2: %v", err)
	}
	if len(page2.Responses) != 1 || page2.HasMore {
		t.Fatalf("page2 = %+v, want 1 item/hasMore=false", page2)
	}
}

func TestGetRipples_Handler_InvalidCursor(t *testing.T) {
	db := openRipplesTestDB(t)
	insertRipplesTestUser(t, db, "author1", "author1")
	insertRipplesTestReed(t, db, "author1", "reed1")
	svc := &DataService{db: db, serverID: ripplesTestServerID}
	h := ripplesTestHandlers(svc)

	rr := getRipples(h, canonicalCommenter1, canonicalAuthor1, "reed1", "before=not-valid-base64!!!", testReedServerSignature)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rr.Code)
	}
}

func deleteRipple(h *Handlers, uid, rippleID string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodDelete, "/api/ripples/"+rippleID, nil)
	req = withRippleVars(req, map[string]string{"rippleID": rippleID})
	if uid != "" {
		req = withRippleUID(req, uid)
	}
	rr := httptest.NewRecorder()
	h.DeleteRipple(rr, req)
	return rr
}

func TestDeleteRipple_Handler_Own(t *testing.T) {
	db := openRipplesTestDB(t)
	insertRipplesTestUser(t, db, "author1", "author1")
	insertRipplesTestUser(t, db, "commenter1", "commenter1")
	insertRipplesTestReed(t, db, "author1", "reed1")
	key := newRippleTestKey(t, db, "commenter1")
	svc := &DataService{db: db, serverID: ripplesTestServerID}
	h := ripplesTestHandlers(svc)

	resp := postTestRipple(t, svc, key, reed1ID, canonicalCommenter1, "hi", nil, time.Now())

	rr := deleteRipple(h, canonicalCommenter1, resp.ID)
	if rr.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204 (body: %s)", rr.Code, rr.Body.String())
	}

	got, err := svc.GetRipple(context.Background(), resp.ID)
	if err != nil {
		t.Fatalf("GetRipple: %v", err)
	}
	if !got.Deleted || got.Content != "[DELETED]" {
		t.Errorf("not soft-deleted after 204: %+v", got)
	}
}

func TestDeleteRipple_Handler_Others(t *testing.T) {
	db := openRipplesTestDB(t)
	insertRipplesTestUser(t, db, "author1", "author1")
	insertRipplesTestUser(t, db, "commenter1", "commenter1")
	insertRipplesTestUser(t, db, "commenter2", "commenter2")
	insertRipplesTestReed(t, db, "author1", "reed1")
	key := newRippleTestKey(t, db, "commenter1")
	svc := &DataService{db: db, serverID: ripplesTestServerID}
	h := ripplesTestHandlers(svc)

	resp := postTestRipple(t, svc, key, reed1ID, canonicalCommenter1, "hi", nil, time.Now())

	rr := deleteRipple(h, canonicalCommenter2, resp.ID)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rr.Code)
	}
}

func TestDeleteRipple_Handler_Missing(t *testing.T) {
	db := openRipplesTestDB(t)
	svc := &DataService{db: db, serverID: ripplesTestServerID}
	h := ripplesTestHandlers(svc)

	rr := deleteRipple(h, canonicalCommenter1, "nonexistent")
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rr.Code)
	}
}

func TestDeleteRipple_Handler_AlreadyDeletedIdempotent(t *testing.T) {
	db := openRipplesTestDB(t)
	insertRipplesTestUser(t, db, "author1", "author1")
	insertRipplesTestUser(t, db, "commenter1", "commenter1")
	insertRipplesTestReed(t, db, "author1", "reed1")
	key := newRippleTestKey(t, db, "commenter1")
	svc := &DataService{db: db, serverID: ripplesTestServerID}
	h := ripplesTestHandlers(svc)

	resp := postTestRipple(t, svc, key, reed1ID, canonicalCommenter1, "hi", nil, time.Now())

	if rr := deleteRipple(h, canonicalCommenter1, resp.ID); rr.Code != http.StatusNoContent {
		t.Fatalf("first delete status = %d", rr.Code)
	}
	if rr := deleteRipple(h, canonicalCommenter1, resp.ID); rr.Code != http.StatusNoContent {
		t.Fatalf("second delete status = %d, want 204 (idempotent)", rr.Code)
	}
}
