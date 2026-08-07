package roles

import "testing"

func TestIsAdmin(t *testing.T) {
	if !IsAdmin(RoleRoot) || !IsAdmin(RoleAdmin) {
		t.Fatal("root and admin must be admin")
	}
	if IsAdmin(RoleUser) || IsAdmin("") || IsAdmin("superuser") {
		t.Fatal("user and unknown roles must not be admin")
	}
}

func TestIsRoot(t *testing.T) {
	if !IsRoot(RootUserID, RoleRoot) {
		t.Fatal("reserved id with root role must be root")
	}
	if IsRoot(RootUserID, RoleAdmin) || IsRoot("other", RoleRoot) {
		t.Fatal("wrong id/role pairs must not be root")
	}
}

func TestCanGrantAdmin(t *testing.T) {
	if !CanGrantAdmin(RoleRoot) || !CanGrantAdmin(RoleAdmin) {
		t.Fatal("root and admin may grant admin invites")
	}
	if CanGrantAdmin(RoleUser) {
		t.Fatal("normal users must not grant admin invites")
	}
}

func TestRoleForSignup(t *testing.T) {
	if got := RoleForSignup(RootUserID); got != RoleRoot {
		t.Fatalf("root mint role = %q want %q", got, RoleRoot)
	}
	if got := RoleForSignup("abc123"); got != RoleUser {
		t.Fatalf("normal signup role = %q want %q", got, RoleUser)
	}
}

func TestRequireAdmin(t *testing.T) {
	if err := RequireAdmin(RoleAdmin); err != nil {
		t.Fatalf("admin: %v", err)
	}
	if err := RequireAdmin(RoleUser); err != ErrAdminRequired {
		t.Fatalf("user: got %v want ErrAdminRequired", err)
	}
}
