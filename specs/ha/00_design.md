# HA 00 — Design, inventory, locked decisions

## Status

Proposed.

## Goal

Let an operator run Syrinx as several app replicas so that losing,
restarting or updating an app host costs users nothing, while keeping the
one-host install exactly as cheap as it is today.

Two modes, one app binary:

- **single** — one replica, holding its own sockets. Everything that only
  exists to coordinate replicas is off. The process is the whole server.
- **ha** — two or more replicas sharing one Postgres, with every client
  socket held by a gateway in front of them.

## Topology

```
                    Internet
                       │
           Cloudflare Tunnel  (or :443 with mTLS)
                       │
        ┌──────────────┴──────────────┐
        │  LB Pi                      │
        │  nginx ── /      SPA files  │
        │       ├── /api/  → app :8080 (least_conn)
        │       └── /ws/   → gateway :8070 (localhost)
        │  syrinx-gateway: holds every client socket
        └──┬──────────┬──────────┬────┘
   :8080 + │          │          │  internal link :8071 (LAN only)
   ┌───────┴───┐ ┌────┴──────┐ ┌─┴─────────┐
   │ app Pi 1  │ │ app Pi 2  │ │ app Pi N  │  syrinx (REPLICA_MODE=ha),
   │ app-1     │ │ app-2     │ │ app-N     │  otel agent, cleanup jobs
   └─────┬─────┘ └─────┬─────┘ └─────┬─────┘
         └───────┐     │     ┌───────┘
          :5432  │     │     │  (LAN only)
              ┌──┴─────┴─────┴──┐
              │  DB Pi          │  Postgres, nightly dump
              └─────────────────┘
```

Single mode collapses this onto one host: nginx, the app (sockets
included) and Postgres on `127.0.0.1`, as `deploy/scripts/syrinx/setup.sh`
installs today.

## Why a gateway

Sockets are the only realtime state that can't live in Postgres. If each
replica holds the sockets of the users connected to it:

- a replica crash or update drops its users, who all reconnect;
- a user's stale socket on one replica and live one on another have to be
  reconciled, and the stale one's teardown must not wipe the live session;
- a crashed replica's presence rows and subscriptions need a survivor to
  find and clean them, which needs a replica registry and heartbeats;
- every delivery to a user on another replica needs a bus.

With one process holding all sockets, none of that exists: it is the same
situation as single mode, where all of it already works. What remains is
that the *work* behind a socket can move between replicas, which is much
easier, because the socket and the session don't move.

The cost is a new component on the LB host, and that a gateway restart
drops every socket. The LB host is already a single point of failure, and a
gateway restart is no worse than a single-mode restart today.

## What the server assumes about being alone today

Checked against the code. "Already safe" means it works across replicas
as-is.

| # | Where | Assumption | single | ha | Step |
|---|---|---|---|---|---|
| 1 | Boot: `InitDB`, `InitServer`, `InitServerKey`, `maybeExportRootKey` | Nobody else is booting. Two replicas on an empty DB would each mint a server key and a root user | Unchanged | Advisory lock through schema + identity init | [02](02_boot_lock.md) |
| 2 | `CLEAR_PRESENCE_ON_BOOT`, `ClearRealtimeState`, `notifyPeersOfRealtimeReset` | This process starting or stopping is the server's realtime state starting or stopping | As today | That's the gateway now: a replica's boot and shutdown touch no realtime state | [07](07_server_wide_reset.md) |
| 3 | `handlePong` → `flushPongs` → `RecordPongs`; `reapStalePresence` (RISKS C2) | One reaper writes heartbeats to Postgres and evicts | Liveness in memory, no heartbeat writes | Same, in the gateway | [03](03_presence_in_memory.md) |
| 4 | `realtimeConnectionManager`: `registerClient` (one socket per user), `SendToUser`, `DisconnectUser`, `HasConnection` | Sockets are in this process | Unchanged | All behind an interface whose HA implementation talks to the gateway | [04](04_connection_holder.md), [05](05_gateway.md) |
| 5 | `realtime_bus.go` (`REALTIME_BUS_ENABLED`) | — | Unused | Unneeded: every replica reaches every socket through the gateway. Deleted | [01](01_replica_mode.md) |
| 6 | `realtimeClient` per-socket state: `user`/`broadcast` subscription flags, sync request id | The process reading the socket keeps it | Unchanged | Kept by the replica the user is routed to; rebuilt when the user moves | [05](05_gateway.md), [06](06_replica_loss.md) |
| 7 | `authenticateWebSocket`, suspension and device checks at upgrade | Done where the socket lands | Unchanged | The gateway asks a replica | [05](05_gateway.md) |
| 8 | `relayTimers` (`startRelayTimer`) | The dispatching process lives until the timer fires | Unchanged | **Already safe** while the dispatcher lives (`handleRelayTimeout` re-reads the event). If it dies, its timers die, and the events stay dispatched forever | [06](06_replica_loss.md) |
| 9 | `ClaimPeerStream` | — | **Already safe** (DB lease) | Same | — |
| 10 | `blockNoticeLocks` | One process sends block notices to a peer | Unchanged | Postgres lease per peer | [08](08_shared_locks_and_jobs.md) |
| 11 | `peerKeyUpdateAttempts` | Per-process throttle | Unchanged | N attempts per window instead of one. Accepted | — |
| 12 | Ledger, invite quota, recovery follows | — | **Already safe** (`pg_advisory_xact_lock`) | Same | — |
| 13 | Request replay window, WS auth nonces, challenges | — | **Already safe** (timestamps, DB tables) | Same | — |
| 14 | `privateKeys` parse cache | — | Per-process cache of immutable data | Same | — |
| 15 | `ripples-cleanup`, `challenges-cleanup` cron | One host runs them | Unchanged | On every app host; one run wins a lock | [08](08_shared_locks_and_jobs.md) |
| 16 | Unlogged realtime tables | — | A **Postgres crash truncates them** while the app keeps its sockets. True today | Same | [07](07_server_wide_reset.md) |
| 17 | DB pool, no `SetMaxOpenConns` | One process can't exhaust `max_connections` | Cap it anyway | Required | [10](10_database_host.md) |
| 18 | Server key loaded once at boot; `ops rotate-key` with the server stopped | One process | Unchanged | All replicas stopped for a rotation | [11](11_deploy_scripts.md) |
| 19 | Metrics gauges (connections, online users) | One process reports the whole server | Unchanged | Socket gauges come from the gateway; request metrics carry `REPLICA_ID` | [12](12_observability.md) |

