package recovery

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"syrinx/coverage"
	"syrinx/identity"
	"syrinx/roles"
	"syrinx/signing"
)

// SaveIdentityResult describes what a save did to the users row.
type SaveIdentityResult struct {
	Created bool
	Updated bool // profile columns written (create or newer-wins)
}

// SaveOwnIdentity upserts a verified own-claim identity + nest. Clears
// unclaimed_accounts and records the user in ongoing_recoveries.
func SaveOwnIdentity(ctx context.Context, db *sql.DB, profile Profile, flat []FlatKey, deviceID string) (*SaveIdentityResult, error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	res, err := upsertIdentity(ctx, tx, profile, flat)
	if err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM unclaimed_accounts WHERE user_id = $1`, profile.ID); err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO ongoing_recoveries (user_id) VALUES ($1)
		ON CONFLICT DO NOTHING
	`, profile.ID); err != nil {
		return nil, err
	}
	if err := drainPendingFollows(ctx, tx, profile.ID); err != nil {
		return nil, err
	}
	if deviceID != "" {
		if err := bindClaimDeviceTx(ctx, tx, profile.ID, deviceID, profile.ServerSignature.Timestamp.UTC()); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return res, nil
}

// SavePeerIdentity upserts a verified peer-reported identity + nest.
// Newly created rows are inserted into unclaimed_accounts; already-claimed
// accounts are never re-marked unclaimed.
func SavePeerIdentity(ctx context.Context, db *sql.DB, profile Profile, flat []FlatKey) (*SaveIdentityResult, error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	res, err := upsertIdentity(ctx, tx, profile, flat)
	if err != nil {
		return nil, err
	}
	if res.Created {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO unclaimed_accounts (user_id) VALUES ($1)
			ON CONFLICT DO NOTHING
		`, profile.ID); err != nil {
			return nil, err
		}
	}
	if err := drainPendingFollows(ctx, tx, profile.ID); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return res, nil
}

func upsertIdentity(ctx context.Context, tx *sql.Tx, profile Profile, flat []FlatKey) (*SaveIdentityResult, error) {
	if len(flat) == 0 {
		return nil, fmt.Errorf("empty key nest")
	}
	activeFP := flat[len(flat)-1].Key.Fingerprint
	incomingSignedAt := profile.ServerSignature.Timestamp.UTC().Truncate(time.Second)

	var existingSignedAt time.Time
	err := tx.QueryRowContext(ctx, `
		SELECT ss.signed_at
		FROM users u
		JOIN server_signatures ss ON ss.id = u.server_signature_id
		WHERE u.id = $1
		FOR UPDATE OF u
	`, profile.ID).Scan(&existingSignedAt)

	created := false
	updated := false

	switch {
	case err == sql.ErrNoRows:
		if err := insertUser(ctx, tx, profile, activeFP, incomingSignedAt); err != nil {
			return nil, err
		}
		created = true
		updated = true
	case err != nil:
		return nil, err
	default:
		wrote, err := updateUserIfNewer(ctx, tx, profile, activeFP, existingSignedAt, incomingSignedAt)
		if err != nil {
			return nil, err
		}
		updated = wrote
	}

	if err := insertKeys(ctx, tx, profile.ID, flat); err != nil {
		return nil, err
	}

	return &SaveIdentityResult{Created: created, Updated: updated}, nil
}

func insertUser(ctx context.Context, tx *sql.Tx, profile Profile, activeFP string, signedAt time.Time) error {
	username, err := claimUsername(ctx, tx, profile.ID, profile.Username, signedAt)
	if err != nil {
		return err
	}
	if err := roles.ValidateProfileRole(profile.ID, profile.Role); err != nil {
		return err
	}
	fingerprint := profile.UserSignature.Fingerprint
	if fingerprint == "" {
		fingerprint = activeFP
	}
	userSignatureID, err := signing.InsertUserSignature(ctx, tx, fingerprint, profile.UserSignature.Armor,
	)
	if err != nil {
		return err
	}
	serverSignatureID, err := signing.InsertServerSignature(ctx, tx, profile.ServerSignature.Fingerprint, profile.ServerSignature.Armor, signedAt,
	)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `
		INSERT INTO users (
			id, username, role, created_at, user_fingerprint, bio,
			user_signature_id, server_signature_id, invited_by
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
	`,
		profile.ID, username, profile.Role, profile.MemberSince.UTC().Truncate(time.Second),
		activeFP, nullIfEmpty(profile.Bio),
		userSignatureID, serverSignatureID, nullIfEmpty(profileInvitedByID(profile)),
	)
	if err != nil {
		return fmt.Errorf("insert user: %w", err)
	}
	return coverage.BumpActiveUsers(ctx, tx, 1)
}

func updateUserIfNewer(
	ctx context.Context,
	tx *sql.Tx,
	profile Profile,
	activeFP string,
	existingSignedAt time.Time,
	incomingSignedAt time.Time,
) (bool, error) {
	if !incomingSignedAt.After(existingSignedAt) {
		return false, nil
	}
	username, err := claimUsername(ctx, tx, profile.ID, profile.Username, incomingSignedAt)
	if err != nil {
		return false, err
	}
	fingerprint := profile.UserSignature.Fingerprint
	if fingerprint == "" {
		fingerprint = activeFP
	}
	userSignatureID, err := signing.InsertUserSignature(ctx, tx, fingerprint, profile.UserSignature.Armor,
	)
	if err != nil {
		return false, err
	}
	serverSignatureID, err := signing.InsertServerSignature(ctx, tx, profile.ServerSignature.Fingerprint, profile.ServerSignature.Armor, incomingSignedAt,
	)
	if err != nil {
		return false, err
	}
	_, err = tx.ExecContext(ctx, `
		UPDATE users SET
			username = $1,
			bio = $2,
			role = $3,
			user_fingerprint = $4,
			user_signature_id = $5,
			server_signature_id = $6
		WHERE id = $7
	`,
		username, nullIfEmpty(profile.Bio),
		profile.Role, activeFP, userSignatureID, serverSignatureID, profile.ID,
	)
	if err != nil {
		return false, fmt.Errorf("update user: %w", err)
	}
	return true, nil
}

func insertKeys(ctx context.Context, tx *sql.Tx, userID string, flat []FlatKey) error {
	for i, fk := range flat {
		var predFP, predSig interface{}
		if fk.PredecessorFingerprint != "" {
			predFP = fk.PredecessorFingerprint
			predSig = fk.PredecessorSignature
		}
		serverSigID, err := signing.InsertServerSignature(ctx, tx,
			fk.Key.ServerSignature.Fingerprint,
			fk.Key.ServerSignature.Armor,
			fk.Key.ServerSignature.Timestamp,
		)
		if err != nil {
			return fmt.Errorf("insert key server signature %s: %w", fk.Key.Fingerprint, err)
		}
		_, err = tx.ExecContext(ctx, `
			INSERT INTO user_keys (
				fingerprint, owner, armor, created_at, expires_at,
				server_signature_id,
				predecessor_signature, predecessor_fingerprint
			) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
			ON CONFLICT (owner, fingerprint) DO NOTHING
		`,
			fk.Key.Fingerprint, userID, fk.Key.Armor,
			fk.Key.CreatedAt.UTC().Truncate(time.Second), fk.Key.ExpiresAt,
			serverSigID,
			predSig, predFP,
		)
		if err != nil {
			return fmt.Errorf("insert key %s: %w", fk.Key.Fingerprint, err)
		}

		if fk.Revocation != nil {
			userSigID, err := signing.InsertUserSignature(ctx, tx, fk.Revocation.Fingerprint, fk.Revocation.UserSignature.Armor,
			)
			if err != nil {
				return fmt.Errorf("insert revocation user signature %s: %w", fk.Key.Fingerprint, err)
			}
			serverSigID, err := signing.InsertServerSignature(ctx, tx,
				fk.Revocation.ServerSignature.Fingerprint,
				fk.Revocation.ServerSignature.Armor,
				fk.Revocation.ServerSignature.Timestamp,
			)
			if err != nil {
				return fmt.Errorf("insert revocation server signature %s: %w", fk.Key.Fingerprint, err)
			}
			_, err = tx.ExecContext(ctx, `
				INSERT INTO user_key_revocations (
					user_fingerprint, owner, reason,
					user_signature_id, server_signature_id
				) VALUES ($1, $2, $3, $4, $5)
				ON CONFLICT (owner, user_fingerprint) DO NOTHING
			`,
				fk.Revocation.Fingerprint, userID, fk.Revocation.Reason,
				userSigID, serverSigID,
			)
			if err != nil {
				return fmt.Errorf("insert revocation %s: %w", fk.Key.Fingerprint, err)
			}
		}

		// After inserting a newer key, point the predecessor revocation's successor.
		if i > 0 {
			_, err := tx.ExecContext(ctx, `
				UPDATE user_key_revocations
				SET successor = $1
				WHERE user_fingerprint = $2 AND owner = $3
				  AND (successor IS NULL OR successor = '')
			`, fk.Key.Fingerprint, flat[i-1].Key.Fingerprint, userID)
			if err != nil {
				return fmt.Errorf("set successor for %s: %w", flat[i-1].Key.Fingerprint, err)
			}
		}
	}
	return nil
}

func claimUsername(ctx context.Context, tx *sql.Tx, userID, username string, signedAt time.Time) (string, error) {
	var holderID string
	var holderSignedAt time.Time
	err := tx.QueryRowContext(ctx, `
		SELECT u.id, ss.signed_at
		FROM users u
		JOIN server_signatures ss ON ss.id = u.server_signature_id
		WHERE LOWER(u.username) = LOWER($1) AND u.id <> $2
		FOR UPDATE OF u
	`, username, userID).Scan(&holderID, &holderSignedAt)
	if err == sql.ErrNoRows {
		return username, nil
	}
	if err != nil {
		return "", err
	}

	holderWins := !signedAt.After(holderSignedAt)
	if holderWins {
		// Incoming loses: store under renamed form.
		return uniqueRenamedUsername(ctx, tx, username, userID)
	}

	// Incoming wins: rename the holder.
	newName, err := uniqueRenamedUsername(ctx, tx, username, holderID)
	if err != nil {
		return "", err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE users SET username = $1 WHERE id = $2`, newName, holderID); err != nil {
		return "", fmt.Errorf("rename collision loser: %w", err)
	}
	return username, nil
}

// CollisionRename builds the permanent suffix form username#idPrefix.
func CollisionRename(username, userID string, idLen int) string {
	if idLen < 1 {
		idLen = 4
	}
	if idLen > len(userID) {
		idLen = len(userID)
	}
	prefix := userID[:idLen]
	base := username
	const maxUsername = 255
	suffix := "#" + prefix
	if len(base)+len(suffix) > maxUsername {
		base = base[:maxUsername-len(suffix)]
	}
	return base + suffix
}

func uniqueRenamedUsername(ctx context.Context, tx *sql.Tx, username, loserID string) (string, error) {
	for n := 4; n <= len(loserID); n++ {
		candidate := CollisionRename(username, loserID, n)
		var exists bool
		err := tx.QueryRowContext(ctx, `
			SELECT EXISTS(SELECT 1 FROM users WHERE LOWER(username) = LOWER($1))
		`, candidate).Scan(&exists)
		if err != nil {
			return "", err
		}
		if !exists {
			return candidate, nil
		}
	}
	// Extremely unlikely: append a disambiguator.
	for i := 0; i < 1000; i++ {
		candidate := fmt.Sprintf("%s#%s-%d", trimForSuffix(username, len(loserID)+8), loserID, i)
		var exists bool
		err := tx.QueryRowContext(ctx, `
			SELECT EXISTS(SELECT 1 FROM users WHERE LOWER(username) = LOWER($1))
		`, candidate).Scan(&exists)
		if err != nil {
			return "", err
		}
		if !exists {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("could not allocate unique renamed username for %s", loserID)
}

func trimForSuffix(username string, suffixLen int) string {
	const maxUsername = 255
	if len(username)+suffixLen <= maxUsername {
		return username
	}
	return username[:maxUsername-suffixLen]
}

func nullIfEmpty(s string) interface{} {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	return s
}

// drainPendingFollows moves pending edges targeting targetUserID into the
// real follow tables, then deletes those pending rows.
func drainPendingFollows(ctx context.Context, tx *sql.Tx, targetUserID string) error {
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO user_following (user_id, following_user_id)
		SELECT follower_user_id, following_user_id
		FROM pending_follows
		WHERE following_user_id = $1
		ON CONFLICT DO NOTHING
	`, targetUserID); err != nil {
		return fmt.Errorf("drain pending following: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO user_followers (user_id, follower_user_id)
		SELECT following_user_id, follower_user_id
		FROM pending_follows
		WHERE following_user_id = $1
		ON CONFLICT DO NOTHING
	`, targetUserID); err != nil {
		return fmt.Errorf("drain pending followers: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		DELETE FROM pending_follows WHERE following_user_id = $1
	`, targetUserID); err != nil {
		return fmt.Errorf("delete pending follows: %w", err)
	}
	return nil
}

// bindClaimDeviceTx binds the claiming device in the own-identity claim transaction.
func bindClaimDeviceTx(ctx context.Context, tx *sql.Tx, userID, deviceID string, now time.Time) error {
	deviceID, err := identity.ParseDeviceID(deviceID)
	if err != nil {
		return err
	}

	if _, err := tx.ExecContext(ctx, `
		UPDATE user_devices SET revoked_at = $2
		WHERE user_id = $1 AND revoked_at IS NULL
	`, userID, now); err != nil {
		return err
	}

	_, err = tx.ExecContext(ctx, `
		INSERT INTO user_devices (user_id, device_id, linked_at, revoked_at)
		VALUES ($1, $2, $3, NULL)
	`, userID, deviceID, now)
	return err
}
