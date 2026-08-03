# Observability — distributed tracing for requests + DB queries

Today the server ships structured logs (zerolog → journald → OTLP →
OpenObserve) and, as of the telemetry host's `hostmetrics` rollout, host-level
CPU/memory/disk/network stats. There are **no trace streams**: nothing in the
request path ever creates a span, so there is no way to answer "how long did
this request take, which queries did it run, and how long did each one take"
from OpenObserve today.

The OTEL SDK wiring for traces already exists in `observability.go`
(`SetupObservability`, `ObservabilityManager`) but is dead code — never called
from `main.go` — and `InstrumentDatabase` is a stub that returns the `*sql.DB`
unchanged. Separately, the app host's OTLP shipper
([`otel-agent.sh`](https://github.com/) in the `rpi` ops repo) has no `otlp`
receiver, so even a correctly instrumented app would have nowhere local to
send spans.

This closes both gaps: a local trace ingress on the app host, and real
span creation for HTTP requests and DB queries, correlated into one trace per
request.

| # | Title | Depends on | Where |
|---|-------|------------|-------|
| [00](00_design.md) | Design + architecture + locked decisions | — | — |
| [01](01_agent_otlp_receiver.md) | OTLP trace receiver on the app-host collector | — | `rpi` ops repo |
| [02](02_app_bootstrap.md) | Wire `SetupObservability` + HTTP request spans | 01 | this repo |
| [03](03_db_instrumentation.md) | DB query spans via `otelsql` | 02 | this repo |
| [04](04_context_threading.md) | Thread `context.Context` so DB spans nest under the request span | 02, 03 | this repo |

## Locked decisions

| Topic | Decision |
|-------|----------|
| Transport | App → `localhost:4317` (gRPC, OTLP) → app-host `otelcol-agent` → telemetry Pi `otelcol-contrib` → OpenObserve. No new binaries; reuses the collector already installed on both hosts. |
| Trace library | `go.opentelemetry.io/otel/sdk/trace` (already a dependency) |
| HTTP spans | `otelmux` (gorilla/mux-specific, matched-route span names) or `otelhttp` (generic, actively maintained) — see [02](02_app_bootstrap.md) for the trade-off |
| DB spans | [`github.com/XSAM/otelsql`](https://github.com/XSAM/otelsql) wrapping `database/sql` |
| Query text | Statement text captured; **query arguments are never captured** (no user data in spans) |
| Correlation | DB spans must be children of the HTTP request span in the same trace (single-trace "waterfall" per request), not disconnected root spans |

## Status

**Proposed.** Nothing in this directory has been implemented yet.
