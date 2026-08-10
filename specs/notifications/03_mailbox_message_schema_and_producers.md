# Notifications 03 — Schema + `SendMailboxMessage` + `ops mailbox-send`

## Status

Proposed.

## Depends on

[00](00_glossary_and_design.md)

## Context

See [00](00_glossary_and_design.md#mailbox-message). This step defines
the durable storage and the two producers: an internal Go helper any
server code can call, and a thin `ops` CLI wrapper for manual admin use.

## Scope

- `user_mailbox` table: encrypted payload, no plaintext columns.
- `SendMailboxMessage(ctx, db, userID, kind, payload)` — internal helper.
- `ops mailbox-send <userID> <message>` — CLI command wrapping the helper.
- Document the `admin_mentions` job-tracking table sketch from
  [00](00_glossary_and_design.md) (schema shape only — no retry logic).

## Non-goals

- No HTTP admin endpoint for sending mailbox messages in v1 — `ops` only.
- No implementation of the broadcast-retry mechanism — documentation of
  the table shape only, per [00](00_glossary_and_design.md).

## Design

### Schema

```sql
CREATE TABLE user_mailbox (
    id         VARCHAR(255) PRIMARY KEY,           -- generateID-style random ID
    user_id    VARCHAR(255) NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    ciphertext TEXT         NOT NULL,               -- armored PGP, encrypted to recipient's active key
    created_at TIMESTAMP    NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_user_mailbox_user_created
    ON user_mailbox(user_id, created_at);
```

No `kind`/`body`/`metadata`/`read_at` columns, unlike the original
proposal 11 schema — those live **inside** the encrypted JSON payload,
readable only after client-side decryption. The row's existence *is* the
delivery state (present = undelivered/unacked); there is no `read_at`
because the row is deleted on ack, not marked read (see
[00](00_glossary_and_design.md), "no durable mailbox read-log").

Payload shape (JSON, pre-encryption):

```go
type MailboxPayload struct {
    Kind    string          `json:"kind"`    // e.g. "reed_processing_error", "admin_message"
    Message string          `json:"message"`
    Meta    json.RawMessage `json:"meta,omitempty"` // kind-specific structured data
}
```

### `SendMailboxMessage` (internal helper)

```go
func (s *DataService) SendMailboxMessage(ctx context.Context, cryptoSvc *crypto.Service, userID, kind, message string, meta any) error {
    fingerprint, err := s.GetActiveKeyFingerprint(ctx, userID)
    if err != nil {
        return err
    }
    key, err := s.GetPublicKey(ctx, userID, fingerprint)
    if err != nil {
        return err
    }
    metaRaw, err := json.Marshal(meta)
    if err != nil {
        return err
    }
    payload, err := json.Marshal(MailboxPayload{Kind: kind, Message: message, Meta: metaRaw})
    if err != nil {
        return err
    }
    ciphertext, err := cryptoSvc.Encrypt(payload, key.Armor)
    if err != nil {
        return err
    }
    _, err = s.db.ExecContext(ctx, `
        INSERT INTO user_mailbox (id, user_id, ciphertext) VALUES ($1, $2, $3)
    `, generateID(), userID, ciphertext)
    return err
}
```

Mirrors the federation connection-payload pattern exactly
(`handlers.go:2495-2505`: marshal → `crypto.Service.Encrypt` → store
armored string) — no new crypto primitive, just a new call site. Any
handler or background job with a `userID` and something to say calls this
directly; it is not gated behind any admin check itself (a handler
reporting *that specific user's own* processing error is not an
admin action).

### `ops mailbox-send` (CLI wrapper)

Follows the existing `ops.go` command shape (`ops.go:49-75`,
`switch os.Args[1]`; `openDB`/`crypto.NewService()` construction per
`runExportIdentity`, `ops.go:134-157`):

```
ops mailbox-send <userID> <message>
    Sends a one-off encrypted mailbox message to <userID>. Manual/admin
    use — for automated messages, call SendMailboxMessage directly from
    the relevant server code path instead.
```

Implementation: `loadOpsConfig()` → `openDB(cfg)` →
`crypto.NewService()` → `SendMailboxMessage(ctx, cryptoSvc, userID,
"admin_message", message, nil)`. No new logic beyond argument parsing —
this command exists so a human doesn't need direct DB/encryption access
to send one message.

### `admin_mentions` job-tracking sketch (documentation only)

As described in [00](00_glossary_and_design.md#broadcast-reliability--documented-gap-not-implemented):

```sql
-- SKETCH ONLY — not created by this proposal, no retry logic implemented.
CREATE TABLE admin_mentions (
    reed_id      VARCHAR(255) PRIMARY KEY REFERENCES reeds(id),
    started_at   TIMESTAMP NOT NULL DEFAULT NOW(),
    completed_at TIMESTAMP
);
```

A future proposal would insert a row when an `@everyone` publish is
accepted, set `completed_at` on successful commit, and scan for
`completed_at IS NULL` rows past some age on boot/sweep to retry the
`@everyone` expansion (safe, since `reed_mentions` inserts are already
`ON CONFLICT DO NOTHING`). This spec documents the shape so it isn't
rediscovered from scratch later; it implements none of it.

## Testing

(Documented expectations — not run here.)

- `SendMailboxMessage` produces a row whose `ciphertext` only decrypts
  with the recipient's private key, never plaintext-readable from the DB.
- `ops mailbox-send` against a nonexistent `userID` fails cleanly (no
  active key to encrypt to).
- Two messages to the same user produce two independent rows (no
  overwrite, no upsert-on-user).

## Dependencies

- [00](00_glossary_and_design.md) for the locked model.
- `crypto.Service.Encrypt`: [`crypto/crypto.go`](../../crypto/crypto.go).
- `GetActiveKeyFingerprint`/`GetPublicKey`: [`services.go`](../../services.go).
- `ops` CLI shape: [`ops.go`](../../ops.go).
