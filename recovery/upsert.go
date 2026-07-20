package recovery

import (
	"database/sql"
	"fmt"
	"strings"
	"time"
)

// SaveIdentityResult describes what a save did to the users row.
type SaveIdentityResult struct {
	Created bool
	Updated bool // profile columns written (create or newer-wins)
}

// SaveOwnIdentity upserts a verified own-claim identity + nest. Clears
// unclaimed_accounts and records the user in ongoing_recoveries.
func SaveOwnIdentity(db *sql.DB, profile Profile, flat []FlatKey) (*SaveIdentityResult, error) {
	tx, err := db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	res, err := upsertIdentity(tx, profile, flat)
	if err != nil {
		return nil, err
	}
	if _, err := tx.Exec(`DELETE FROM unclaimed_accounts WHERE user_id = $1`, profile.ID); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(`
		INSERT INTO ongoing_recoveries (user_id) VALUES ($1)
		ON CONFLICT DO NOTHING
	`, profile.ID); err != nil {
		return nil, err
	}
	if err := drainPendingFollows(tx, profile.ID); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return res, nil
}

// SavePeerIdentity upserts a verified peer-reported identity + nest.
// Newly created rows are inserted into unclaimed_accounts; already-claimed
// accounts are never re-marked unclaimed.
func SavePeerIdentity(db *sql.DB, profile Profile, flat []FlatKey) (*SaveIdentityResult, error) {
	tx, err := db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	res, err := upsertIdentity(tx, profile, flat)
	if err != nil {
		return nil, err
	}
	if res.Created {
		if _, err := tx.Exec(`
			INSERT INTO unclaimed_accounts (user_id) VALUES ($1)
			ON CONFLICT DO NOTHING
		`, profile.ID); err != nil {
			return nil, err
		}
	}
	if err := drainPendingFollows(tx, profile.ID); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return res, nil
}

func upsertIdentity(tx *sql.Tx, profile Profile, flat []FlatKey) (*SaveIdentityResult, error) {
	if len(flat) == 0 {
		return nil, fmt.Errorf("empty key nest")
	}
	activeFP := flat[len(flat)-1].Key.Fingerprint
	incomingSignedAt := profile.Server.Timestamp.UTC().Truncate(time.Second)

	var existingSignedAt sql.NullTime
	err := tx.QueryRow(`
		SELECT server_signed_at FROM users WHERE id = $1 FOR UPDATE
	`, profile.ID).Scan(&existingSignedAt)

	created := false
	updated := false

	switch {
	case err == sql.ErrNoRows:
		if err := insertUser(tx, profile, activeFP, incomingSignedAt); err != nil {
			return nil, err
		}
		created = true
		updated = true
	case err != nil:
		return nil, err
	default:
		wrote, err := updateUserIfNewer(tx, profile, activeFP, existingSignedAt, incomingSignedAt)
		if err != nil {
			return nil, err
		}
		updated = wrote
	}

	if err := insertKeys(tx, profile.ID, flat); err != nil {
		return nil, err
	}

	return &SaveIdentityResult{Created: created, Updated: updated}, nil
}

func insertUser(tx *sql.Tx, profile Profile, activeFP string, signedAt time.Time) error {
	username, err := claimUsername(tx, profile.ID, profile.Username, signedAt)
	if err != nil {
		return err
	}
	_, err = tx.Exec(`
		INSERT INTO users (
			id, username, created_at, fingerprint, avatar_url, bio,
			user_signature, server_signature,
			server_signed_at, server_fingerprint
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
	`,
		profile.ID, username, profile.MemberSince.UTC().Truncate(time.Second),
		activeFP, nullIfEmpty(profile.AvatarURL), nullIfEmpty(profile.Bio),
		profile.Signature, profile.Server.Signature,
		signedAt, profile.Server.Fingerprint,
	)
	if err != nil {
		return fmt.Errorf("insert user: %w", err)
	}
	return nil
}

func updateUserIfNewer(
	tx *sql.Tx,
	profile Profile,
	activeFP string,
	existingSignedAt sql.NullTime,
	incomingSignedAt time.Time,
) (bool, error) {
	shouldUpdate := !existingSignedAt.Valid || incomingSignedAt.After(existingSignedAt.Time)
	if !shouldUpdate {
		return false, nil
	}
	username, err := claimUsername(tx, profile.ID, profile.Username, incomingSignedAt)
	if err != nil {
		return false, err
	}
	_, err = tx.Exec(`
		UPDATE users SET
			username = $1,
			avatar_url = $2,
			bio = $3,
			fingerprint = $4,
			user_signature = $5,
			server_signature = $6,
			server_signed_at = $7,
			server_fingerprint = $8
		WHERE id = $9
	`,
		username, nullIfEmpty(profile.AvatarURL), nullIfEmpty(profile.Bio),
		activeFP, profile.Signature, profile.Server.Signature,
		incomingSignedAt, profile.Server.Fingerprint, profile.ID,
	)
	if err != nil {
		return false, fmt.Errorf("update user: %w", err)
	}
	return true, nil
}

