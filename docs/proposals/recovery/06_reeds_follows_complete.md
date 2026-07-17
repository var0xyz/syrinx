# Recovery 06 — Reeds, follows, complete

## Status

Proposed.

## Depends on

[04](04_own_identity_claim.md) (peer report 05 is independent and may land
in parallel after 04)

## Context

Claimed users re-report holdings and follow edges, then clear the import
gate. See [README](README.md) *Phase 2*, *Phase 3*, *Ending recovery*.

## Scope

- `POST /api/recovery/reeds` — **one** reed: verify historical countersig
  against restored server key by fingerprint; upsert `reeds` +
  `reed_allocations` for reporter; author must exist.
- `POST /api/recovery/following` — body `{ userIDs }`, **max 100**; follow
  existing targets; skip missing; reject >100.
- `POST /api/recovery/complete` — delete caller’s `ongoing_recoveries` row.
- Optional `GET /api/recovery/status` → `{ importing: bool }`.

## Non-goals

- No reed content storage.
- No SPA.
- No change to live `SignReed` / follow handlers beyond coexistence.

## Design

All handlers in `syrinx/recovery`. Reed payload via `identity.BuildReedPayload`
(or equivalent). Completing import lifts the step-03 middleware gate for that
user while `RECOVERY_MODE` remains on.

## Test plan

- [ ] Valid reed restores metadata + allocation; idempotent re-POST
- [ ] Unknown author → 400
- [ ] Bad countersig → 400
- [ ] Follows: 101 IDs → 400; missing targets skipped; existing followed
- [ ] Complete clears ongoing; subsequent normal API allowed
