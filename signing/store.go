package signing

import (
	"database/sql"
	"fmt"
	"time"
)

// DBTX is the subset of *sql.DB / *sql.Tx needed by signature store helpers.
type DBTX interface {
	Exec(query string, args ...any) (sql.Result, error)
	QueryRow(query string, args ...any) *sql.Row
}

// UserSignature is one row in user_signatures.
type UserSignature struct {
	ID          int64
	Fingerprint string
	Signature   string
}

// ServerSignature is one row in server_signatures.
type ServerSignature struct {
	ID          int64
	Fingerprint string
	Signature   string
	SignedAt    time.Time
}

// WireUserSignature is the nested userSignature wire block.
type WireUserSignature struct {
	Fingerprint string `json:"fingerprint"`
	Armor       string `json:"armor"`
}

// WireServerSignature is the nested serverSignature wire block.
type WireServerSignature struct {
	ServerID    string    `json:"serverID"`
	Fingerprint string    `json:"fingerprint"`
	Armor       string    `json:"armor"`
	Timestamp   time.Time `json:"timestamp"`
}

// InsertUserSignature inserts a user attestation row and returns its id.
func InsertUserSignature(db DBTX, fingerprint, signature string) (int64, error) {
	var id int64
	err := db.QueryRow(`
		INSERT INTO user_signatures (fingerprint, signature)
		VALUES ($1, $2)
		RETURNING id
	`, fingerprint, signature).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("insert user_signatures: %w", err)
	}
	return id, nil
}

// InsertServerSignature inserts a server countersignature row and returns
// its id. signedAt is stored UTC truncated to seconds.
func InsertServerSignature(db DBTX, fingerprint, signature string, signedAt time.Time) (int64, error) {
	signedAt = signedAt.UTC().Truncate(time.Second)
	var id int64
	err := db.QueryRow(`
		INSERT INTO server_signatures (fingerprint, signature, signed_at)
		VALUES ($1, $2, $3)
		RETURNING id
	`, fingerprint, signature, signedAt).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("insert server_signatures: %w", err)
	}
	return id, nil
}

// GetUserSignature loads a user_signatures row by id.
func GetUserSignature(db DBTX, id int64) (*UserSignature, error) {
	var row UserSignature
	err := db.QueryRow(`
		SELECT id, fingerprint, signature
		FROM user_signatures
		WHERE id = $1
	`, id).Scan(
		&row.ID,
		&row.Fingerprint,
		&row.Signature,
	)
	if err != nil {
		return nil, err
	}
	return &row, nil
}

// GetServerSignature loads a server_signatures row by id.
func GetServerSignature(db DBTX, id int64) (*ServerSignature, error) {
	var row ServerSignature
	err := db.QueryRow(`
		SELECT id, fingerprint, signature, signed_at
		FROM server_signatures
		WHERE id = $1
	`, id).Scan(
		&row.ID,
		&row.Fingerprint,
		&row.Signature,
		&row.SignedAt,
	)
	if err != nil {
		return nil, err
	}
	row.SignedAt = row.SignedAt.UTC().Truncate(time.Second)
	return &row, nil
}

// UserWire assembles the nested userSignature block from a row.
func UserWire(row *UserSignature) WireUserSignature {
	return WireUserSignature{
		Fingerprint: row.Fingerprint,
		Armor:       row.Signature,
	}
}

// ServerWire assembles the nested serverSignature block.
// serverID is the serving server's id (wire `serverID`), not stored on
// the signature row.
func ServerWire(row *ServerSignature, serverID string) WireServerSignature {
	return WireServerSignature{
		ServerID:    serverID,
		Fingerprint: row.Fingerprint,
		Armor:       row.Signature,
		Timestamp:   row.SignedAt.UTC().Truncate(time.Second),
	}
}
