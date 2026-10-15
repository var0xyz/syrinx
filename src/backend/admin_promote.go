//go:build !ops && !ripplescleanup && !challengescleanup

package main

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
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

// recountersignProfileTx sets userID's role and countersigns their current
// profile with it and a fresh timestamp. The user's own signature stays.
func (h *Handlers) recountersignProfileTx(ctx context.Context, tx *sql.Tx, userID, role string) error {
	var username, bio, inviteID, keyID, userSignature string
	var memberSince time.Time
	var oldServerSigID sql.NullInt64
	if err := tx.QueryRowContext(ctx, `
		SELECT u.username, COALESCE(u.bio, ''), u.created_at, COALESCE(u.invite_id, ''),
			us.public_key_id, us.signature, u.server_signature_id
		FROM users u JOIN user_signatures us ON us.id = u.user_signature_id
		WHERE u.id = $1
	`, userID).Scan(&username, &bio, &memberSince, &inviteID, &keyID, &userSignature, &oldServerSigID); err != nil {
		return fmt.Errorf("load profile: %w", err)
	}
	signedAt := time.Now().UTC().Truncate(time.Second)
	sig, err := h.countersign(buildProfilePayload(userID, username, keyID, h.services.db.GetServerID(),
		h.signingKey.Fingerprint, userSignature, inviteID, role, bio, memberSince.UTC(), signedAt), signedAt)
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
	return nil
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
			target := req.Targets[0]
			if err := h.recountersignProfileTx(ctx, tx, target, roleAdmin); err != nil {
				return err
			}
			return setUserPermissionsTx(ctx, tx, target, perms)
		},
		after: func(req *adminRequest) { h.announceProfile(req.Targets[0]) },
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
			return setUserPermissionsTx(ctx, tx, req.Targets[0], perms)
		},
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
			if err := h.recountersignProfileTx(ctx, tx, target, roleUser); err != nil {
				return err
			}
			if err := setUserPermissionsTx(ctx, tx, target, nil); err != nil {
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
