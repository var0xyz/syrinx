package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// errServerKeyRevoked rejects revoking a key that already has a successor.
var errServerKeyRevoked = errors.New("server signing key is already revoked")

// mintedServerKey is a freshly generated server key, before it is stored.
// PrivateArmor is decrypted; EncryptedArmor is what private_keys holds.
type mintedServerKey struct {
	KeyID          string
	Fingerprint    string
	PrivateArmor   string
	EncryptedArmor string
	PublicArmor    string
	SelfSignature  string
	SignedAt       time.Time
}

// serverKeyRevocation retires a server key in favour of its successor.
// Signature is the revoked key's, SuccessorSignature the successor's.
type serverKeyRevocation struct {
	KeyID              string    `json:"keyID"`
	Successor          string    `json:"successor"`
	Compromised        bool      `json:"compromised"`
	Reason             string    `json:"reason"`
	SignedAt           time.Time `json:"signedAt"`
	Signature          string    `json:"signature"`
	SuccessorSignature string    `json:"successorSignature"`
}

// mintServerKey generates a server key and countersigns its public half with
// itself, the same payload shape as any user key's countersignature.
func mintServerKey(cryptoSvc *cryptoService, serverID, passphrase string) (*mintedServerKey, error) {
	keyPair, err := cryptoSvc.createKeyPair(serverID, "", "")
	if err != nil {
		return nil, fmt.Errorf("create server key pair: %w", err)
	}
	encrypted, err := cryptoSvc.encryptPrivateKey(keyPair.PrivateKey, passphrase)
	if err != nil {
		return nil, fmt.Errorf("encrypt server private key: %w", err)
	}

	now := time.Now().UTC().Truncate(time.Second)
	// The key has no owner, so its own ID stands in for the user ID header.
	keyID := string(canonicalID(serverID, keyPair.Fingerprint))
	payload := buildPublicKeyPayload(serverID, keyID, keyID, keyPair.Fingerprint, keyPair.PublicKey, now)
	selfSig, err := cryptoSvc.sign(string(payload), keyPair.PrivateKey)
	if err != nil {
		return nil, fmt.Errorf("self-countersign server public key: %w", err)
	}

	return &mintedServerKey{
		KeyID:          keyID,
		Fingerprint:    keyPair.Fingerprint,
		PrivateArmor:   keyPair.PrivateKey,
		EncryptedArmor: encrypted,
		PublicArmor:    keyPair.PublicKey,
		SelfSignature:  selfSig,
		SignedAt:       now,
	}, nil
}

// insertServerKeyTx stores a minted key's private half, its public half and
// its self-countersignature. predecessorKeyID is empty for the first key.
func insertServerKeyTx(ctx context.Context, tx *sql.Tx, k *mintedServerKey, predecessorKeyID string) error {
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO private_keys (id, armor) VALUES ($1, $2)`,
		k.KeyID, k.EncryptedArmor,
	); err != nil {
		return fmt.Errorf("insert private key: %w", err)
	}

	serverSigID, err := insertRecoveryServerSignature(ctx, tx, k.KeyID, k.SelfSignature, k.SignedAt)
	if err != nil {
		return fmt.Errorf("insert server key self-signature: %w", err)
	}

	predecessor := sql.NullString{String: predecessorKeyID, Valid: predecessorKeyID != ""}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO public_keys (id, armor, server_signature_id, predecessor_id)
		VALUES ($1, $2, $3, $4)
	`, k.KeyID, k.PublicArmor, serverSigID, predecessor); err != nil {
		return fmt.Errorf("insert public key: %w", err)
	}
	return nil
}

