# Publish 01 — HTTP SignReed + WS `PUBLISH_READY` + SPA

## Status

Proposed.

## Depends on

[00](00_design.md)

## Context

`SignReed` today ends with `writeResponse` then
`broadcastChan <- NewReed`. That must move to READY. The SPA already
`storeReed`s after countersign and has reconnect / `processUnsignedReeds`
hooks suitable for READY retries.

## Scope

- Remove fanout from the HTTP SignReed success path (including first create
  only — replay already skips broadcast).
- Persist an **announced / fanout-ready** bit on the tip (or equivalent) so
  READY is idempotent and reconnect can detect “not yet announced.”
- WS `PUBLISH_READY` handling.
- SPA: send READY after store; resend on reconnect for unannounced tips.

## Non-goals

- Changing SignReed request/response wire shape.
- Coverage `/stats` or `SUBSCRIBE_REED` (separate feature).

## Design

### Schema

Blank-slate addition on `reeds` (name TBD; pick one and use consistently):

```sql
-- false until author PUBLISH_READY; fanout runs once when set true
fanout_ready BOOLEAN NOT NULL DEFAULT FALSE
```

Author allocation at create still happens in SignReed (author is first
holder). Fanout to **others** waits for `fanout_ready`.

### HTTP `SignReed`

On successful create (and on idempotent replay):

- Persist tip + signatures + author allocation as today.
- **Do not** send `NewReed` on `broadcastChan`.
- Leave `fanout_ready = false` on first insert; replay leaves flag as-is.

### WS: `PUBLISH_READY`

Client → server (authenticated):

```json
{ "type": "PUBLISH_READY", "data": { "reed_id": "<reedID>" } }
```

Server:

1. Resolve tip by `reed_id`; 404 / ignore if missing.
2. Require `client.userID == tip.user_id` (author only).
3. If `fanout_ready` already true → ack / no-op (idempotent).
4. Else set `fanout_ready = true` in the same TX as any bookkeeping, then
   run the existing `NewReed` fanout path (followers, broadcast subs,
   profile subs, `dispatchMany` / `dispatchNext(author)`).

Optional server → client ack:

```json
{ "type": "PUBLISH_READY_ACK", "data": { "reed_id": "<reedID>" } }
```

Useful for debugging; not required for correctness if fanout is observable
via relay traffic.

### SPA

After successful countersign + `storeReed` (and after
`fulfillPendingRelayRequest` if any — order: **store, then READY**, so
local serveability is true before fanout):

```text
storeReed(published)
PUBLISH_READY { reed_id }
```

On WS connect / auth (and alongside `processUnsignedReeds` / sync):

- For each locally held tip authored by the current user that the server
  still has `fanout_ready = false`, send `PUBLISH_READY`.
- Discovery options (pick in implementation):
  - Server exposes a thin list or includes `fanout_ready` on a tip GET; or
  - Client always sends READY for recent self-authored reeds in IDB (server
    no-ops if already ready / unknown).

Prefer: client sends READY for every self-authored reed in the local
`reeds` store on reconnect; server no-ops when already announced or not
author. Cheap and avoids a new list API.

Pending unsigned reeds: no READY (no tip / no countersign yet).

### Tests / checklist

- SignReed 201 → no pending events / no `RELAY_REQUEST` until READY.
- READY → fanout → author `RELAY_REQUEST` → `RELAY_RESPONSE` without miss.
- Double READY → single fanout (no duplicate pending storm).
- Reconnect after SignReed but before READY → READY → fanout.
- Non-author READY → rejected / ignored.
- Idempotent SignReed replay → still no fanout without READY (or with
  `fanout_ready` already true, still no second fanout from HTTP).
