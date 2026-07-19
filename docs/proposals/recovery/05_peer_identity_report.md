# Recovery 05 — Peer identity report-back

## Status

Implemented.

## Depends on

[04](04_own_identity_claim.md)

## Context

After claiming, a user may seed **other** users from cached countersigned
profiles. Those accounts stay unclaimed until their owners claim. See
[README](README.md) *Phase 1b*.

## Scope

- `POST /api/recovery/identity` (authenticated): one peer per request,
  body `{ profile, key }` same nest shape as claim (no challenge).
- Reject if `profile.id == caller`.
- Same full-chain verification and oldest-first insert as claim.
- Newest-wins profile upsert; username collision rename as in step 04.
- If the `users` row is **created** → `INSERT unclaimed_accounts`.
- Never resurrect unclaimed for an already-claimed account.

## Non-goals

- No batch/list of users (verification cost).
- No reeds/follows/complete.
- Clients that lack a full nest for a peer must skip that peer (document only).

## Design

Reuse verify/store from step 04. Endpoint registered only in `RECOVERY_MODE`
via `recovery.RegisterRoutes`.

## Test plan

- [ ] Creates peer + unclaimed row
- [ ] Update existing peer with older `server_signed_at` → no overwrite
- [ ] Update with newer → profile wins; no new unclaimed if already claimed
- [ ] Self-submit rejected
- [ ] Incomplete nest rejected
