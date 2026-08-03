# Observability 01 — OTLP trace receiver on the app-host collector

## Status

Proposed.

## Depends on

—

## Where

This step lives in the **`rpi` ops repo**, not this one — specifically
`scripts/otel-agent.sh`, function `otel_agent_write_config()`. It's listed
here so the observability spec is complete in one place, but there is no
Syrinx source change in this step.

## Context

`otel_agent_write_config()` currently generates a collector config with two
receivers only:

- `journald` — ships `${service_name}.service` unit logs
- `hostmetrics` — CPU/memory/disk/network/load for the app host itself

There is no `otlp` receiver, so there is nowhere on the app host for the
Syrinx process to send spans (or app-level metrics) even after
[02](02_app_bootstrap.md) starts producing them.

## Scope

Add an `otlp` receiver bound to `127.0.0.1` only (the app process is the only
intended caller — no need to expose it on the LAN) and a `traces` pipeline
that forwards through the existing `otlphttp` exporter to the telemetry Pi,
the same path logs and metrics already use.

```yaml
receivers:
  otlp:
    protocols:
      grpc:
        endpoint: 127.0.0.1:4317
      http:
        endpoint: 127.0.0.1:4318
  journald: ...      # unchanged
  hostmetrics: ...   # unchanged

processors:
  batch: ...          # unchanged
  resource: ...       # unchanged
  resourcedetection: ... # unchanged

exporters:
  otlphttp: ...       # unchanged

service:
  pipelines:
    traces:
      receivers: [otlp]
      processors: [resource, batch]
      exporters: [otlphttp]
    logs: ...      # unchanged
    metrics: ...   # unchanged
```

`resource` here tags spans with `service.name` the same way it already tags
logs, so traces are filterable/attributable per service in OpenObserve.

## Non-goals

- Exposing the receiver beyond `127.0.0.1` — the app and its collector run on
  the same host.
- Any change to the telemetry Pi's collector or OpenObserve config — the
  `otlp` receiver already listening there (for logs/metrics) accepts traces
  on the same ports with no changes needed.

## Rollout

Picked up automatically the next time `scripts/setup.sh` or
`scripts/update.sh` runs `otel_agent_install`/`otel_agent_write_config` on
the app host — no manual step beyond re-running the existing update script.
