// Package observability wires up OpenTelemetry tracing + DB-pool metrics for
// the Syrinx API. Telemetry is optional: Setup decides once, at boot, whether
// a local OTLP collector is configured, and returns a Manager whose every
// method degrades to a plain passthrough when it isn't. Callers (main.go)
// never branch on "is this enabled" themselves — that check lives here,
// exactly once, so the disabled path costs nothing per request or per query.
package observability

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"time"

	"github.com/XSAM/otelsql"
	"github.com/gorilla/mux"
	"go.opentelemetry.io/contrib/instrumentation/github.com/gorilla/mux/otelmux"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/propagation"
	metricsdk "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.24.0"

	"syrinx/observability/metrics"
)

// Manager is either backed by a live OTEL SDK (host was configured at Setup)
// or is the zero value, in which case every method is an inert passthrough.
// A nil *Manager also behaves as disabled, so callers can't crash on it.
type Manager struct {
	enabled        bool
	tracerProvider *sdktrace.TracerProvider
	meterProvider  *metricsdk.MeterProvider
}

// Setup builds the tracing/metrics pipeline against host:port when host is
// non-empty. An empty host (the default — no collector configured) returns a
// disabled Manager and a nil error: that's the expected shape for dev
// laptops/CI, not a failure. Once host is set, this is a readiness check:
// Setup force-flushes a real boot metric through the exporter and returns an
// error if the collector isn't actually reachable, so callers should treat a
// non-nil error here as fatal — a configured-but-unreachable host means
// operator intent (telemetry required) can't be honored. Once observability
// is running, a later, ongoing outage is not this package's concern; the
// Manager keeps degrading its own instruments gracefully at that point (see
// observability/metrics).
func Setup(host, port string) (*Manager, error) {
	if host == "" {
		return &Manager{}, nil
	}

	grpcEndpoint := fmt.Sprintf("%s:%s", host, port)

	// Host identity comes from the SDK detectors, not hardcoded here: WithHost
	// adds host.name, WithFromEnv honours OTEL_RESOURCE_ATTRIBUTES /
	// OTEL_SERVICE_NAME so an operator can pin a distinct name/id per machine
	// (see specs/observability). Without this, every app process on every host
	// would report an identical resource and collide into one series in
	// OpenObserve. The explicit ServiceName/Version below still win over any
	// env-provided service.name because they are merged last.
	res, err := resource.New(context.Background(),
		resource.WithFromEnv(),
		resource.WithHost(),
		resource.WithAttributes(
			semconv.ServiceName("syrinx-api"),
			semconv.ServiceVersion("1.0.0"),
		),
	)
	if err != nil {
		return &Manager{}, fmt.Errorf("build resource: %w", err)
	}

	traceExporter, err := otlptrace.New(
		context.Background(),
		otlptracegrpc.NewClient(
			otlptracegrpc.WithInsecure(),
			otlptracegrpc.WithEndpoint(grpcEndpoint),
		),
	)
	if err != nil {
		return &Manager{}, fmt.Errorf("build trace exporter: %w", err)
	}

	tracerProvider := sdktrace.NewTracerProvider(
		// AlwaysSample: closed-community traffic is low enough that full
		// request waterfalls are worth the storage cost. See specs/observability/.
		sdktrace.WithSampler(sdktrace.AlwaysSample()),
		sdktrace.WithBatcher(traceExporter),
		sdktrace.WithResource(res),
	)

	// Metrics ride the OTLP HTTP port, one above the gRPC trace port — same
	// convention the app-host collector already uses for logs/metrics today.
	httpEndpoint := fmt.Sprintf("%s:4318", host)
	metricsExporter, err := otlpmetrichttp.New(
		context.Background(),
		otlpmetrichttp.WithInsecure(),
		otlpmetrichttp.WithEndpoint(httpEndpoint),
	)
	if err != nil {
		return &Manager{}, fmt.Errorf("build metrics exporter: %w", err)
	}

	meterProvider := metricsdk.NewMeterProvider(
		metricsdk.WithReader(metricsdk.NewPeriodicReader(metricsExporter, metricsdk.WithInterval(10*time.Second))),
		metricsdk.WithResource(res),
	)

	// Readiness probe: OTLP exporters connect lazily, so nothing above
	// actually touches the network. Recording and force-flushing one real
	// metric is the only way to know the collector is actually reachable
	// before deciding boot succeeded — a misconfigured/unreachable host
	// should fail startup, not silently drop telemetry forever after. Runs
	// before any global otel.Set* call so a failed probe leaves no global
	// state behind.
	bootCounter, err := meterProvider.Meter("syrinx/boot").Int64Counter("syrinx.system.boot")
	if err != nil {
		return &Manager{}, fmt.Errorf("build boot counter: %w", err)
	}
	bootCounter.Add(context.Background(), 1)
	flushCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := meterProvider.ForceFlush(flushCtx); err != nil {
		return &Manager{}, fmt.Errorf("telemetry collector unreachable at %s: %w", httpEndpoint, err)
	}

	otel.SetTracerProvider(tracerProvider)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))
	otel.SetMeterProvider(meterProvider)

	return &Manager{
		enabled:        true,
		tracerProvider: tracerProvider,
		meterProvider:  meterProvider,
	}, nil
}

// OpenDB opens the DB connection, instrumented with otelsql query spans only
// when enabled. Either way the result is a regular *sql.DB.
func (m *Manager) OpenDB(driverName, dataSourceName string) (*sql.DB, error) {
	if m == nil || !m.enabled {
		return sql.Open(driverName, dataSourceName)
	}
	return otelsql.Open(driverName, dataSourceName, otelsql.WithAttributes(semconv.DBSystemPostgreSQL))
}

// RegisterDBStats wires connection-pool metrics (open/idle/in-use
// connections, wait count/duration) when enabled. The returned unregister
// func is always safe to defer, even when disabled.
func (m *Manager) RegisterDBStats(db *sql.DB) (unregister func(), err error) {
	if m == nil || !m.enabled {
		return func() {}, nil
	}
	reg, err := otelsql.RegisterDBStatsMetrics(db, otelsql.WithAttributes(semconv.DBSystemPostgreSQL))
	if err != nil {
		return func() {}, err
	}
	return func() { _ = reg.Unregister() }, nil
}

// Middleware returns the mux middleware that starts one span per HTTP
// request (method, matched route, status, duration) when enabled, or a
// pure passthrough otherwise — same call shape either way.
func (m *Manager) Middleware(serverName string) mux.MiddlewareFunc {
	if m == nil || !m.enabled {
		return func(next http.Handler) http.Handler { return next }
	}
	return otelmux.Middleware(serverName)
}

// Metrics returns a business-metrics recorder backed by OTEL when enabled, or a
// no-op otherwise.
func (m *Manager) Metrics() metrics.Recorder {
	if m == nil || !m.enabled {
		return metrics.Noop{}
	}
	return metrics.New(otel.Meter("syrinx/business"))
}

// Shutdown flushes and closes the tracer/meter providers. No-op when
// disabled or nil.
func (m *Manager) Shutdown() {
	if m == nil || !m.enabled {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = m.tracerProvider.Shutdown(ctx)
	_ = m.meterProvider.Shutdown(ctx)
}
