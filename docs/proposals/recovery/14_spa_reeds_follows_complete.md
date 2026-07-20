# Recovery 14 — SPA reeds, follows, and complete

## Status

Proposed.

## Depends on

[13](13_spa_peer_identities.md)

## Context

Final recovery phases: replay reed metadata, follow edges, then end the per-user
import lock. See [06](06_reeds_follows_complete.md) and [README](README.md)
*Phase 2–3*, *Ending recovery*.

## Scope

- **Reeds:** for each recoverable reed in the progress table,
  `POST /api/recovery/reeds` (one per request). Set `reed:<id>` start/end times
  ([11](11_spa_recovery_progress.md)).
- **Follows:** `POST /api/recovery/following` in pages of ≤100 user IDs from
  the `following` store. Set each follow **page** start/end times.
- **Complete:** `POST /api/recovery/complete`. Mark `complete` endTime; clear
  the recovery-run marker from [10](10_spa_unified_restore.md).
- **Done UX:** generic success (optional total duration from summed deltas);
  offer a button to reload (full navigation → `/`). Do not auto-navigate into
  the app without user action. Do not expose phase breakdowns.
- After reload, normal startup handles any remaining pending stores
  (`unsignedReeds`, `pendingFollows`, `pendingRevocation`, etc.) via existing
  sync paths — no special recovery-time drain.

## Non-goals

- Import-gate client mirror ([15](15_spa_import_gate_mirror.md)).
- Playwright coverage (deferred).

## Design

Order: reeds → follows → complete. Idempotent re-POSTs on resume are acceptable
(server handlers are idempotent). Progress UI remains the generic percent bar
from 11.

## Test plan

- [ ] Full run through complete → success message → reload → home
- [ ] Resume after partial reeds → continues from progress table
- [ ] `complete` called once; second run is no-op or safe idempotent
