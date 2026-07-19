# Recovery 04 — Own identity claim

## Status

Implemented.

## Depends on

[03](03_bookkeeping_and_gates.md)

## Context

Only the private-key holder can put their own identity back on record: a
short-lived challenge plus the countersigned profile and **full nested**
public-key chain. See [README](README.md) *Phase 1a*, *Nested key chain*.

## Scope

- Wire types + nest flatten/verify under `syrinx/recovery`.
- `GET /api/recovery/identity/claim` → `{ challenge: <unix seconds> }`.
- `POST /api/recovery/identity/claim` → `{ challenge, signature, profile, key }`.
- Validate challenge ≤ 60s; verify full nest (server countersigs + predecessor
  links + optional revocations); verify challenge sig with outermost key.
- Upsert user by verbatim `userID`, newest-wins on `server_signed_at`;
  username collision → rename loser with permanent suffix.
- Insert keys **oldest → newest** (preserve `predecessor_fingerprint` FK).
- Create → not unclaimed; if peer-seeded → delete `unclaimed_accounts`.
- Insert `ongoing_recoveries` for claimant.
- `recovery.RegisterRoutes` when `RECOVERY_MODE`; claim paths unauthenticated
  in middleware / SPA allowlist.

## Non-goals

- No peer `POST /recovery/identity`, reeds, follows, or SPA ledger.
- Incomplete nests are rejected (no partial writes).

## Design

Nested key JSON as in the README. Verification uses `syrinx/identity`
builders + restored server pubkeys by fingerprint. Handlers and store code
only under `syrinx/recovery`.

## Test plan

- [ ] Fresh claim creates user + keys; not in `unclaimed_accounts`; in `ongoing_recoveries`
- [ ] Stale / future challenge rejected
- [ ] Bad challenge signature rejected
- [ ] Broken predecessor link aborts; no partial user row
- [ ] Rotation nest inserts without FK violations
- [ ] Username collision renames loser; newest `server_signed_at` keeps name
