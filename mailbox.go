package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"unicode/utf8"

	"syrinx/crypto"
	"syrinx/roles"
)

// ErrNoActiveKey means the recipient has no usable (present, unrevoked)
// active public key to encrypt a mailbox message to.
var ErrNoActiveKey = errors.New("recipient has no active public key")

// ErrMailboxRecipientNotFound means the given userID does not exist in
// users(id). Named distinctly from services.go's ErrUserNotFound because
// this file (unlike services.go) is also compiled into the `ops` build,
// which cannot see services.go's declaration.
var ErrMailboxRecipientNotFound = errors.New("recipient user not found")

// ErrMailboxMessageTooLong means message exceeds MaxMailboxMessageChars.
var ErrMailboxMessageTooLong = errors.New("mailbox message exceeds 140 characters")

// MaxMailboxMessageChars mirrors MaxRippleContentChars/MaxReedVisibleChars
// (handlers.go) — the same 140-char limit used elsewhere in this app.
const MaxMailboxMessageChars = 140

// MailboxCategory splits mailbox messages into the SPA's two popover tabs.
// A stable producer-supplied field, not inferred from Kind string
// matching, which would grow fragile as more kinds are added later.
type MailboxCategory string

const (
	// MailboxCategorySystem is server/admin/ops-originated: account
	// events, processing errors, manual ops mailbox-send messages.
	MailboxCategorySystem MailboxCategory = "system"
	// MailboxCategoryInteraction is triggered by another user's action
	// (mention, reply, echo, like) — SenderUserID identifies who.
	MailboxCategoryInteraction MailboxCategory = "interaction"
)

// MailboxPayload is JSON-marshaled then encrypted to the recipient's active
// public key before storage — see specs/notifications/03. Link is an
// app-relative client route (e.g. "/mentions"); the server never
// interprets it, it's opaque bytes to every producer except the SPA.
type MailboxPayload struct {
	Kind         string          `json:"kind"`
	Category     MailboxCategory `json:"category"`
	Message      string          `json:"message"`
	Link         string          `json:"link,omitempty"`
	SenderUserID string          `json:"senderUserID,omitempty"` // canonical userID@serverID; whose identicon the SPA shows
	Meta         json.RawMessage `json:"meta,omitempty"`
}

// SendMailboxMessage encrypts a message to userID's current active public
// key and stores it in user_mailbox. Standalone (not a DataService method)
// because ops.go's `ops` build tag excludes services.go — this must be
// callable from both the server and the ops CLI. Not gated behind any
// admin check itself: a handler reporting that specific user's own
// processing error is not an admin action (see specs/notifications/03).
// Returns the new row's id and ciphertext so the caller (main package) can
// hand them to realtime.RealtimeService.NotifyMailboxMessage for live
// delivery — this function itself has no access to the realtime package's
// connection registry.
// userID accepts either canonical form ("userID@serverID") or a bare local
// userID, resolved against this server's own id — mailbox is local-only
// (the server never mails a foreign user), so there's no ambiguity to
// resolve a bare id against.
//
// senderUserID identifies whose identicon the SPA shows for this message
// (canonical or bare, same resolution as userID above). Empty defaults to
// this server's root user — accurate for every producer today (server
// internals, ops mailbox-send), since MailboxCategoryInteraction
// producers that pass a real sender don't exist yet.
func SendMailboxMessage(ctx context.Context, db *sql.DB, cryptoSvc *crypto.Service, userID string, category MailboxCategory, kind, message, link, senderUserID string, meta any) (id, ciphertext string, err error) {
	if utf8.RuneCountInString(message) > MaxMailboxMessageChars {
		return "", "", ErrMailboxMessageTooLong
	}

	var selfServerID string
	if !strings.Contains(userID, "@") || !strings.Contains(senderUserID, "@") {
		if err := db.QueryRowContext(ctx, `SELECT id FROM servers WHERE self = TRUE`).Scan(&selfServerID); err != nil {
			return "", "", err
		}
	}
	if !strings.Contains(userID, "@") {
		userID = userID + "@" + selfServerID
	}
	if senderUserID == "" {
		senderUserID = roles.RootUserID + "@" + selfServerID
	} else if !strings.Contains(senderUserID, "@") {
		senderUserID = senderUserID + "@" + selfServerID
	}

	var fingerprint sql.NullString
	err = db.QueryRowContext(ctx, `SELECT user_fingerprint FROM users WHERE id = $1`, userID).Scan(&fingerprint)
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
		       EXISTS(SELECT 1 FROM public_key_revocations rv WHERE rv.revoked_id = pk.id)
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
		Kind:         kind,
		Category:     category,
		Message:      message,
		Link:         link,
		SenderUserID: senderUserID,
		Meta:         metaRaw,
	})
	if err != nil {
		return "", "", err
	}
	ciphertext, err = cryptoSvc.Encrypt(payload, armor)
	if err != nil {
		return "", "", err
	}

	id, err = crypto.NewID()
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
