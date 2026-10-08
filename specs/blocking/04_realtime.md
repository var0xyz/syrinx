# Blocking 04 — `USER_BLOCKED` / `USER_UNBLOCKED` push and catch-up

## Status

Proposed.

## Depends on

[02](02_api.md)

## Messages

`proto/websocket.proto` (regenerate with `make proto`, mirror in
`lib/proto/websocket_pb.ts`):

| Type | Direction | Payload |
|------|-----------|---------|
| `USER_BLOCKED` | server → client | `BlockCert block`, optional `request_id` |
| `USER_BLOCKED_ACK` | client → server | `user_id` (the blocking user) |
| `USER_UNBLOCKED` | server → client | `user_id` (the blocking user) |
| `USER_UNBLOCKED_ACK` | client → server | `user_id` (the blocking user) |

`USER_BLOCKED` with a `request_id` is the refusal answer from
[03](03_enforcement.md); without one it is the push.

## Owed events

The block is a resource; what gets delivered is an event about it. The
blocked user's client is owed at most one event per pair, the latest:

```sql
CREATE TABLE IF NOT EXISTS user_block_events (
  user_id         VARCHAR(255) NOT NULL REFERENCES identities(id) ON DELETE CASCADE,
  blocked_user_id VARCHAR(255) NOT NULL REFERENCES identities(id) ON DELETE CASCADE,
  kind            VARCHAR(8)   NOT NULL CHECK (kind IN ('block', 'unblock')),
  PRIMARY KEY (user_id, blocked_user_id)
);
```

- Blocking upserts `block`, unblocking upserts `unblock`, in the same
  transaction as the block itself (`oweBlockEventTx`). A later event
  replaces one still owed: only the latest state matters.
- An ack deletes the event of its own kind only, so a late ack of a block
  never drops the lift that replaced it.
- Once acked, nothing is sent again, which is what lets a forgotten
  certificate stay forgotten.
- The same helper owes the event to the blocked user's home server instead
  when they are on a peer ([05](05_federation.md)).

A lift is owed even when the block it lifts never reached the client: the
client can't be told which blocks it holds without delivery state on the
block, and a lift for a certificate it doesn't have is a no-op it acks.

## Push

After a block or lift is committed for a local blocked user, the server
sends `USER_BLOCKED` / `USER_UNBLOCKED` to them if online (`pushBlock`,
`pushUnblock`). The push is fire-and-forget; catch-up covers a missed one.

## Catch-up

On `SYNC_REQUEST`, the server sends one message per owed event
(`catchUpBlocks`): `USER_BLOCKED` with the stored certificate, or
`USER_UNBLOCKED`. They are plain messages, not relay events: nothing is
held, so they bypass `pending_events`.

## Tests

- Online blocked user gets `USER_BLOCKED`; the ack drops the event; a later
  `SYNC_REQUEST` sends nothing. Another user's ack drops nothing of theirs.
- Offline through the block → sent on catch-up until acked.
- Unblock replaces an owed block: catch-up sends only `USER_UNBLOCKED`, and
  a late block ack leaves it owed.
- Block, unblock, block → catch-up sends only `USER_BLOCKED`.
