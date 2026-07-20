# Recovery 15 — SPA import-gate mirror

## Status

Proposed.

## Depends on

[11](11_spa_recovery_progress.md), [12](12_spa_own_identity_claim.md).

## Context

After a successful own claim the server inserts `ongoing_recoveries` and returns
**403** on non-recovery API routes until `POST /api/recovery/complete`. The SPA
must mirror that block. See [README](README.md) *Import gate* and the client
state model in [10](10_spa_unified_restore.md).

## Scope

- From successful claim (12) until `complete` (14): client is **import-gated**.
- Block normal app routes (`/reeds`, `/profile`, …) → recovery progress UI.
- Allow recovery endpoints, `/api/server/info`, `/api/server/keys/`, and
  `POST /api/users/status` (same spirit as server allowlist; status is
  unauthenticated and always reachable).
- On app load: if local recovery run is in progress (10/11), send the user to
  the recovery progress UI even if `userId` is present in `localStorage`.
- After `complete` + user reload (14), gate is off — normal routes work.
- Optional fallback: signed request returns 403 “finish recovery” → force
  recovery UI / re-probe status ([09](09_user_status.md)).

## Non-goals

- Server middleware changes (03).
- Playwright (deferred).

## Design

Gate from **local recovery run state** (claim finished ∧ `complete` not done),
not from “has `userId`” alone. Align with 10’s state model.

## Test plan

- [ ] After claim, `/reeds` redirects to recovery progress
- [ ] Recovery API calls still work while gated
- [ ] `userId` present + recovery in progress → not treated as normal login
- [ ] After complete + reload, `/reeds` accessible
