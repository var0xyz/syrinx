package metrics

import (
	"context"
	"errors"
	"testing"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/metric/noop"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

// failingMeter wraps a working no-op meter but forces Int64Counter to error
// for one instrument name, simulating a registration failure.
type failingMeter struct {
	noop.Meter
	failName string
}

func (f failingMeter) Int64Counter(name string, opts ...metric.Int64CounterOption) (metric.Int64Counter, error) {
	if name == f.failName {
		return nil, errors.New("boom")
	}
	return f.Meter.Int64Counter(name, opts...)
}

// New must not panic when an instrument fails to register, and every
// recording method must be a safe no-op for the instrument(s) that failed —
// callers should never crash because metrics are unavailable.
func TestNewDoesNotPanicOnRegistrationFailure(t *testing.T) {
	t.Parallel()

	m := failingMeter{failName: "syrinx.keys.fetch_errors"}

	var r *OTEL
	func() {
		defer func() {
			if p := recover(); p != nil {
				t.Fatalf("New() panicked: %v", p)
			}
		}()
		r = New(m)
	}()

	if r.keyFetchErrors != nil {
		t.Fatalf("keyFetchErrors should be nil after a failed registration")
	}

	func() {
		defer func() {
			if p := recover(); p != nil {
				t.Fatalf("KeyFetchError panicked with a nil instrument: %v", p)
			}
		}()
		r.KeyFetchError(context.Background(), "reporter", "target", "FP1")
	}()
}

// collect returns every data point attribute set recorded on name.
func collect(t *testing.T, reader *sdkmetric.ManualReader, name string) []attribute.Set {
	t.Helper()
	var rm metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &rm); err != nil {
		t.Fatal(err)
	}
	var sets []attribute.Set
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name != name {
				continue
			}
			switch data := m.Data.(type) {
			case metricdata.Sum[int64]:
				for _, dp := range data.DataPoints {
					sets = append(sets, dp.Attributes)
				}
			case metricdata.Gauge[int64]:
				for _, dp := range data.DataPoints {
					sets = append(sets, dp.Attributes)
				}
			}
		}
	}
	return sets
}

// Rate counters stay bounded: no entity ids, so every publish lands on the
// same series. Abuse counters keep raw ids so an abuser can be named.
func TestOTELIDsOnlyOnAbuseCounters(t *testing.T) {
	t.Parallel()
	reader := sdkmetric.NewManualReader()
	r := New(sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader)).Meter(scope))

	r.UserCreated(context.Background(), "invite")
	r.UserCreated(context.Background(), "invite")
	r.AuthRejected(context.Background(), "alice@srv/FP1", "alice@srv", "bad_signature", "/reeds")

	created := collect(t, reader, "syrinx.users.created")
	if len(created) != 1 {
		t.Fatalf("users.created points = %d, want 1", len(created))
	}
	if created[0].Len() != 1 {
		t.Errorf("users.created attributes = %v, want only signup.mode", created[0].ToSlice())
	}
	rejected := collect(t, reader, "syrinx.auth.rejected")
	if len(rejected) != 1 {
		t.Fatalf("auth.rejected points = %d", len(rejected))
	}
	if got, _ := rejected[0].Value("user.id"); got.AsString() != "alice@srv" {
		t.Errorf("user.id = %q", got.AsString())
	}
}

// A thread counts once, and its size is recorded as the part count.
func TestOTELThreadPublished(t *testing.T) {
	t.Parallel()
	reader := sdkmetric.NewManualReader()
	r := New(sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader)).Meter(scope))

	r.ThreadPublished(context.Background(), 5)

	published := collect(t, reader, "syrinx.threads.published")
	if len(published) != 1 {
		t.Fatalf("threads.published points = %d", len(published))
	}

	var rm metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &rm); err != nil {
		t.Fatal(err)
	}
	var sum, count int64
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if h, ok := m.Data.(metricdata.Histogram[int64]); ok && m.Name == "syrinx.thread.size" {
				for _, dp := range h.DataPoints {
					sum += dp.Sum
					count += int64(dp.Count)
				}
			}
		}
	}
	if count != 1 || sum != 5 {
		t.Errorf("thread.size count = %d sum = %d, want 1 and 5", count, sum)
	}
}
