# Publish 01 — HTTP SignReed + WS `PUBLISH_READY` + SPA

## Status

Implemented.

## Depends on

[00](00_design.md)

## Context

`SignReed` previously ended with `writeResponse` then
`broadcastChan <- NewReed`. That moved to READY. The SPA already
`storeReed`s after countersign and has reconnect / `processUnsignedReeds`
hooks suitable for READY retries.

## Scope

- Remove fanout from the HTTP SignReed success path (including first create
  only — replay already skips broadcast).
- Persist a **`pending_fanout`** row (1:1 with the tip) so READY is idempotent.
- WS `PUBLISH_READY` handling.
- SPA: send READY after store; resend on reconnect for unannounced tips.

## Non-goals

- Changing SignReed request/response wire shape.
- Coverage stats delivery ([coverage](../coverage/README.md)).

## Design

### Schema

Blank-slate `pending_fanout` table (1:1 with tip reeds):

```sql
CREATE UNLOGGED TABLE pending_fanout (
    user_id VARCHAR(255) NOT NULL,
    reed_id VARCHAR(255) NOT NULL,
    PRIMARY KEY (user_id, reed_id),
    FOREIGN KEY (user_id, reed_id) REFERENCES reeds(user_id, id) ON DELETE CASCADE
);
```

Row present → fanout not yet run. Row absent → fanout done (or never scheduled).

Author allocation at create still happens in SignReed (author is first
holder). Fanout to **others** waits until `PUBLISH_READY` removes the row.

### HTTP `SignReed`

On successful create (and on idempotent replay):

- Persist tip + signatures + author allocation as today.
- **Do not** send `NewReed` on `broadcastChan`.
- On first insert, insert `pending_fanout` in the same transaction; replay
  leaves existing rows as-is.

### WS: `PUBLISH_READY`

Client → server (authenticated):

```json
{ "type": "PUBLISH_READY", "data": { "reed_id": "<reedID>", "broadcast": true } }
```

`broadcast` is optional and defaults to `true`. When `false`, fanout runs for
followers and profile subscribers but **skips** broadcast subscribers (used for
contentless echoes today; reserved for per-post broadcast opt-out later).

Server:

1. Require `client.userID` is the tip author (`pending_fanout.user_id` /
   `reeds.user_id`).
2. If a `pending_fanout` row exists for `(author, reed_id)` → delete it and run
   the existing `NewReed` fanout path (followers; broadcast subs when
   `broadcast` is not `false`; profile subs; `dispatchMany` /
   `dispatchNext(author)`). Delete before fanout so concurrent READY messages
   only run fanout once.
3. If no row but the tip exists for this author → no-op fanout (already done).
4. If the tip does not exist for this author → ignore (no ack).
5. Otherwise send **`PUBLISH_READY_ACK`** `{ reed_id }` so the client can clear
   its local pending READY.

Server → client ack (required for client pending-clear):

```json
{ "type": "PUBLISH_READY_ACK", "data": { "reed_id": "<reedID>" } }
```

### SPA

After successful countersign + `storeReed` (order: **store, then READY**):

```text
storeReed(published)
pendingPublication.put(reed_id)
PUBLISH_READY { reed_id }
```

On `PUBLISH_READY_ACK`: delete `reed_id` from `pendingPublication`.

On WS connect / reconnect:

- Resend `PUBLISH_READY` only for rows still in `pendingPublication`.
- Server acks again when fanout already ran (idempotent), so the client clears.

Pending unsigned reeds: no READY (no tip / no countersign yet).

### Tests / checklist

- SignReed 201 → no pending events / no `RELAY_REQUEST` until READY.
- READY → fanout → author `RELAY_REQUEST` → `RELAY_RESPONSE` without miss.
- Double READY → single fanout (no duplicate pending storm).
- Reconnect after SignReed but before READY → READY → fanout.
- Non-author READY → rejected / ignored.
- Idempotent SignReed replay → still no fanout without READY (or with no
  `pending_fanout` row, still no second fanout from HTTP).
