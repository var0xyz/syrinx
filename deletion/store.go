package deletion

import (
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// ErrConflict is returned when an existing reed_removals row differs from the
// cert being inserted (identical replay succeeds).
var ErrConflict = errors.New("reed removal conflict")

// Cert is the stored reed-removal attestation (DB shape).
type Cert struct {
	ReedID            string
	UserID            string
	UserSignature     string
	UserFingerprint   string
	ServerSignature   string
	ServerFingerprint string
	ServerSignedAt    time.Time
}

// InsertCert stores a reed-removal cert once. Same signatures → no-op;
// different signatures for the same (userID, reedID) → ErrConflict.
func InsertCert(db *sql.DB, cert Cert) error {
	cert.ServerSignedAt = cert.ServerSignedAt.UTC().Truncate(time.Second)

	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var existing Cert
	err = tx.QueryRow(`
		SELECT reed_id, user_id, user_signature, user_fingerprint,
		       server_signature, server_fingerprint, server_signed_at
		FROM reed_removals
		WHERE user_id = $1 AND reed_id = $2
		FOR UPDATE
	`, cert.UserID, cert.ReedID).Scan(
		&existing.ReedID,
		&existing.UserID,
		&existing.UserSignature,
		&existing.UserFingerprint,
		&existing.ServerSignature,
		&existing.ServerFingerprint,
		&existing.ServerSignedAt,
	)

	switch {
	case err == sql.ErrNoRows:
		if _, err := tx.Exec(`
			INSERT INTO reed_removals (
				reed_id, user_id, user_signature, user_fingerprint,
				server_signature, server_fingerprint, server_signed_at
			) VALUES ($1, $2, $3, $4, $5, $6, $7)
		`, cert.ReedID, cert.UserID, cert.UserSignature, cert.UserFingerprint,
			cert.ServerSignature, cert.ServerFingerprint, cert.ServerSignedAt); err != nil {
			return fmt.Errorf("insert reed removal: %w", err)
		}
	case err != nil:
		return err
	default:
		existing.ServerSignedAt = existing.ServerSignedAt.UTC().Truncate(time.Second)
		if existing.UserSignature != cert.UserSignature ||
			existing.UserFingerprint != cert.UserFingerprint ||
			existing.ServerSignature != cert.ServerSignature ||
			existing.ServerFingerprint != cert.ServerFingerprint ||
			!existing.ServerSignedAt.Equal(cert.ServerSignedAt) {
			return ErrConflict
		}
	}

	return tx.Commit()
}

// GetCert returns the stored cert for (userID, reedID), or nil if none.
func GetCert(db *sql.DB, userID, reedID string) (*Cert, error) {
	var cert Cert
	err := db.QueryRow(`
		SELECT reed_id, user_id, user_signature, user_fingerprint,
		       server_signature, server_fingerprint, server_signed_at
		FROM reed_removals
		WHERE user_id = $1 AND reed_id = $2
	`, userID, reedID).Scan(
		&cert.ReedID,
		&cert.UserID,
		&cert.UserSignature,
		&cert.UserFingerprint,
		&cert.ServerSignature,
		&cert.ServerFingerprint,
		&cert.ServerSignedAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &cert, nil
}
