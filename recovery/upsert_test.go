package recovery

import "testing"

func TestCollisionRename_TruncatesLongUsername(t *testing.T) {
	long := make([]byte, 250)
	for i := range long {
		long[i] = 'a'
	}
	got := CollisionRename(string(long), "abcdefgh", 4)
	if len(got) > 255 {
		t.Fatalf("len=%d", len(got))
	}
	if got[len(got)-5:] != "#abcd" {
		t.Fatalf("suffix=%q", got[len(got)-5:])
	}
}
