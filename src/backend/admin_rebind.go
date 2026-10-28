//go:build !ops && !ripplescleanup && !challengescleanup

package main

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"syrinx/observability/metrics"
	"time"
)

// rebindKeyID is the ID the key a rebind uploads gets, "" when its armor
// doesn't parse.
func (h *Handlers) rebindKeyID(req *adminRequest) string {
	if req.Action != adminActionRebind || len(req.Targets) != 1 {
		return ""
	}
	fingerprint, err := h.services.crypto.extractFingerprintFromArmor(req.Body)
	if err != nil {
		return ""
	}
	return string(appendEntity(identityID(req.Targets[0]), fingerprint))
}

// rebindExecutor revokes the target's active key as lost, signed by the two
// admins, and binds the uploaded key in its place, restricted to rotating.
func (h *Handlers) rebindExecutor() adminExecutor {
	var revokedKeyID string
	var revokedInvites int64
	return adminExecutor{
		validate: func(ctx context.Context, q adminQuerier, req *adminRequest) error {
			target := req.Targets[0]
			if by, err := suspendedByQ(ctx, q, target); err != nil || by != "" {
				if err != nil {
					return err
				}
				return refuse(http.StatusConflict, "target_suspended", "%s is suspended", target)
			}
			keyID := h.rebindKeyID(req)
			if keyID == "" {
				return refuse(http.StatusBadRequest, "bad_key", "The public key doesn't parse")
			}
			var exists bool
			if err := q.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM public_keys WHERE id = $1)`, keyID).Scan(&exists); err != nil {
				return err
			}
			if exists {
				return refuse(http.StatusConflict, "key_exists", "That key is already registered")
			}
			return nil
		},
		execute: func(ctx context.Context, tx *sql.Tx, req *adminRequest) error {
			target, newKeyID := req.Targets[0], h.rebindKeyID(req)
			serverID := h.services.db.GetServerID()
			if err := tx.QueryRowContext(ctx, `
				SELECT active_key_id FROM users WHERE id = $1 FOR UPDATE
			`, target).Scan(&revokedKeyID); err != nil {
				return fmt.Errorf("load active key: %w", err)
			}
			now := time.Now().UTC().Truncate(time.Second)

			revocationSig, err := h.countersign(ctx, buildAdminRevocationServerPayload(target, revokedKeyID, req.ID,
				req.RequesterSignature.Armor, req.Decision.Signature.Armor, serverID, h.signingKey.Fingerprint, now), now)
			if err != nil {
				return err
			}
			revocationSigID, err := insertServerSignature(ctx, tx, revocationSig.ID, revocationSig.Armor, now)
			if err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO public_key_revocations (key_id, reason, admin_request_id, server_signature_id)
				VALUES ($1, $2, $3, $4)
			`, revokedKeyID, keyRevocationReasonLost, req.ID, revocationSigID); err != nil {
				return fmt.Errorf("revoke lost key: %w", err)
			}
			if err := enqueuePeerEventTx(ctx, tx, peerEventKeyRevocation, revokedKeyID); err != nil {
				return err
			}

			keySig, err := h.countersign(ctx, buildPublicKeyPayload(serverID, target, newKeyID, h.signingKey.Fingerprint, req.Body, now), now)
			if err != nil {
				return err
			}
			keySigID, err := insertServerSignature(ctx, tx, keySig.ID, keySig.Armor, now)
			if err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO public_keys (id, owner, armor, created_at, server_signature_id, predecessor_id, restricted)
				VALUES ($1, $2, $3, $4, $5, $6, TRUE)
			`, newKeyID, target, req.Body, now, keySigID, revokedKeyID); err != nil {
				return fmt.Errorf("bind rebound key: %w", err)
			}
			if _, err := tx.ExecContext(ctx, `
				UPDATE public_key_revocations SET successor = $1 WHERE key_id = $2
			`, newKeyID, revokedKeyID); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `UPDATE users SET active_key_id = $1 WHERE id = $2`, newKeyID, target); err != nil {
				return err
			}
			// The lost device holds the open invites' secrets, so they all go.
			res, err := tx.ExecContext(ctx, `
				UPDATE invites SET revoked_at = $2
				WHERE created_by = $1 AND claimed_at IS NULL AND revoked_at IS NULL
			`, target, now)
			if err != nil {
				return err
			}
			revokedInvites, _ = res.RowsAffected()
			return nil
		},
		after: func(req *adminRequest) {
			h.kickUserDevices(req.Targets[0])
			h.broadcastChan <- realtimeBroadcastMessage{Type: realtimeKeyRevoked, KeyID: revokedKeyID}
			h.drainPeers("")
			h.metrics.KeyRevoked(context.Background(), metrics.RevocationRebind)
			h.recordInvitesRevoked(req.Action, req.Targets[0], revokedInvites)
		},
	}
}
