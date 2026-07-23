# Deletion 00 — Design + trust model

## Status

Proposed.

## Depends on

—

## Context

Today reed (and account) deletion is a hard server-side wipe with no
author attestation and no way for holders to verify they should purge
local copies. See [README](README.md).

## Scope

- Lock the trust model, offline-first queues, idempotent countersign,
  410 + `type` bodies, and the split between reed vs account tracks.
- Document non-goals and cross-links (tip check, keys retained).

## Non-goals

- Implementing schema, API, SPA, or fanout (01–09).
- Recovery ingest of deletion certificates.
- GC / pruning of old deletion tombs.

## Design

### Trust split

| Layer | Who | Attests |
|-------|-----|---------|
| User signature | Author’s active key | “I remove this reed / this account” |
| Server countersignature | Active server key | “This server witnessed that removal at `server.timestamp`” |

Clients **must** verify both before deleting local reed content or
purging peer account data. Either failure → ignore the event / keep data.

### Offline-first author flow (reeds)

1. Write `pendingRemoval` (serverID, userID, reedID, user signature).
2. DELETE to server when online (body carries user signature; retry across
   restarts).
3. Persist returned cert in `removedReeds`.
4. Delete reed from `reeds`.
5. Delete `pendingRemoval` entry.

Account author flow is **online-only** (sign → DELETE → verify → wipe
local session) with an optional `note` (≤140). No `pendingAccountRemoval`
queue — unlike reed removal, this must not silently retry later.

### Idempotency

First successful accept: server stores user sig + countersignature.
Later identical (or retry) requests return the **same** cert — never mint
a second server signature for the same reed/account removal.

### Fanout + catch-up

Reuse the **new-reed realtime path**: live `pending_events` / dispatch for
online recipients; on `SYNC_REQUEST`, recompute a diff and enqueue the same
event type (see [04](04_reed_fanout.md)). Deletion catch-up is
`reed_allocations ∩ reed_removals` (then clear allocation after apply) — the
dual of `GetMissingOut`. No separate notification outbox. GET **410** remains
a safety net. Scale concerns deferred until measured.

### Account covers reeds

One `type: "account"` cert authorizes peers to drop **all** local reeds
by that `userID`. No per-reed certs required for account deletion. Public
keys remain on server and devices.

### Tip check

Once reed removal exists, [recovery 16](../recovery/16_reed_tip_check.md)
tip = newest reed that is **not** covered by a reed-removal cert (and
author is not account-removed).

## Resolved

See [README — Resolved](README.md#resolved).

## Test plan

- [ ] Spec review: queue order, idempotency, 410 `type` examples match README
- [ ] Tip-check cross-link acknowledged for implementers of recovery 16
