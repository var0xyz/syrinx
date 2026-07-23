# Deletion 04 — Reed-removal realtime fanout + catch-up

## Status

Implemented.

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
- Event name `reed_removed` / WS `REED_REMOVED`; payload is the **full
  signed cert** (server-sourced — no holder relay).
- **Catch-up on `SYNC_REQUEST`:** `reed_allocations ∩ reed_removals`;
  deliver certs; on `DATA_ACK`, **clear the allocation**.
- Do **not** re-fanout on idempotent API retries.
- Live `reeds` row is **retained** so allocations are not CASCADE-deleted
  before holders apply (tip/list still exclude `reed_removals`).

## Non-goals

- Separate durable `pending_deletion_notifications` table.
- SPA verify/apply (06).

## Design

### Live path

`DeleteReed` first insert → `BroadcastMessage{ReedRemoved, cert}` →
`handleBroadcasts` → `CreatePendingEvent(reed_removed)` + direct
`REED_REMOVED` send to each online recipient with a sync request id.

### Catch-up path

`GetMissingRemovals` → pending `reed_removed` → `REED_REMOVED` delivery.

### Ack

`DATA_ACK` on `reed_removed`: `DeleteReedAllocation` + drop pending event.

## Test plan

- [x] First removal broadcasts; idempotent retry does not
- [x] Catch-up query + delivery path wired
- [x] DATA_ACK clears allocation for reed_removed
- [ ] End-to-end with SPA (06)
