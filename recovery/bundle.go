package recovery

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"syrinx/crypto"
)

const BundleVersion = 1

// Bundle is the plaintext identity export (before symmetric encryption).
type Bundle struct {
	Version                 int         `json:"version"`
	ExportedAt              time.Time   `json:"exportedAt"`
	ServerID                string      `json:"serverID"`
	SigningKeyFingerprint   string      `json:"signingKeyFingerprint"`
	Keys                    []BundleKey `json:"keys"`
}

// BundleKey is one server signing key (active or rotated/revoked).
// PrivateKeyArmor remains passphrase-wrapped; export never decrypts it.
type BundleKey struct {
	Fingerprint     string     `json:"fingerprint"`
	PrivateKeyArmor string     `json:"privateKeyArmor"`
	PublicKeyArmor  string     `json:"publicKeyArmor"`
	CreatedAt       time.Time  `json:"createdAt"`
	RevokedAt       *time.Time `json:"revokedAt"`
	RevokeReason    *string    `json:"revokeReason"`
}

// DefaultExportFilename returns syrinx-<serverID>-<YYYYMMDDTHHMMSSZ>.json.gpg.
func DefaultExportFilename(serverID string, exportedAt time.Time) string {
	ts := exportedAt.UTC().Format("20060102T150405Z")
	return fmt.Sprintf("syrinx-%s-%s.json.gpg", serverID, ts)
}

// ExportFromDB builds a Bundle from the self server row and full key history.
// exportedAt should be UTC truncated to seconds; it becomes Bundle.ExportedAt.
func ExportFromDB(db *sql.DB, exportedAt time.Time) (*Bundle, error) {
	exportedAt = exportedAt.UTC().Truncate(time.Second)

	var serverID, signingFP string
	err := db.QueryRow(`
		SELECT id, signing_key FROM servers WHERE self = TRUE
	`).Scan(&serverID, &signingFP)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("no self server row")
	}
	if err != nil {
		return nil, fmt.Errorf("load self server: %w", err)
	}
	if signingFP == "" {
		return nil, fmt.Errorf("self server has no signing_key")
	}

	rows, err := db.Query(`
		SELECT pk.fingerprint, pk.armor, pub.armor, pk.created_at, pk.revoked_at, pk.revoke_reason
		FROM private_keys pk
		JOIN public_keys pub ON pub.fingerprint = pk.fingerprint
		ORDER BY pk.created_at ASC, pk.fingerprint ASC
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
			&k.Fingerprint, &k.PrivateKeyArmor, &k.PublicKeyArmor, &k.CreatedAt, &revokedAt, &reason,
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
		Version:               BundleVersion,
		ExportedAt:            exportedAt,
		ServerID:              serverID,
		SigningKeyFingerprint: signingFP,
		Keys:                  keys,
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
	if b.SigningKeyFingerprint == "" {
		return fmt.Errorf("signingKeyFingerprint is empty")
	}
	if len(b.Keys) == 0 {
		return fmt.Errorf("keys is empty")
	}
	seen := make(map[string]struct{}, len(b.Keys))
	foundSigning := false
	for i, k := range b.Keys {
		if k.Fingerprint == "" {
			return fmt.Errorf("keys[%d]: fingerprint is empty", i)
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
		if _, ok := seen[k.Fingerprint]; ok {
			return fmt.Errorf("duplicate fingerprint %s", k.Fingerprint)
		}
		seen[k.Fingerprint] = struct{}{}
		if k.Fingerprint == b.SigningKeyFingerprint {
			foundSigning = true
		}
	}
	if !foundSigning {
		return fmt.Errorf("signingKeyFingerprint %s not present in keys", b.SigningKeyFingerprint)
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
			return fmt.Errorf("keys[%d] (%s): wrong server key passphrase or corrupt armor", i, k.Fingerprint)
		}
	}
	return nil
}

// MarshalBundleJSON encodes the bundle as JSON (second-precision times).
func MarshalBundleJSON(b *Bundle) ([]byte, error) {
	if err := ValidateShape(b); err != nil {
		return nil, err
	}
	return json.MarshalIndent(b, "", "  ")
}

// ParseBundleJSON decodes and shape-validates a plaintext bundle.
func ParseBundleJSON(data []byte) (*Bundle, error) {
	var b Bundle
	if err := json.Unmarshal(data, &b); err != nil {
		return nil, fmt.Errorf("invalid bundle JSON")
	}
	if err := ValidateShape(&b); err != nil {
		return nil, err
	}
	return &b, nil
}

// SetIdentityBackupAt records a successful export time on the self server row.
func SetIdentityBackupAt(db *sql.DB, at time.Time) error {
	at = at.UTC().Truncate(time.Second)
	res, err := db.Exec(`UPDATE servers SET identity_backup_at = $1 WHERE self = TRUE`, at)
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
func StaleIdentityBackupMessage(db *sql.DB) (string, error) {
	var serverID string
	var backupAt sql.NullTime
	var newestKey sql.NullTime

	err := db.QueryRow(`
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
