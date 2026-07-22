# Recovery 11 — SPA recovery progress (entity ledger + generic UI)

## Status

Implemented.

## Depends on

[10](10_spa_unified_restore.md).

## Context

After [10](10_spa_unified_restore.md) writes the backup under recovery mode, the
client owns a work queue built from **restored** IndexedDB and shows progress
while [12](12_spa_own_identity_claim.md)–[14](14_spa_reeds_follows_complete.md)
execute. Users see a **generic percent** bar and timing — not phase names
(own identity / peers / reeds / …).

## Scope

### Enumerate (after backup write)

Build the work set from restored stores:

- **Own identity** — one item (claim).
- **Peer identities** — one per `users` row except the claimant; peers without a
  buildable full key nest are **skipped** (not failures). Do not infer peers
  from `reeds` / `following`.
- **Reeds** — one per `reeds` row that has a `server` countersignature block
  (`unsignedReeds` out of scope).
- **Follows** — `following` edges in pages of ≤100 user IDs.
- **Complete** — one terminal step.

### Persist (local recovery table)

Keep a local map (IndexedDB or `localStorage`) of each unit of work:

- Entity keys such as `own_identity`, `peer:<userId>`, `reed:<reedId>`,
  `complete`.
- Value: `{ startTime?: number, endTime?: number, skipped?: boolean, skipReason?: string }`
  (timestamps in unix ms). A finished item has both times; duration is
  `endTime - startTime`.
- **Follows** cannot be one row per edge for API purposes. Use one logical
  follows group with **pages**:
  `follows` → `{ pages: [ { index, userIds, startTime?, endTime? }, ... ] }`.

Resume = skip units that already have `endTime` (and skipped peers). Refresh
mid-run must not restart completed work.

Total recovery duration for the user-facing summary = sum of all completed
deltas (entity durations + follow page durations).

### Progress UI

- Single percent bar: completed units / total units (count skipped as completed
  for the denominator progress, or exclude from both — pick one and keep it
  consistent; prefer counting skipped as done so the bar can reach 100%).
- Optional elapsed / estimated copy from summed durations.
- No user-facing breakdown of own identity vs peers vs reeds.
- On full completion: success message; user confirms reload ([14](14_spa_reeds_follows_complete.md)).

## Non-goals

- Network execution (12–14).
- Import-gate routing (15).
- Playwright (deferred).

## Design

A dedicated module (e.g. `recoveryProgress`) owns enumerate, the progress table,
and percent events. Enumeration runs once after backup write / on resume, not
under the assumption that the new origin already had data.

## Test plan

- [x] After fixture backup write, totals match restored IndexedDB
  (`enumerateRecoveryWork` / `ensureRecoveryProgress`)
- [x] Peers from `users` minus self only
- [x] Incomplete peer nest → skipped entry with reason
  (`canAssembleKeyNest`)
- [x] Follow pages of ≤100 with per-page timestamps
- [x] Refresh mid-run → completed entities keep `endTime`; work resumes
  (`mergeRecoveryProgress`)
- [x] UI shows only generic percent (+ optional total duration), not phase lists
