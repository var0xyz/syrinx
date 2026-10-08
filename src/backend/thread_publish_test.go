//go:build !ops

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sort"
	"testing"
)

// threadFixture is a server with one signed-up author whose key signs
// thread records for real.
type threadFixture struct {
	h      *Handlers
	author string
	kp     cryptoKeyPair
}

func newThreadFixture(t *testing.T) *threadFixture {
	t.Helper()
	db := newTestDatabase(t, InitDB)
	h := newSignupGateHandlers(t, db, AppConfig{ServerName: "test", SignupMode: "open"})
	kp := signedUpUser(t, h, "u1", "alice")
	return &threadFixture{h: h, author: string(canonicalID(h.services.db.GetServerID(), "u1")), kp: kp}
}

// ids returns n reed IDs for the author, ascending.
func (f *threadFixture) ids(t *testing.T, n int) []string {
	t.Helper()
	ids := make([]string, n)
	for i := range ids {
		ids[i] = f.author + "/" + newTestReedID(t)
	}
	return ids
}

// request builds a body for ids, with the thread record signed over signed.
func (f *threadFixture) request(t *testing.T, ids, signed []string) createThreadRequest {
	t.Helper()
	payload := buildThreadUserPayload(f.h.services.db.GetServerID(), signed[0], signed)
	sig, err := f.h.services.crypto.sign(string(payload), f.kp.PrivateKey)
	if err != nil {
		t.Fatal(err)
	}
	req := createThreadRequest{ThreadSignature: sig}
	for i, id := range ids {
		req.Reeds = append(req.Reeds, threadPartRequest{ReedID: id, Signature: "partsig" + string(rune('a'+i))})
	}
	return req
}

func (f *threadFixture) post(t *testing.T, req createThreadRequest) *httptest.ResponseRecorder {
	t.Helper()
	b, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	rr := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/threads", bytes.NewReader(b))
	f.h.CreateThread(rr, withInviteUID(r, f.author))
	return rr
}

func TestCreateThreadHandler_StoresThread(t *testing.T) {
	f := newThreadFixture(t)
	ids := f.ids(t, 3)
	rr := f.post(t, f.request(t, ids, ids))
	if rr.Code != http.StatusCreated {
		t.Fatalf("status %d: %s", rr.Code, rr.Body.String())
	}
	var sigs threadSignatures
	if err := json.Unmarshal(rr.Body.Bytes(), &sigs); err != nil {
		t.Fatal(err)
	}
	if sigs.ServerSignature.Armor == "" || len(sigs.Reeds) != 3 {
		t.Fatalf("unexpected signatures: %+v", sigs)
	}
	rec, err := f.h.services.db.GetThreadRecord(context.Background(), ids[0])
	if err != nil || rec == nil || len(rec.ReedIDs) != 3 {
		t.Fatalf("GetThreadRecord: %+v, %v", rec, err)
	}
	var tip string
	if err := f.h.services.db.db.QueryRow(
		`SELECT id FROM reeds WHERE user_id = $1 ORDER BY signed_at DESC, id DESC LIMIT 1`, f.author,
	).Scan(&tip); err != nil || tip != ids[2] {
		t.Fatalf("tip = %s (%v), want the last part %s", tip, err, ids[2])
	}
}

func TestCreateThreadHandler_Rejects(t *testing.T) {
	f := newThreadFixture(t)
	ids := f.ids(t, 3)
	foreign := append([]string{}, ids...)
	foreign[1] = "bob@" + f.h.services.db.GetServerID() + "/" + newTestReedID(t)
	descending := []string{ids[1], ids[0]}
	reordered := []string{ids[0], ids[2], ids[1]}

	tests := []struct {
		name string
		req  createThreadRequest
	}{
		{"one part", f.request(t, ids[:1], ids[:1])},
		{"thirty-one parts", func() createThreadRequest {
			many := f.ids(t, MaxThreadReeds+1)
			return f.request(t, many, many)
		}()},
		{"another author's reed", f.request(t, foreign, foreign)},
		{"descending IDs", f.request(t, descending, descending)},
		{"signature over another order", f.request(t, ids, reordered)},
		{"duplicate part", f.request(t, []string{ids[0], ids[0]}, []string{ids[0], ids[0]})},
	}
	for _, tc := range tests {
		if rr := f.post(t, tc.req); rr.Code != http.StatusBadRequest {
			t.Errorf("%s: status %d, want 400 (%s)", tc.name, rr.Code, rr.Body.String())
		}
	}
}

