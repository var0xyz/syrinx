//go:build !ops

package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestListReceivedRipples_OwnReedIncluded(t *testing.T) {
	db := openRipplesTestDB(t)
	insertRipplesTestUser(t, db, "author1", "author")
	insertRipplesTestUser(t, db, "commenter1", "commenter")
	insertRipplesTestReed(t, db, "author1", "reed1")
	key := newRippleTestKey(t, db, "commenter1")

	svc := &DataService{db: db, serverID: ripplesTestServerID}
	posted := postTestRipple(t, svc, key, reed1ID, canonicalCommenter1, "nice reed", nil, time.Now())

	list, err := svc.ListReceivedRipples(context.Background(), canonicalAuthor1, 50, "")
	if err != nil {
		t.Fatalf("ListReceivedRipples: %v", err)
	}
	if len(list.Ripples) != 1 || list.Ripples[0].ID != posted.ID {
		t.Fatalf("got %d ripples, want 1 comment on author1's own reed", len(list.Ripples))
	}
	if list.Ripples[0].ReedID != reed1ID {
		t.Errorf("ReedID = %q, want %q", list.Ripples[0].ReedID, reed1ID)
	}
	if list.Ripples[0].ExpiresAt.IsZero() {
		t.Error("ExpiresAt must be populated from the ripples bookkeeping row")
	}
}

func TestListReceivedRipples_ReplyToOwnRippleIncludedOnForeignReed(t *testing.T) {
	db := openRipplesTestDB(t)
	insertRipplesTestUser(t, db, "author1", "author")
	insertRipplesTestUser(t, db, "commenter1", "commenter")
	insertRipplesTestUser(t, db, "commenter2", "commenter2")
	insertRipplesTestReed(t, db, "author1", "reed1")
	key1 := newRippleTestKey(t, db, "commenter1")
	key2 := newRippleTestKey(t, db, "commenter2")

	svc := &DataService{db: db, serverID: ripplesTestServerID}
	root := postTestRipple(t, svc, key1, reed1ID, canonicalCommenter1, "root", nil, time.Now())
	reply := postTestRipple(t, svc, key2, reed1ID, canonicalCommenter2, "reply", &root.ID, time.Now().Add(time.Second))

	list, err := svc.ListReceivedRipples(context.Background(), canonicalCommenter1, 50, "")
	if err != nil {
		t.Fatalf("ListReceivedRipples: %v", err)
	}
	if len(list.Ripples) != 1 || list.Ripples[0].ID != reply.ID {
		t.Fatalf("got %d ripples, want 1 (the reply to commenter1's own root comment)", len(list.Ripples))
	}
}

func TestListReceivedRipples_OwnCommentsExcluded(t *testing.T) {
	db := openRipplesTestDB(t)
	insertRipplesTestUser(t, db, "author1", "author")
	insertRipplesTestUser(t, db, "commenter1", "commenter")
	insertRipplesTestReed(t, db, "author1", "reed1")
	key := newRippleTestKey(t, db, "commenter1")

	svc := &DataService{db: db, serverID: ripplesTestServerID}
	postTestRipple(t, svc, key, reed1ID, canonicalCommenter1, "my own comment", nil, time.Now())

	list, err := svc.ListReceivedRipples(context.Background(), canonicalCommenter1, 50, "")
	if err != nil {
		t.Fatalf("ListReceivedRipples: %v", err)
	}
	if len(list.Ripples) != 0 {
		t.Fatalf("got %d ripples, want 0 — commenter1's own comment must not appear in their own inbox", len(list.Ripples))
	}
}

func TestListReceivedRipples_UnrelatedUserSeesNothing(t *testing.T) {
	db := openRipplesTestDB(t)
	insertRipplesTestUser(t, db, "author1", "author")
	insertRipplesTestUser(t, db, "commenter1", "commenter")
	insertRipplesTestUser(t, db, "bystander", "bystander")
	insertRipplesTestReed(t, db, "author1", "reed1")
	key := newRippleTestKey(t, db, "commenter1")

	svc := &DataService{db: db, serverID: ripplesTestServerID}
	postTestRipple(t, svc, key, reed1ID, canonicalCommenter1, "hello", nil, time.Now())

	list, err := svc.ListReceivedRipples(context.Background(), string(canonicalID(ripplesTestServerID, "bystander")), 50, "")
	if err != nil {
		t.Fatalf("ListReceivedRipples: %v", err)
	}
	if len(list.Ripples) != 0 {
		t.Fatalf("got %d ripples, want 0 — an unrelated user has no reeds and no comments here", len(list.Ripples))
	}
}

