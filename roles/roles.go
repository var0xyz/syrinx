// Package roles defines local instance role tiers (root, admin, user) and
// helpers for authorization checks. Role is bound on the profile server
// countersignature (identity-server payload); users never sign role.
package roles

import (
	"errors"
	"fmt"
	"strings"

	"syrinx/identity"
)

const (
	// RootUserID is the reserved bare userID for the operator root account.
	// This stays a bare compile-time constant since the full identity
	// ("1@serverID") can't be — serverID is only known at runtime.
	RootUserID = "1"

	RoleRoot  = "root"
	RoleAdmin = "admin"
	RoleUser  = "user"
)

// ErrAdminRequired is returned when an action requires admin or root.
var ErrAdminRequired = errors.New("admin required")

// ErrInvalidRole is returned when a role value is unknown or inconsistent
// with the user id.
var ErrInvalidRole = errors.New("invalid role")

// IsAdmin reports whether role is root or admin.
func IsAdmin(role string) bool {
	return role == RoleRoot || role == RoleAdmin
}

// IsRoot reports whether userID is this server's own reserved root
// identity with role root. Compare the full "1@serverID" form, never the
// bare "1" — a remote user whose bare id is also "1" must not match.
func IsRoot(userID, role, serverID string) bool {
	return isRootIdentity(userID, serverID) && role == RoleRoot
}

// isRootIdentity is the shared comparison core for IsRoot/RoleForSignup/
// SignupRole/ValidateProfileRole — see IsRoot's doc comment for the full
// security reasoning.
func isRootIdentity(userID, serverID string) bool {
	rootIdentity := identity.CanonicalID(serverID, RootUserID)
	// userID may arrive bare or already in "userID@serverID" form; try both.
	if userID == string(rootIdentity) {
		return true
	}
	return userID == RootUserID
}

// CanGrantAdmin reports whether the caller may create admin-granting invites.
func CanGrantAdmin(role string) bool {
	return IsAdmin(role)
}

// RoleForSignup returns the role persisted for a brand-new account with no
// invite redeem (open signup or root mint). See IsRoot's doc comment for
// why the root comparison must use serverID, not a bare literal match.
func RoleForSignup(userID, serverID string) string {
	if isRootIdentity(userID, serverID) {
		return RoleRoot
	}
	return RoleUser
}

// RoleFromInviteGrant maps invite.granted_role to users.role. Never root.
func RoleFromInviteGrant(grantedRole string) string {
	if grantedRole == RoleAdmin {
		return RoleAdmin
	}
	return RoleUser
}

// SignupRole returns users.role for a signup insert. When hasInvite is true,
// inviteGrantedRole from the invites row is applied (never root). See
// IsRoot's doc comment for why the root comparison must use serverID.
func SignupRole(userID, inviteGrantedRole string, hasInvite bool, serverID string) string {
	if isRootIdentity(userID, serverID) {
		return RoleRoot
	}
	if hasInvite {
		return RoleFromInviteGrant(inviteGrantedRole)
	}
	return RoleUser
}

// RequireAdmin returns nil when role is root or admin.
func RequireAdmin(role string) error {
	if !IsAdmin(role) {
		return ErrAdminRequired
	}
	return nil
}

// ValidateProfileRole checks role is a known tier and consistent with userID
// (root only on the reserved id). See IsRoot's doc comment for why the root
// comparison must use serverID.
func ValidateProfileRole(userID, role, serverID string) error {
	role = strings.TrimSpace(role)
	switch role {
	case RoleRoot, RoleAdmin, RoleUser:
	default:
		return fmt.Errorf("%w: %q", ErrInvalidRole, role)
	}
	isRoot := isRootIdentity(userID, serverID)
	if isRoot {
		if role != RoleRoot {
			return fmt.Errorf("%w: root user must have role root", ErrInvalidRole)
		}
		return nil
	}
	if role == RoleRoot {
		return fmt.Errorf("%w: only user id %q (this server's own root identity) may have role root", ErrInvalidRole, RootUserID)
	}
	return nil
}
