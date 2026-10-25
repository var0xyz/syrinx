package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
)

// ErrNoActiveKey means the recipient has no usable (present, unrevoked)
// active public key to encrypt a mailbox message to.
var ErrNoActiveKey = errors.New("recipient has no active public key")

// ErrMailboxRecipientNotFound means the given userID does not exist in
// users(id). Named distinctly from services.go's ErrUserNotFound because
// this file (unlike services.go) is also compiled into the `ops` build,
// which cannot see services.go's declaration.
var ErrMailboxRecipientNotFound = errors.New("recipient user not found")

// ErrMailboxMessageTooLong means message exceeds a long text's limits.
var ErrMailboxMessageTooLong = errors.New("mailbox message exceeds 420 characters")

// MaxMailboxMessageChars is a long text's visible limit.
const MaxMailboxMessageChars = maxLongTextChars

// MailboxPayload is JSON-marshaled then encrypted to the recipient's active
// public key before storage. Link is an
// app-relative client route (e.g. "/mentions"); the server never
// interprets it, it's opaque bytes to every producer except the SPA.
type MailboxPayload struct {
	Kind    string          `json:"kind"`
	Message string          `json:"message"`
	Link    string          `json:"link,omitempty"`
	Meta    json.RawMessage `json:"meta,omitempty"`
}

// SendMailboxMessage encrypts a message to userID's current active public
// key and stores it in user_mailbox. Standalone (not a DataService method)
// because ops.go's `ops` build tag excludes services.go — this must be
// callable from both the server and the ops CLI. Not gated behind any
// admin check itself: a handler reporting that specific user's own
// processing error is not an admin action.
// Returns the new row's id and ciphertext so the caller can hand them to
// realtimeService.NotifyMailboxMessage for live delivery — this function
// itself has no access to the realtime connection registry.
// userID accepts either canonical form ("userID@serverID") or a bare local
// userID, resolved against this server's own id — mailbox is local-only
// (the server never mails a foreign user), so there's no ambiguity to
// resolve a bare id against.
func SendMailboxMessage(ctx context.Context, db *sql.DB, cryptoSvc *cryptoService, userID, kind, message, link string, meta any) (id, ciphertext string, err error) {
	if !longTextFits(message) {
		return "", "", ErrMailboxMessageTooLong
	}

	if !strings.Contains(userID, "@") {
		var selfServerID string
		if err := db.QueryRowContext(ctx, `SELECT id FROM servers WHERE self = TRUE`).Scan(&selfServerID); err != nil {
			return "", "", err
		}
		userID = userID + "@" + selfServerID
	}

	var fingerprint sql.NullString
	err = db.QueryRowContext(ctx, `SELECT active_key_id FROM users WHERE id = $1`, userID).Scan(&fingerprint)
	if err == sql.ErrNoRows {
		return "", "", ErrMailboxRecipientNotFound
	}
	if err != nil {
		return "", "", err
	}
	if !fingerprint.Valid || fingerprint.String == "" {
		return "", "", ErrNoActiveKey
	}

	var armor string
	var revoked bool
	err = db.QueryRowContext(ctx, `
		SELECT pk.armor,
		       EXISTS(SELECT 1 FROM public_key_revocations rv WHERE rv.key_id = pk.id)
		FROM public_keys pk
		WHERE pk.id = $1
	`, fingerprint.String).Scan(&armor, &revoked)
	if err != nil {
		return "", "", err
	}
	if revoked {
		return "", "", ErrNoActiveKey
	}

	metaRaw, err := json.Marshal(meta)
	if err != nil {
		return "", "", err
	}
	payload, err := json.Marshal(MailboxPayload{
		Kind:    kind,
		Message: message,
		Link:    link,
		Meta:    metaRaw,
	})
	if err != nil {
		return "", "", err
	}
	ciphertext, err = cryptoSvc.encrypt(payload, armor)
	if err != nil {
		return "", "", err
	}

	id, err = newCryptoID()
	if err != nil {
		return "", "", err
	}
	_, err = db.ExecContext(ctx, `
		INSERT INTO user_mailbox (id, user_id, ciphertext) VALUES ($1, $2, $3)
	`, id, userID, ciphertext)
	if err != nil {
		return "", "", err
	}
	return id, ciphertext, nil
}

// MailboxRow is one pending (undelivered) mailbox message.
type MailboxRow struct {
	ID         string
	Ciphertext string
}

// GetPendingMailbox returns every undelivered message for userID, oldest
// first — every row present is undelivered by definition, so this doubles
// as both the live-send source and the reconnect catch-up query.
func GetPendingMailbox(ctx context.Context, db *sql.DB, userID string) ([]MailboxRow, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT id, ciphertext FROM user_mailbox WHERE user_id = $1 ORDER BY created_at
	`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []MailboxRow
	for rows.Next() {
		var m MailboxRow
		if err := rows.Scan(&m.ID, &m.Ciphertext); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// DeleteMailboxMessage deletes one message, scoped to userID so a client
// can only ack/delete its own mail.
func DeleteMailboxMessage(ctx context.Context, db *sql.DB, id, userID string) error {
	_, err := db.ExecContext(ctx, `
		DELETE FROM user_mailbox WHERE id = $1 AND user_id = $2
	`, id, userID)
	return err
}
