package recovery

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"syrinx/crypto"
	"syrinx/identity"
	"syrinx/signing"
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
	ID    string
	Armor string
}

// ExistingSelf is the self servers row used for match comparison.
type ExistingSelf struct {
	ID         string
	Name       string
	SigningKey string
}

// IdentityMatches reports whether existing self + keys equal the bundle.
// Both sides are already canonical ids — no stripping/re-wrapping needed.
func IdentityMatches(b *Bundle, self ExistingSelf, keys []ExistingKey) bool {
	if b == nil {
		return false
	}
	if self.ID != b.ServerID || self.Name != b.ServerName ||
		self.SigningKey != b.SigningKeyID {
		return false
	}
	if len(keys) != len(b.Keys) {
		return false
	}
	byID := make(map[string]string, len(keys))
	for _, k := range keys {
		byID[k.ID] = k.Armor
	}
	for _, k := range b.Keys {
		armor, ok := byID[k.ID]
		if !ok || armor != k.PrivateKeyArmor {
			return false
		}
	}
	return true
}

// ImportIntoDB restores identity from bundle into db. Caller must have run
// InitDB and already validated every key decrypts under passphrase
// (recovery.ValidateDecrypt) — that same passphrase is used here to produce
// a fresh self-countersignature for each restored public key, since the
// original self-signature isn't part of the bundle. On mismatch with an
// existing self identity, returns an error and writes nothing.
func ImportIntoDB(ctx context.Context, db *sql.DB, cryptoSvc *crypto.Service, passphrase string, b *Bundle) (ImportResult, error) {
	if err := ValidateShape(b); err != nil {
		return 0, err
	}

	var self ExistingSelf
	err := db.QueryRowContext(ctx, `
		SELECT id, name, COALESCE(signing_key, '') FROM servers WHERE self = TRUE
	`).Scan(&self.ID, &self.Name, &self.SigningKey)

	if err == nil {
		keys, kerr := loadExistingPrivateKeys(ctx, db)
		if kerr != nil {
			return 0, kerr
		}
		if IdentityMatches(b, self, keys) {
			return ImportAlreadyPresent, nil
		}
		return 0, fmt.Errorf(
			"self identity already exists and does not match the bundle (db id=%s name=%s signing=%s; bundle id=%s name=%s signing=%s) — resolve manually before re-importing",
			self.ID, self.Name, self.SigningKey, b.ServerID, b.ServerName, b.SigningKeyID,
		)
	}
	if err != sql.ErrNoRows {
		return 0, fmt.Errorf("load self server: %w", err)
	}

	tx, err := db.BeginTx(ctx, nil)
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
		keyID := k.ID
		bareFP, _, ok := identity.ParseIdentityID(identity.IdentityID(keyID))
		if !ok {
			return 0, fmt.Errorf("malformed bundle key id: %s", keyID)
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO private_keys (id, armor, created_at, revoked_at, revoke_reason)
			VALUES ($1, $2, $3, $4, $5)
		`, keyID, k.PrivateKeyArmor, k.CreatedAt.UTC(), revokedAt, reason); err != nil {
			return 0, fmt.Errorf("insert private_keys %s: %w", keyID, err)
		}

		plainPrivate, err := cryptoSvc.DecryptPrivateKey(k.PrivateKeyArmor, passphrase)
		if err != nil {
			return 0, fmt.Errorf("decrypt private key %s: %w", keyID, err)
		}
		selfPayload := identity.BuildPublicKeyPayload(
			b.ServerID, keyID, keyID, bareFP, k.PublicKeyArmor, k.CreatedAt.UTC(),
		)
		selfSigArmor, err := cryptoSvc.Sign(string(selfPayload), plainPrivate)
		if err != nil {
			return 0, fmt.Errorf("self-countersign restored key %s: %w", keyID, err)
		}
		serverSignatureID, err := signing.InsertServerSignature(ctx, tx, keyID, selfSigArmor, k.CreatedAt.UTC())
		if err != nil {
			return 0, fmt.Errorf("insert server signature for %s: %w", keyID, err)
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO public_keys (id, armor, created_at, server_signature_id)
			VALUES ($1, $2, $3, $4)
		`, keyID, k.PublicKeyArmor, k.CreatedAt.UTC(), serverSignatureID); err != nil {
			return 0, fmt.Errorf("insert public_keys %s: %w", keyID, err)
		}
	}

	backupAt := b.ExportedAt.UTC().Truncate(time.Second)
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO servers (id, name, self, signing_key, identity_backup_at)
		VALUES ($1, $2, TRUE, $3, $4)
	`, b.ServerID, b.ServerName, b.SigningKeyID, backupAt); err != nil {
		return 0, fmt.Errorf("insert self server: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return ImportApplied, nil
}

func loadExistingPrivateKeys(ctx context.Context, db *sql.DB) ([]ExistingKey, error) {
	rows, err := db.QueryContext(ctx, `SELECT id, armor FROM private_keys`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var keys []ExistingKey
	for rows.Next() {
		var k ExistingKey
		if err := rows.Scan(&k.ID, &k.Armor); err != nil {
			return nil, err
		}
		keys = append(keys, k)
	}
	return keys, rows.Err()
}
