package recovery

import "fmt"

// ErrNoIdentityFound is returned when RECOVERY_MODE is on but no self
// server row exists (operator must run ops import-identity first).
var ErrNoIdentityFound = fmt.Errorf(
	"RECOVERY_MODE requires a restored server identity; run: ops import-identity <path-to-bundle>",
)
