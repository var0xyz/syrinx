# Observability 02 — Wire `SetupObservability` + HTTP request spans

## Status

Proposed.

## Depends on

[01](01_agent_otlp_receiver.md) (needs somewhere local to send spans, though
this step can be developed/tested against any OTLP endpoint, e.g. a local
`otel-collector` + `jaeger` docker-compose, before 01 lands on the Pi).

## Context

`observability.go` already defines `SetupObservability(host, port)`,
`ObservabilityManager`, and `ShutdownObservability()`, but `main.go` never
calls them — the entire tracing/metrics/OTEL-logging subsystem is dead code
today. There's also a latent bug: `NewObservabilityManager` builds a
`sdktrace.TracerProvider`, calls `otel.SetTracerProvider` on it, then
immediately builds a **second** `TracerProvider` and sets that one globally
instead — the first is silently discarded. Harmless while nothing calls the
tracer, but must be fixed before this is turned on.

## Scope

### 2.1 Fix the double `TracerProvider` in `NewObservabilityManager`

Keep a single `sdktrace.NewTracerProvider(...)` call (with the batcher +
resource + sampler options merged), call `otel.SetTracerProvider` once, and
reuse that same instance for `ObservabilityManager.tracerProvider`.

### 2.2 Call `SetupObservability` from `main.go`

Right after `SetupLogger()`:

```go
obsLogger, err := SetupObservability("127.0.0.1", "4317")
if err != nil {
    log.Warn().Err(err).Msg("[WARN] Observability disabled")
} else {
    defer ShutdownObservability()
}
```

Gate this behind an env var (e.g. `OTEL_ENABLED` or simply "endpoint reachable
or not") so environments without a local collector (dev laptops, CI) don't
block startup or spam warnings — mirrors how `TELEMETRY_HOST` gates the OTEL
agent on the infra side. Exact env var name/shape is an open point (see
below).

### 2.3 HTTP request spans

Add middleware so every request gets a span with method, matched route
template, status code, and duration. Two candidates:

| Option | Package | Pros | Cons |
|---|---|---|---|
| A | `go.opentelemetry.io/contrib/instrumentation/github.com/gorilla/mux/otelmux` (`v0.69.0`) | Purpose-built for `gorilla/mux`; span name is automatically the matched route template (`/api/reeds/{userID}/{reedID}`), not the raw URL | Marked "abandoned" upstream (no maintainer) as of writing — still functional, but no guarantee of future updates |
| B | `go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp` | Actively maintained, generic | Needs a small per-route wrapper (`otelhttp.WithRouteTag`) to get the templated route name instead of the raw URL |

Recommendation: start with **A** (`otelmux`) since it's a one-line
`router.Use(...)` and matches the existing router shape with zero
per-route changes; revisit if the upstream deprecation becomes a real
maintenance problem.

```go
import "go.opentelemetry.io/contrib/instrumentation/github.com/gorilla/mux/otelmux"

router := mux.NewRouter()
router.Use(otelmux.Middleware(cfg.ServerName))
// existing api.Use(loggingMiddleware) etc. unchanged, on the api subrouter
```

Place it on the root `router`, before subrouters are attached — `otelmux`
docs confirm it traces across subrouters and reports the full matched path.

### 2.4 Host identity on the emitted resource

The OTEL resource in `observability.Setup` must carry a per-host identity, not
just `service.name`. Without it, the app process on every host reports an
identical resource and OpenObserve collapses them into one series (the same
failure the `hostmetrics` streams already hit when two machines both report
`host.name="telemetry"`). `Setup` builds the resource with
`resource.WithHost()` (auto-detects `host.name`) and `resource.WithFromEnv()`
(honours `OTEL_RESOURCE_ATTRIBUTES` / `OTEL_SERVICE_NAME`), so an operator can
pin a distinct name/id per machine via env — documented in `.env.example`.
Fixing the analogous label on the *`hostmetrics`* streams is a separate change
in the `rpi` ops repo (`otel-agent.sh` `resourcedetection`/`resource`), out of
scope here. **Done** (the `Setup` resource wiring; the rest of 02 is still
proposed).

## Non-goals

- DB spans — [03](03_db_instrumentation.md).
- Correlating DB spans under the request span — [04](04_context_threading.md).
- Recording into the existing (currently-unused) `CustomMetrics` histograms —
  optional follow-up, not required to answer "how long did this request
  take" (the span duration already answers that).

## Open points

- Exact config knob for enabling/disabling observability in non-Pi
  environments (env var name, default off vs. default on with a
  fail-open reachability check).
- Whether `ObservabilityLogger`'s `WriteLogEntry`/OTEL-log path
  (`NewOTELLogger`) is worth turning on too, given logs already ship via
  journald → `otelcol-agent`. Likely redundant; probably leave the OTEL-log
  exporter path unused and keep zerolog → journald as the one log path.
