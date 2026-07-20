# Recovery 13 — SPA import-gate mirror

## Status

Proposed.

## Depends on

[10](10_spa_own_identity_claim.md)

## Context

After a successful own claim the server inserts `ongoing_recoveries` and returns
**403** on non-recovery API routes until `POST /api/recovery/complete`. The SPA
must mirror that block so users cannot use normal screens mid-import. See
[README](README.md) *Import gate*.

## Scope

- From successful claim (10) until `complete` (12): treat the client as
  **import-gated**.
- Redirect or block navigation to normal app routes (`/reeds`, `/profile`, …)
  → `/recover` (ledger / progress).
- Allow recovery endpoints, `/api/server/info`, and `/api/server/keys/` (same
  allowlist as server middleware).
- On app load: if gated (persisted ledger shows claim done but not complete),
  send user to `/recover` even if they bookmarked `/reeds`.
- After `complete` + user reload (12), gate is off — normal routes work.

## Non-goals

- Server middleware changes (03).
- Playwright coverage (deferred).

## Design

Gate state derives from persisted ledger (claim complete ∧ complete not done),
not a separate flag. Optional: detect 403 on a signed request as a fallback to
force `/recover`.

## Test plan

- [ ] After claim, visiting `/reeds` redirects to `/recover`
- [ ] Recovery API calls still work while gated
- [ ] After complete + reload, `/reeds` accessible
