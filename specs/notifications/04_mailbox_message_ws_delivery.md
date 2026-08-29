# Notifications 04 — WS delivery + ACK-and-delete

## Status

Proposed.

## Depends on

[03](03_mailbox_message_schema_and_producers.md)

## Context

Mailbox delivery deliberately does **not** reuse the `pending_events` /
`CreatePendingReedEvent` machinery that reed removal and other fanout
types use (`realtime/db.go:248`, `realtime/service.go`'s
`handleBroadcasts`). That machinery exists to track per-recipient
delivery across a fanout to *many* viewers of one event. A mailbox
message has exactly one recipient and one durable row already
(`user_mailbox`, [03](03_mailbox_message_schema_and_producers.md)) — that row
**is** the pending-delivery record. Adding a second bookkeeping table on
top would just be indirection with no payoff.

## Scope

- New WS message type, e.g. `MAILBOX`, server → client only.
- Live delivery: on `SendMailboxMessage` insert, if the recipient is
  online, send immediately.
- Catch-up: on `SYNC_REQUEST` (or plain reconnect), deliver any rows still
  present for that user.
- ACK: client confirms receipt → server deletes the row.

## Non-goals

- No `pending_events` involvement (see Context above).
- No redelivery-count/backoff tracking — a row either exists (keep
  trying to deliver: live send when online, catch-up when they
  reconnect) or it doesn't (delivered).

## Design

### Live path

After `SendMailboxMessage` commits the row, attempt
`connManager.SendToUser(userID, msg)` (`realtime/connection_manager.go:140`,
the same direct-send primitive `deliverReedRemoved` uses for its online
case). If the user isn't currently connected, nothing else happens here —
the row simply waits for the catch-up path below. No pending-event insert
needed, unlike `REED_REMOVED`'s live path, because there's no
separate delivery-tracking table to keep in sync.

Message payload: the raw `ciphertext` plus the row `id` (so the client's
ACK can reference it) — the server never decrypts or inspects the
payload itself.

### Catch-up path

On `SYNC_REQUEST` (mirroring `catchUp`, `realtime/service.go:1434`, which
already handles missing reeds/removals/account-removals in the same
function): query `user_mailbox WHERE user_id = $1 ORDER BY created_at`
and send each via the same `MAILBOX` message. No diff query needed against
a second table — every row present *is* undelivered by definition, so
"missing" and "all rows for this user" are the same set.

### ACK

New inbound WS message (e.g. `MAILBOX_ACK { id }`), dispatched the same
way `DATA_ACK`/`DATA_INVALID` are (`realtime/service.go`, switch around
line 700): a small `handleMailboxAck(client, data)` that runs
`DELETE FROM user_mailbox WHERE id = $1 AND user_id = $2` (scoped to the
acking client's own `userID`, so a client can only ack/delete its own
mail). No `DeletePendingEvent` call needed, since there is no
corresponding `pending_events` row for mailbox in the first place.

A failed-decrypt case on the client does **not** send `MAILBOX_ACK` — see
[05](05_mailbox_message_spa_bell.md). The row stays, and gets redelivered on the
next connect/catch-up, matching this app's general fail-closed pattern
for content the client couldn't process.

## Testing

- Online recipient: message sent live, immediately visible client-side —
  only true for an in-process producer (`Handlers.SendMailboxMessage`);
  `ops mailbox-send` runs in a separate process with no access to the
  live connection registry, so it always lands via catch-up regardless of
  the recipient's online state (see [03](03_mailbox_message_schema_and_producers.md)).
- Offline recipient: message delivered on next connect via catch-up, not
  lost.
- ACK deletes exactly the acked row, scoped to the acking user (a
  malicious/buggy client cannot ack/delete another user's mail by
  guessing an ID).
- Multiple pending messages for one user all deliver on reconnect, each
  independently ackable.

## Dependencies

- [03](03_mailbox_message_schema_and_producers.md) — the `user_mailbox` table
  this operates on.
- WS dispatch pattern: [`realtime/service.go`](../../realtime/service.go)
  (`DATA_ACK`/`DATA_INVALID` cases, `catchUp`).
- Direct-send primitive: [`realtime/connection_manager.go`](../../realtime/connection_manager.go)
  `SendToUser`.
