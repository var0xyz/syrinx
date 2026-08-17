package recovery

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"time"

	"syrinx/coverage"
	"syrinx/identity"
	"syrinx/signing"

	"github.com/lib/pq"
)

// ErrReedConflict is returned when an existing reed row's metadata does not
// match the countersigned submission (should be impossible under the bind).
var ErrReedConflict = errors.New("reed metadata conflict")

// ErrAuthorNotFound is returned when the reed author has no identities row.
var ErrAuthorNotFound = errors.New("reed author not found")

// SaveReed inserts reed metadata if missing; rejects conflicting metadata;
// always upserts an allocation for reporterUserID. Caller must have
// verified the countersignature. Checks identities, not users, so a
// provisional row still works for a remote author.
func SaveReed(ctx context.Context,
	db *sql.DB,
	serverID string,
	reedID, authorID, fingerprint string,
	signedAt time.Time,
	reporterUserID string,
	userFingerprint, userSignatureB64 string,
	serverSignatureB64 string,
) error {
	signedAt = signedAt.UTC().Truncate(time.Second)
	authorIdentity := identity.LocalID(authorID, serverID)
	reporterIdentity := identity.LocalID(reporterUserID, serverID)

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var exists bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM identities WHERE id = $1)`, authorIdentity).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return ErrAuthorNotFound
	}

	var existingAuthor, existingFP string
	var existingAt time.Time
	err = tx.QueryRowContext(ctx, `
		SELECT user_id, private_key_fingerprint, signed_at
		FROM reeds WHERE user_id = $1 AND id = $2
		FOR UPDATE
	`, authorIdentity, reedID).Scan(&existingAuthor, &existingFP, &existingAt)

	switch {
	case err == sql.ErrNoRows:
		userSigID, err := signing.InsertUserSignature(ctx, tx, userFingerprint, userSignatureB64)
		if err != nil {
			return err
		}
		serverSigID, err := signing.InsertServerSignature(ctx, tx, fingerprint, serverSignatureB64, signedAt)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO reeds (
				id, user_id, private_key_fingerprint, signed_at,
				user_signature_id, server_signature_id
			)
			VALUES ($1, $2, $3, $4, $5, $6)
		`, reedID, authorIdentity, fingerprint, signedAt, userSigID, serverSigID); err != nil {
			return fmt.Errorf("insert reed: %w", err)
		}
	case err != nil:
		return err
	default:
		existingAt = existingAt.UTC().Truncate(time.Second)
		if existingAuthor != string(authorIdentity) || existingFP != fingerprint || !existingAt.Equal(signedAt) {
			log.Printf(
				"[ERR] recovery reed conflict: reedID=%s existing=(author=%s fp=%s at=%s) incoming=(author=%s fp=%s at=%s)",
				reedID, existingAuthor, existingFP, existingAt.Format(time.RFC3339),
				authorIdentity, fingerprint, signedAt.Format(time.RFC3339),
			)
			return ErrReedConflict
		}
	}

	// reed_allocations.holder_user_id is a direct FK to identities(id);
	// author_user_id is composite-FK'd via reeds(user_id, id).
	res, err := tx.ExecContext(ctx, `
		INSERT INTO reed_allocations (reed_id, holder_user_id, author_user_id)
		VALUES ($1, $2, $3)
		ON CONFLICT DO NOTHING
	`, reedID, reporterIdentity, authorIdentity)
	if err != nil {
		return fmt.Errorf("insert reed allocation: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n > 0 {
		// reeds.user_id is the same FK'd column as above.
		if err := coverage.BumpAllocationCount(ctx, tx, string(authorIdentity), reedID, 1); err != nil {
			return err
		}
	}

	return tx.Commit()
}

// SaveFollowing writes follow edges for followerUserID. Existing targets go
// into user_following / user_followers; missing targets go into
// pending_follows. Caller must reject self-follows before calling.
// followerUserID/targetIDs are bare userIDs local to serverID.
func SaveFollowing(ctx context.Context, db *sql.DB, serverID string, followerUserID string, targetIDs []string) error {
	if len(targetIDs) == 0 {
		return nil
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	followerIdentity := identity.LocalID(followerUserID, serverID)

	// Check identities, not users, same reason as SaveReed above.
	existing := make(map[string]bool, len(targetIDs))
	targetIdentities := make(map[string]identity.IdentityID, len(targetIDs))
	canonicalTargets := make([]string, 0, len(targetIDs))
	for _, targetID := range targetIDs {
		if targetID == "" {
			continue
		}
		targetIdentity := identity.LocalID(targetID, serverID)
		targetIdentities[targetID] = targetIdentity
		canonicalTargets = append(canonicalTargets, string(targetIdentity))
	}
	rows, err := tx.QueryContext(ctx, `
		SELECT id FROM identities WHERE id = ANY($1)
	`, pq.Array(canonicalTargets))
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return err
		}
		existing[id] = true
	}
	if err := rows.Err(); err != nil {
		return err
	}

	for _, targetID := range targetIDs {
		if targetID == "" {
			continue
		}
		targetIdentity := targetIdentities[targetID]
		if existing[string(targetIdentity)] {
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO user_following (user_id, following_user_id)
				VALUES ($1, $2)
				ON CONFLICT DO NOTHING
			`, followerIdentity, targetIdentity); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO user_followers (user_id, follower_user_id)
				VALUES ($1, $2)
				ON CONFLICT DO NOTHING
			`, targetIdentity, followerIdentity); err != nil {
				return err
			}
			continue
		}
		// pending_follows.following_user_id has no FK by design (the target
		// may have no identities row yet) — stays bare. follower_user_id
		// IS FK'd to identities.
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO pending_follows (follower_user_id, following_user_id)
			VALUES ($1, $2)
			ON CONFLICT DO NOTHING
		`, followerIdentity, targetID); err != nil {
			return err
		}
	}

	return tx.Commit()
}
