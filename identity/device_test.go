package identity

import (
	"errors"
	"testing"
)

func TestParseDeviceID(t *testing.T) {
	valid := "550e8400-e29b-41d4-a716-446655440000"

	t.Run("valid", func(t *testing.T) {
		got, err := ParseDeviceID(valid)
		if err != nil {
			t.Fatal(err)
		}
		if got != valid {
			t.Fatalf("got %q", got)
		}
	})

	t.Run("trimmed", func(t *testing.T) {
		got, err := ParseDeviceID("  " + valid + "  ")
		if err != nil {
			t.Fatal(err)
		}
		if got != valid {
			t.Fatalf("got %q", got)
		}
	})

	t.Run("empty", func(t *testing.T) {
		_, err := ParseDeviceID("")
		if !errors.Is(err, ErrMissingDevice) {
			t.Fatalf("err = %v", err)
		}
	})

	t.Run("invalid uuid", func(t *testing.T) {
		_, err := ParseDeviceID("not-a-uuid")
		if !errors.Is(err, ErrMissingDevice) {
			t.Fatalf("err = %v", err)
		}
	})
}
