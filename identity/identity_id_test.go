package identity

import "testing"

func TestCanonicalIDRoundTrip(t *testing.T) {
	id := CanonicalID("wxyz9876", "abcd1234")
	if id != "abcd1234@wxyz9876" {
		t.Fatalf("CanonicalID = %q, want %q", id, "abcd1234@wxyz9876")
	}

	userID, serverID, ok := ParseIdentityID(id)
	if !ok {
		t.Fatalf("ParseIdentityID(%q) ok = false, want true", id)
	}
	if userID != "abcd1234" {
		t.Errorf("userID = %q, want %q", userID, "abcd1234")
	}
	if serverID != "wxyz9876" {
		t.Errorf("serverID = %q, want %q", serverID, "wxyz9876")
	}

	if got := id.UserID(); got != "abcd1234" {
		t.Errorf("UserID() = %q, want %q", got, "abcd1234")
	}
	if got := id.ServerID(); got != "wxyz9876" {
		t.Errorf("ServerID() = %q, want %q", got, "wxyz9876")
	}
	if got := id.String(); got != "abcd1234@wxyz9876" {
		t.Errorf("String() = %q, want %q", got, "abcd1234@wxyz9876")
	}
}

func TestParseIdentityIDMalformed(t *testing.T) {
	cases := []string{
		"",
		"noseparator",
		"@leadingsep",
		"trailingsep@",
		"@",
	}
	for _, c := range cases {
		if _, _, ok := ParseIdentityID(IdentityID(c)); ok {
			t.Errorf("ParseIdentityID(%q) ok = true, want false", c)
		}
	}
}

func TestParseIdentityIDLastIndex(t *testing.T) {
	// ParseIdentityID must split on the LAST separator, so ids with an
	// extra "@" in a prefix still parse correctly.
	id := IdentityID("weird@user@serverID")
	userID, serverID, ok := ParseIdentityID(id)
	if !ok {
		t.Fatalf("ParseIdentityID(%q) ok = false, want true", id)
	}
	if userID != "weird@user" || serverID != "serverID" {
		t.Errorf("got (%q, %q), want (%q, %q)", userID, serverID, "weird@user", "serverID")
	}
}

func TestUserIDServerIDPanicOnMalformed(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("UserID() on malformed id: expected panic, got none")
		}
	}()
	IdentityID("malformed").UserID()
}
