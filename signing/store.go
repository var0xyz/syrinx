package signing

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/lib/pq"
)

// DBTX is the subset of *sql.DB / *sql.Tx needed by signature store helpers.
type DBTX interface {
	Exec(query string, args ...any) (sql.Result, error)
	QueryRow(query string, args ...any) *sql.Row
}

// UserSignature is one row in user_signatures.
type UserSignature struct {
	ID           int64
	Fingerprint  string
	Signature    string
	Algorithm    string
	SignedFields []string
}

// ServerSignature is one row in server_signatures.
type ServerSignature struct {
	ID           int64
	Fingerprint  string
	Signature    string
	SignedAt     time.Time
	Algorithm    string
	SignedFields []string
}

// WireUserAttestation is the flattened user-signature fields on a
// resource (root signature / signatureFingerprint / signedFields).
type WireUserAttestation struct {
	Signature            string   `json:"signature"`
	SignatureFingerprint string   `json:"signatureFingerprint"`
	SignedFields         []string `json:"signedFields"`
}

// WireServer is the nested server countersignature block, including
// signedFields.
type WireServer struct {
	ID           string    `json:"id"`
	Fingerprint  string    `json:"fingerprint"`
	Algorithm    string    `json:"algorithm"`
	Signature    string    `json:"signature"`
	Timestamp    time.Time `json:"timestamp"`
	SignedFields []string  `json:"signedFields"`
}

// defaultAlgorithm matches identity.Algorithm and the DB column DEFAULT.
const defaultAlgorithm = "PGP+base64"

func normalizeFields(fields []string) []string {
	if fields == nil {
		return []string{}
	}
	return fields
}

// InsertUserSignature inserts a user attestation row and returns its id.
// signedFields may be nil (stored as {}). Algorithm defaults in the DB.
func InsertUserSignature(db DBTX, fingerprint, signature string, signedFields []string) (int64, error) {
	var id int64
	err := db.QueryRow(`
		INSERT INTO user_signatures (fingerprint, signature, signed_fields)
		VALUES ($1, $2, $3)
		RETURNING id
	`, fingerprint, signature, pq.Array(normalizeFields(signedFields))).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("insert user_signatures: %w", err)
	}
	return id, nil
}

// InsertServerSignature inserts a server countersignature row and returns
// its id. signedAt is stored UTC truncated to seconds. signedFields may
// be nil (stored as {}). Algorithm defaults in the DB.
func InsertServerSignature(db DBTX, fingerprint, signature string, signedAt time.Time, signedFields []string) (int64, error) {
	signedAt = signedAt.UTC().Truncate(time.Second)
	var id int64
	err := db.QueryRow(`
		INSERT INTO server_signatures (fingerprint, signature, signed_at, signed_fields)
		VALUES ($1, $2, $3, $4)
		RETURNING id
	`, fingerprint, signature, signedAt, pq.Array(normalizeFields(signedFields))).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("insert server_signatures: %w", err)
	}
	return id, nil
}

// GetUserSignature loads a user_signatures row by id.
func GetUserSignature(db DBTX, id int64) (*UserSignature, error) {
	var row UserSignature
	var fields []string
	err := db.QueryRow(`
		SELECT id, fingerprint, signature, algorithm, signed_fields
		FROM user_signatures
		WHERE id = $1
	`, id).Scan(
		&row.ID,
		&row.Fingerprint,
		&row.Signature,
		&row.Algorithm,
		pq.Array(&fields),
	)
	if err != nil {
		return nil, err
	}
	row.SignedFields = normalizeFields(fields)
	return &row, nil
}

// GetServerSignature loads a server_signatures row by id.
func GetServerSignature(db DBTX, id int64) (*ServerSignature, error) {
	var row ServerSignature
	var fields []string
	err := db.QueryRow(`
		SELECT id, fingerprint, signature, signed_at, algorithm, signed_fields
		FROM server_signatures
		WHERE id = $1
	`, id).Scan(
		&row.ID,
		&row.Fingerprint,
		&row.Signature,
		&row.SignedAt,
		&row.Algorithm,
		pq.Array(&fields),
	)
	if err != nil {
		return nil, err
	}
	row.SignedAt = row.SignedAt.UTC().Truncate(time.Second)
	row.SignedFields = normalizeFields(fields)
	return &row, nil
}

// UserWire assembles flattened user attestation wire fields from a row.
func UserWire(row *UserSignature) WireUserAttestation {
	return WireUserAttestation{
		Signature:            row.Signature,
		SignatureFingerprint: row.Fingerprint,
		SignedFields:         normalizeFields(row.SignedFields),
	}
}

// ServerWire assembles the nested server countersignature block.
// serverID is the serving server's id (wire `server.id`), not stored on
// the signature row.
func ServerWire(row *ServerSignature, serverID string) WireServer {
	algo := row.Algorithm
	if algo == "" {
		algo = defaultAlgorithm
	}
	return WireServer{
		ID:           serverID,
		Fingerprint:  row.Fingerprint,
		Algorithm:    algo,
		Signature:    row.Signature,
		Timestamp:    row.SignedAt.UTC().Truncate(time.Second),
		SignedFields: normalizeFields(row.SignedFields),
	}
}
