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

// RoleForSignup returns the role persisted for a brand-new account.
// Admin-granting invites (roles 02) override this at redeem time.
func RoleForSignup(userID string) string {
	if userID == RootUserID {
		return RoleRoot
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
