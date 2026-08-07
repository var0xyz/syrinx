package metrics

import "testing"

func TestUserIDHashStable(t *testing.T) {
	a := UserIDHash("user-abc")
	b := UserIDHash("user-abc")
	if a != b {
		t.Fatalf("hash not stable: %q vs %q", a, b)
	}
	if len(a) != 64 {
		t.Fatalf("expected 64 hex chars, got %d", len(a))
	}
	if UserIDHash("user-xyz") == a {
		t.Fatal("different users must not collide in test sample")
	}
}