## Behaviour by mode

| Concern | single | ha |
|---|---|---|
| Who holds sockets | The app's connection manager | The gateway |
| Who handles a user's frames | The app | The replica the user hashes to |
| Delivery to a user | Write to the local socket | Send to the gateway, from any replica |
| One socket per user | Close older local sockets | The gateway closes older sockets |
| Socket liveness | `lastSeen` in memory, closed after the TTL | Same, in the gateway |
| Presence rows | Written at connect, deleted at teardown | Same, by the user's replica, told by the gateway |
| A replica dies | n/a (the server died) | Its users move to other replicas on the same socket; a survivor re-queues its relay requests |
| A replica restarts or updates | n/a | Users move off and back; nobody reconnects |
| Realtime reset (clear state, tell peers) | Every boot and shutdown | Gateway start and stop |
| Postgres lost unlogged tables | Epoch check: close sockets, tell peers | Same, triggered by any replica, carried out by the gateway |
| Cron jobs | Run | Run on every app host; one wins the lock |
| Per-peer notice ordering | `sync.Mutex` | DB lease |

## Why no bus, and no Redis

Every frame to a user goes to the gateway, which holds every socket, so
there is never a question of which replica can reach a user. The existing
Postgres bus (`realtime_bus.go`) only existed for that question and is
removed. Redis would solve the same non-problem.

Delivery stays best-effort, exactly as today: what makes the system correct
is the durable path (`pending_events` and the client's `SYNC_REQUEST`
catch-up).

## Failure modes

| Failure | What users see | What happens |
|---|---|---|
| App host dies | A short pause; maybe a spinner while their subscription is replayed | The gateway notices its link drop, rehashes that replica's users, tells each client to resubscribe and catch up on the same socket, and asks a survivor to re-queue the dead replica's relay requests ([06](06_replica_loss.md)) |
| App host restarted or updated | Nothing | Same as above, but the replica drains first: stops taking new users, its users move, then it exits |
| All app hosts down | API errors; sockets stay open but idle; SPA works offline | The gateway refuses new handshakes, holds existing sockets for a grace period, then closes them. When a replica returns, users are assigned and resubscribe |
| Gateway restarts | Every socket drops and reconnects | Server-wide realtime reset, as a single-mode restart today ([07](07_server_wide_reset.md)) |
| DB host down | API errors, SPA works offline | Replicas stay up; the epoch check decides on recovery whether state was lost |
| Postgres crash-restart | Reconnect | Unlogged tables are empty: the epoch check resets realtime state |
| LB host down | Site down | Nothing to do |
| Replica cut off from the gateway but not the DB | Its users move | It still runs timers for relay requests it dispatched; dispatch updates are conditional on who dispatched, so its late timeouts can't undo a survivor's re-queue ([06](06_replica_loss.md)) |

## Non-goals

- **Database HA.** The DB Pi is a single point of failure. A streaming
  replica would not carry the unlogged tables, so failover is a realtime
  reset at best, and automatic failover between two Pis invites
  split-brain. Nightly dumps and the existing recovery story cover total
  loss.
- **LB and gateway HA.** Two gateways would bring back the "which process
  holds this socket" problem the gateway exists to remove. A standby LB
  with a floating IP can come later; it would start with empty gateway
  state, i.e. a reset.
- **Auto-scaling.** Replicas are a fixed list in the topology.
- **Geographic distribution.** One LAN.

## Capacity notes (Pi 4/5, LAN)

- Gateway: one goroutine pair per socket, a few KB each. Thousands of
  sockets fit comfortably in a Pi's RAM; frames are small protobufs. It
  parses only each frame's type, to answer heartbeats itself.
- Every frame crosses the LAN once more (client → gateway → replica and
  back). Sub-millisecond on a LAN, invisible next to the WAN hop.
- Each replica: a capped DB pool (default 10). Three replicas ≈ 30+
  connections against Postgres' default 100.
- App↔DB queries now cross the LAN (~0.3 ms each) instead of loopback;
  routes that run many queries per request feel it most.
