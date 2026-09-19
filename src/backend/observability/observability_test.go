package observability

import (
	"testing"
)

// No host configured: Setup must succeed without touching the network,
// same as today — this is the normal dev/CI shape.
func TestSetupNoHostSucceeds(t *testing.T) {
	t.Parallel()

	m, err := Setup("", "")
	if err != nil {
		t.Fatalf("Setup(\"\", \"\") error = %v, want nil", err)
	}
	if m == nil {
		t.Fatal("Setup(\"\", \"\") returned nil Manager")
	}
}

// A host is configured but unreachable: Setup must return an error (so the
// caller can refuse to boot) rather than silently succeeding with a
// collector that will never actually receive telemetry.
func TestSetupUnreachableHostErrors(t *testing.T) {
	t.Parallel()

	// 10.255.255.1 is a standard non-routable "black hole" test address:
	// connection attempts time out rather than resolving instantly, so this
	// exercises the same failure shape an operator would hit with a
	// firewalled/misconfigured collector.
	_, err := Setup("10.255.255.1", "4317")
	if err == nil {
		t.Fatal("Setup() with an unreachable host returned nil error, want a readiness error")
	}
}
