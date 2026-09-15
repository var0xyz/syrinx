package main

import (
	"reflect"
	"sort"
	"testing"
)

func TestValidateMentionClaims(t *testing.T) {
	tests := []struct {
		name     string
		claims   []string
		authorID string
		want     []ReedRef
	}{
		{"empty", nil, "alice", nil},
		{
			"well formed",
			[]string{"e5F6g7H8@srv1xyz1"},
			"a1B2c3D4",
			[]ReedRef{{ServerID: "srv1xyz1", AuthorID: "e5F6g7H8"}},
		},
		{
			// IDs are not fixed-length — the root user's id is "1".
			"short id: root user",
			[]string{"1@CcODhAr7"},
			"a1B2c3D4",
			[]ReedRef{{ServerID: "CcODhAr7", AuthorID: "1"}},
		},
		{
			"dedup same target",
			[]string{"e5F6g7H8@srv1xyz1", "e5F6g7H8@srv1xyz1"},
			"a1B2c3D4",
			[]ReedRef{{ServerID: "srv1xyz1", AuthorID: "e5F6g7H8"}},
		},
		{
			"multiple distinct targets",
			[]string{"e5F6g7H8@srv1xyz1", "i9J0k1L2@srv1xyz1"},
			"a1B2c3D4",
			[]ReedRef{{ServerID: "srv1xyz1", AuthorID: "e5F6g7H8"}, {ServerID: "srv1xyz1", AuthorID: "i9J0k1L2"}},
		},
		{
			"self mention skipped: authorID is canonical",
			[]string{"a1B2c3D4@srv1xyz1"},
			"a1B2c3D4@srv1xyz1",
			nil,
		},
		{
			"self mention among others still yields others",
			[]string{"a1B2c3D4@srv1xyz1", "e5F6g7H8@srv1xyz1"},
			"a1B2c3D4@srv1xyz1",
			[]ReedRef{{ServerID: "srv1xyz1", AuthorID: "e5F6g7H8"}},
		},
		{
			// Same bare userID on a DIFFERENT server is not a self-mention —
			// canonical comparison must not collapse across servers.
			"same bare userID on a different server is not self",
			[]string{"a1B2c3D4@othersrv"},
			"a1B2c3D4@srv1xyz1",
			[]ReedRef{{ServerID: "othersrv", AuthorID: "a1B2c3D4"}},
		},
		{
			"foreign server accepted, any length",
			[]string{"e5F6g7H8@othrsrv1longername"},
			"a1B2c3D4",
			[]ReedRef{{ServerID: "othrsrv1longername", AuthorID: "e5F6g7H8"}},
		},
		{
			"missing @ separator: not a mention",
			[]string{"e5F6g7H8srv1xyz1"},
			"a1B2c3D4",
			nil,
		},
		{
			"empty userID before @: not a mention",
			[]string{"@srv1xyz1"},
			"a1B2c3D4",
			nil,
		},
		{
			"empty serverID after @: not a mention",
			[]string{"e5F6g7H8@"},
			"a1B2c3D4",
			nil,
		},
		{
			"whitespace trimmed",
			[]string{"  e5F6g7H8@srv1xyz1  "},
			"a1B2c3D4",
			[]ReedRef{{ServerID: "srv1xyz1", AuthorID: "e5F6g7H8"}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ValidateMentionClaims(tt.claims, tt.authorID)
			sort.Slice(got, func(i, j int) bool { return got[i].AuthorID < got[j].AuthorID })
			want := append([]ReedRef(nil), tt.want...)
			sort.Slice(want, func(i, j int) bool { return want[i].AuthorID < want[j].AuthorID })
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("got %+v want %+v", got, want)
			}
		})
	}
}
