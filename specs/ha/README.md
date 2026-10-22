# High availability — several app replicas behind a gateway

Run Syrinx either as **one replica** (today's setup: one host runs nginx,
the app and Postgres) or as **HA**: a load-balancer host running nginx and
a **WebSocket gateway**, a database host, and two or more app hosts, all
Raspberry Pis on one LAN. One switch, `REPLICA_MODE=single|ha`, picks the
mode.

The gateway holds every client WebSocket; app replicas hold none. A replica
can crash, restart or be updated without a single user reconnecting, and
"one socket per user", presence and delivery work the way they do in
single mode, because all sockets are still in one process. Single mode keeps
that process inside the app, as today, and pays nothing for HA.

Read [00](00_design.md) first: it inventories every place the server
assumes it is alone, and says what each mode does about it.

**Blank slate** — schema changes go in `InitDB`, no migrations, no
backwards compatibility. Server, gateway and SPA ship in lockstep.

## Status

**Proposed.** Nothing here is implemented.

## Steps

| # | Title | Depends on | Status |
|---|-------|------------|--------|
| [00](00_design.md) | Design, inventory, locked decisions | — | Proposed |
| [01](01_replica_mode.md) | `REPLICA_MODE` + `REPLICA_ID`; drop the Postgres bus | 00 | Proposed |
| [02](02_boot_lock.md) | Serialize schema and server-identity init across replicas | 01 | Proposed |
| [03](03_presence_in_memory.md) | Socket liveness in memory; no per-user heartbeat writes | 01 | Proposed |
| [04](04_connection_holder.md) | Sockets behind a connection-holder interface | 03 | Proposed |
| [05](05_gateway.md) | The gateway: handshake, routing, internal protocol | 04 | Proposed |
| [06](06_replica_loss.md) | Losing a replica: reassign users, re-queue its relay requests | 05 | Proposed |
| [07](07_server_wide_reset.md) | Realtime reset on gateway restart; Postgres crash detection | 05 | Proposed |
| [08](08_shared_locks_and_jobs.md) | In-process locks and cron jobs made replica-safe | 01 | Proposed |
| [09](09_load_balancer.md) | LB host: nginx, gateway, SPA, draining | 05 | Proposed |
| [10](10_database_host.md) | DB host: Postgres on the LAN, pool caps, backups | 01 | Proposed |
| [11](11_deploy_scripts.md) | `deploy/syrinx.sh` topologies, per-role setup, rolling update | 09, 10 | Proposed |
| [12](12_observability.md) | Gateway and per-replica telemetry | 05 | Proposed |
| [13](13_testing.md) | Local HA dev setup and failure tests | 06, 07, 09 | Proposed |

01–08 are code and can be built and tested on one machine before any deploy
work. 03 and 07 improve single mode too; 04 is a refactor that leaves single
mode's behaviour unchanged.

## Locked decisions

| Topic | Decision |
|---|---|
| Modes | `REPLICA_MODE=single` (default) or `ha`. Replaces `CLEAR_PRESENCE_ON_BOOT` and `REALTIME_BUS_ENABLED` |
| Where sockets live | **single**: in the app, as today. **ha**: in the gateway on the LB host. Replicas never hold a client socket |
| Cross-replica messaging | None needed for delivery: every replica sends to users through the gateway. The Postgres `LISTEN/NOTIFY` bus (`realtime_bus.go`) is deleted. No Redis |
| Inbound routing | The gateway sends all of a user's frames to one replica, chosen by rendezvous hashing over live replicas, so they're handled in order. A user moves only when their replica goes away |
| A replica dies | Its users are rehashed and told to resubscribe and catch up **on the same socket**. Its in-flight relay requests are re-queued by a survivor |
| The gateway restarts | Every socket drops, as in a single-mode restart: that is the server-wide realtime reset peers are told about |
| What HA covers | App replicas: crash, restart, update. The LB host (nginx + gateway) and the DB host stay single points of failure ([00](00_design.md#non-goals)) |
| Handshake auth | Done by a replica (it has the DB and keys); the gateway forwards the handshake's signed parameters and gets a user id or a refusal |
| Gateway build | Same repo, root package, `//go:build gateway`, like `ops`. No DB, no keys |
| Singletons | Cron jobs and per-peer senders take a Postgres lease or advisory lock in HA; single mode keeps in-process mutexes |
| SPA | Served by nginx on the LB host; app hosts only serve `/api` |
| Deploy | One `deploy/syrinx.sh`; the saved topology says single or HA. HA `update` restarts app hosts one at a time, invisibly to users |