func TestListReceivedRipples_ExcludesRemovedParentReed(t *testing.T) {
	db := openRipplesTestDB(t)
	insertRipplesTestUser(t, db, "author1", "author")
	insertRipplesTestUser(t, db, "commenter1", "commenter")
	insertRipplesTestReed(t, db, "author1", "reed1")
	key := newRippleTestKey(t, db, "commenter1")

	svc := &DataService{db: db, serverID: ripplesTestServerID}
	postTestRipple(t, svc, key, reed1ID, canonicalCommenter1, "hello", nil, time.Now())
	insertReedRemoval(t, db, "author1", "reed1")

	list, err := svc.ListReceivedRipples(context.Background(), canonicalAuthor1, 50, "")
	if err != nil {
		t.Fatalf("ListReceivedRipples: %v", err)
	}
	if len(list.Ripples) != 0 {
		t.Fatalf("got %d ripples, want 0 — comments on a removed reed must not appear", len(list.Ripples))
	}
}

func TestListReceivedRipples_ExcludesRemovedAccountParent(t *testing.T) {
	db := openRipplesTestDB(t)
	insertRipplesTestUser(t, db, "author1", "author")
	insertRipplesTestUser(t, db, "commenter1", "commenter")
	insertRipplesTestReed(t, db, "author1", "reed1")
	key := newRippleTestKey(t, db, "commenter1")

	svc := &DataService{db: db, serverID: ripplesTestServerID}
	postTestRipple(t, svc, key, reed1ID, canonicalCommenter1, "hello", nil, time.Now())
	insertAccountRemoval(t, db, "author1")

	list, err := svc.ListReceivedRipples(context.Background(), canonicalAuthor1, 50, "")
	if err != nil {
		t.Fatalf("ListReceivedRipples: %v", err)
	}
	if len(list.Ripples) != 0 {
		t.Fatalf("got %d ripples, want 0 — comments on a removed account's reed must not appear", len(list.Ripples))
	}
}

func TestListReceivedRipples_Pagination(t *testing.T) {
	db := openRipplesTestDB(t)
	insertRipplesTestUser(t, db, "author1", "author")
	insertRipplesTestUser(t, db, "commenter1", "commenter")
	insertRipplesTestReed(t, db, "author1", "reed1")
	key := newRippleTestKey(t, db, "commenter1")

	svc := &DataService{db: db, serverID: ripplesTestServerID}
	base := time.Now().Add(-1 * time.Hour)

	var posted []string
	for i := 0; i < 5; i++ {
		resp := postTestRipple(t, svc, key, reed1ID, canonicalCommenter1, "msg", nil, base.Add(time.Duration(i)*time.Second))
		posted = append(posted, resp.ID)
	}

	page1, err := svc.ListReceivedRipples(context.Background(), canonicalAuthor1, 2, "")
	if err != nil {
		t.Fatalf("page1: %v", err)
	}
	if len(page1.Ripples) != 2 || !page1.HasMore {
		t.Fatalf("page1: got %d ripples, hasMore=%v, want 2/true", len(page1.Ripples), page1.HasMore)
	}

	var seen []string
	for _, r := range page1.Ripples {
		seen = append(seen, r.ID)
	}
	cursor := page1.NextCursor
	for len(seen) < 5 {
		page, err := svc.ListReceivedRipples(context.Background(), canonicalAuthor1, 2, cursor)
		if err != nil {
			t.Fatalf("page fetch: %v", err)
		}
		if len(page.Ripples) == 0 {
			t.Fatal("page fetch returned zero ripples before exhausting all 5")
		}
		for _, r := range page.Ripples {
			seen = append(seen, r.ID)
		}
		cursor = page.NextCursor
		if !page.HasMore {
			break
		}
	}

	if len(seen) != 5 {
		t.Fatalf("total items seen across pages = %d, want 5", len(seen))
	}
	for i, id := range posted {
		if seen[i] != id {
			t.Errorf("position %d: got %q, want %q — duplicate or missing item across pages", i, seen[i], id)
		}
	}
}

