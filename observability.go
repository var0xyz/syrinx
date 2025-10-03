package main

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"go.opentelemetry.io/contrib/instrumentation/runtime"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploghttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	otellog "go.opentelemetry.io/otel/log"
	"go.opentelemetry.io/otel/log/global"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/propagation"
	logsdk "go.opentelemetry.io/otel/sdk/log"
	metricsdk "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.24.0"
)

// ObservabilityManager manages all observability components
type ObservabilityManager struct {
	observabilityPlatformURL string
	resource                 *resource.Resource
	tracerProvider           *sdktrace.TracerProvider
	meterProvider            *metricsdk.MeterProvider
	logger                   *OTELLogger
}

// OTELLogger implements ObservabilityLogger interface for OTEL logging
type OTELLogger struct {
	observabilityPlatformURL string
	httpClient               *http.Client
	batch                    []string
	batchMutex               sync.Mutex
	batchSize                int
	flushInterval            time.Duration
	stopChan                 chan struct{}
	wg                       sync.WaitGroup
	loggerProvider           *logsdk.LoggerProvider
}

// WriteLogEntry implements ObservabilityLogger interface
func (l *OTELLogger) WriteLogEntry(entry []byte) error {
	l.batchMutex.Lock()
	defer l.batchMutex.Unlock()

	// Add log entry to batch
	l.batch = append(l.batch, string(entry))

	// If batch is full, flush immediately
	if len(l.batch) >= l.batchSize {
		go l.flush()
	}

	return nil
}

// Close implements ObservabilityLogger interface
func (l *OTELLogger) Close() error {
	close(l.stopChan)
	l.wg.Wait()

	// Shutdown the logger provider
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return l.loggerProvider.Shutdown(ctx)
}

// flushLoop runs in the background and flushes the batch periodically
func (l *OTELLogger) flushLoop() {
	defer l.wg.Done()
	ticker := time.NewTicker(l.flushInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			l.flush()
		case <-l.stopChan:
			// Final flush on shutdown
			l.flush()
			return
		}
	}
}

// flush sends the current batch to OpenObserve via OTEL
func (l *OTELLogger) flush() {
	l.batchMutex.Lock()
	if len(l.batch) == 0 {
		l.batchMutex.Unlock()
		return
	}

	// Create a copy of the batch and clear the original
	batchCopy := make([]string, len(l.batch))
	copy(batchCopy, l.batch)
	l.batch = l.batch[:0] // Reset slice but keep capacity
	l.batchMutex.Unlock()

	// Send each log entry via OTEL (which will batch them)
	ctx := context.Background()
	otelLogger := l.loggerProvider.Logger("syrinx-api")

	for _, logEntry := range batchCopy {
		// Create a log record
		record := otellog.Record{}
		record.SetBody(otellog.StringValue(logEntry))
		record.SetTimestamp(time.Now())

		// Emit the record
		otelLogger.Emit(ctx, record)
	}
}

// NewOTELLogger creates a new OTEL logger
func NewOTELLogger(observabilityPlatformURL string, batchSize int, flushInterval time.Duration) (*OTELLogger, error) {
	// Create resource
	res, err := resource.New(context.Background(),
		resource.WithAttributes(
			semconv.ServiceName("syrinx-api"),
			semconv.ServiceVersion("1.0.0"),
		),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create resource: %w", err)
	}

	// Create OTLP HTTP exporter
	exporter, err := otlploghttp.New(
		context.Background(),
		otlploghttp.WithInsecure(),
		otlploghttp.WithEndpoint(observabilityPlatformURL),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create OTLP exporter: %w", err)
	}

	// Create batch processor
	batchProcessor := logsdk.NewBatchProcessor(exporter,
		logsdk.WithExportInterval(flushInterval),
		logsdk.WithExportMaxBatchSize(batchSize),
	)

	// Create logger provider
	loggerProvider := logsdk.NewLoggerProvider(
		logsdk.WithResource(res),
		logsdk.WithProcessor(batchProcessor),
	)

	// Set global logger provider
	global.SetLoggerProvider(loggerProvider)

	logger := &OTELLogger{
		observabilityPlatformURL: observabilityPlatformURL,
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
		batch:          make([]string, 0, batchSize),
		batchSize:      batchSize,
		flushInterval:  flushInterval,
		stopChan:       make(chan struct{}),
		loggerProvider: loggerProvider,
	}

	// Start the background flusher
	logger.wg.Add(1)
	go logger.flushLoop()

	return logger, nil
}

