# HA 12 — Gateway and per-replica telemetry

## Status

Proposed.

## Depends on

[05](05_gateway.md)

## Context

Today one process reports the whole server. In HA, sockets are counted by
the gateway, requests and DB work by each replica, and a slow replica hides
in an average.

## Change

- Replicas: resource attribute `service.instance.id = REPLICA_ID`; absent
  in single mode. The OTEL agent adds it to host metrics and logs too.
- The gateway exports to the LB host's OTEL agent as service
  `syrinx-gateway`:
  - `syrinx.gateway.sockets` (gauge) and `syrinx.gateway.users_per_replica`
    (gauge, by `replica.id`)
  - `syrinx.gateway.frames` (counter; `direction` = in | out)
  - `syrinx.gateway.handshakes` (counter; `outcome` = ok | refused | no_replica)
  - `syrinx.gateway.users_moved` (counter; `reason` = lost | drain)
  - `syrinx.gateway.replica_links` (gauge, up links)
  - `syrinx.gateway.forward_latency` (histogram, frame in → handed to
    replica)
- Replicas: `syrinx.relay.requeued` (counter, from [06](06_replica_loss.md)).
- Both modes: `syrinx.realtime.epoch_lost` (counter, from
  [07](07_server_wide_reset.md)).

Socket counts move from the app to the gateway in HA; the WebSocket panels
read whichever source has data.

## Dashboard

`generate_syrinx_dashboard.py` gets a **Replicas** tab:

- Users per replica (stacked), replica links up.
- Requests and p95 latency per replica.
- Users moved (annotated with reason), relay requests re-queued.
- Gateway: sockets, handshakes by outcome, frames per second, forward
  latency.
- DB connections per replica against `max_connections`.

## Tests

Generate the dashboard and check unique layout ids; panels show "no data"
in single mode, not errors.
