package recovery

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"syrinx/crypto"
	"syrinx/encoding"
)

const BundleVersion = 1

// Bundle is the plaintext identity export (before symmetric encryption).
type Bundle struct {
	Version      int         `json:"version"`
	ExportedAt   time.Time   `json:"exportedAt"`
	ServerID     string      `json:"serverID"`
	ServerName   string      `json:"serverName"`
	SigningKeyID string      `json:"signingKeyID"`
	Keys         []BundleKey `json:"keys"`
}

// BundleKey is one server signing key (active or rotated/revoked).
// PrivateKeyArmor remains passphrase-wrapped; export never decrypts it.
type BundleKey struct {
	ID              string     `json:"id"`
	PrivateKeyArmor string     `json:"privateKeyArmor"`
	PublicKeyArmor  string     `json:"publicKeyArmor"`
	CreatedAt       time.Time  `json:"createdAt"`
	RevokedAt       *time.Time `json:"revokedAt"`
	RevokeReason    *string    `json:"revokeReason"`
}

// DefaultExportFilename returns syrinx-<serverID>-<YYYYMMDDTHHMMSSZ>.sxi.gpg.
func DefaultExportFilename(serverID string, exportedAt time.Time) string {
	ts := exportedAt.UTC().Format("20060102T150405Z")
	return fmt.Sprintf("syrinx-%s-%s.sxi.gpg", serverID, ts)
}

// ExportFromDB builds a Bundle from the self server row and full key history.
// exportedAt should be UTC truncated to seconds; it becomes Bundle.ExportedAt.
func ExportFromDB(ctx context.Context, db *sql.DB, exportedAt time.Time) (*Bundle, error) {
	exportedAt = exportedAt.UTC().Truncate(time.Second)

	var serverID, serverName, signingFP string
	err := db.QueryRowContext(ctx, `
		SELECT id, name, signing_key FROM servers WHERE self = TRUE
	`).Scan(&serverID, &serverName, &signingFP)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("no self server row")
	}
	if err != nil {
		return nil, fmt.Errorf("load self server: %w", err)
	}
	if signingFP == "" {
		return nil, fmt.Errorf("self server has no signing_key")
	}
	if serverName == "" {
		return nil, fmt.Errorf("self server has no name")
	}

	rows, err := db.QueryContext(ctx, `
		SELECT pk.id, pk.armor, pub.armor, pk.created_at, pk.revoked_at, pk.revoke_reason
		FROM private_keys pk
		JOIN public_keys pub ON pub.id = pk.id
		ORDER BY pk.created_at ASC, pk.id ASC
	`)
	if err != nil {
		return nil, fmt.Errorf("list keys: %w", err)
	}
	defer rows.Close()

	var keys []BundleKey
	for rows.Next() {
		var k BundleKey
		var revokedAt sql.NullTime
		var reason sql.NullString
		if err := rows.Scan(
			&k.ID, &k.PrivateKeyArmor, &k.PublicKeyArmor, &k.CreatedAt, &revokedAt, &reason,
		); err != nil {
			return nil, fmt.Errorf("scan key: %w", err)
		}
		k.CreatedAt = k.CreatedAt.UTC()
		if revokedAt.Valid {
			t := revokedAt.Time.UTC()
			k.RevokedAt = &t
		}
		if reason.Valid {
			r := reason.String
			k.RevokeReason = &r
		}
		keys = append(keys, k)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(keys) == 0 {
		return nil, fmt.Errorf("no server keys to export")
	}

	b := &Bundle{
		Version:      BundleVersion,
		ExportedAt:   exportedAt,
		ServerID:     serverID,
		ServerName:   serverName,
		SigningKeyID: signingFP,
		Keys:         keys,
	}
	if err := ValidateShape(b); err != nil {
		return nil, err
	}
	return b, nil
}

// ValidateShape checks structural integrity of a decrypted bundle.
func ValidateShape(b *Bundle) error {
	if b == nil {
		return fmt.Errorf("bundle is nil")
	}
	if b.Version != BundleVersion {
		return fmt.Errorf("unsupported bundle version %d", b.Version)
	}
	if b.ServerID == "" {
		return fmt.Errorf("serverID is empty")
	}
	if b.ServerName == "" {
		return fmt.Errorf("serverName is empty")
	}
	if b.SigningKeyID == "" {
		return fmt.Errorf("signingKeyID is empty")
	}
	if len(b.Keys) == 0 {
		return fmt.Errorf("keys is empty")
	}
	seen := make(map[string]struct{}, len(b.Keys))
	foundSigning := false
	for i, k := range b.Keys {
		if k.ID == "" {
			return fmt.Errorf("keys[%d]: id is empty", i)
		}
		if k.PrivateKeyArmor == "" {
			return fmt.Errorf("keys[%d]: privateKeyArmor is empty", i)
		}
		if k.PublicKeyArmor == "" {
			return fmt.Errorf("keys[%d]: publicKeyArmor is empty", i)
		}
		if k.CreatedAt.IsZero() {
			return fmt.Errorf("keys[%d]: createdAt is zero", i)
		}
		if _, ok := seen[k.ID]; ok {
			return fmt.Errorf("duplicate key id %s", k.ID)
		}
		seen[k.ID] = struct{}{}
		if k.ID == b.SigningKeyID {
			foundSigning = true
		}
	}
	if !foundSigning {
		return fmt.Errorf("signingKeyID %s not present in keys", b.SigningKeyID)
	}
	return nil
}

