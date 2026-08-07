# Observability — distributed tracing + custom business metrics

Today the server ships structured logs (zerolog → journald → OTLP →
OpenObserve) and, as of the telemetry host's `hostmetrics` rollout, host-level
CPU/memory/disk/network stats. **Traces** and **domain metrics** are partially
wired: `observability.Setup` runs from `main.go` when `OTEL_COLLECTOR_HOST` is
set (HTTP spans via `otelmux`, DB pool metrics via `otelsql`), and DB query
spans nest under the request span via context-aware SQL methods ([04](04_context_threading.md)).
**Custom business metrics** are implemented in `observability/metrics`.

The app host's `otelcol-agent` receives Syrinx OTLP on `127.0.0.1:4317/:4318`
and forwards to the telemetry Pi ([01](01_agent_otlp_receiver.md)).

| # | Title | Depends on | Where |
|---|-------|------------|-------|
| [00](00_design.md) | Design + architecture + locked decisions | — | — |
| [01](01_agent_otlp_receiver.md) | OTLP trace receiver on the app-host collector | — | `rpi` ops repo |
| [02](02_app_bootstrap.md) | Wire `SetupObservability` + HTTP request spans | 01 | this repo |
| [03](03_db_instrumentation.md) | DB query spans via `otelsql` | 02 | this repo |
| [04](04_context_threading.md) | Thread `context.Context` so DB spans nest under the request span | 02, 03 | this repo |
| [05](05_custom_metrics.md) | Custom business metrics (signups, reeds, deletions, WS, coverage, length, tags) | 02 | this repo |

## Locked decisions

| Topic | Decision |
|-------|----------|
| Transport | App → `localhost:4317` (gRPC, OTLP traces) + `:4318` (HTTP, OTLP metrics) → app-host `otelcol-agent` → telemetry Pi `otelcol-contrib` → OpenObserve. No new binaries; reuses the collector already installed on both hosts. |
| Trace library | `go.opentelemetry.io/otel/sdk/trace` (already a dependency) |
| HTTP spans | `otelmux` (gorilla/mux-specific, matched-route span names) or `otelhttp` (generic, actively maintained) — see [02](02_app_bootstrap.md) for the trade-off |
| DB spans | [`github.com/XSAM/otelsql`](https://github.com/XSAM/otelsql) wrapping `database/sql` |
| Query text | Statement text captured; **query arguments are never captured** (no user data in spans) |
| Correlation | DB spans must be children of the HTTP request span in the same trace (single-trace "waterfall" per request), not disconnected root spans |
| Business metrics | Counters + histograms on the OTLP metrics pipeline; `syrinx.*` instrument prefix — see [05](05_custom_metrics.md) |
| Metrics privacy | No usernames, tag text, or content; user IDs and reed IDs allowed; echoes/replies distinguished by `reed.kind` on publish counter |
| Echo targeting | `syrinx.echoes.targeted` on the echoed reed (indexed), separate from `syrinx.reeds.published{kind=echo}` on the echoing reed |
| Deletions | `syrinx.reeds.deleted` and `syrinx.users.deleted` on first successful cert persist (not replay); account removal carries `note.has` (bool), never note text |
| Per-reed coverage | Record `allocation_count` + `coveragePercent` whenever coverage is recomputed (same hook as WS `REED_COVERAGE`) |

## Status

| Step | Status |
|------|--------|
| 00 | Proposed |
| 01 | Implemented (`rpi`: local OTLP ingress on `127.0.0.1:4317/:4318`) |
| 02 | **Implemented** — `observability.Setup`, `otelmux`, env-gated in `main.go` |
| 03 | **Implemented** — `obs.OpenDB` via `otelsql` + pool metrics |
| 04 | **Implemented** — `context.Context` threaded through all DB packages; HTTP handlers pass `r.Context()` |
| 05 | **Implemented** — business metrics in `observability/metrics` |
