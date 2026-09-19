// Local instance role tiers (root, admin, user) and helpers for
// authorization checks. Role is bound on the profile server
// countersignature (identity-server payload); users never sign role.
package main

import (
	"errors"
	"fmt"
	"strings"
)

const (
	roleRoot  = "root"
	roleAdmin = "admin"
	roleUser  = "user"
)

// errAdminRequired is returned when an action requires admin or root.
var errAdminRequired = errors.New("admin required")

// errInvalidRole is returned when a role value is unknown or inconsistent
// with the user id.
var errInvalidRole = errors.New("invalid role")

// isAdmin reports whether role is root or admin.
func isAdmin(role string) bool {
	return role == roleRoot || role == roleAdmin
}

// isRoot reports whether userID is this server's own reserved root
// identity with role root. Compare the full "1@serverID" form, never the
// bare "1" — a remote user whose bare id is also "1" must not match.
func isRoot(userID, role, serverID string) bool {
	return isRootIdentity(userID, serverID) && role == roleRoot
}

// isRootIdentity is the shared comparison core for isRoot/roleForSignup/
// signupRole/validateProfileRole — see isRoot's doc comment for the full
// security reasoning.
func isRootIdentity(userID, serverID string) bool {
	rootIdentity := canonicalID(serverID, rootUserID)
	// userID may arrive bare or already in "userID@serverID" form; try both.
	if userID == string(rootIdentity) {
		return true
	}
	return userID == rootUserID
}

// canGrantAdmin reports whether the caller may create admin-granting invites.
func canGrantAdmin(role string) bool {
	return isAdmin(role)
}

// roleForSignup returns the role persisted for a brand-new account with no
// invite redeem (open signup or root mint). See isRoot's doc comment for
// why the root comparison must use serverID, not a bare literal match.
func roleForSignup(userID, serverID string) string {
	if isRootIdentity(userID, serverID) {
		return roleRoot
	}
	return roleUser
}

// roleFromInviteGrant maps invite.granted_role to users.role. Never root.
func roleFromInviteGrant(grantedRole string) string {
	if grantedRole == roleAdmin {
		return roleAdmin
	}
	return roleUser
}

// signupRole returns users.role for a signup insert. When hasInvite is true,
// inviteGrantedRole from the invites row is applied (never root). See
// isRoot's doc comment for why the root comparison must use serverID.
func signupRole(userID, inviteGrantedRole string, hasInvite bool, serverID string) string {
	if isRootIdentity(userID, serverID) {
		return roleRoot
	}
	if hasInvite {
		return roleFromInviteGrant(inviteGrantedRole)
	}
	return roleUser
}

// requireAdmin returns nil when role is root or admin.
func requireAdmin(role string) error {
	if !isAdmin(role) {
		return errAdminRequired
	}
	return nil
}

// validateProfileRole checks role is a known tier and consistent with userID
// (root only on the reserved id). See isRoot's doc comment for why the root
// comparison must use serverID.
func validateProfileRole(userID, role, serverID string) error {
	role = strings.TrimSpace(role)
	switch role {
	case roleRoot, roleAdmin, roleUser:
	default:
		return fmt.Errorf("%w: %q", errInvalidRole, role)
	}
	rootMatch := isRootIdentity(userID, serverID)
	if rootMatch {
		if role != roleRoot {
			return fmt.Errorf("%w: root user must have role root", errInvalidRole)
		}
		return nil
	}
	if role == roleRoot {
		return fmt.Errorf("%w: only user id %q (this server's own root identity) may have role root", errInvalidRole, rootUserID)
	}
	return nil
}
