# HA 13 — Local HA dev setup and failure tests

## Status

Proposed.

## Depends on

[06](06_replica_loss.md), [07](07_server_wide_reset.md),
[09](09_load_balancer.md)

## Local setup

`scripts/ha-dev-up.sh` / `ha-dev-down.sh`, modelled on
`federation-dev-up.sh`: one tmux session (`syrinx-ha`), one build of
`bin/syrinx` and `bin/syrinx-gateway`, the shared `syrinx_db` Postgres
container with its own database (`syrinx_ha`), and one pane each for:

- `app-1` on `:8091` and `app-2` on `:8092`, `REPLICA_MODE=ha`;
- the gateway on `:8070` (clients) and `:8071` (internal);
- nginx in a container on `:8090`, with the [09](09_load_balancer.md)
  config: `/api/` to both apps, `/ws/` to the gateway. The Vite dev
  server's `API_HOST` points at `:8090`.

Each process can be killed (`kill -9`) or stopped (Ctrl-C) in its own pane.
Combined with the federation setup, it shows whether a replica restart
leaves peers alone.

## Go tests

Two replicas (`realtimeService`s on separate pools, test DB) and an
in-process gateway on loopback:

| Test | Asserts |
|---|---|
| Cross-replica relay | Requester hashed to A, holder to B: request, response and ACK arrive |
| One socket per user | Second connect closes the first; replica doesn't tear down |
| Ordering | A user's frames reach one replica, in order |
| Replica crash | Kill A: its users get `SESSION_MOVED`, keep sockets; its dispatched events re-queued ([06](06_replica_loss.md)) |
| Partition | A's late timer can't undo the re-queue |
| Drain | SIGTERM A: users move first, no socket closes |
| Gateway restart | One boot notice; realtime tables cleared ([07](07_server_wide_reset.md)) |
| Replica restart | No reset notice |
| Epoch lost | `TRUNCATE realtime_epoch`: sockets closed, one notice |
| Boot race | Two boots on an empty DB: one key, one root ([02](02_boot_lock.md)) |
| Single mode | No gateway code paths; no `online_users` writes while idle |

SPA: a Playwright test that receives `SESSION_MOVED` and checks the active
subscription is replayed and a `SYNC_REQUEST` sent, without reconnecting.

## Load test

Once [loadtest](../loadtest/README.md) exists, run its mix through the LB
with 1, 2 and 3 replicas and record p95 latency, fanout latency, gateway
forward latency and DB connections. Expected: the gateway adds a LAN hop to
WebSocket traffic and nothing measurable to `/api`; adding replicas spreads
load without raising p95.
