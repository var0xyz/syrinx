package main

import (
	"reflect"
	"sort"
	"testing"
)

func TestExtractMentions(t *testing.T) {
	tests := []struct {
		name     string
		content  string
		authorID string
		want     []ReedRef
	}{
		{"empty", "", "alice", nil},
		{"no tokens", "hello world", "a1B2c3D4", nil},
		{
			"well formed",
			"hi ~e5F6g7H8@srv1xyz1 there",
			"a1B2c3D4",
			[]ReedRef{{ServerID: "srv1xyz1", AuthorID: "e5F6g7H8"}},
		},
		{
			// IDs are not fixed-length — the root user's id is "1".
			"short id: root user",
			"ping ~1@CcODhAr7 please",
			"a1B2c3D4",
			[]ReedRef{{ServerID: "CcODhAr7", AuthorID: "1"}},
		},
		{
			"dedup same target",
			"~e5F6g7H8@srv1xyz1 and ~e5F6g7H8@srv1xyz1 again",
			"a1B2c3D4",
			[]ReedRef{{ServerID: "srv1xyz1", AuthorID: "e5F6g7H8"}},
		},
		{
			"multiple distinct targets",
			"~e5F6g7H8@srv1xyz1 ~i9J0k1L2@srv1xyz1",
			"a1B2c3D4",
			[]ReedRef{{ServerID: "srv1xyz1", AuthorID: "e5F6g7H8"}, {ServerID: "srv1xyz1", AuthorID: "i9J0k1L2"}},
		},
		{
			"self mention skipped",
			"~a1B2c3D4@srv1xyz1",
			"a1B2c3D4",
			nil,
		},
		{
			"self mention among others still yields others",
			"~a1B2c3D4@srv1xyz1 ~e5F6g7H8@srv1xyz1",
			"a1B2c3D4",
			[]ReedRef{{ServerID: "srv1xyz1", AuthorID: "e5F6g7H8"}},
		},
		{
			"foreign server accepted, any length",
			"~e5F6g7H8@othrsrv1longername",
			"a1B2c3D4",
			[]ReedRef{{ServerID: "othrsrv1longername", AuthorID: "e5F6g7H8"}},
		},
		{
			"missing @ separator: not a mention",
			"~e5F6g7H8srv1xyz1",
			"a1B2c3D4",
			nil,
		},
		{
			"@ with nothing alphanumeric after it: not a mention",
			"~e5F6g7H8@ rest",
			"a1B2c3D4",
			nil,
		},
		{
			// Server IDs containing punctuation are never mentionable by
			// design — the alphanumeric run simply stops at the hyphen,
			// still yielding a well-formed (shorter) mention.
			"punctuation in candidate serverID stops the run",
			"~e5F6g7H8@some-id",
			"a1B2c3D4",
			[]ReedRef{{ServerID: "some", AuthorID: "e5F6g7H8"}},
		},
		{
			"chained @ right after a serverID run",
			"~e5F6g7H8@srv1xyz1@more",
			"a1B2c3D4",
			[]ReedRef{{ServerID: "srv1xyz1", AuthorID: "e5F6g7H8"}},
		},
		{
			// The SPA reads ~ID@ID~ (closing ~) as strikethrough, not a
			// mention — but the backend has no concept of strikethrough,
			// so it still extracts the mention shape regardless of a
			// trailing '~'. This mismatch is deliberate: the backend's
			// job is "does this content contain a well-formed mention
			// token," not "how did the SPA choose to render it."
			"closing ~ present: backend still extracts the mention",
			"~e5F6g7H8@srv1xyz1~",
			"a1B2c3D4",
			[]ReedRef{{ServerID: "srv1xyz1", AuthorID: "e5F6g7H8"}},
		},
		{
			"pipe hashtag alongside a mention",
			"#tag ~e5F6g7H8@srv1xyz1",
			"a1B2c3D4",
			[]ReedRef{{ServerID: "srv1xyz1", AuthorID: "e5F6g7H8"}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ExtractMentions(tt.content, tt.authorID)
			sort.Slice(got, func(i, j int) bool { return got[i].AuthorID < got[j].AuthorID })
			want := append([]ReedRef(nil), tt.want...)
			sort.Slice(want, func(i, j int) bool { return want[i].AuthorID < want[j].AuthorID })
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("got %+v want %+v", got, want)
			}
		})
	}
}