func TestListReceivedRipples_ReplyOnOwnReedToOwnCommentNotDuplicated(t *testing.T) {
	db := openRipplesTestDB(t)
	insertRipplesTestUser(t, db, "author1", "author")
	insertRipplesTestReed(t, db, "author1", "reed1")
	key := newRippleTestKey(t, db, "author1")

	svc := &DataService{db: db, serverID: ripplesTestServerID}
	root := postTestRipple(t, svc, key, reed1ID, canonicalAuthor1, "root by reed owner", nil, time.Now())

	list, err := svc.ListReceivedRipples(context.Background(), canonicalAuthor1, 50, "")
	if err != nil {
		t.Fatalf("ListReceivedRipples: %v", err)
	}
	if len(list.Ripples) != 0 {
		t.Fatalf("got %d ripples, want 0 — author1 commenting on their own reed is still their own comment, excluded", len(list.Ripples))
	}
	_ = root
}

func getReceivedRipples(h *Handlers, uid, query string) *httptest.ResponseRecorder {
	url := "/api/ripples"
	if query != "" {
		url += "?" + query
	}
	req := httptest.NewRequest(http.MethodGet, url, nil)
	if uid != "" {
		req = withRippleUID(req, uid)
	}
	rr := httptest.NewRecorder()
	h.GetReceivedRipples(rr, req)
	return rr
}

func TestGetReceivedRipples_Handler_Success(t *testing.T) {
	db := openRipplesTestDB(t)
	insertRipplesTestUser(t, db, "author1", "author1")
	insertRipplesTestUser(t, db, "commenter1", "commenter1")
	insertRipplesTestReed(t, db, "author1", "reed1")
	key := newRippleTestKey(t, db, "commenter1")
	svc := &DataService{db: db, serverID: ripplesTestServerID}
	h := ripplesTestHandlers(svc)

	posted := postTestRipple(t, svc, key, reed1ID, canonicalCommenter1, "hello", nil, time.Now())

	rr := getReceivedRipples(h, canonicalAuthor1, "")
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", rr.Code, rr.Body.String())
	}
	var body receivedRippleListResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.Ripples) != 1 || body.Ripples[0].Hash != posted.ID {
		t.Fatalf("got %+v, want exactly the one ripple posted on author1's reed", body)
	}
	if body.Ripples[0].ReedID != reed1ID {
		t.Errorf("ReedID = %q, want %q", body.Ripples[0].ReedID, reed1ID)
	}
	if body.Ripples[0].ReedAuthorID != canonicalAuthor1 {
		t.Errorf("ReedAuthorID = %q, want %q", body.Ripples[0].ReedAuthorID, canonicalAuthor1)
	}
}

func TestGetReceivedRipples_Handler_Empty(t *testing.T) {
	db := openRipplesTestDB(t)
	insertRipplesTestUser(t, db, "author1", "author1")
	svc := &DataService{db: db, serverID: ripplesTestServerID}
	h := ripplesTestHandlers(svc)

	rr := getReceivedRipples(h, canonicalAuthor1, "")
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", rr.Code, rr.Body.String())
	}
	var body receivedRippleListResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.Ripples) != 0 || body.HasMore {
		t.Errorf("unexpected non-empty list: %+v", body)
	}
}

func TestGetReceivedRipples_Handler_InvalidCursor(t *testing.T) {
	db := openRipplesTestDB(t)
	insertRipplesTestUser(t, db, "author1", "author1")
	svc := &DataService{db: db, serverID: ripplesTestServerID}
	h := ripplesTestHandlers(svc)

	rr := getReceivedRipples(h, canonicalAuthor1, "before=not-a-cursor")
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (body: %s)", rr.Code, rr.Body.String())
	}
}

func TestGetReceivedRipples_Handler_InvalidLimit(t *testing.T) {
	db := openRipplesTestDB(t)
	insertRipplesTestUser(t, db, "author1", "author1")
	svc := &DataService{db: db, serverID: ripplesTestServerID}
	h := ripplesTestHandlers(svc)

	rr := getReceivedRipples(h, canonicalAuthor1, "limit=0")
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (body: %s)", rr.Code, rr.Body.String())
	}
}
