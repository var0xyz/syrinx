# HA 03 — Socket liveness in memory, no per-user heartbeat writes

## Status

Proposed. Fixes [RISKS.md C2](../../RISKS.md).

## Depends on

[01](01_replica_mode.md)

## Context

Clients send `PONG` every 60 s. `handlePong` flags the socket, and every
30 s `reapStalePresence` writes all flagged users' `online_users.last_pong`
in one statement (`RecordPongs`), then scans for rows older than the 2 min
TTL (`StalePresence`) and evicts them. The DB heartbeat exists to catch
**half-open sockets**: there's no read deadline, so a client that vanished
without a FIN looks connected until a write fails, which can take minutes.

The process holding a socket already knows when it last heard from it.
Writing that to Postgres only helps some *other* process. In single mode
there is none, and in HA every socket is in the gateway, so there is none
either.

## Change

In whatever holds the sockets (the app's connection manager in single
mode, the gateway in HA):

1. Each socket keeps `lastSeen atomic.Int64` (unix seconds), set on every
   inbound frame, not only `PONG`.
2. The existing 30 s ping pass also closes any socket whose `lastSeen` is
   older than `realtimePresenceTTL` (2 min). Closing runs the normal close
   path, which tears the user down.
3. A read deadline of TTL + 30 s, refreshed on every frame, so a reader
   goroutine never blocks forever on a dead peer.

Removed: `pongPending`, `takePongs`, `flushPongs`, `RecordPongs`,
`StalePresence`, `EvictStalePresence`, `startPeriodicCleanup`,
`reapStalePresence`, the `presence.reap` span, and the `last_pong` column.
`handlePong` only updates `lastSeen`. In HA the gateway answers heartbeats
itself and never forwards them ([05](05_gateway.md)).

`online_users` rows then exist exactly while a socket does. A crash leaves
rows behind, and the reset at the next start removes them
([07](07_server_wide_reset.md)).

## What this removes

One `UPDATE online_users … WHERE user_id = ANY($1)` and one stale-row scan
every 30 s. A per-user write load proportional to online users becomes
zero.

## Tests

- A client that stops sending frames is closed after the TTL and torn down.
- A client sending only `PONG`s stays connected.
- No statement touches `online_users` during an idle minute.
- `RISKS.md`: remove C2.
