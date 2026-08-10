package metrics

import (
	"context"
	"errors"
	"testing"

	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/metric/noop"
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
