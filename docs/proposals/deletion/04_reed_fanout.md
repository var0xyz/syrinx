# Deletion 04 — Reed-removal realtime fanout + catch-up

## Status

Proposed.

## Depends on

[03](03_reed_api.md)

## Context

Holders must learn about removals live and after being offline. **Reuse the
existing new-reed realtime machinery** (`pending_events`, `dispatchMany`,
`SYNC_REQUEST` → `catchUp`) rather than a separate outbox or join-on-connect
product. Scale the same way new reeds already do; revisit only if that
proves insufficient.

See [`realtime/`](../../../realtime/) (`catchUp` / `GetMissingOut`,
`dispatchMany`).

## Scope

- On **first** successful reed-removal accept: notify the same audience
  class as new-reed distribution for that author (online followers /
  broadcast / profile subscribers as applicable), via the existing
  broadcast channel + `pending_events` path.
- New event name (e.g. `reed_removed`) whose delivery carries the **full
  signed cert** (`type: "reed"`). No content relay from a holder — the cert
  is the payload.
- **Catch-up on `SYNC_REQUEST`:** dual of `GetMissingOut` — find removals
  for reeds this user still holds via
  `reed_allocations ∩ reed_removals` (“holdings” = rows in
  `reed_allocations`), enqueue the same event type, deliver certs. After
  the client applies (or server acks delivery), **drop / clear the
  allocation** so the next sync does not repeat (same progress role
  allocations play for new reeds).
- Do **not** re-fanout on idempotent API retries.
- GET **410** remains a safety net if a reed is touched before sync.

## Non-goals

- Separate durable `pending_deletion_notifications` table.
- Watermark/cursor protocol beyond what sync+allocations already provide.
- SPA verify/apply (06).
- Changing UNLOGGED / online-scoped semantics of `pending_events` (catch-up
  recomputes the diff, as with new reeds).

## Design

### Live path

Mirror `NewReed` handling in `handleBroadcasts`:

1. First cert insert succeeds.
2. Emit `BroadcastMessage` with a removal type + reed/author ids (handlers
   load cert from `reed_removals` when building the client message if not
   inlined).
3. For online recipients with a sync request id: `CreatePendingEvent` +
   dispatch. Payload to the client is the cert JSON (same as 410 body).

Idempotent removal API retries: no second broadcast.

### Catch-up path

Extend `catchUp` (or a sibling called from the same `SYNC_REQUEST` hook):

```text
reed_allocations ∩ reed_removals  →  pending reed_removed events  →  deliver certs
```

Suggested query shape (indexes already favor this):

```sql
SELECT rr.*
FROM reed_allocations ra
JOIN reed_removals rr ON rr.reed_id = ra.reed_id
WHERE ra.user_id = $1
```

Cost is **O(|reed_allocations|)** per sync in the naive form — same class of
work as other sync diffs. Accept for v1; if it hurts, bound with
`rr.server_signed_at` / allocation cleanup (dropped rows shrink the set).

### Progress

| New reed | Reed removal |
|----------|--------------|
| Allocation appears when held | Allocation removed after removal applied |
| `GetMissingOut`: follows ⋉ reeds − allocations | `reed_allocations ∩ reed_removals` |

### Client

Treat WS/sync delivery like a pushed 410: switch on `type`, verify both
sigs, purge local reed, acknowledge so allocation can clear. Exact WS ack
vs clearing allocation when the pending event completes remains open — see
[README open questions](README.md#open-questions).

## Test plan

- [ ] First removal → online holders get cert via pending_events path
- [ ] Idempotent retry → no second fanout
- [ ] Offline through removal → SYNC_REQUEST catch-up delivers cert once
- [ ] After apply/ack → allocation gone; second sync does not re-deliver
- [ ] Bad cert not applied client-side (06); allocation handling still safe
- [ ] GET 410 still works if sync has not run
