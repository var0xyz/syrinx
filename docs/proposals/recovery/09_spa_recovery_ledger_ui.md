# Recovery 09 — SPA recovery ledger: enumerate, persist, progress UI

## Status

Proposed.

## Depends on

[08](08_spa_recovery_landing.md)

## Context

Recovery is client-driven: the device builds a work queue from local data, shows
progress, then executes it in later steps. See [README](README.md) *Client
responsibilities* and *Phase 1a–3*.

## Scope

- `/recover` route: shown when `recoveryMode` and not logged in (forfeit rule
  from 08). Redirect away if recovery is off or user is logged in.
- **Enumerate** local work items (no network yet):
  - **Own identity** — 1 step (claimant’s user row + key material present).
  - **Peer identities** — one item per entry in the IndexedDB `users` store
    **except** the claimant. These are cached profile records the device has
    seen; do not derive peers by scanning `reeds`, `following`, or other stores.
    Peers without a buildable full key nest are counted as **skipped** (not
    failures).
  - **Reeds** — one item per reed in the `reeds` store that has a `server`
    countersignature block (recoverable metadata). `unsignedReeds` are out of
    scope for the ledger (no server countersig to replay).
  - **Follows** — edges from the `following` store, batched into chunks of at
    most 100 user IDs per API call for progress display.
  - **Complete** — 1 terminal step (placeholder until 12).
- **Persist** ledger state (phase, completed IDs, skip reasons) in
  `localStorage` or IndexedDB so refresh mid-run resumes instead of restarting.
- Progress UI: phase name, counts (done / total / skipped), minimal controls
  (“Start” / “Resume” when execution lands in 10+).

## Non-goals

- Network calls to recovery endpoints (10–12).
- Folding `pendingFollows`, `pendingRevocation`, `unsignedReeds`, or `unfollow`
  into the recovery send path — after recovery completes, normal app startup
  syncs pending stores as it does today.
- Import-gate routing (13).
- Playwright coverage (deferred).

## Design

Prefer a `recoveryLedger` module owning enumeration, persistence, and progress
events. Enumeration runs on `/recover` load (or on “Start” if deferring scan).

Peer nest viability check can be shallow here (flag peers that clearly lack key
chain data); full nest build happens in 11.

## Test plan

- [ ] `/recover` lists correct totals for a fixture IndexedDB
- [ ] Peer list matches `users` store minus self; not inferred from reeds
- [ ] Incomplete peer nest → skipped, not blocking
- [ ] Refresh mid-run → progress restored from persistence
