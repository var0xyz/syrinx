# Recovery 06 — Reeds, follows, complete

## Status

Implemented.

## Depends on

[04](04_own_identity_claim.md) (peer report 05 is independent and may land
in parallel after 04)

## Context

Claimed users re-report holdings and follow edges, then clear the import
gate. See [README](README.md) *Phase 2*, *Phase 3*, *Ending recovery*.

## Scope

- `POST /api/recovery/reeds` — **one** reed: nested `server` block; verify
  historical countersig against restored server key by fingerprint; upsert
  `reeds` + `reed_allocations` for reporter; author must exist. Quiet (no
  realtime broadcast). Same metadata re-POST is idempotent; conflicting
  metadata for an existing `reedID` → **409** + error log (should be
  impossible under the countersig bind).
- `POST /api/recovery/following` — body `{ userIDs }`, **max 100**; self →
  400; existing targets → `user_following` + `user_followers`; missing →
  `pending_follows` (no FK on target); batch still 200.
- `POST /api/recovery/complete` — delete caller’s `ongoing_recoveries` row
  (idempotent).
- DDL: `pending_follows`. Drain into real follow tables when a target user
  appears via own claim or peer identity report (steps 04/05). Leave rows
  alone when `RECOVERY_MODE` turns off.

## Non-goals

- No reed content storage.
- No SPA.
- No `GET /api/recovery/status` (client owns import progress; must call
  `/complete` to finish).
- No change to live `SignReed` / follow handlers beyond coexistence.
- No drain on live signup (new random IDs never match pending targets).

## Design

All handlers in `syrinx/recovery`. Reed payload via `identity.BuildReedPayload`
(or equivalent). Completing import lifts the step-03 middleware gate for that
user while `RECOVERY_MODE` remains on.

Reed body:

```json
{
  "reedID": "...",
  "authorID": "...",
  "userSignature": "<base64 armored>",
  "server": {
    "id": "<serverID>",
    "fingerprint": "<server signing key>",
    "timestamp": "...",
    "signature": "<base64 armored countersig>"
  }
}
```

`pending_follows (follower_user_id → users, following_user_id TEXT, PK)` —
no FK on the target ID. Index `following_user_id` for drain.

## Test plan

- [ ] Valid reed restores metadata + allocation; idempotent re-POST
- [ ] Unknown author → 400
- [ ] Bad countersig → 400
- [ ] Conflicting reed metadata → 409
- [ ] Follows: 101 IDs → 400; self → 400; existing followed; missing → pending
- [ ] Drain pending when target claimed / peer-reported
- [ ] Complete clears ongoing; subsequent normal API allowed