// NewObservabilityManager creates a new observability manager
func NewObservabilityManager(grpcEndpoint string) (*ObservabilityManager, error) {
	// Create resource
	res, err := resource.New(context.Background(),
		resource.WithAttributes(
			semconv.ServiceName("syrinx-api"),
			semconv.ServiceVersion("1.0.0"),
		),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create resource: %w", err)
	}

	// Create OTLP trace exporter (gRPC)
	traceExporter, err := otlptrace.New(
		context.Background(),
		otlptracegrpc.NewClient(
			otlptracegrpc.WithInsecure(),
			otlptracegrpc.WithEndpoint(grpcEndpoint),
		),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create trace exporter: %w", err)
	}

	otel.SetTracerProvider(
		sdktrace.NewTracerProvider(
			sdktrace.WithSampler(sdktrace.AlwaysSample()),
			sdktrace.WithBatcher(traceExporter),
			sdktrace.WithResource(res),
		),
	)

	// Create HTTP endpoint for metrics and logs
	httpEndpoint := strings.Replace(grpcEndpoint, ":4317", ":4318", 1)

	// Create OTLP metrics exporter (HTTP)
	metricsExporter, err := otlpmetrichttp.New(
		context.Background(),
		otlpmetrichttp.WithInsecure(),
		otlpmetrichttp.WithEndpoint(httpEndpoint),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create metrics exporter: %w", err)
	}

	// Create trace provider
	tracerProvider := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(traceExporter),
		sdktrace.WithResource(res),
	)

	// Create meter provider
	meterProvider := metricsdk.NewMeterProvider(
		metricsdk.WithReader(metricsdk.NewPeriodicReader(metricsExporter, metricsdk.WithInterval(10*time.Second))),
		metricsdk.WithResource(res),
	)

	// Set global providers
	otel.SetTracerProvider(tracerProvider)
	otel.SetMeterProvider(meterProvider)

	// Set global propagator
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))

	// Create OTEL logger (HTTP)
	otelLogger, err := NewOTELLogger(httpEndpoint, 100, 5*time.Second)
	if err != nil {
		return nil, fmt.Errorf("failed to create OTEL logger: %w", err)
	}

	return &ObservabilityManager{
		observabilityPlatformURL: grpcEndpoint,
		resource:                 res,
		tracerProvider:           tracerProvider,
		meterProvider:            meterProvider,
		logger:                   otelLogger,
	}, nil
}

// SetupInstrumentation sets up automatic instrumentation
func (om *ObservabilityManager) SetupInstrumentation() error {
	// Start runtime metrics collection
	if err := runtime.Start(runtime.WithMinimumReadMemStatsInterval(time.Second)); err != nil {
		return fmt.Errorf("failed to start runtime metrics: %w", err)
	}

	return nil
}

// InstrumentDatabase instruments the database connection
func (om *ObservabilityManager) InstrumentDatabase(db *sql.DB) (*sql.DB, error) {
	// For now, we'll create a wrapper that adds basic instrumentation
	// In a real implementation, you might want to use a more sophisticated approach
	return db, nil
}

