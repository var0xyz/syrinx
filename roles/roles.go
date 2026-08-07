// Package roles defines local instance role tiers (root, admin, user) and
// helpers for authorization checks. Roles are server-local policy — not
// part of the signed identity wire format.
package roles

import "errors"

const (
	// RootUserID is the reserved users.id for the operator root account.
	RootUserID = "1"

	RoleRoot  = "root"
	RoleAdmin = "admin"
	RoleUser  = "user"
)

// ErrAdminRequired is returned when an action requires admin or root.
var ErrAdminRequired = errors.New("admin required")

// IsAdmin reports whether role is root or admin.
func IsAdmin(role string) bool {
	return role == RoleRoot || role == RoleAdmin
}

// IsRoot reports whether userID is the reserved root identity with role root.
func IsRoot(userID, role string) bool {
	return userID == RootUserID && role == RoleRoot
}

// CanGrantAdmin reports whether the caller may create admin-granting invites.
func CanGrantAdmin(role string) bool {
	return IsAdmin(role)
}

// RoleForSignup returns the role persisted for a brand-new account with no
// invite redeem (open signup or root mint).
func RoleForSignup(userID string) string {
	if userID == RootUserID {
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
// inviteGrantedRole from the invites row is applied (never root).
func SignupRole(userID, inviteGrantedRole string, hasInvite bool) string {
	if userID == RootUserID {
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
