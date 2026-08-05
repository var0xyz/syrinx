package identity

import (
	"errors"
	"strings"

	"github.com/google/uuid"
)

// ErrMissingDevice is returned when a device id header or field is empty or not a UUID.
var ErrMissingDevice = errors.New("missing or invalid device id")

// ParseDeviceID validates and canonicalises a client device id (UUID string).
func ParseDeviceID(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", ErrMissingDevice
	}
	parsed, err := uuid.Parse(raw)
	if err != nil {
		return "", ErrMissingDevice
	}
	return parsed.String(), nil
}
