package deletion

import (
	"database/sql"
	"fmt"
	"time"
	"unicode/utf8"

	"syrinx/signing"
)

// MaxAccountNoteLen is the goodbye note limit (API + DB).
const MaxAccountNoteLen = 140

// AccountCert is the account-removal attestation (in-memory / wire-facing).
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

	existing, err := loadAccountCertTx(tx, cert.UserID, true)
	switch {
	case err == sql.ErrNoRows:
		userSigID, err := signing.InsertUserSignature(
			tx, cert.UserFingerprint, cert.UserSignature,
		)
		if err != nil {
			return err
		}
		serverSigID, err := signing.InsertServerSignature(
			tx, cert.ServerFingerprint, cert.ServerSignature, cert.ServerSignedAt,
		)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(`
			INSERT INTO account_removals (
				user_id, note, user_fingerprint,
				user_signature_id, server_signature_id
			) VALUES ($1, $2, $3, $4, $5)
		`, cert.UserID, cert.Note, cert.UserFingerprint, userSigID, serverSigID); err != nil {
			return fmt.Errorf("insert account removal: %w", err)
		}
	case err != nil:
		return err
	default:
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
	cert, err := loadAccountCertTx(db, userID, false)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return cert, nil
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

func loadAccountCertTx(q reedQuerier, userID string, forUpdate bool) (*AccountCert, error) {
	query := `
		SELECT note, user_fingerprint, user_signature_id, server_signature_id
		FROM account_removals
		WHERE user_id = $1`
	if forUpdate {
		query += ` FOR UPDATE`
	}
	var note, userFP string
	var userSigID, serverSigID int64
	err := q.QueryRow(query, userID).Scan(&note, &userFP, &userSigID, &serverSigID)
	if err != nil {
		return nil, err
	}
	dbtx, ok := q.(signing.DBTX)
	if !ok {
		return nil, fmt.Errorf("account removal load: querier is not signing.DBTX")
	}
	userRow, err := signing.GetUserSignature(dbtx, userSigID)
	if err != nil {
		return nil, err
	}
	serverRow, err := signing.GetServerSignature(dbtx, serverSigID)
	if err != nil {
		return nil, err
	}
	return &AccountCert{
		UserID:            userID,
		Note:              note,
		UserFingerprint:   userFP,
		UserSignature:     userRow.Signature,
		ServerSignature:   serverRow.Signature,
		ServerFingerprint: serverRow.Fingerprint,
		ServerSignedAt:    serverRow.SignedAt,
	}, nil
}
