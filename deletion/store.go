package deletion

import (
	"database/sql"
	"errors"
	"fmt"
	"time"

	"syrinx/signing"
)

// ErrConflict is returned when an existing removal row differs from the
// cert being inserted (identical replay succeeds). Used for reed and
// account removals.
var ErrConflict = errors.New("removal conflict")

// Cert is the reed-removal attestation (in-memory / wire-facing shape).
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

	existing, err := loadReedCertTx(tx, cert.UserID, cert.ReedID, true)
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
			INSERT INTO reed_removals (
				reed_id, user_id, user_fingerprint,
				user_signature_id, server_signature_id
			) VALUES ($1, $2, $3, $4, $5)
		`, cert.ReedID, cert.UserID, cert.UserFingerprint, userSigID, serverSigID); err != nil {
			return fmt.Errorf("insert reed removal: %w", err)
		}
	case err != nil:
		return err
	default:
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
	cert, err := loadReedCertTx(db, userID, reedID, false)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return cert, nil
}

// GetCertByReedID looks up a removal cert by reed id only (UNIQUE).
func GetCertByReedID(db *sql.DB, reedID string) (*Cert, error) {
	var userID string
	var userSigID, serverSigID int64
	var userFP string
	err := db.QueryRow(`
		SELECT user_id, user_fingerprint, user_signature_id, server_signature_id
		FROM reed_removals
		WHERE reed_id = $1
	`, reedID).Scan(&userID, &userFP, &userSigID, &serverSigID)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return assembleReedCert(db, reedID, userID, userFP, userSigID, serverSigID)
}

type reedQuerier interface {
	QueryRow(query string, args ...any) *sql.Row
}

func loadReedCertTx(q reedQuerier, userID, reedID string, forUpdate bool) (*Cert, error) {
	query := `
		SELECT user_fingerprint, user_signature_id, server_signature_id
		FROM reed_removals
		WHERE user_id = $1 AND reed_id = $2`
	if forUpdate {
		query += ` FOR UPDATE`
	}
	var userFP string
	var userSigID, serverSigID int64
	err := q.QueryRow(query, userID, reedID).Scan(&userFP, &userSigID, &serverSigID)
	if err != nil {
		return nil, err
	}
	return assembleReedCert(q, reedID, userID, userFP, userSigID, serverSigID)
}

func assembleReedCert(q reedQuerier, reedID, userID, userFP string, userSigID, serverSigID int64) (*Cert, error) {
	// signing helpers need DBTX; *sql.DB and *sql.Tx both work.
	dbtx, ok := q.(signing.DBTX)
	if !ok {
		return nil, fmt.Errorf("reed removal load: querier is not signing.DBTX")
	}
	userRow, err := signing.GetUserSignature(dbtx, userSigID)
	if err != nil {
		return nil, err
	}
	serverRow, err := signing.GetServerSignature(dbtx, serverSigID)
	if err != nil {
		return nil, err
	}
	return &Cert{
		ReedID:            reedID,
		UserID:            userID,
		UserFingerprint:   userFP,
		UserSignature:     userRow.Signature,
		ServerSignature:   serverRow.Signature,
		ServerFingerprint: serverRow.Fingerprint,
		ServerSignedAt:    serverRow.SignedAt,
	}, nil
}
