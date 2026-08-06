# Publish ready (fanout gate)

This directory specifies fixing the **publish / relay race**: HTTP
countersign must not fan out until the author confirms the fully signed
reed is in local storage via a WebSocket **`PUBLISH_READY`**.

**Blank slate — no migration, no backwards compatibility** where schema
changes are needed (`pending_fanout` table, 1:1 with tip reeds).

| # | Title | Depends on |
|---|-------|------------|
| [00](00_design.md) | Design + race + locked model | — |
| [01](01_publish_ready.md) | HTTP SignReed + WS `PUBLISH_READY` + SPA | 00 |
| [02](02_relay_miss.md) | Real `RELAY_MISS` (drop allocation + retry) | 01 |

Related: current behaviour and temporary mitigation in
[`realtime/README.md`](../../realtime/README.md) (Known Issues).

---

## Status

**Implemented** (00–02).

## Locked decisions

| Topic | Decision |
|-------|----------|
| Countersign | Stay on **HTTP** `POST /reeds` |
| Fanout | **Never** from the HTTP handler; only after **`PUBLISH_READY`** |
| READY when | After client `storeReed` (and again on reconnect / catch-up for tips not yet announced) |
| Relay | All `RELAY_REQUEST`s treated equally — no “just published” special case |
| `RELAY_MISS` | Real unavailability: **remove** that holder’s allocation and retry another holder |
| Unheld body | **`REED_NOT_HELD`** when metadata exists but no holder can serve (see [02](02_relay_miss.md)) |

## Motivation

Previously the server fanned out on SignReed and immediately
`RELAY_REQUEST`ed the author before IndexedDB had the countersigned reed.
The author sent `RELAY_MISS`; the server temporarily ignored misses so the
author’s allocation was not orphaned, but pending deliveries could stick until
the requester `SYNC_REQUEST`ed. Gating fanout on READY removes the race by
construction and lets miss handling mean what it says again.