func insertKeys(tx *sql.Tx, userID string, flat []FlatKey) error {
	for i, fk := range flat {
		var predFP, predSig interface{}
		if fk.PredecessorFingerprint != "" {
			predFP = fk.PredecessorFingerprint
			predSig = fk.PredecessorSignature
		}
		_, err := tx.Exec(`
			INSERT INTO user_keys (
				fingerprint, owner, armor, created_at, expires_at,
				server_signature, server_fingerprint, server_signed_at,
				predecessor_signature, predecessor_fingerprint
			) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
			ON CONFLICT (owner, fingerprint) DO NOTHING
		`,
			fk.Key.Fingerprint, userID, fk.Key.Armor,
			fk.Key.CreatedAt.UTC().Truncate(time.Second), fk.Key.ExpiresAt,
			fk.Key.Server.Signature, fk.Key.Server.Fingerprint,
			fk.Key.Server.Timestamp.UTC().Truncate(time.Second),
			predSig, predFP,
		)
		if err != nil {
			return fmt.Errorf("insert key %s: %w", fk.Key.Fingerprint, err)
		}

		if fk.Revocation != nil {
			_, err := tx.Exec(`
				INSERT INTO user_key_revocations (
					fingerprint, owner, reason,
					user_signature, server_signature,
					server_fingerprint, server_signed_at
				) VALUES ($1, $2, $3, $4, $5, $6, $7)
				ON CONFLICT (fingerprint, owner) DO NOTHING
			`,
				fk.Revocation.Fingerprint, userID, fk.Revocation.Reason,
				fk.Revocation.Signature, fk.Revocation.Server.Signature,
				fk.Revocation.Server.Fingerprint,
				fk.Revocation.Server.Timestamp.UTC().Truncate(time.Second),
			)
			if err != nil {
				return fmt.Errorf("insert revocation %s: %w", fk.Key.Fingerprint, err)
			}
		}

		// After inserting a newer key, point the predecessor revocation's successor.
		if i > 0 {
			_, err := tx.Exec(`
				UPDATE user_key_revocations
				SET successor = $1
				WHERE fingerprint = $2 AND owner = $3
				  AND (successor IS NULL OR successor = '')
			`, fk.Key.Fingerprint, flat[i-1].Key.Fingerprint, userID)
			if err != nil {
				return fmt.Errorf("set successor for %s: %w", flat[i-1].Key.Fingerprint, err)
			}
		}
	}
	return nil
}

func claimUsername(tx *sql.Tx, userID, username string, signedAt time.Time) (string, error) {
	var holderID string
	var holderSignedAt sql.NullTime
	err := tx.QueryRow(`
		SELECT id, server_signed_at FROM users
		WHERE LOWER(username) = LOWER($1) AND id <> $2
		FOR UPDATE
	`, username, userID).Scan(&holderID, &holderSignedAt)
	if err == sql.ErrNoRows {
		return username, nil
	}
	if err != nil {
		return "", err
	}

	holderWins := holderSignedAt.Valid && !signedAt.After(holderSignedAt.Time)
	if holderWins {
		// Incoming loses: store under renamed form.
		return uniqueRenamedUsername(tx, username, userID)
	}

	// Incoming wins: rename the holder.
	newName, err := uniqueRenamedUsername(tx, username, holderID)
	if err != nil {
		return "", err
	}
	if _, err := tx.Exec(`UPDATE users SET username = $1 WHERE id = $2`, newName, holderID); err != nil {
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

func uniqueRenamedUsername(tx *sql.Tx, username, loserID string) (string, error) {
	for n := 4; n <= len(loserID); n++ {
		candidate := CollisionRename(username, loserID, n)
		var exists bool
		err := tx.QueryRow(`
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
		err := tx.QueryRow(`
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
func drainPendingFollows(tx *sql.Tx, targetUserID string) error {
	if _, err := tx.Exec(`
		INSERT INTO user_following (user_id, following_user_id)
		SELECT follower_user_id, following_user_id
		FROM pending_follows
		WHERE following_user_id = $1
		ON CONFLICT DO NOTHING
	`, targetUserID); err != nil {
		return fmt.Errorf("drain pending following: %w", err)
	}
	if _, err := tx.Exec(`
		INSERT INTO user_followers (user_id, follower_user_id)
		SELECT following_user_id, follower_user_id
		FROM pending_follows
		WHERE following_user_id = $1
		ON CONFLICT DO NOTHING
	`, targetUserID); err != nil {
		return fmt.Errorf("drain pending followers: %w", err)
	}
	if _, err := tx.Exec(`
		DELETE FROM pending_follows WHERE following_user_id = $1
	`, targetUserID); err != nil {
		return fmt.Errorf("delete pending follows: %w", err)
	}
	return nil
}
