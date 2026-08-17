package roles

import "testing"

// testServerID is this server's own id, used throughout this file's
// IsRoot/RoleForSignup/SignupRole/ValidateProfileRole calls.
const testServerID = "srv"

func TestIsAdmin(t *testing.T) {
	if !IsAdmin(RoleRoot) || !IsAdmin(RoleAdmin) {
		t.Fatal("root and admin must be admin")
	}
	if IsAdmin(RoleUser) || IsAdmin("") || IsAdmin("superuser") {
		t.Fatal("user and unknown roles must not be admin")
	}
}

func TestIsRoot(t *testing.T) {
	if !IsRoot(RootUserID, RoleRoot, testServerID) {
		t.Fatal("reserved id with root role must be root")
	}
	if IsRoot(RootUserID, RoleAdmin, testServerID) || IsRoot("other", RoleRoot, testServerID) {
		t.Fatal("wrong id/role pairs must not be root")
	}
	if IsRoot(RootUserID+"@otherServer", RoleRoot, testServerID) {
		t.Fatal("a remote identity sharing the bare root id must not be root")
	}
	if !IsRoot(RootUserID+"@"+testServerID, RoleRoot, testServerID) {
		t.Fatal("already-canonical local root identity must still be root")
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
	if got := RoleForSignup(RootUserID, testServerID); got != RoleRoot {
		t.Fatalf("root mint role = %q want %q", got, RoleRoot)
	}
	if got := RoleForSignup("abc123", testServerID); got != RoleUser {
		t.Fatalf("normal signup role = %q want %q", got, RoleUser)
	}
}

func TestRoleFromInviteGrant(t *testing.T) {
	if got := RoleFromInviteGrant(RoleAdmin); got != RoleAdmin {
		t.Fatalf("admin grant = %q", got)
	}
	if got := RoleFromInviteGrant(RoleUser); got != RoleUser {
		t.Fatalf("user grant = %q", got)
	}
	if got := RoleFromInviteGrant("root"); got != RoleUser {
		t.Fatalf("root grant must map to user, got %q", got)
	}
}

func TestSignupRole(t *testing.T) {
	if got := SignupRole(RootUserID, RoleAdmin, true, testServerID); got != RoleRoot {
		t.Fatalf("root signup = %q", got)
	}
	if got := SignupRole("u2", RoleAdmin, true, testServerID); got != RoleAdmin {
		t.Fatalf("admin invite = %q", got)
	}
	if got := SignupRole("u2", RoleUser, true, testServerID); got != RoleUser {
		t.Fatalf("user invite = %q", got)
	}
	if got := SignupRole("u2", RoleAdmin, false, testServerID); got != RoleUser {
		t.Fatalf("open signup = %q", got)
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

func TestValidateProfileRole(t *testing.T) {
	if err := ValidateProfileRole("u2", RoleAdmin, testServerID); err != nil {
		t.Fatalf("admin: %v", err)
	}
	if err := ValidateProfileRole(RootUserID, RoleRoot, testServerID); err != nil {
		t.Fatalf("root: %v", err)
	}
	if err := ValidateProfileRole("u2", RoleRoot, testServerID); err == nil {
		t.Fatal("non-root id with root role must fail")
	}
	if err := ValidateProfileRole(RootUserID, RoleAdmin, testServerID); err == nil {
		t.Fatal("root id without root role must fail")
	}
	if err := ValidateProfileRole("u2", "superuser", testServerID); err == nil {
		t.Fatal("unknown role must fail")
	}
}
