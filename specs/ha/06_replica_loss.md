# HA 06 — Losing a replica

## Status

Proposed. With [07](07_server_wide_reset.md), fixes
[RISKS.md C1](../../RISKS.md).

## Depends on

[05](05_gateway.md)

## Context

With sockets in the gateway, a replica dying (power loss, OOM, kernel
panic) leaves no orphaned sockets, presence or subscriptions: they all
belong to sockets that are still open. Two things are left:

1. **Its users** need a new replica.
2. **Its relay requests.** `startRelayTimer` arms an in-memory timer on the
   replica that dispatched a request. When that replica dies, the timer dies
   too and the `pending_events` row stays `dispatched_at IS NOT NULL,
   relayed_at IS NULL` forever.

## Detection

The gateway's link to the replica closes, or misses two heartbeats
(10 s apart). No registry table and no DB heartbeats: the gateway is the
one place that needs to know which replicas are alive.

## Users

For each user hashed to the lost replica, the gateway picks the next
replica by the same hash and moves them (`MOVED_IN` + `SESSION_MOVED`, see
[05](05_gateway.md#moving-a-user-moved_in)). Their sockets stay open.

## Relay requests

`pending_events` gains `dispatched_by TEXT NOT NULL DEFAULT ''` (the
dispatching `REPLICA_ID`; `''` in single mode), set by the existing
claim (`UPDATE … SET dispatched_at = NOW() … WHERE dispatched_at IS NULL`).

The gateway sends `REPLICA_LOST(id)` to one surviving replica, which runs:

```sql
UPDATE pending_events SET dispatched_at = NULL, dispatched_to = NULL,
                          dispatched_by = ''
WHERE dispatched_by = $1 AND relayed_at IS NULL
RETURNING event_id, dispatched_to
```

and calls `dispatchNext` for each affected holder, the path a relay
timeout already takes.

**Not actually dead.** A replica cut off from the gateway but not from
Postgres still has its timers. When one fires after the re-queue, it must
not undo the survivor's work. So the timeout path (`handleRelayTimeout` →
`handleFailedRelay`) and every reset of a dispatch become conditional on
`dispatched_by = me` (the existing `resetEvent` already conditions on
`dispatched_to`; add `dispatched_by`). A late timer whose row was
re-queued finds nothing to update and stops.

Such a replica also can't deliver to anyone (no link), and reconnects to
the gateway when the network returns; it is simply a live replica again.

## Single mode

Nothing runs. `dispatched_by` is always `''`.

## Tests

- Kill a replica without close paths: its users get `SESSION_MOVED` within
  ~20 s and keep their sockets; its dispatched, unrelayed events are
  dispatched again by a survivor.
- Partition: block a replica's link only, re-queue, then fire its old
  timer: no change to the re-queued event.
- Users of other replicas don't move.
