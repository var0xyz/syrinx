//go:build !ops && !ripplescleanup && !challengescleanup

package main

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// setUserPermissionsTx replaces userID's permissions with perms.
func setUserPermissionsTx(ctx context.Context, tx *sql.Tx, userID string, perms []string) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM user_permissions WHERE user_id = $1`, userID); err != nil {
		return err
	}
	for _, p := range perms {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO user_permissions (user_id, permission) VALUES ($1, $2)
		`, userID, p); err != nil {
			return err
		}
	}
	return nil
}

// validateProfilePermissions refuses permissions on a non-admin, or a set
// that isn't known, sorted and unique.
func validateProfilePermissions(role string, perms []string) error {
	if len(perms) == 0 {
		return nil
	}
	if role != roleAdmin {
		return fmt.Errorf("only an admin's profile carries permissions")
	}
	_, err := parsePermissionSet(strings.Join(perms, ","))
	return err
}

// recountersignProfileTx sets userID's role and permissions and countersigns
// their profile with them and a fresh timestamp. The user's signature stays.
func (h *Handlers) recountersignProfileTx(ctx context.Context, tx *sql.Tx, userID, role string, perms []string) error {
	var username, bio, inviteID, keyID, userSignature string
	var memberSince time.Time
	var oldServerSigID sql.NullInt64
	if err := tx.QueryRowContext(ctx, `
		SELECT COALESCE(u.username, uc.username), COALESCE(u.bio, ''), u.created_at, COALESCE(u.invite_id, ''),
			us.public_key_id, us.signature, u.server_signature_id
		FROM users u JOIN user_signatures us ON us.id = u.user_signature_id
		LEFT JOIN username_collisions uc ON uc.loser_id = u.id
		WHERE u.id = $1
	`, userID).Scan(&username, &bio, &memberSince, &inviteID, &keyID, &userSignature, &oldServerSigID); err != nil {
		return fmt.Errorf("load profile: %w", err)
	}
	signedAt := time.Now().UTC().Truncate(time.Second)
	sig, err := h.countersign(ctx, buildProfilePayload(userID, username, keyID, h.services.db.GetServerID(),
		h.signingKey.Fingerprint, userSignature, inviteID, role, perms, bio, memberSince.UTC(), signedAt), signedAt)
	if err != nil {
		return err
	}
	sigID, err := insertServerSignature(ctx, tx, sig.ID, sig.Armor, sig.SignedAt)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE users SET role = $2, server_signature_id = $3 WHERE id = $1
	`, userID, role, sigID); err != nil {
		return err
	}
	if oldServerSigID.Valid {
		if _, err := tx.ExecContext(ctx, `DELETE FROM server_signatures WHERE id = $1`, oldServerSigID.Int64); err != nil {
			return err
		}
	}
	return setUserPermissionsTx(ctx, tx, userID, perms)
}

// announceProfile pushes userID's re-countersigned profile to whoever
// follows or holds it, as a profile edit does.
func (h *Handlers) announceProfile(userID string) {
	profile, err := h.services.db.GetUserProfile(context.Background(), userID)
	if err != nil || profile == nil {
		h.services.log.GetLogger(context.Background()).Error().Err(err).Str("userID", userID).Msg("Failed to load re-countersigned profile")
		return
	}
	h.broadcastChan <- realtimeBroadcastMessage{
		Type:       realtimeUserUpdate,
		UserID:     userID,
		UserUpdate: &userUpdateBroadcast{Username: profile.Username, Bio: profile.Bio},
	}
}

// mailboxKindNewAdmin tells the other admins someone became an admin.
const mailboxKindNewAdmin = "new-admin"

// activeLocalAdmins returns every active local admin and root but exclude.
func (s *DataService) activeLocalAdmins(ctx context.Context, exclude string) ([]string, error) {
	return s.queryStrings(ctx, `
		SELECT u.id FROM users u
		WHERE u.role IN ($1, $2) AND u.id <> $3
			AND u.suspended_by IS NULL AND u.username IS NOT NULL
			AND NOT EXISTS (SELECT 1 FROM account_removals ar WHERE ar.user_id = u.id)
	`, roleRoot, roleAdmin, exclude)
}

// notifyAdminsOfNewAdmin mails every other admin that newAdminID became
// one; how completes "@username …". One failure doesn't stop the rest.
func (h *Handlers) notifyAdminsOfNewAdmin(newAdminID, how string) {
	ctx := context.Background()
	log := h.services.log.GetLogger(ctx)
	var username string
	if err := h.services.db.db.QueryRowContext(ctx, `SELECT username FROM users WHERE id = $1`, newAdminID).Scan(&username); err != nil {
		log.Error().Err(err).Str("userID", newAdminID).Msg("Failed to load the new admin's username")
		return
	}
	admins, err := h.services.db.activeLocalAdmins(ctx, newAdminID)
	if err != nil {
		log.Error().Err(err).Str("userID", newAdminID).Msg("Failed to list admins to tell about a new admin")
		return
	}
	message := "@" + username + " " + how + "."
	for _, adminID := range admins {
		if err := h.SendMailboxMessage(ctx, adminID, mailboxKindNewAdmin, message, "/profile/"+newAdminID, nil); err != nil {
			log.Error().Err(err).Str("userID", adminID).Msg("Failed to tell an admin about a new admin")
		}
	}
}

// targetRoleIs refuses unless every remaining target has role.
func targetRoleIs(ctx context.Context, q adminQuerier, req *adminRequest, role string) error {
	for _, target := range req.remainingTargets() {
		got, err := userRoleQ(ctx, q, target)
		if err != nil {
			return err
		}
		if got != role {
			return refuse(http.StatusConflict, "target_role", "%s's role is %s, not %s", target, got, role)
		}
	}
	return nil
}

// promoteExecutor makes the target an admin with the request's permissions.
func (h *Handlers) promoteExecutor() adminExecutor {
	return adminExecutor{
		validate: func(ctx context.Context, q adminQuerier, req *adminRequest) error {
			return targetRoleIs(ctx, q, req, roleUser)
		},
		execute: func(ctx context.Context, tx *sql.Tx, req *adminRequest) error {
			perms, err := parsePermissionSet(req.Body)
			if err != nil {
				return refuse(http.StatusBadRequest, "bad_permissions", "Invalid permission set: %v", err)
			}
			return h.recountersignProfileTx(ctx, tx, req.Targets[0], roleAdmin, perms)
		},
		after: func(req *adminRequest) {
			h.announceProfile(req.Targets[0])
			go h.notifyAdminsOfNewAdmin(req.Targets[0], "is now an admin")
		},
	}
}

// permissionsExecutor replaces an admin's permissions with the request's.
func (h *Handlers) permissionsExecutor() adminExecutor {
	return adminExecutor{
		validate: func(ctx context.Context, q adminQuerier, req *adminRequest) error {
			return targetRoleIs(ctx, q, req, roleAdmin)
		},
		execute: func(ctx context.Context, tx *sql.Tx, req *adminRequest) error {
			perms, err := parsePermissionSet(req.Body)
			if err != nil {
				return refuse(http.StatusBadRequest, "bad_permissions", "Invalid permission set: %v", err)
			}
			return h.recountersignProfileTx(ctx, tx, req.Targets[0], roleAdmin, perms)
		},
		after: func(req *adminRequest) { h.announceProfile(req.Targets[0]) },
	}
}

// demoteExecutor makes an admin a user again: no permissions, and their
// unclaimed admin invites revoked.
func (h *Handlers) demoteExecutor() adminExecutor {
	var revokedInvites int64
	return adminExecutor{
		validate: func(ctx context.Context, q adminQuerier, req *adminRequest) error {
			return targetRoleIs(ctx, q, req, roleAdmin)
		},
		execute: func(ctx context.Context, tx *sql.Tx, req *adminRequest) error {
			target := req.Targets[0]
			if err := h.recountersignProfileTx(ctx, tx, target, roleUser, nil); err != nil {
				return err
			}
			res, err := tx.ExecContext(ctx, `
				UPDATE invites SET revoked_at = NOW()
				WHERE created_by = $1 AND granted_role = 'admin' AND claimed_at IS NULL AND revoked_at IS NULL
			`, target)
			if err != nil {
				return err
			}
			revokedInvites, _ = res.RowsAffected()
			return nil
		},
		after: func(req *adminRequest) {
			h.announceProfile(req.Targets[0])
			h.recordInvitesRevoked(req.Action, req.Targets[0], revokedInvites)
		},
	}
}
