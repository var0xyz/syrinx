package deletion

import (
	"database/sql"
	"fmt"
	"time"
	"unicode/utf8"
)

// MaxAccountNoteLen is the goodbye note limit (API + DB).
const MaxAccountNoteLen = 140

// AccountCert is the stored account-removal attestation (DB shape).
type AccountCert struct {
	UserID            string
	Note              string
	UserSignature     string
	UserFingerprint   string
	ServerSignature   string
	ServerFingerprint string
	ServerSignedAt    time.Time
}

// ValidateAccountNote returns an error if note exceeds MaxAccountNoteLen.
func ValidateAccountNote(note string) error {
	if utf8.RuneCountInString(note) > MaxAccountNoteLen {
		return fmt.Errorf("note exceeds %d characters", MaxAccountNoteLen)
	}
	return nil
}

// InsertAccountCert stores an account-removal cert once. Same signatures →
// no-op; different signatures for the same userID → ErrConflict.
func InsertAccountCert(db *sql.DB, cert AccountCert) error {
	if err := ValidateAccountNote(cert.Note); err != nil {
		return err
	}
	cert.ServerSignedAt = cert.ServerSignedAt.UTC().Truncate(time.Second)

	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var existing AccountCert
	err = tx.QueryRow(`
		SELECT user_id, note, user_signature, user_fingerprint,
		       server_signature, server_fingerprint, server_signed_at
		FROM account_removals
		WHERE user_id = $1
		FOR UPDATE
	`, cert.UserID).Scan(
		&existing.UserID,
		&existing.Note,
		&existing.UserSignature,
		&existing.UserFingerprint,
		&existing.ServerSignature,
		&existing.ServerFingerprint,
		&existing.ServerSignedAt,
	)

	switch {
	case err == sql.ErrNoRows:
		if _, err := tx.Exec(`
			INSERT INTO account_removals (
				user_id, note, user_signature, user_fingerprint,
				server_signature, server_fingerprint, server_signed_at
			) VALUES ($1, $2, $3, $4, $5, $6, $7)
		`, cert.UserID, cert.Note, cert.UserSignature, cert.UserFingerprint,
			cert.ServerSignature, cert.ServerFingerprint, cert.ServerSignedAt); err != nil {
			return fmt.Errorf("insert account removal: %w", err)
		}
	case err != nil:
		return err
	default:
		existing.ServerSignedAt = existing.ServerSignedAt.UTC().Truncate(time.Second)
		if existing.Note != cert.Note ||
			existing.UserSignature != cert.UserSignature ||
			existing.UserFingerprint != cert.UserFingerprint ||
			existing.ServerSignature != cert.ServerSignature ||
			existing.ServerFingerprint != cert.ServerFingerprint ||
			!existing.ServerSignedAt.Equal(cert.ServerSignedAt) {
			return ErrConflict
		}
	}

	return tx.Commit()
}

// GetAccountCert returns the stored account-removal cert, or nil if none.
func GetAccountCert(db *sql.DB, userID string) (*AccountCert, error) {
	var cert AccountCert
	err := db.QueryRow(`
		SELECT user_id, note, user_signature, user_fingerprint,
		       server_signature, server_fingerprint, server_signed_at
		FROM account_removals
		WHERE user_id = $1
	`, userID).Scan(
		&cert.UserID,
		&cert.Note,
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

// HasAccountRemoval reports whether userID has an account-removal cert.
func HasAccountRemoval(db *sql.DB, userID string) (bool, error) {
	var one int
	err := db.QueryRow(`
		SELECT 1 FROM account_removals WHERE user_id = $1
	`, userID).Scan(&one)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}
