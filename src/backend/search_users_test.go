//go:build !ops && !ripplescleanup && !challengescleanup

package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

func TestMergeUserSearchResults_LocalExactMatchLeads(t *testing.T) {
	local := []UserSearchResult{{ID: "alice@home", Username: "alice"}}
	foreign := []UserSearchResult{{ID: "aalice@peer", Username: "aalice"}, {ID: "alice@peer", Username: "alice"}}

	got, _, hasMore := mergeUserSearchResults("alice", local, foreign, 20, true)

	want := []UserSearchResult{
		{ID: "alice@home", Username: "alice"},
		{ID: "aalice@peer", Username: "aalice"},
		{ID: "alice@peer", Username: "alice"},
	}
	if !reflect.DeepEqual(got, want) || hasMore {
		t.Fatalf("got %+v (hasMore %v), want %+v", got, hasMore, want)
	}
}

func TestMergeUserSearchResults_ForeignExactMatchLeadsWhenNoLocalExactMatch(t *testing.T) {
	local := []UserSearchResult{{ID: "alicia@home", Username: "alicia"}}
	foreign := []UserSearchResult{{ID: "alice@peer", Username: "alice"}, {ID: "aalice@peer", Username: "aalice"}}

	got, _, _ := mergeUserSearchResults("alice", local, foreign, 20, true)

	want := []UserSearchResult{
		{ID: "alice@peer", Username: "alice"},
		{ID: "aalice@peer", Username: "aalice"},
		{ID: "alicia@home", Username: "alicia"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestMergeUserSearchResults_CaseInsensitiveExactMatch(t *testing.T) {
	local := []UserSearchResult{{ID: "malice@home", Username: "Malice"}}
	foreign := []UserSearchResult{{ID: "alice@peer", Username: "ALICE"}}

	got, _, _ := mergeUserSearchResults("alice", local, foreign, 20, true)

	if len(got) != 2 || got[0].Username != "ALICE" {
		t.Fatalf("expected case-insensitive foreign exact match first, got %+v", got)
	}
}

func TestMergeUserSearchResults_SortsAcrossServersCaseInsensitively(t *testing.T) {
	local := []UserSearchResult{{ID: "c@home", Username: "Carol"}, {ID: "a@home", Username: "alice"}}
	foreign := []UserSearchResult{{ID: "b@peer", Username: "Bob"}}

	got, _, _ := mergeUserSearchResults("o", local, foreign, 20, true)

	want := []string{"a@home", "b@peer", "c@home"}
	if ids := userSearchIDs(got); !reflect.DeepEqual(ids, want) {
		t.Fatalf("got %v, want %v", ids, want)
	}
}

func TestMergeUserSearchResults_PageCutAndHasMore(t *testing.T) {
	local := []UserSearchResult{{ID: "1@home", Username: "bob"}, {ID: "2@home", Username: "bobby"}}
	foreign := []UserSearchResult{{ID: "1@peer", Username: "baa"}, {ID: "2@peer", Username: "bab"}, {ID: "3@peer", Username: "bac"}}

	got, _, hasMore := mergeUserSearchResults("b", local, foreign, 2, false)

	if ids := userSearchIDs(got); !reflect.DeepEqual(ids, []string{"1@peer", "2@peer"}) || !hasMore {
		t.Fatalf("got %v (hasMore %v)", ids, hasMore)
	}
}

func TestMergeUserSearchResults_LeadNeverTakesTheLastRow(t *testing.T) {
	local := []UserSearchResult{{ID: "1@home", Username: "zed"}, {ID: "2@home", Username: "azed"}, {ID: "3@home", Username: "bzed"}}

	got, _, hasMore := mergeUserSearchResults("zed", local, nil, 2, true)

	// The next cursor is the last row, so it must be a sorted one.
	if ids := userSearchIDs(got); !reflect.DeepEqual(ids, []string{"1@home", "2@home"}) || !hasMore {
		t.Fatalf("got %v (hasMore %v)", ids, hasMore)
	}

	got, _, _ = mergeUserSearchResults("zed", local, nil, 1, true)
	if ids := userSearchIDs(got); !reflect.DeepEqual(ids, []string{"2@home"}) {
		t.Fatalf("limit 1: got %v", ids)
	}
}

func TestMergeUserSearchResults_ReturnsLeadIDsOnFirstPageOnly(t *testing.T) {
	local := []UserSearchResult{{ID: "1@home", Username: "azed"}, {ID: "2@home", Username: "zed"}}

	_, lead, _ := mergeUserSearchResults("zed", local, nil, 20, true)
	if !reflect.DeepEqual(lead, []string{"2@home"}) {
		t.Fatalf("first page lead = %v", lead)
	}

	got, lead, _ := mergeUserSearchResults("zed", local, nil, 20, false)
	if ids := userSearchIDs(got); len(lead) != 0 || !reflect.DeepEqual(ids, []string{"1@home", "2@home"}) {
		t.Fatalf("later page = %v, lead %v", ids, lead)
	}
}

func TestMergeUserSearchResults_DropsDuplicateIDs(t *testing.T) {
	local := []UserSearchResult{{ID: "1@peer", Username: "alice"}}
	foreign := []UserSearchResult{{ID: "1@peer", Username: "alice"}}

	got, _, _ := mergeUserSearchResults("al", local, foreign, 20, true)

	if len(got) != 1 {
		t.Fatalf("got %+v", got)
	}
}

func TestMergeUserSearchResults_EmptyInputs(t *testing.T) {
	got, _, hasMore := mergeUserSearchResults("alice", nil, nil, 20, true)
	if len(got) != 0 || hasMore {
		t.Fatalf("expected empty result, got %+v", got)
	}
}

func TestUserSearchAfter(t *testing.T) {
	c := userSearchCursor{Username: "Bob", ID: "2@home"}
	cases := []struct {
		u    UserSearchResult
		want bool
	}{
		{UserSearchResult{ID: "1@home", Username: "bob"}, false},
		{UserSearchResult{ID: "2@home", Username: "bob"}, false},
		{UserSearchResult{ID: "3@home", Username: "BOB"}, true},
		{UserSearchResult{ID: "1@home", Username: "bobby"}, true},
		{UserSearchResult{ID: "9@home", Username: "alice"}, false},
	}
	for _, tc := range cases {
		if got := userSearchAfter(tc.u, c); got != tc.want {
			t.Errorf("userSearchAfter(%+v) = %v, want %v", tc.u, got, tc.want)
		}
	}
}

func TestUserSearchCursorRoundTrip(t *testing.T) {
	c := userSearchCursor{Username: "bob", ID: "2@home", Lead: []string{"1@peer"}}
	got, err := decodeUserSearchCursor(encodeUserSearchCursor(c))
	if err != nil || !reflect.DeepEqual(*got, c) {
		t.Fatalf("got %+v, %v", got, err)
	}
	if _, err := decodeUserSearchCursor("not base64!"); err == nil {
		t.Fatal("expected an invalid cursor to fail")
	}
}

func userSearchIDs(rows []UserSearchResult) []string {
	ids := make([]string, len(rows))
	for i, u := range rows {
		ids[i] = u.ID
	}
	return ids
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

	got := h.fanoutUserSearchToPeers(context.Background(), "alice", nil, 20, searchUsersFanoutTimeout)
	if len(got) != 0 {
		t.Fatalf("expected no results with no connected peers, got %+v", got)
	}
}

func TestEscapeLike(t *testing.T) {
	cases := map[string]string{
		"alice": "alice",
		"%":     `\%`,
		"a_c":   `a\_c`,
		`a\b`:   `a\\b`,
		`50%_\`: `50\%\_\\`,
	}
	for in, want := range cases {
		if got := escapeLike(in); got != want {
			t.Errorf("escapeLike(%q) = %q, want %q", in, got, want)
		}
	}
}
