//go:build !ops && !ripplescleanup

package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

func TestMergeUserSearchResults_LocalExactMatchKeepsLocalFirst(t *testing.T) {
	local := []UserSearchResult{{ID: "alice@home", Username: "alice"}}
	foreign := []UserSearchResult{{ID: "alice@peer", Username: "alice"}, {ID: "bob@peer", Username: "bob"}}

	got := mergeUserSearchResults("alice", local, foreign)

	want := []UserSearchResult{
		{ID: "alice@home", Username: "alice"},
		{ID: "alice@peer", Username: "alice"},
		{ID: "bob@peer", Username: "bob"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestMergeUserSearchResults_ForeignExactMatchSurfacesFirstWhenNoLocalExactMatch(t *testing.T) {
	local := []UserSearchResult{{ID: "alicia@home", Username: "alicia"}}
	foreign := []UserSearchResult{{ID: "bob@peer", Username: "bob"}, {ID: "alice@peer", Username: "alice"}}

	got := mergeUserSearchResults("alice", local, foreign)

	want := []UserSearchResult{
		{ID: "alice@peer", Username: "alice"}, // foreign exact match first
		{ID: "alicia@home", Username: "alicia"},
		{ID: "bob@peer", Username: "bob"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestMergeUserSearchResults_CaseInsensitiveExactMatch(t *testing.T) {
	local := []UserSearchResult{{ID: "somebody@home", Username: "Somebody"}}
	foreign := []UserSearchResult{{ID: "alice@peer", Username: "ALICE"}}

	got := mergeUserSearchResults("alice", local, foreign)

	if len(got) != 2 || got[0].Username != "ALICE" {
		t.Fatalf("expected case-insensitive foreign exact match first, got %+v", got)
	}
}

func TestMergeUserSearchResults_NoExactMatchAnywhereKeepsLocalThenForeign(t *testing.T) {
	local := []UserSearchResult{{ID: "alicia@home", Username: "alicia"}}
	foreign := []UserSearchResult{{ID: "alicent@peer", Username: "alicent"}}

	got := mergeUserSearchResults("alic", local, foreign)

	want := []UserSearchResult{
		{ID: "alicia@home", Username: "alicia"},
		{ID: "alicent@peer", Username: "alicent"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestMergeUserSearchResults_EmptyInputs(t *testing.T) {
	got := mergeUserSearchResults("alice", nil, nil)
	if len(got) != 0 {
		t.Fatalf("expected empty result, got %+v", got)
	}
}

func TestSearchUsersFromPeer_RejectsNonPeerCaller(t *testing.T) {
	h := newBareRelayTestHandlers("home1234")
	body := `{"query":"alice","limit":20}`
	req := httptest.NewRequest(http.MethodPost, "/api/federation/relay/search-users", strings.NewReader(body))
	rr := httptest.NewRecorder()

	h.SearchUsersFromPeer(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d (no peerServerIDKey in context)", rr.Code, http.StatusUnauthorized)
	}
}

func TestSearchUsersFromPeer_RejectsInvalidBody(t *testing.T) {
	h := newBareRelayTestHandlers("home1234")
	req := withPeer(httptest.NewRequest(http.MethodPost, "/api/federation/relay/search-users", strings.NewReader("not json")), "peer5678")
	rr := httptest.NewRecorder()

	h.SearchUsersFromPeer(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusBadRequest)
	}
}

// TestFanoutUserSearchToPeers_NoPeersReturnsEmpty uses the mentions
// integration schema (mentions_integration_test.go) purely for its
// `servers` table with a self=TRUE row and no peers — ListConnectedPeers
// filters on self=FALSE, so this exercises the genuine "zero connected
// peers" path against a real DB rather than a nil one.
func TestFanoutUserSearchToPeers_NoPeersReturnsEmpty(t *testing.T) {
	db := openMentionsTestDB(t)
	h := &Handlers{
		services: &Services{
			db:  &DataService{db: db, serverID: "testserver"},
			log: NewLoggingService(),
		},
	}

	got := h.fanoutUserSearchToPeers(context.Background(), "alice", 20, searchUsersFanoutTimeout)
	if len(got) != 0 {
		t.Fatalf("expected no results with no connected peers, got %+v", got)
	}
}
