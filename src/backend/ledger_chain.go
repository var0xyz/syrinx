package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"time"
)

// ledgerEntry is one public record of an admin action. PrevID is 0 on the
// first entry. Messages never carry usernames: the users an entry is
// about are Targets, as user IDs. For a request or an admin invite,
// RequesterID proposed it and UserID approved or claimed it. A server key
// rotation has no UserID: the operator isn't a signer.
type ledgerEntry struct {
	ID              uint64
	PrevID          uint64
	Kind            string
	UserID          string
	RequesterID     string
	RequestedAt     time.Time
	Message         string
	Content         string
	Targets         []string
	ServerSignature ServerSignature
}

// What a ledger entry records, so a client never parses its message.
const (
	ledgerKindRequest     = "request"
	ledgerKindAdminInvite = "admin-invite"
	ledgerKindMesh        = "mesh"
	ledgerKindServerKey   = "server-key"
)

// ledgerSigner countersigns a ledger entry's payload.
type ledgerSigner func(payload []byte, ts time.Time) (ServerSignature, error)

// errLedgerSignerRotated: the key would sign an entry after a rotation
// retired it. The server must restart to pick up its successor.
var errLedgerSignerRotated = errors.New("ledger signer is no longer the server's signing key")

// appendLedgerEntryTx chains e after the last entry and stores it. The
// advisory lock serializes writers, so the ledger never forks, and the
// signing key is re-read under it, so a rotation can't be signed past.
func appendLedgerEntryTx(ctx context.Context, tx *sql.Tx, sign ledgerSigner, serverID, serverKeyFingerprint string, e *ledgerEntry) error {
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtext('ledger_entries'))`); err != nil {
		return fmt.Errorf("lock ledger: %w", err)
	}
	var signingKey string
	if err := tx.QueryRowContext(ctx, `SELECT key_id FROM server_self`).Scan(&signingKey); err != nil {
		return fmt.Errorf("load signing key: %w", err)
	}
	if signingKey != string(canonicalID(serverID, serverKeyFingerprint)) {
		return errLedgerSignerRotated
	}
	var prevID sql.NullInt64
	var prevSignature sql.NullString
	err := tx.QueryRowContext(ctx, `
		SELECT e.id, ss.signature
		FROM ledger_entries e
		JOIN server_signatures ss ON ss.id = e.server_signature_id
		WHERE NOT EXISTS (SELECT 1 FROM ledger_entries n WHERE n.prev_id = e.id)
	`).Scan(&prevID, &prevSignature)
	if err != nil && err != sql.ErrNoRows {
		return fmt.Errorf("load last ledger entry: %w", err)
	}
	var id uint64
	if err := tx.QueryRowContext(ctx, `SELECT nextval(pg_get_serial_sequence('ledger_entries', 'id'))`).Scan(&id); err != nil {
		return fmt.Errorf("next ledger id: %w", err)
	}

	e.ID = id
	e.PrevID = uint64(prevID.Int64)
	sort.Strings(e.Targets)
	now := time.Now().UTC().Truncate(time.Second)
	payload := buildLedgerEntryPayload(e.ID, e.PrevID, prevSignature.String, e.Kind, e.UserID, e.RequesterID,
		e.RequestedAt, e.Message, e.Content, e.Targets, serverID, serverKeyFingerprint, now)
	sig, err := sign(payload, now)
	if err != nil {
		return fmt.Errorf("sign ledger entry: %w", err)
	}
	serverSigID, err := insertRecoveryServerSignature(ctx, tx, sig.ID, sig.Armor, sig.SignedAt)
	if err != nil {
		return err
	}
	var requestedAt sql.NullTime
	if !e.RequestedAt.IsZero() {
		requestedAt = sql.NullTime{Time: e.RequestedAt.UTC(), Valid: true}
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO ledger_entries (id, prev_id, kind, user_id, requester_id, requested_at, message, content, signed_at, server_signature_id)
		VALUES ($1, $2, $3, NULLIF($4, ''), NULLIF($5, ''), $6, $7, NULLIF($8, ''), $9, $10)
	`, e.ID, prevID, e.Kind, e.UserID, e.RequesterID, requestedAt, e.Message, e.Content, now, serverSigID); err != nil {
		return fmt.Errorf("insert ledger entry: %w", err)
	}
	for _, target := range e.Targets {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO ledger_entry_targets (entry_id, user_id) VALUES ($1, $2)
		`, e.ID, target); err != nil {
			return fmt.Errorf("insert ledger entry target: %w", err)
		}
	}
	e.ServerSignature = sig
	return nil
}
