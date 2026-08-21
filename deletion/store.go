package deletion

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"syrinx/identity"
	"syrinx/signing"
)

// ErrConflict is returned when an existing removal row differs from the
// cert being inserted (identical replay succeeds). Used for reed and
// account removals.
var ErrConflict = errors.New("removal conflict")

// Cert is the reed-removal attestation (in-memory / wire-facing shape).
//
// UserID asymmetry (same as AccountCert): InsertCert/GetCert's userID
// params are bare, but every Cert RETURNED by GetCert/loadReedCertTx holds
// the full "userID@serverID" form.
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
// cert.UserID stays bare and is converted internally before touching
// reed_removals, which FKs to identities(id).
func InsertCert(ctx context.Context, db *sql.DB, cert Cert, serverID string) error {
	cert.ServerSignedAt = cert.ServerSignedAt.UTC().Truncate(time.Second)
	selfIdentity := identity.CanonicalID(serverID, cert.UserID)

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	existing, err := loadReedCertTx(ctx, tx, selfIdentity, cert.ReedID, true)
	switch {
	case err == sql.ErrNoRows:
		userSigID, err := signing.InsertUserSignature(
			ctx, tx, cert.UserFingerprint, cert.UserSignature,
		)
		if err != nil {
			return err
		}
		serverSigID, err := signing.InsertServerSignature(
			ctx, tx, cert.ServerFingerprint, cert.ServerSignature, cert.ServerSignedAt,
		)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO reed_removals (
				reed_id, user_id, public_key_id,
				user_signature_id, server_signature_id
			) VALUES ($1, $2, $3, $4, $5)
		`, cert.ReedID, selfIdentity, cert.UserFingerprint, userSigID, serverSigID); err != nil {
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
// userID (the lookup param) is bare; the RETURNED cert's UserID field is
// the full "userID@serverID" form — see Cert's doc comment.
func GetCert(ctx context.Context, db *sql.DB, userID, reedID, serverID string) (*Cert, error) {
	selfIdentity := identity.CanonicalID(serverID, userID)
	cert, err := loadReedCertTx(ctx, db, selfIdentity, reedID, false)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return cert, nil
}

type reedQuerier interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

func loadReedCertTx(ctx context.Context, q reedQuerier, selfIdentity identity.IdentityID, reedID string, forUpdate bool) (*Cert, error) {
	query := `
		SELECT public_key_id, user_signature_id, server_signature_id
		FROM reed_removals
		WHERE user_id = $1 AND reed_id = $2`
	if forUpdate {
		query += ` FOR UPDATE`
	}
	var userFP string
	var userSigID, serverSigID int64
	err := q.QueryRowContext(ctx, query, selfIdentity, reedID).Scan(&userFP, &userSigID, &serverSigID)
	if err != nil {
		return nil, err
	}
	return assembleReedCert(ctx, q, reedID, string(selfIdentity), userFP, userSigID, serverSigID)
}

func assembleReedCert(ctx context.Context, q reedQuerier, reedID, userID, userFP string, userSigID, serverSigID int64) (*Cert, error) {
	// signing helpers need DBTX; *sql.DB and *sql.Tx both work.
	dbtx, ok := q.(signing.DBTX)
	if !ok {
		return nil, fmt.Errorf("reed removal load: querier is not signing.DBTX")
	}
	userRow, err := signing.GetUserSignature(ctx, dbtx, userSigID)
	if err != nil {
		return nil, err
	}
	serverRow, err := signing.GetServerSignature(ctx, dbtx, serverSigID)
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
