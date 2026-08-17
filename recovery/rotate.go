package recovery

import (
	"context"
	"database/sql"
	"fmt"

	"syrinx/crypto"
)

// RotateServerKeyPassphrase re-wraps every private_keys.armor under newPassphrase.
// Private key material is decrypted with oldPassphrase then re-encrypted; fingerprints
// and public keys are unchanged.
func RotateServerKeyPassphrase(ctx context.Context, db *sql.DB, cryptoSvc *crypto.Service, oldPassphrase, newPassphrase string) error {
	if len(newPassphrase) < 16 {
		return fmt.Errorf("new passphrase must be at least 16 characters")
	}

	rows, err := db.QueryContext(ctx, `SELECT id, armor FROM private_keys`)
	if err != nil {
		return fmt.Errorf("list private keys: %w", err)
	}
	defer rows.Close()

	type item struct {
		fp    string
		armor string
	}
	var items []item
	for rows.Next() {
		var it item
		if err := rows.Scan(&it.fp, &it.armor); err != nil {
			return err
		}
		items = append(items, it)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if len(items) == 0 {
		return fmt.Errorf("no private keys to re-wrap")
	}

	rewrapped := make([]item, 0, len(items))
	for _, it := range items {
		plain, err := cryptoSvc.DecryptPrivateKey(it.armor, oldPassphrase)
		if err != nil {
			return fmt.Errorf("decrypt %s (wrong current passphrase?): %w", it.fp, err)
		}
		enc, err := cryptoSvc.EncryptPrivateKey(plain, newPassphrase)
		if err != nil {
			return fmt.Errorf("encrypt %s: %w", it.fp, err)
		}
		rewrapped = append(rewrapped, item{fp: it.fp, armor: enc})
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	for _, it := range rewrapped {
		if _, err := tx.ExecContext(ctx, `UPDATE private_keys SET armor = $1 WHERE id = $2`, it.armor, it.fp); err != nil {
			return fmt.Errorf("update %s: %w", it.fp, err)
		}
	}
	return tx.Commit()
}
