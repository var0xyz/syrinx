package recovery

import (
	"database/sql"
	"fmt"
	"time"
)

// ImportResult describes the outcome of ImportIntoDB.
type ImportResult int

const (
	// ImportApplied means the self identity and keys were written.
	ImportApplied ImportResult = iota
	// ImportAlreadyPresent means the DB already held a matching identity.
	ImportAlreadyPresent
)

// ExistingKey is one private_keys row used for match comparison.
type ExistingKey struct {
	Fingerprint string
	Armor       string
}

// ExistingSelf is the self servers row used for match comparison.
type ExistingSelf struct {
	ID         string
	Name       string
	SigningKey string
}

// IdentityMatches reports whether existing self + keys equal the bundle
// (serverID, serverName, signingKeyFingerprint, and every fingerprint+armor).
func IdentityMatches(b *Bundle, self ExistingSelf, keys []ExistingKey) bool {
	if b == nil {
		return false
	}
	if self.ID != b.ServerID || self.Name != b.ServerName || self.SigningKey != b.SigningKeyFingerprint {
		return false
	}
	if len(keys) != len(b.Keys) {
		return false
	}
	byFP := make(map[string]string, len(keys))
	for _, k := range keys {
		byFP[k.Fingerprint] = k.Armor
	}
	for _, k := range b.Keys {
		armor, ok := byFP[k.Fingerprint]
		if !ok || armor != k.PrivateKeyArmor {
			return false
		}
	}
	return true
}

// ImportIntoDB restores identity from bundle into db. Caller must have run InitDB.
// On mismatch with an existing self identity, returns an error and writes nothing.
func ImportIntoDB(db *sql.DB, b *Bundle) (ImportResult, error) {
	if err := ValidateShape(b); err != nil {
		return 0, err
	}

	var self ExistingSelf
	err := db.QueryRow(`
		SELECT id, name, COALESCE(signing_key, '') FROM servers WHERE self = TRUE
	`).Scan(&self.ID, &self.Name, &self.SigningKey)

	if err == nil {
		keys, kerr := loadExistingPrivateKeys(db)
		if kerr != nil {
			return 0, kerr
		}
		if IdentityMatches(b, self, keys) {
			return ImportAlreadyPresent, nil
		}
		return 0, fmt.Errorf(
			"self identity already exists and does not match the bundle (db id=%s name=%s signing=%s; bundle id=%s name=%s signing=%s) — resolve manually before re-importing",
			self.ID, self.Name, self.SigningKey, b.ServerID, b.ServerName, b.SigningKeyFingerprint,
		)
	}
	if err != sql.ErrNoRows {
		return 0, fmt.Errorf("load self server: %w", err)
	}

	tx, err := db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	for _, k := range b.Keys {
		var revokedAt interface{}
		var reason interface{}
		if k.RevokedAt != nil {
			revokedAt = k.RevokedAt.UTC()
		}
		if k.RevokeReason != nil {
			reason = *k.RevokeReason
		}
		if _, err := tx.Exec(`
			INSERT INTO private_keys (fingerprint, armor, created_at, revoked_at, revoke_reason)
			VALUES ($1, $2, $3, $4, $5)
		`, k.Fingerprint, k.PrivateKeyArmor, k.CreatedAt.UTC(), revokedAt, reason); err != nil {
			return 0, fmt.Errorf("insert private_keys %s: %w", k.Fingerprint, err)
		}
		if _, err := tx.Exec(`
			INSERT INTO public_keys (fingerprint, armor, created_at)
			VALUES ($1, $2, $3)
		`, k.Fingerprint, k.PublicKeyArmor, k.CreatedAt.UTC()); err != nil {
			return 0, fmt.Errorf("insert public_keys %s: %w", k.Fingerprint, err)
		}
	}

	backupAt := b.ExportedAt.UTC().Truncate(time.Second)
	if _, err := tx.Exec(`
		INSERT INTO servers (id, name, self, signing_key, identity_backup_at)
		VALUES ($1, $2, TRUE, $3, $4)
	`, b.ServerID, b.ServerName, b.SigningKeyFingerprint, backupAt); err != nil {
		return 0, fmt.Errorf("insert self server: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return ImportApplied, nil
}

func loadExistingPrivateKeys(db *sql.DB) ([]ExistingKey, error) {
	rows, err := db.Query(`SELECT fingerprint, armor FROM private_keys`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var keys []ExistingKey
	for rows.Next() {
		var k ExistingKey
		if err := rows.Scan(&k.Fingerprint, &k.Armor); err != nil {
			return nil, err
		}
		keys = append(keys, k)
	}
	return keys, rows.Err()
}
