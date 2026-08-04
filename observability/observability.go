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
// laptops/CI, not a failure. Once host is set, a real SDK/exporter failure is
// returned as an error and the caller should treat observability as
// unavailable for this run (Manager is still safe to use — nil-like).
func Setup(host, port string) (*Manager, error) {
	if host == "" {
		return &Manager{}, nil
	}

	grpcEndpoint := fmt.Sprintf("%s:%s", host, port)

	res, err := resource.New(context.Background(),
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
		sdktrace.WithSampler(sdktrace.AlwaysSample()),
		sdktrace.WithBatcher(traceExporter),
		sdktrace.WithResource(res),
	)
	otel.SetTracerProvider(tracerProvider)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))

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