func TestCreateThreadHandler_Replay(t *testing.T) {
	f := newThreadFixture(t)
	ids := f.ids(t, 2)
	req := f.request(t, ids, ids)
	first := f.post(t, req)
	if first.Code != http.StatusCreated {
		t.Fatalf("status %d: %s", first.Code, first.Body.String())
	}
	again := f.post(t, req)
	if again.Code != http.StatusOK {
		t.Fatalf("replay status %d: %s", again.Code, again.Body.String())
	}
	var a, b threadSignatures
	_ = json.Unmarshal(first.Body.Bytes(), &a)
	_ = json.Unmarshal(again.Body.Bytes(), &b)
	if a.ServerSignature.Armor != b.ServerSignature.Armor || a.Reeds[1].Armor != b.Reeds[1].Armor {
		t.Fatal("replay returned different signatures")
	}

	req.Reeds[1].Signature = "another"
	if rr := f.post(t, req); rr.Code != http.StatusConflict {
		t.Fatalf("mismatched replay status %d, want 409", rr.Code)
	}
}

// seedThread stores a thread whose parts carry the given tags and mentions.
func (f *threadFixture) seedThread(t *testing.T, tags, mentions [][]string) []string {
	t.Helper()
	ids := f.ids(t, len(tags))
	req := f.request(t, ids, ids)
	for i := range req.Reeds {
		req.Reeds[i].Tags = tags[i]
		req.Reeds[i].Mentions = mentions[i]
	}
	if rr := f.post(t, req); rr.Code != http.StatusCreated {
		t.Fatalf("status %d: %s", rr.Code, rr.Body.String())
	}
	return ids
}

func TestThreadPublish_ClaimsEveryPartOnce(t *testing.T) {
	f := newThreadFixture(t)
	ctx := context.Background()
	db := f.h.services.db
	ids := f.seedThread(t, [][]string{{"a", "b"}, {"b"}, {"c"}}, [][]string{nil, nil, nil})

	if claimed, _, err := db.ClaimPendingFanout(ctx, ids[1]); err != nil || claimed {
		t.Fatalf("a later part claimed fanout on its own: %v, %v", claimed, err)
	}
	claimed, tags, err := db.ClaimPendingFanout(ctx, ids[0])
	if err != nil || !claimed {
		t.Fatalf("head did not claim: %v, %v", claimed, err)
	}
	sort.Strings(tags)
	if len(tags) != 3 || tags[0] != "a" || tags[1] != "b" || tags[2] != "c" {
		t.Fatalf("tags = %v, want the union [a b c]", tags)
	}
	if claimed, _, _ := db.ClaimPendingFanout(ctx, ids[0]); claimed {
		t.Fatal("second claim succeeded")
	}
	var unpublished int
	if err := db.db.QueryRow(`SELECT COUNT(*) FROM reeds WHERE thread_head = $1 AND published_at IS NULL`, ids[0]).Scan(&unpublished); err != nil || unpublished != 0 {
		t.Fatalf("%d parts left unpublished (%v)", unpublished, err)
	}
}

func TestThreadPublish_MentionsOncePerThread(t *testing.T) {
	f := newThreadFixture(t)
	ctx := context.Background()
	db := f.h.services.db
	signedUpUser(t, f.h, "u2", "bob")
	bob := string(canonicalID(db.GetServerID(), "u2"))
	ids := f.seedThread(t, [][]string{nil, nil, nil}, [][]string{{bob}, nil, {bob}})

	missing, err := db.GetMissingMentions(ctx, bob)
	if err != nil {
		t.Fatal(err)
	}
	if len(missing) != 1 || missing[0].ReedID != ids[0] {
		t.Fatalf("missing mentions = %+v, want the head once", missing)
	}

	if _, err := db.db.Exec(`INSERT INTO online_users (user_id) VALUES ($1)`, bob); err != nil {
		t.Fatal(err)
	}
	online, err := db.GetOnlineMentionedUsers(ctx, ids[0])
	if err != nil || len(online) != 1 || online[0] != bob {
		t.Fatalf("online mentioned = %v (%v), want bob once", online, err)
	}
	if online, _ := db.GetOnlineMentionedUsers(ctx, ids[2]); len(online) != 0 {
		t.Fatalf("a later part notified on its own: %v", online)
	}
	mentioned, err := db.GetReedMentions(ctx, ids[0])
	if err != nil || len(mentioned) != 1 {
		t.Fatalf("head mentions = %v (%v), want bob once", mentioned, err)
	}
}
