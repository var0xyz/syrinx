# Observability 01 — OTLP trace receiver on the app-host collector

## Status

Implemented (`rpi` repo: `scripts/otel-agent.sh`, `setup.sh`, `update.sh`).

## Depends on

—

## Where

This step lives in the **`rpi` ops repo**, not this one — specifically
`scripts/otel-agent.sh`, function `otel_agent_write_config()`. It's listed
here so the observability spec is complete in one place, but there is no
Syrinx source change in this step.

## Context

`otel_agent_write_config()` previously generated a collector config with two
receivers only:

- `journald` — ships `${service_name}.service` unit logs
- `hostmetrics` — CPU/memory/disk/network/load for the app host itself

There was no `otlp` receiver, so there was nowhere on the app host for the
Syrinx process to send spans or app-level metrics via the localhost-collector
path.

## Scope

Add an `otlp` receiver bound to `127.0.0.1` only (the app process is the only
intended caller — no need to expose it on the LAN) and pipelines that forward
through the existing `otlphttp` exporter to the telemetry Pi, the same path
logs and host metrics already use.

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
    metrics:
      receivers: [hostmetrics, otlp]   # otlp = Syrinx app metrics
      processors: [resourcedetection, resource, batch]
      exporters: [otlphttp]
```

`resource` tags traces with `service.name` the same way it already tags
logs. App metrics from Syrinx already carry `service.name` / `host.name` from
the OTEL SDK resource in `observability.Setup`.

`setup.sh` / `update.sh` set `OTEL_COLLECTOR_HOST=127.0.0.1` (not the
telemetry Pi hostname) when `TELEMETRY_HOST` is configured, so Syrinx talks
to the local agent only.

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

After rollout, confirm:

1. `systemctl status otelcol-agent` is active.
2. App `.env` has `OTEL_COLLECTOR_HOST=127.0.0.1` and `OTEL_COLLECTOR_PORT=4317`.
3. Traces and `syrinx_*` metrics appear in OpenObserve within one export interval.

## Verification

On the app host:

```bash
ss -tlnp | grep -E '4317|4318'   # otelcol listening on 127.0.0.1
grep OTEL_COLLECTOR /path/to/app/.env
systemctl restart otelcol-agent syrinx   # or your unit name
```

In OpenObserve: HTTP traces (`span_kind = '2'`) and business metrics should
continue to flow; if the app previously pointed `OTEL_COLLECTOR_HOST` at the
telemetry Pi directly, switch to `127.0.0.1` and re-run `update.sh`.
