# HA 07 — Realtime reset on gateway restart; Postgres crash detection

## Status

Proposed.

## Depends on

[05](05_gateway.md)

## Context

[Federation 08](../federation/08_server_reset.md) clears all realtime
state and tells peers to forget what they share with us whenever the
server's realtime state is lost: at boot (a crash can't announce itself)
and at shutdown. Today that's tied to the app process and switched off with
replicas, so peers keep stale subscriptions and relay requests.

Separately, in **both** modes: the realtime tables are `UNLOGGED`, and
Postgres truncates them after a crash. The app survives a DB crash and
keeps its sockets; clients look connected while their subscriptions and
pending events are gone, and nothing tells them or the peers.

## Change

### Who resets

Realtime state lives exactly as long as the sockets, so the reset follows
whoever holds them:

- **single**: unchanged. Every app boot clears and sends the boot notice;
  every shutdown sends the shutdown notice.
- **ha**: the gateway. On start, once the first replica link is up, it
  sends `RESET(boot)` to that replica, which runs `ClearRealtimeState` and
  `notifyPeersOfRealtimeReset(boot)`. On SIGTERM it sends `RESET(shutdown)`
  before closing sockets. Replicas never reset on their own boot or
  shutdown; a rolling update therefore never resets peers.

If the gateway dies without a shutdown, its next start sends the boot
notice, as a crashed single-mode server does today.

`RESET` is one more `GatewayEnvelope` kind ([05](05_gateway.md)).

### Realtime epoch (both modes)

```sql
CREATE UNLOGGED TABLE IF NOT EXISTS realtime_epoch (
    id    BOOLEAN PRIMARY KEY DEFAULT TRUE CHECK (id),
    epoch TEXT NOT NULL
);
```

- A reset writes a fresh random epoch. The app (single) or every replica
  (ha) keeps the current one in memory.
- Every 10 s, each app process reads it. Missing or changed by nobody it
  knows of means Postgres lost realtime state:
  - **single**: close every socket, write a new epoch, send the boot notice.
  - **ha**: the replica that notices sends `DISCONNECT_ALL` with a reset
    request to the gateway; the gateway closes every socket and performs a
    `RESET(boot)` as above. The gateway ignores duplicate requests for the
    same lost epoch.
- A DB error isn't a lost epoch: retry next tick.

Cost in single mode: one single-row read every 10 s.

### RISKS

C1 goes once 06 and 07 land; C2 once [03](03_presence_in_memory.md) does.
Note under concurrency that the LB host (nginx + gateway) and the DB host
are single points of failure by design.

## Tests

- ha: restart every replica in turn: no reset notice. Restart the gateway:
  one boot notice, all realtime tables cleared.
- Both modes: `TRUNCATE realtime_epoch` → all sockets closed within one
  tick, exactly one reset notice.
