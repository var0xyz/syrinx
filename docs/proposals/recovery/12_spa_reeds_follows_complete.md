# Recovery 12 — SPA reeds, follows, and complete

## Status

Proposed.

## Depends on

[11](11_spa_peer_identities.md)

## Context

Final recovery phases: replay reed metadata, follow edges, then end the per-user
import lock. See [06](06_reeds_follows_complete.md) and [README](README.md)
*Phase 2–3*, *Ending recovery*.

## Scope

- **Reeds:** for each recoverable reed in the ledger, `POST /api/recovery/reeds`
  (one per request). Update persisted progress per reed.
- **Follows:** `POST /api/recovery/following` in chunks of ≤100 user IDs from
  the `following` store. Update progress per batch.
- **Complete:** `POST /api/recovery/complete`. Clear or mark terminal state in
  persisted ledger.
- **Done UX:** inform the user recovery finished successfully and offer a button
  to reload the page (full navigation reload → landing `/`, where normal
  post-login routing applies). Do not auto-navigate into the app without user
  action.
- After reload, normal startup handles any remaining pending stores
  (`unsignedReeds`, `pendingFollows`, `pendingRevocation`, etc.) via existing
  sync paths — no special recovery-time drain.

## Non-goals

- Import-gate client mirror (13).
- Playwright coverage (deferred).

## Design

Order: reeds → follows → complete. Idempotent re-POSTs on resume are acceptable
(server handlers are idempotent).

## Test plan

- [ ] Full run through complete → success message → reload → home
- [ ] Resume after partial reeds → continues from ledger
- [ ] `complete` called once; second run is no-op or safe idempotent
