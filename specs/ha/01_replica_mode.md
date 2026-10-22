# HA 01 — `REPLICA_MODE` and `REPLICA_ID`; drop the Postgres bus

## Status

Proposed.

## Depends on

[00](00_design.md)

## Context

Two booleans control replica behaviour today: `CLEAR_PRESENCE_ON_BOOT`
(default on) and `REALTIME_BUS_ENABLED` (default off). Only two of their
four combinations make sense, and with a gateway ([05](05_gateway.md))
neither is the right model: realtime state belongs to whoever holds the
sockets, and replicas never need to reach each other.

## Change

| Variable | Values | Default | Meaning |
|---|---|---|---|
| `REPLICA_MODE` | `single`, `ha` | `single` | Which column of [00](00_design.md#behaviour-by-mode) applies |
| `REPLICA_ID` | `[a-z0-9-]{1,32}` | empty | Required in `ha`, refused in `single`. Stable per host (the deploy script uses the host's short name) |
| `GATEWAY_ADDR` | `host:port` | empty | `ha` only: where the gateway's internal link listens |
| `GATEWAY_TOKEN` | secret | empty | `ha` only: shared with the gateway, authenticates the internal link both ways |

Boot fails closed on: `ha` without `REPLICA_ID`, `GATEWAY_ADDR` or
`GATEWAY_TOKEN`; a malformed id; `single` with any of them set.

In code, one value carries the mode:

```go
type replicaMode struct {
	ha bool
	id string // empty in single mode
}
```

Removed:

- `ClearPresenceOnBoot`, `RealtimeBusEnabled` from `AppConfig`, and from
  `.env.example`, `docker-compose.yml`, deploy scripts and
  `docs/operators.md`. Single mode always clears at boot; HA never does
  ([07](07_server_wide_reset.md)).
- `realtime_bus.go`, `SetBus`, `DeliverLocal`, the `bus` field and every
  `cm.bus != nil` branch, and their tests.
- Mentions of `CLEAR_PRESENCE_ON_BOOT` in RISKS C1 and federation 08 are
  updated to describe the new rule.

`REPLICA_ID` becomes the OTEL `service.instance.id` ([12](12_observability.md)).

## Tests

- Config validation table: each invalid combination refuses to boot.
- `single` behaves exactly as today with `CLEAR_PRESENCE_ON_BOOT=true` and
  no bus.
