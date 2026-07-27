package recovery

import (
	"database/sql"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/lib/pq"
)

// ErrReedConflict is returned when an existing reed row's metadata does not
// match the countersigned submission (should be impossible under the bind).
var ErrReedConflict = errors.New("reed metadata conflict")

// ErrAuthorNotFound is returned when the reed author is not in users.
var ErrAuthorNotFound = errors.New("reed author not found")

// SaveReed inserts reed metadata if missing; rejects conflicting metadata;
// always upserts an allocation for reporterUserID. Caller must have verified
// the countersignature.
func SaveReed(
	db *sql.DB,
	reedID, authorID, fingerprint string,
	signedAt time.Time,
	reporterUserID string,
) error {
	signedAt = signedAt.UTC().Truncate(time.Second)

	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var exists bool
	if err := tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM users WHERE id = $1)`, authorID).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return ErrAuthorNotFound
	}

	var existingAuthor, existingFP string
	var existingAt time.Time
	err = tx.QueryRow(`
		SELECT user_id, private_key_fingerprint, signed_at
		FROM reeds WHERE id = $1
		FOR UPDATE
	`, reedID).Scan(&existingAuthor, &existingFP, &existingAt)

	switch {
	case err == sql.ErrNoRows:
		if _, err := tx.Exec(`
			INSERT INTO reeds (id, user_id, private_key_fingerprint, signed_at)
			VALUES ($1, $2, $3, $4)
		`, reedID, authorID, fingerprint, signedAt); err != nil {
			return fmt.Errorf("insert reed: %w", err)
		}
	case err != nil:
		return err
	default:
		existingAt = existingAt.UTC().Truncate(time.Second)
		if existingAuthor != authorID || existingFP != fingerprint || !existingAt.Equal(signedAt) {
			log.Printf(
				"[ERR] recovery reed conflict: reedID=%s existing=(author=%s fp=%s at=%s) incoming=(author=%s fp=%s at=%s)",
				reedID, existingAuthor, existingFP, existingAt.Format(time.RFC3339),
				authorID, fingerprint, signedAt.Format(time.RFC3339),
			)
			return ErrReedConflict
		}
	}

	if _, err := tx.Exec(`
		INSERT INTO reed_allocations (reed_id, holder_user_id, author_user_id)
		VALUES ($1, $2, $3)
		ON CONFLICT DO NOTHING
	`, reedID, reporterUserID, authorID); err != nil {
		return fmt.Errorf("insert reed allocation: %w", err)
	}

	return tx.Commit()
}

// SaveFollowing writes follow edges for followerUserID. Existing targets go
// into user_following / user_followers; missing targets go into pending_follows.
// Caller must reject self-follows before calling.
func SaveFollowing(db *sql.DB, followerUserID string, targetIDs []string) error {
	if len(targetIDs) == 0 {
		return nil
	}

	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	existing := make(map[string]bool, len(targetIDs))
	rows, err := tx.Query(`
		SELECT id FROM users WHERE id = ANY($1)
	`, pq.Array(targetIDs))
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
		if existing[targetID] {
			if _, err := tx.Exec(`
				INSERT INTO user_following (user_id, following_user_id)
				VALUES ($1, $2)
				ON CONFLICT DO NOTHING
			`, followerUserID, targetID); err != nil {
				return err
			}
			if _, err := tx.Exec(`
				INSERT INTO user_followers (user_id, follower_user_id)
				VALUES ($1, $2)
				ON CONFLICT DO NOTHING
			`, targetID, followerUserID); err != nil {
				return err
			}
			continue
		}
		if _, err := tx.Exec(`
			INSERT INTO pending_follows (follower_user_id, following_user_id)
			VALUES ($1, $2)
			ON CONFLICT DO NOTHING
		`, followerUserID, targetID); err != nil {
			return err
		}
	}

	return tx.Commit()
}
