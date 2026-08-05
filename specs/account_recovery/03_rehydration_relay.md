# Account recovery 03 — Rehydration via client reed requests

## Status

Implemented (client-driven; no server orchestration).

## Depends on

[02](02_challenge_bootstrap.md)

## Context

After bootstrap the client has tip **id** and can publish (Approach B).
Own reed **bodies** still live on peers. Rehydration uses the **existing**
`REQUEST_REED` → `pending_events` → `RELAY_REQUEST` path — no second
content plane and no server-side rehydration table.

## Scope

- After bootstrap, client seeds IndexedDB `reedRequests` from
  `bootstrap.reedIDs` (tip first; skip already held locally).
- Paced drainer sends `REQUEST_REED` over WS at **≤1 per second**.
- On verify + `storeReed`, delete the `reedRequests` row; server deletes
  its `pending_events` row on `DATA_ACK` as usual.
- Reconnect: drainer re-sends remaining `reedRequests` rows (server
  in-flight events were cleared on disconnect).

## Non-goals

- Server `StartRehydration`, `account_rehydrations`, or
  `POST /account-recovery/complete`.
- Restoring other users’ reeds or peer profiles.
- Blocking normal API use while rehydrating.
- Changing tip-check / SignReed.

## Design

### Client `reedRequests` store (IndexedDB v28)

| Field | Purpose |
|-------|---------|
| `requestId` | keyPath — `md5("REQUEST_REED:{serverId}/{authorId}/{reedId}")` |
| `serverId` | Server scope |
| `authorId` | Reed author |
| `reedId` | Reed id |
| `requestedAt` | FIFO ordering (tip-first seed uses incrementing timestamps) |

Replaces per-request `sessionStorage` tracking for all explicit fetches.

### Flow

1. Import/bootstrap → seed `reedRequests` from catalog.
2. WS connect + `SYNC_REQUEST` → start drainer.
3. Drainer sends one `REQUEST_REED` per second for pending rows.
4. Holder side unchanged: `GetNextPendingForHolder` → `RELAY_REQUEST`.
5. `DATA_RESPONSE` → verify → store → delete `reedRequests` row → `DATA_ACK`.
6. **`REED_NOT_HELD`** or **`REED_NOT_FOUND`** → delete `reedRequests` row;
   do not retry until the user explicitly re-requests ([publish 02](../publish/02_relay_miss.md)).

Bootstrap returns **all** non-removed `reedIDs`, including reeds with no
peer holders; the server fails fast on fetch when the body is unheld.

### Interaction with tip publish

Server does **not** gate SignReed on rehydration state. Client gates
compose only on having `tipReedID` from bootstrap (or genesis).

## Test plan

- [ ] Bootstrap seeds `reedRequests`; drainer ≤1/sec
- [ ] Successful relay deletes row; reed in IndexedDB
- [ ] Reconnect re-sends remaining rows only
- [ ] `REED_NOT_HELD` deletes `reedRequests` row; no endless retry
- [ ] Unheld reed ids still seeded from bootstrap
- [ ] Normal open-reed fetch uses same store
