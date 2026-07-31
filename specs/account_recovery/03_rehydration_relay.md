# Account recovery 03 — Rehydration relay + complete

## Status

Proposed.

## Depends on

[02](02_challenge_bootstrap.md)

## Context

After bootstrap the client has tip **id** and can publish (Approach B).
Own reed **bodies** still live on peers. The server already tracks
allocations and relays via `pending_events` / `RELAY_REQUEST`. This step
makes the server **push** the recovering user’s own reeds to them,
tip first, without a new content plane.

## Scope

- When a user with an open `account_rehydrations` row connects (WS ready
  / `SYNC_REQUEST` or explicit start), enqueue relay of each catalogued
  own reed toward that user, **tip first**.
- Prefer online holders from `holderUserIDs`; never treat the recovering
  user as the sole source for their own missing body.
- Reuse existing relay / miss / retry behavior
  ([publish 02](../publish/02_relay_miss.md) when real).
- Authenticated **`POST /api/account-recovery/complete`** — sets
  `completed_at` (idempotent); stops proactive rehydration enqueue for
  that user.
- Optional: authenticated **`GET /api/account-recovery/status`** —
  `{ startedAt, completedAt, reedIDs: [...] }` if the SPA needs a
  server-side checklist; otherwise the client may own progress entirely
  from the bootstrap catalog (prefer **client-owned progress** for v1 and
  skip this GET unless needed).

## Non-goals

- Restoring other users’ reeds or peer profiles.
- Blocking normal API use while rehydrating.
- Changing tip-check / SignReed.
- SPA UI ([05](05_spa_rehydration_publish.md)).

## Design

### Trigger

After bootstrap, the client opens a normal authenticated WebSocket.
Server path (pick one, document in code):

1. **On WS auth success** (or first `SYNC_REQUEST`): if
   `account_rehydrations` is open for this user, call
   `StartRehydration(userID)` once per connection (idempotent).
2. And/or authenticated **`POST /api/account-recovery/rehydrate`** that
   the SPA calls after writing the session — same helper.

Prefer **explicit SPA POST** after session install so relay does not race
IndexedDB key write; WS connect alone is a resume path if the POST was
lost.

### `StartRehydration(userID)`

1. Load open rehydration row; if completed or missing → no-op.
2. Load own non-removed reed tips ordered with **current tip first**, then
   `signed_at DESC`.
3. For each reed, if the recovering user is **not** yet able to serve it
   (no need to check local — server does not know bodies), ensure a
   pending delivery toward `userID` using the same machinery as an
   ordinary fetch/catch-up for that reed, choosing holders from
   allocations where `holder_user_id != userID` and the holder is online
   when possible.
4. Do not create allocations for random users; only ask existing holders
   to relay.
5. Quiet: no `NewReed` fanout; this is recovery of the author’s own
   content to themselves.

If a reed has **zero** non-self holders, skip enqueue (client already
knows from bootstrap `holderUserIDs: []`).

### Delivery path

Identical to normal relay once a pending event exists: holder gets
`RELAY_REQUEST` → `RELAY_RESPONSE` / `RELAY_MISS` → recovering client gets
data → verifies → `DATA_ACK`. Author allocation may already exist from
create; ACK remains the possession signal.

### Complete

`POST /api/account-recovery/complete` (signature-auth):

```text
UPDATE account_rehydrations
SET completed_at = COALESCE(completed_at, now())
WHERE user_id = $1
```

Idempotent 204/200. Client may call when the local catalog is done or the
user dismisses remaining gaps ([05](05_spa_rehydration_publish.md)).
After complete, `StartRehydration` is a no-op; ordinary per-reed fetch
still works if the user opens a missing reed later.

### Interaction with tip publish

Server does **not** gate SignReed on rehydration state. Client gates
compose only on having `tipReedID` from bootstrap (or genesis).

## Test plan

- [ ] Bootstrap + rehydrate POST enqueues tip before older reeds
- [ ] Reed with only self allocation → no holder relay attempted
- [ ] Online holder receives RELAY_REQUEST for recovering author
- [ ] Complete sets completed_at; second complete ok
- [ ] After complete, StartRehydration no-ops
- [ ] RECOVERY_MODE / ongoing_recoveries untouched
