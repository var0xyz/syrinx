package recovery

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"syrinx/coverage"
	"syrinx/identity"
	"syrinx/roles"
	"syrinx/signing"
)

// Every subject handled by this file (profile.ID, follow targets, reed
// authors/reporters) is a bare userID local to serverID; every identity
// minted or looked up here uses identity.CanonicalID(serverID, userID) —
// cross-server subjects aren't handled by this package.

// SaveIdentityResult describes what a save did to the users row.
type SaveIdentityResult struct {
	Created bool
	Updated bool // profile columns written (create or newer-wins)
	// Rejected is true when profile.Username collided with an existing
	// holder whose server_signed_at was newer or equal — the incoming
	// submission was discarded, nothing was written.
	Rejected bool
}

// errUsernameCollisionLoss signals that the incoming profile lost a
// username collision and must not be persisted.
var errUsernameCollisionLoss = fmt.Errorf("incoming profile lost username collision")

// SaveOwnIdentity upserts a verified own-claim identity + nest. Clears
// unclaimed_accounts and records the user in ongoing_recoveries.
func SaveOwnIdentity(ctx context.Context, db *sql.DB, serverID string, profile Profile, flat []FlatKey, deviceID string) (*SaveIdentityResult, error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	selfIdentity := identity.CanonicalID(serverID, profile.ID)

	res, err := upsertIdentity(ctx, tx, serverID, profile, flat)
	if err != nil {
		return nil, err
	}
	if res.Rejected {
		if err := tx.Commit(); err != nil {
			return nil, err
		}
		return res, nil
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM unclaimed_accounts WHERE user_id = $1`, selfIdentity); err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO ongoing_recoveries (user_id) VALUES ($1)
		ON CONFLICT DO NOTHING
	`, selfIdentity); err != nil {
		return nil, err
	}
	if err := drainPendingFollows(ctx, tx, profile.ID, selfIdentity); err != nil {
		return nil, err
	}
	if deviceID != "" {
		if err := bindClaimDeviceTx(ctx, tx, selfIdentity, deviceID, profile.ServerSignature.Timestamp.UTC()); err != nil {
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
func SavePeerIdentity(ctx context.Context, db *sql.DB, serverID string, profile Profile, flat []FlatKey) (*SaveIdentityResult, error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	selfIdentity := identity.CanonicalID(serverID, profile.ID)

	res, err := upsertIdentity(ctx, tx, serverID, profile, flat)
	if err != nil {
		return nil, err
	}
	if res.Rejected {
		if err := tx.Commit(); err != nil {
			return nil, err
		}
		return res, nil
	}
	if res.Created {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO unclaimed_accounts (user_id) VALUES ($1)
			ON CONFLICT DO NOTHING
		`, selfIdentity); err != nil {
			return nil, err
		}
	}
	if err := drainPendingFollows(ctx, tx, profile.ID, selfIdentity); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return res, nil
}

func upsertIdentity(ctx context.Context, tx *sql.Tx, serverID string, profile Profile, flat []FlatKey) (*SaveIdentityResult, error) {
	if len(flat) == 0 {
		return nil, fmt.Errorf("empty key nest")
	}
	activeFP := flat[len(flat)-1].Key.Fingerprint
	incomingSignedAt := profile.ServerSignature.Timestamp.UTC().Truncate(time.Second)
	selfIdentity := identity.CanonicalID(serverID, profile.ID)

	// Lock/check the identities row, not users — identities is the actual FK
	// target. users.id IS identities.id directly, so the join is on u.id.
	var existingSignedAt time.Time
	err := tx.QueryRowContext(ctx, `
		SELECT ss.signed_at
		FROM identities i
		JOIN users u ON u.id = i.id
		JOIN server_signatures ss ON ss.id = u.server_signature_id
		WHERE i.id = $1
		FOR UPDATE OF i
	`, selfIdentity).Scan(&existingSignedAt)

	created := false
	updated := false

	switch {
	case err == sql.ErrNoRows:
		if err := insertUser(ctx, tx, serverID, profile, activeFP, incomingSignedAt); err != nil {
			if errors.Is(err, errUsernameCollisionLoss) {
				return &SaveIdentityResult{Rejected: true}, nil
			}
			return nil, err
		}
		created = true
		updated = true
	case err != nil:
		return nil, err
	default:
		wrote, err := updateUserIfNewer(ctx, tx, selfIdentity, profile, activeFP, existingSignedAt, incomingSignedAt)
		if err != nil {
			if errors.Is(err, errUsernameCollisionLoss) {
				return &SaveIdentityResult{Rejected: true}, nil
			}
			return nil, err
		}
		updated = wrote
	}

	if err := insertKeys(ctx, tx, selfIdentity, flat); err != nil {
		return nil, err
	}

	return &SaveIdentityResult{Created: created, Updated: updated}, nil
}

// insertUser mints both the identities row and its satellite users row.
// Every subject in this package is local, so the identities row is minted
// verified=TRUE with server_id=serverID (self) — mirroring services.go's Signup.
func insertUser(ctx context.Context, tx *sql.Tx, serverID string, profile Profile, activeFP string, signedAt time.Time) error {
	selfIdentity := identity.CanonicalID(serverID, profile.ID)

	username, err := claimUsername(ctx, tx, selfIdentity, profile.Username, signedAt)
	if err != nil {
		return err
	}
	if err := roles.ValidateProfileRole(profile.ID, profile.Role, serverID); err != nil {
		return err
	}

	// Mint the identities row before its satellite users row — users.id
	// REFERENCES identities(id) ON DELETE CASCADE, so this must land first.
	// ON CONFLICT DO NOTHING: a stale identities row surviving an earlier
	// partial run is safe to leave in place.
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO identities (id, remote_user_id, server_id, verified)
		VALUES ($1, $2, $3, TRUE)
		ON CONFLICT (id) DO NOTHING
	`, selfIdentity, profile.ID, serverID); err != nil {
		return fmt.Errorf("insert identity: %w", err)
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
	// invited_by FKs identities(id): the inviter is always a local user,
	// so the same CanonicalID conversion applies, as in services.go's Signup.
	var invitedBy any
	if inviter := profileInvitedByID(profile); inviter != "" {
		invitedBy = identity.CanonicalID(serverID, inviter)
	}
	// users.id IS identities.id directly — selfIdentity is the sole PK
	// value, same pattern as services.go's Signup INSERT.
	_, err = tx.ExecContext(ctx, `
		INSERT INTO users (
			id, username, role, created_at, user_fingerprint, bio,
			user_signature_id, server_signature_id, invited_by
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
	`,
		selfIdentity, username, profile.Role, profile.MemberSince.UTC().Truncate(time.Second),
		activeFP, nullIfEmpty(profile.Bio),
		userSignatureID, serverSignatureID, invitedBy,
	)
	if err != nil {
		return fmt.Errorf("insert user: %w", err)
	}
	return coverage.BumpActiveUsers(ctx, tx, 1)
}

func updateUserIfNewer(
	ctx context.Context,
	tx *sql.Tx,
	selfIdentity identity.IdentityID,
	profile Profile,
	activeFP string,
	existingSignedAt time.Time,
	incomingSignedAt time.Time,
) (bool, error) {
	if !incomingSignedAt.After(existingSignedAt) {
		return false, nil
	}
	username, err := claimUsername(ctx, tx, selfIdentity, profile.Username, incomingSignedAt)
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
	// Profile fields only — role/username/bio/fingerprint/signatures. This
	// updates the existing satellite users row in place; the identities row
	// is untouched. WHERE id = $7 targets users.id, which IS identities.id
	// — must bind selfIdentity, not bare profile.ID, or this updates zero rows.
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
		profile.Role, activeFP, userSignatureID, serverSignatureID, selfIdentity,
	)
	if err != nil {
		return false, fmt.Errorf("update user: %w", err)
	}
	return true, nil
}

func insertKeys(ctx context.Context, tx *sql.Tx, owner identity.IdentityID, flat []FlatKey) error {
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
			fk.Key.Fingerprint, owner, fk.Key.Armor,
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
				fk.Revocation.Fingerprint, owner, fk.Revocation.Reason,
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
			`, fk.Key.Fingerprint, flat[i-1].Key.Fingerprint, owner)
			if err != nil {
				return fmt.Errorf("set successor for %s: %w", flat[i-1].Key.Fingerprint, err)
			}
		}
	}
	return nil
}

// claimUsername resolves a username collision by deleting whichever side
// has the older (or equal) server_signed_at. If the incoming profile loses,
// the existing holder is left untouched and errUsernameCollisionLoss is
// returned — callers must abort the whole upsert without writing anything.
// If the incoming profile wins (or there is no collision), the holder row
// (if any) is hard-deleted — ON DELETE CASCADE removes its keys,
// signatures, and recovery/social bookkeeping — and username is returned
// unchanged for the caller to store.
func claimUsername(ctx context.Context, tx *sql.Tx, selfIdentity identity.IdentityID, username string, signedAt time.Time) (string, error) {
	var holderIdentityID string
	var holderSignedAt time.Time
	// users.id IS identities.id directly now, so both the self-exclusion
	// comparison and the selected holder id must use that form — comparing
	// bare here would make "u.id <> $2" always-true, wrongly treating a
	// same-identity re-report as a collision.
	err := tx.QueryRowContext(ctx, `
		SELECT u.id, ss.signed_at
		FROM users u
		JOIN server_signatures ss ON ss.id = u.server_signature_id
		WHERE LOWER(u.username) = LOWER($1) AND u.id <> $2
		FOR UPDATE OF u
	`, username, selfIdentity).Scan(&holderIdentityID, &holderSignedAt)
	if err == sql.ErrNoRows {
		return username, nil
	}
	if err != nil {
		return "", err
	}

	holderWins := !signedAt.After(holderSignedAt)
	if holderWins {
		return "", errUsernameCollisionLoss
	}

	// Incoming wins: the holder is a different, older, provably-signed
	// identity — not a duplicate of the incoming one. Deleting it is
	// destructive by design (see specs/recovery/README.md#recovery-flow):
	// a renamed-in-place row would carry a username that no longer matches
	// what its owner signed, permanently breaking verification instead.
	//
	// Deletes FROM identities, not FROM users: identities is the actual FK
	// root, so ON DELETE CASCADE removes the satellite users row and
	// everything else (keys, signatures, recovery/social bookkeeping) in one shot.
	if _, err := tx.ExecContext(ctx, `DELETE FROM identities WHERE id = $1`, holderIdentityID); err != nil {
		return "", fmt.Errorf("delete username collision loser %s: %w", holderIdentityID, err)
	}
	if err := coverage.BumpActiveUsers(ctx, tx, -1); err != nil {
		return "", err
	}
	return username, nil
}

func nullIfEmpty(s string) interface{} {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	return s
}

// drainPendingFollows moves pending edges targeting targetUserID into the
// real follow tables, then deletes those pending rows. targetUserID is bare
// (pending_follows has no FK); targetIdentity is the same subject's
// identities.id, used for the fully-FK'd destination tables.
func drainPendingFollows(ctx context.Context, tx *sql.Tx, targetUserID string, targetIdentity identity.IdentityID) error {
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO user_following (user_id, following_user_id)
		SELECT follower_user_id, $2
		FROM pending_follows
		WHERE following_user_id = $1
		ON CONFLICT DO NOTHING
	`, targetUserID, targetIdentity); err != nil {
		return fmt.Errorf("drain pending following: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO user_followers (user_id, follower_user_id)
		SELECT $2, follower_user_id
		FROM pending_follows
		WHERE following_user_id = $1
		ON CONFLICT DO NOTHING
	`, targetUserID, targetIdentity); err != nil {
		return fmt.Errorf("drain pending followers: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		DELETE FROM pending_follows WHERE following_user_id = $1
	`, targetUserID); err != nil {
		return fmt.Errorf("delete pending follows: %w", err)
	}
	return nil
}

// bindClaimDeviceTx binds the claiming device in the own-identity claim
// transaction. Device binding is local-account-only, so ownerIdentity is
// always a local selfIdentity — same convention as services.go's BindDeviceTx.
func bindClaimDeviceTx(ctx context.Context, tx *sql.Tx, ownerIdentity identity.IdentityID, deviceID string, now time.Time) error {
	deviceID, err := identity.ParseDeviceID(deviceID)
	if err != nil {
		return err
	}

	if _, err := tx.ExecContext(ctx, `
		UPDATE user_devices SET revoked_at = $2
		WHERE user_id = $1 AND revoked_at IS NULL
	`, ownerIdentity, now); err != nil {
		return err
	}

	_, err = tx.ExecContext(ctx, `
		INSERT INTO user_devices (user_id, device_id, linked_at, revoked_at)
		VALUES ($1, $2, $3, NULL)
	`, ownerIdentity, deviceID, now)
	return err
}
