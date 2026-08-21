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
// different signatures for the same reedID → ErrConflict. cert.ReedID is
// canonical (embeds the author), so no separate user_id column is needed.
func InsertCert(ctx context.Context, db *sql.DB, cert Cert, serverID string) error {
	cert.ServerSignedAt = cert.ServerSignedAt.UTC().Truncate(time.Second)

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	existing, err := loadReedCertTx(ctx, tx, cert.ReedID, true)
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
				reed_id, public_key_id,
				user_signature_id, server_signature_id
			) VALUES ($1, $2, $3, $4)
		`, cert.ReedID, cert.UserFingerprint, userSigID, serverSigID); err != nil {
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

// GetCert returns the stored cert for reedID (canonical), or nil if none.
func GetCert(ctx context.Context, db *sql.DB, reedID, serverID string) (*Cert, error) {
	cert, err := loadReedCertTx(ctx, db, reedID, false)
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

func loadReedCertTx(ctx context.Context, q reedQuerier, reedID string, forUpdate bool) (*Cert, error) {
	query := `
		SELECT public_key_id, user_signature_id, server_signature_id
		FROM reed_removals
		WHERE reed_id = $1`
	if forUpdate {
		query += ` FOR UPDATE`
	}
	var userFP string
	var userSigID, serverSigID int64
	err := q.QueryRowContext(ctx, query, reedID).Scan(&userFP, &userSigID, &serverSigID)
	if err != nil {
		return nil, err
	}
	userID, _, _, ok := identity.ParseKeyFingerprint(identity.IdentityID(reedID))
	if !ok {
		return nil, fmt.Errorf("malformed reed id: %s", reedID)
	}
	return assembleReedCert(ctx, q, reedID, userID, userFP, userSigID, serverSigID)
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