// CreateCustomMetrics creates custom application metrics
func (om *ObservabilityManager) CreateCustomMetrics() (*CustomMetrics, error) {
	meter := otel.Meter("syrinx-api")

	// HTTP request metrics
	httpRequestDuration, err := meter.Float64Histogram(
		"http_request_duration_seconds",
		metric.WithDescription("HTTP request duration in seconds"),
		metric.WithUnit("s"),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create http_request_duration metric: %w", err)
	}

	httpRequestTotal, err := meter.Int64Counter(
		"http_requests_total",
		metric.WithDescription("Total number of HTTP requests"),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create http_requests_total metric: %w", err)
	}

	httpRequestSize, err := meter.Int64Histogram(
		"http_request_size_bytes",
		metric.WithDescription("HTTP request size in bytes"),
		metric.WithUnit("By"),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create http_request_size metric: %w", err)
	}

	// Database metrics
	dbQueryDuration, err := meter.Float64Histogram(
		"db_query_duration_seconds",
		metric.WithDescription("Database query duration in seconds"),
		metric.WithUnit("s"),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create db_query_duration metric: %w", err)
	}

	dbQueryTotal, err := meter.Int64Counter(
		"db_queries_total",
		metric.WithDescription("Total number of database queries"),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create db_queries_total metric: %w", err)
	}

	// Application metrics
	activeUsers, err := meter.Int64UpDownCounter(
		"active_users",
		metric.WithDescription("Number of active users"),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create active_users metric: %w", err)
	}

	usersCreatedTotal, err := meter.Int64Counter(
		"users_created_total",
		metric.WithDescription("Total number of users created"),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create users_created_total metric: %w", err)
	}

	usersDeletedTotal, err := meter.Int64Counter(
		"users_deleted_total",
		metric.WithDescription("Total number of users deleted"),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create users_deleted_total metric: %w", err)
	}

	return &CustomMetrics{
		HTTPRequestDuration: httpRequestDuration,
		HTTPRequestTotal:    httpRequestTotal,
		HTTPRequestSize:     httpRequestSize,
		DBQueryDuration:     dbQueryDuration,
		DBQueryTotal:        dbQueryTotal,
		ActiveUsers:         activeUsers,
		UsersCreatedTotal:   usersCreatedTotal,
		UsersDeletedTotal:   usersDeletedTotal,
	}, nil
}

// CustomMetrics holds custom application metrics
type CustomMetrics struct {
	HTTPRequestDuration metric.Float64Histogram
	HTTPRequestTotal    metric.Int64Counter
	HTTPRequestSize     metric.Int64Histogram
	DBQueryDuration     metric.Float64Histogram
	DBQueryTotal        metric.Int64Counter
	ActiveUsers         metric.Int64UpDownCounter
	UsersCreatedTotal   metric.Int64Counter
	UsersDeletedTotal   metric.Int64Counter
}

// GetLogger returns the observability logger
func (om *ObservabilityManager) GetLogger() ObservabilityLogger {
	return om.logger
}

// Shutdown gracefully shuts down the observability manager
func (om *ObservabilityManager) Shutdown() error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Shutdown logger first
	if om.logger != nil {
		if err := om.logger.Close(); err != nil {
			return fmt.Errorf("failed to shutdown logger: %w", err)
		}
	}

	// Shutdown tracer provider
	if err := om.tracerProvider.Shutdown(ctx); err != nil {
		return fmt.Errorf("failed to shutdown tracer provider: %w", err)
	}

	// Shutdown meter provider
	if err := om.meterProvider.Shutdown(ctx); err != nil {
		return fmt.Errorf("failed to shutdown meter provider: %w", err)
	}

	return nil
}

// Global observability manager
var globalObservabilityManager *ObservabilityManager
var globalMetrics *CustomMetrics

// SetupObservability sets up comprehensive observability
func SetupObservability(observabilityPlatformHost string, observabilityPlatformPort string) (ObservabilityLogger, error) {
	// Create observability manager with gRPC endpoint for traces
	grpcEndpoint := observabilityPlatformHost + ":4317"
	om, err := NewObservabilityManager(grpcEndpoint)
	if err != nil {
		return nil, fmt.Errorf("failed to create observability manager: %w", err)
	}

	// Setup instrumentation
	if err := om.SetupInstrumentation(); err != nil {
		return nil, fmt.Errorf("failed to setup instrumentation: %w", err)
	}

	// Create custom metrics
	metrics, err := om.CreateCustomMetrics()
	if err != nil {
		return nil, fmt.Errorf("failed to create custom metrics: %w", err)
	}

	globalObservabilityManager = om
	globalMetrics = metrics

	return om.GetLogger(), nil
}

// ShutdownObservability shuts down observability
func ShutdownObservability() {
	if globalObservabilityManager != nil {
		if err := globalObservabilityManager.Shutdown(); err != nil {
			fmt.Fprintf(os.Stderr, "Failed to shutdown observability: %v\n", err)
		}
	}
}