// ValidateDecrypt checks that every private armor decrypts with passphrase.
func ValidateDecrypt(b *Bundle, cryptoSvc *crypto.Service, passphrase string) error {
	if err := ValidateShape(b); err != nil {
		return err
	}
	for i, k := range b.Keys {
		if _, err := cryptoSvc.DecryptPrivateKey(k.PrivateKeyArmor, passphrase); err != nil {
			return fmt.Errorf("keys[%d] (%s): wrong server key passphrase or corrupt armor", i, k.ID)
		}
	}
	return nil
}

// MarshalBundleJSON encodes the bundle as JSON (second-precision times).
// Key armor is base64-encoded on the way out — the bundle's in-memory
// BundleKey fields are plain armor (matching the DB), but the serialized
// file, like every other signed/keyed artifact that crosses a wire or file
// boundary, carries base64.
func MarshalBundleJSON(b *Bundle) ([]byte, error) {
	if err := ValidateShape(b); err != nil {
		return nil, err
	}
	wire := *b
	wire.Keys = make([]BundleKey, len(b.Keys))
	for i, k := range b.Keys {
		wire.Keys[i] = k
		wire.Keys[i].PrivateKeyArmor = encoding.Base64Encode(k.PrivateKeyArmor)
		wire.Keys[i].PublicKeyArmor = encoding.Base64Encode(k.PublicKeyArmor)
	}
	return json.MarshalIndent(&wire, "", "  ")
}

// ParseBundleJSON decodes a serialized bundle, base64-decoding key armor
// back to plain armor before shape validation — the mirror of
// MarshalBundleJSON's encode step.
func ParseBundleJSON(data []byte) (*Bundle, error) {
	var b Bundle
	if err := json.Unmarshal(data, &b); err != nil {
		return nil, fmt.Errorf("invalid bundle JSON")
	}
	for i, k := range b.Keys {
		priv, err := encoding.Base64Decode(k.PrivateKeyArmor)
		if err != nil {
			return nil, fmt.Errorf("keys[%d]: invalid privateKeyArmor encoding", i)
		}
		pub, err := encoding.Base64Decode(k.PublicKeyArmor)
		if err != nil {
			return nil, fmt.Errorf("keys[%d]: invalid publicKeyArmor encoding", i)
		}
		b.Keys[i].PrivateKeyArmor = priv
		b.Keys[i].PublicKeyArmor = pub
	}
	if err := ValidateShape(&b); err != nil {
		return nil, err
	}
	return &b, nil
}

// SetIdentityBackupAt records a successful export time on the self server row.
func SetIdentityBackupAt(ctx context.Context, db *sql.DB, at time.Time) error {
	at = at.UTC().Truncate(time.Second)
	res, err := db.ExecContext(ctx, `UPDATE servers SET identity_backup_at = $1 WHERE self = TRUE`, at)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return fmt.Errorf("no self server row to update")
	}
	return nil
}

// StaleIdentityBackupMessage returns a non-empty warning when the self
// identity has never been backed up, or the backup is older than the newest
// private key. Empty string means no warning.
func StaleIdentityBackupMessage(ctx context.Context, db *sql.DB) (string, error) {
	var serverID string
	var backupAt sql.NullTime
	var newestKey sql.NullTime

	err := db.QueryRowContext(ctx, `
		SELECT s.id, s.identity_backup_at,
			(SELECT MAX(created_at) FROM private_keys)
		FROM servers s
		WHERE s.self = TRUE
	`).Scan(&serverID, &backupAt, &newestKey)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	if !newestKey.Valid {
		return "", nil
	}
	if !backupAt.Valid {
		return fmt.Sprintf(
			"server identity %s has never been exported — run: ops export-identity",
			serverID,
		), nil
	}
	if backupAt.Time.Before(newestKey.Time) {
		return fmt.Sprintf(
			"server identity backup is stale (backup %s < newest key %s) — run: ops export-identity",
			backupAt.Time.UTC().Format(time.RFC3339),
			newestKey.Time.UTC().Format(time.RFC3339),
		), nil
	}
	return "", nil
}
