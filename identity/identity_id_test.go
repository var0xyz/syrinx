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

func TestCanonicalIDWithEntityID(t *testing.T) {
	id := CanonicalID("wxyz9876", "abcd1234", "FPR1")
	want := IdentityID("abcd1234@wxyz9876/FPR1")
	if id != want {
		t.Fatalf("CanonicalID = %q, want %q", id, want)
	}

	// Empty entityID behaves like the 2-arg form.
	id2 := CanonicalID("wxyz9876", "abcd1234", "")
	if id2 != "abcd1234@wxyz9876" {
		t.Errorf("CanonicalID with empty entityID = %q, want %q", id2, "abcd1234@wxyz9876")
	}
}

func TestCanonicalIDMoreThanOneEntityIDPanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("CanonicalID with 2 entityIDs: expected panic, got none")
		}
	}()
	CanonicalID("wxyz9876", "abcd1234", "FPR1", "FPR2")
}

func TestParseKeyFingerprintRoundTrip(t *testing.T) {
	id := CanonicalID("wxyz9876", "abcd1234", "FPR1")
	userID, serverID, fingerprint, ok := ParseKeyFingerprint(id)
	if !ok {
		t.Fatalf("ParseKeyFingerprint(%q) ok = false, want true", id)
	}
	if userID != "abcd1234" || serverID != "wxyz9876" || fingerprint != "FPR1" {
		t.Errorf("got (%q, %q, %q), want (%q, %q, %q)", userID, serverID, fingerprint, "abcd1234", "wxyz9876", "FPR1")
	}
}

func TestParseKeyFingerprintMalformed(t *testing.T) {
	cases := []string{
		"",
		"noSlash@server",
		"abcd1234@wxyz9876/",
		"noAtSign/FPR1",
	}
	for _, c := range cases {
		if _, _, _, ok := ParseKeyFingerprint(IdentityID(c)); ok {
			t.Errorf("ParseKeyFingerprint(%q) ok = true, want false", c)
		}
	}
}

func TestAppendEntity(t *testing.T) {
	userIdentity := CanonicalID("wxyz9876", "abcd1234")
	got := AppendEntity(userIdentity, "FPR1")
	want := IdentityID("abcd1234@wxyz9876/FPR1")
	if got != want {
		t.Fatalf("AppendEntity = %q, want %q", got, want)
	}

	userID, serverID, fingerprint, ok := ParseKeyFingerprint(got)
	if !ok || userID != "abcd1234" || serverID != "wxyz9876" || fingerprint != "FPR1" {
		t.Errorf("ParseKeyFingerprint(AppendEntity(...)) = (%q, %q, %q, %v), want (%q, %q, %q, true)",
			userID, serverID, fingerprint, ok, "abcd1234", "wxyz9876", "FPR1")
	}
}