// revokeServerKey revokes the current signing key and mints its successor;
// both sign the revocation. compromised marks the old key as no longer
// trusted, and then a reason is required.
func revokeServerKey(ctx context.Context, db *sql.DB, cryptoSvc *cryptoService, passphrase string, compromised bool, reason string) (*serverKeyRevocation, error) {
	reason = strings.TrimSpace(reason)
	if compromised && reason == "" {
		return nil, fmt.Errorf("revoking a compromised key needs a reason")
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	var serverID, oldKeyID, oldEncrypted string
	var revokedAt sql.NullTime
	err = tx.QueryRowContext(ctx, `
		SELECT sv.id, pk.id, pk.armor, pk.revoked_at
		FROM servers sv
		JOIN private_keys pk ON pk.id = sv.signing_key
		WHERE sv.self = TRUE
		FOR UPDATE
	`).Scan(&serverID, &oldKeyID, &oldEncrypted, &revokedAt)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("no current server signing key")
	}
	if err != nil {
		return nil, fmt.Errorf("load current server signing key: %w", err)
	}
	if revokedAt.Valid {
		return nil, errServerKeyRevoked
	}

	oldPrivate, err := cryptoSvc.decryptPrivateKey(oldEncrypted, passphrase)
	if err != nil {
		return nil, fmt.Errorf("decrypt current server key (wrong passphrase?): %w", err)
	}
	next, err := mintServerKey(cryptoSvc, serverID, passphrase)
	if err != nil {
		return nil, err
	}

	rev := serverKeyRevocation{
		KeyID:       oldKeyID,
		Successor:   next.KeyID,
		Compromised: compromised,
		Reason:      reason,
		SignedAt:    next.SignedAt,
	}
	payload := string(buildServerKeyRevocationPayload(serverID, rev.KeyID, rev.Successor, rev.Compromised, rev.Reason, rev.SignedAt))
	if rev.Signature, err = cryptoSvc.sign(payload, oldPrivate); err != nil {
		return nil, fmt.Errorf("sign revocation with the revoked key: %w", err)
	}
	if rev.SuccessorSignature, err = cryptoSvc.sign(payload, next.PrivateArmor); err != nil {
		return nil, fmt.Errorf("sign revocation with the successor: %w", err)
	}

	if err := insertServerKeyTx(ctx, tx, next, oldKeyID); err != nil {
		return nil, err
	}
	if err := insertServerKeyRevocationTx(ctx, tx, rev); err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE private_keys SET revoked_at = $2 WHERE id = $1`, oldKeyID, rev.SignedAt,
	); err != nil {
		return nil, fmt.Errorf("mark key revoked: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE servers SET signing_key = $1 WHERE self = TRUE`, next.KeyID,
	); err != nil {
		return nil, fmt.Errorf("set signing key: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return &rev, nil
}

// insertServerKeyRevocationTx stores a revocation, its two signatures in
// server_signatures under the key that made each.
func insertServerKeyRevocationTx(ctx context.Context, tx *sql.Tx, rev serverKeyRevocation) error {
	sigID, err := insertRecoveryServerSignature(ctx, tx, rev.KeyID, rev.Signature, rev.SignedAt)
	if err != nil {
		return fmt.Errorf("insert revocation signature: %w", err)
	}
	successorSigID, err := insertRecoveryServerSignature(ctx, tx, rev.Successor, rev.SuccessorSignature, rev.SignedAt)
	if err != nil {
		return fmt.Errorf("insert successor signature: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO private_key_revocations (
			key_id, reason, compromised, successor,
			server_signature_id, successor_signature_id
		) VALUES ($1, $2, $3, $4, $5, $6)
	`, rev.KeyID, rev.Reason, rev.Compromised, rev.Successor, sigID, successorSigID); err != nil {
		return fmt.Errorf("insert revocation of %s: %w", rev.KeyID, err)
	}
	return nil
}

// loadServerKeyRevocations returns every server key revocation in chain
// order, from the first key's to the one before the current key.
func loadServerKeyRevocations(ctx context.Context, db *sql.DB) ([]serverKeyRevocation, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT r.key_id, r.successor, r.compromised, r.reason,
			sig.signed_at, sig.signature, succ.signature
		FROM private_key_revocations r
		JOIN server_signatures sig ON sig.id = r.server_signature_id
		JOIN server_signatures succ ON succ.id = r.successor_signature_id
	`)
	if err != nil {
		return nil, fmt.Errorf("list key revocations: %w", err)
	}
	defer rows.Close()
	var out []serverKeyRevocation
	for rows.Next() {
		var rev serverKeyRevocation
		if err := rows.Scan(&rev.KeyID, &rev.Successor, &rev.Compromised, &rev.Reason,
			&rev.SignedAt, &rev.Signature, &rev.SuccessorSignature); err != nil {
			return nil, err
		}
		rev.SignedAt = rev.SignedAt.UTC()
		out = append(out, rev)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return chainOrder(out)
}

// chainOrder sorts revocations by following each one's successor to the next.
// Timestamps can't order them: two revocations may share a second.
func chainOrder(revs []serverKeyRevocation) ([]serverKeyRevocation, error) {
	byKey := make(map[string]serverKeyRevocation, len(revs))
	isSuccessor := make(map[string]bool, len(revs))
	for _, rev := range revs {
		byKey[rev.KeyID] = rev
		isSuccessor[rev.Successor] = true
	}
	var next string
	for _, rev := range revs {
		if !isSuccessor[rev.KeyID] {
			if next != "" {
				return nil, fmt.Errorf("key revocations form more than one chain")
			}
			next = rev.KeyID
		}
	}

	ordered := make([]serverKeyRevocation, 0, len(revs))
	for len(ordered) < len(revs) {
		rev, ok := byKey[next]
		if !ok {
			return nil, fmt.Errorf("key revocations are not one unbroken chain")
		}
		ordered = append(ordered, rev)
		next = rev.Successor
	}
	return ordered, nil
}
